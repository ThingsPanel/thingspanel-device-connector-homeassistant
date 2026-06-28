package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"sync"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

type homeAssistantServiceHandler struct {
	logger           *slog.Logger
	mu               sync.RWMutex
	devices          map[string]sdk.DeviceAddRequest
	deviceNumberIDs  map[string]string
	immediateTelemCh chan sdk.DeviceAddRequest
}

func newHomeAssistantServiceHandler() *homeAssistantServiceHandler {
	return &homeAssistantServiceHandler{
		logger:           slog.Default(),
		devices:          make(map[string]sdk.DeviceAddRequest),
		deviceNumberIDs:  make(map[string]string),
		immediateTelemCh: make(chan sdk.DeviceAddRequest, 32),
	}
}

func (h *homeAssistantServiceHandler) FormConfig(_ context.Context) (sdk.FormConfig, error) {
	return h.accessPointFormConfig(), nil
}

func (h *homeAssistantServiceHandler) FormConfigFor(_ context.Context, req sdk.FormConfigRequest) (sdk.FormConfig, error) {
	switch req.FormType {
	case "CFG":
		return h.deviceFormConfig(), nil
	default:
		return h.accessPointFormConfig(), nil
	}
}

func (h *homeAssistantServiceHandler) RawFormDataFor(_ context.Context, req sdk.FormConfigRequest) (data any, handled bool, err error) {
	if req.FormType == "VCRT" {
		return map[string]any{}, true, nil
	}
	return nil, false, nil
}

func (h *homeAssistantServiceHandler) accessPointFormConfig() sdk.FormConfig {
	return sdk.FormConfig{Schema: map[string]any{
		"type":  "object",
		"title": "HomeAssistant 接入服务",
		"properties": map[string]any{
			"base_url": map[string]any{"type": "string", "title": "Home Assistant 地址", "description": "例如 http://192.168.31.97:8123"},
			"token":    map[string]any{"type": "string", "title": "长期访问令牌", "inputType": "password"},
		},
		"required": []string{"base_url", "token"},
	}}
}

func (h *homeAssistantServiceHandler) deviceFormConfig() sdk.FormConfig {
	return sdk.FormConfig{Schema: map[string]any{
		"type":       "object",
		"title":      "HomeAssistant 设备配置",
		"properties": map[string]any{},
	}}
}

func (h *homeAssistantServiceHandler) ListDevices(ctx context.Context, req sdk.DeviceListRequest) (sdk.DeviceListResponse, error) {
	cfg := map[string]any{}
	if strings.TrimSpace(req.Voucher) != "" {
		if err := json.Unmarshal([]byte(req.Voucher), &cfg); err != nil {
			return sdk.DeviceListResponse{}, fmt.Errorf("invalid voucher json: %w", err)
		}
	}
	client, err := h.haClientFromConfig(cfg)
	if err != nil {
		return sdk.DeviceListResponse{}, err
	}
	states, err := client.ListStates(ctx)
	if err != nil {
		return sdk.DeviceListResponse{}, err
	}
	devices := make([]sdk.DiscoveredDevice, 0, len(states))
	for _, state := range states {
		domain := entityDomain(state.EntityID)
		if domain != "light" && domain != "switch" && domain != "sensor" && domain != "binary_sensor" {
			continue
		}
		name := state.EntityID
		if friendly, ok := state.Attributes["friendly_name"].(string); ok && strings.TrimSpace(friendly) != "" {
			name = friendly
		}
		deviceNumber := normalizedDeviceNumber("ha", state.EntityID)
		protocolConfig := jsonString(map[string]any{
			"entity_id":     state.EntityID,
			"domain":        domain,
			"device_number": deviceNumber,
		})
		additionalInfo := jsonString(map[string]any{
			"source": "homeassistant-service",
			"state":  state.State,
		})
		devices = append(devices, sdk.DiscoveredDevice{
			DeviceName:     name,
			DeviceNumber:   deviceNumber,
			Description:    "Home Assistant entity: " + state.EntityID,
			ProtocolConfig: protocolConfig,
			AdditionalInfo: additionalInfo,
		})
	}
	return paginateDevices(devices, req.Page, req.PageSize), nil
}

func (h *homeAssistantServiceHandler) OnDeviceAdd(_ context.Context, req sdk.DeviceAddRequest) error {
	deviceNumber := deviceNumberFromAdd(req)
	h.logger.Info("home assistant device added", "deviceID", req.DeviceID, "number", deviceNumber)

	h.mu.Lock()
	h.devices[req.DeviceID] = req
	if deviceNumber != "" {
		h.deviceNumberIDs[deviceNumber] = req.DeviceID
	}
	h.mu.Unlock()

	select {
	case h.immediateTelemCh <- req:
	default:
	}
	return nil
}

func (h *homeAssistantServiceHandler) OnDeviceDelete(_ context.Context, req sdk.DeviceDeleteRequest) error {
	h.mu.Lock()
	if existing, ok := h.devices[req.DeviceID]; ok {
		delete(h.deviceNumberIDs, deviceNumberFromAdd(existing))
	}
	delete(h.devices, req.DeviceID)
	h.mu.Unlock()
	return nil
}

func (h *homeAssistantServiceHandler) OnConfigUpdate(_ context.Context, req sdk.ConfigUpdateRequest) error {
	h.mu.Lock()
	if existing, ok := h.devices[req.DeviceID]; ok {
		oldNumber := deviceNumberFromAdd(existing)
		existing.DeviceConfig = req.DeviceConfig
		h.devices[req.DeviceID] = existing
		newNumber := deviceNumberFromAdd(existing)
		if oldNumber != "" && oldNumber != newNumber {
			delete(h.deviceNumberIDs, oldNumber)
		}
		if newNumber != "" {
			h.deviceNumberIDs[newNumber] = req.DeviceID
		}
	}
	h.mu.Unlock()
	return nil
}

func (h *homeAssistantServiceHandler) OnDisconnect(context.Context, sdk.DisconnectRequest) error {
	return nil
}

func (h *homeAssistantServiceHandler) OnEvent(context.Context, sdk.EventNotification) error {
	return nil
}

func (h *homeAssistantServiceHandler) OnCommand(ctx context.Context, req sdk.CommandRequest) (sdk.CommandResponse, error) {
	cfg := h.configFor(req.DeviceID)
	ha, err := h.haClientFromConfig(cfg)
	if err != nil {
		return sdk.CommandResponse{}, fmt.Errorf("home assistant is not configured: %w", err)
	}
	entityID := configString(cfg, "entity_id", envAny("HA_ENTITY_ID", "HA_LIGHT_ENTITY_ID"))
	if entityID == "" {
		return sdk.CommandResponse{}, fmt.Errorf("entity_id is required")
	}

	ctx, cancel := context.WithTimeout(ctx, homeAssistantCommandTimeout)
	defer cancel()

	switch {
	case hasCommand(req.Command, "switch"):
		state, err := parseSwitch(req.Command["switch"])
		if err != nil {
			return sdk.CommandResponse{}, err
		}
		if err := ha.SetState(ctx, entityID, state); err != nil {
			return sdk.CommandResponse{}, fmt.Errorf("home assistant switch control failed: %w", err)
		}
		return sdk.CommandResponse{OK: true, Message: fmt.Sprintf("homeassistant %s switch=%s", entityID, state)}, nil
	case hasCommand(req.Command, "brightness"):
		brightness, err := parsePercent(req.Command["brightness"])
		if err != nil {
			return sdk.CommandResponse{}, err
		}
		if err := ha.SetBrightnessPercent(ctx, entityID, brightness); err != nil {
			return sdk.CommandResponse{}, fmt.Errorf("home assistant brightness control failed: %w", err)
		}
		return sdk.CommandResponse{OK: true, Message: fmt.Sprintf("homeassistant %s brightness=%d", entityID, brightness)}, nil
	case hasCommand(req.Command, "turn_on"):
		params, err := parseTurnOnParams(req.Command["turn_on"])
		if err != nil {
			return sdk.CommandResponse{}, err
		}
		if err := ha.TurnOn(ctx, entityID, params); err != nil {
			return sdk.CommandResponse{}, fmt.Errorf("home assistant turn_on failed: %w", err)
		}
		return sdk.CommandResponse{OK: true, Message: fmt.Sprintf("homeassistant %s turn_on=%v", entityID, params)}, nil
	case hasCommand(req.Command, "query"):
		queryKey := strings.ToLower(strings.TrimSpace(fmt.Sprint(req.Command["query"])))
		if queryKey != "" && queryKey != "state" {
			return sdk.CommandResponse{}, fmt.Errorf("unsupported home assistant query %q", queryKey)
		}
		state, err := ha.GetState(ctx, entityID)
		if err != nil {
			return sdk.CommandResponse{}, fmt.Errorf("home assistant state query failed: %w", err)
		}
		return sdk.CommandResponse{OK: true, Message: "state=" + state.State}, nil
	default:
		return sdk.CommandResponse{}, fmt.Errorf("unsupported command keys: %v", req.Command)
	}
}

func (h *homeAssistantServiceHandler) haClientFromConfig(cfg map[string]any) (*homeAssistantClient, error) {
	baseURL := configString(cfg, "base_url", envAny("HA_BASE_URL", "HOME_ASSISTANT_BASE_URL"))
	token := configString(cfg, "token", envAny("HA_ACCESS_TOKEN", "HOME_ASSISTANT_TOKEN"))
	return newHomeAssistantClient(baseURL, token)
}

func (h *homeAssistantServiceHandler) configFor(deviceID string) map[string]any {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if req, ok := h.devices[deviceID]; ok && req.DeviceConfig != nil {
		return req.DeviceConfig
	}
	return map[string]any{
		"base_url":  envAny("HA_BASE_URL", "HOME_ASSISTANT_BASE_URL"),
		"entity_id": envAny("HA_ENTITY_ID", "HA_LIGHT_ENTITY_ID"),
		"token":     envAny("HA_ACCESS_TOKEN", "HOME_ASSISTANT_TOKEN"),
	}
}

func (h *homeAssistantServiceHandler) deviceIDForNumber(deviceNumber string) string {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.deviceNumberIDs[deviceNumber]
}

func (h *homeAssistantServiceHandler) boundDevices() []sdk.DeviceAddRequest {
	h.mu.RLock()
	defer h.mu.RUnlock()
	devices := make([]sdk.DeviceAddRequest, 0, len(h.devices))
	for _, req := range h.devices {
		devices = append(devices, req)
	}
	return devices
}

func deviceNumberFromAdd(req sdk.DeviceAddRequest) string {
	if req.DeviceConfig != nil {
		if dn := configString(req.DeviceConfig, "device_number", ""); dn != "" {
			return dn
		}
		if entityID := configString(req.DeviceConfig, "entity_id", ""); entityID != "" {
			return normalizedDeviceNumber("ha", entityID)
		}
	}
	return ""
}

func hasCommand(command map[string]any, key string) bool {
	_, ok := command[key]
	return ok
}

func parseSwitch(v any) (string, error) {
	switch value := v.(type) {
	case bool:
		if value {
			return "on", nil
		}
		return "off", nil
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "on", "true", "1":
			return "on", nil
		case "off", "false", "0":
			return "off", nil
		}
	case float64:
		if value != 0 {
			return "on", nil
		}
		return "off", nil
	}
	return "", fmt.Errorf("switch value must be on/off or boolean, got %v", v)
}

func parsePercent(v any) (int, error) {
	switch value := v.(type) {
	case float64:
		if float64(int(value)) != value {
			return 0, fmt.Errorf("percentage must be an integer, got %v", v)
		}
		return validatePercent(int(value))
	case int:
		return validatePercent(value)
	case string:
		n, err := strconv.Atoi(strings.TrimSpace(value))
		if err != nil {
			return 0, err
		}
		return validatePercent(n)
	default:
		return 0, fmt.Errorf("percentage must be numeric, got %T", v)
	}
}

func validatePercent(n int) (int, error) {
	if n < 1 || n > 100 {
		return 0, fmt.Errorf("percentage must be between 1 and 100, got %d", n)
	}
	return n, nil
}

func parseTurnOnParams(v any) (map[string]any, error) {
	switch value := v.(type) {
	case map[string]any:
		if len(value) == 0 {
			return nil, fmt.Errorf("turn_on params must be a non-empty object")
		}
		return value, nil
	case nil:
		return nil, fmt.Errorf("turn_on params are required")
	default:
		raw, err := json.Marshal(value)
		if err != nil {
			return nil, fmt.Errorf("turn_on params must be an object, got %T", v)
		}
		var params map[string]any
		if err := json.Unmarshal(raw, &params); err != nil || len(params) == 0 {
			return nil, fmt.Errorf("turn_on params must be a non-empty object")
		}
		return params, nil
	}
}

func configString(cfg map[string]any, key, fallback string) string {
	if cfg != nil {
		if v := strings.TrimSpace(fmt.Sprint(cfg[key])); v != "" && v != "<nil>" {
			return v
		}
	}
	return fallback
}

func envAny(keys ...string) string {
	for _, key := range keys {
		if value := strings.TrimSpace(os.Getenv(key)); value != "" {
			return value
		}
	}
	return ""
}

func normalizedDeviceNumber(prefix, value string) string {
	source := strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(".", "-", "_", "-", " ", "-", "/", "-", ":", "-")
	normalized := replacer.Replace(source)
	var b strings.Builder
	lastDash := false
	for _, r := range normalized {
		allowed := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if allowed {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash {
			b.WriteByte('-')
			lastDash = true
		}
	}
	slug := strings.Trim(b.String(), "-")
	if slug == "" {
		slug = "device"
	}
	result := prefix + "-" + slug
	if len(result) <= 36 {
		return result
	}

	// ThingsPanel device_number is capped at 36 chars. Plain truncation makes
	// long Home Assistant entity IDs collide (e.g. all iPhone sensors become
	// ha-sensor-zhang-jun-hong-s-iphone-17). Keep a readable prefix and append
	// a deterministic hash of the full source identity.
	hash := sha256.Sum256([]byte(source))
	suffix := hex.EncodeToString(hash[:4]) // 8 hex chars
	maxReadable := 36 - 1 - len(suffix)
	readable := result
	if len(readable) > maxReadable {
		readable = strings.TrimRight(readable[:maxReadable], "-")
	}
	return readable + "-" + suffix
}

func paginateDevices(devices []sdk.DiscoveredDevice, page, pageSize int) sdk.DeviceListResponse {
	total := len(devices)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 10
	}
	start := (page - 1) * pageSize
	if start >= total {
		return sdk.DeviceListResponse{Total: total, List: []sdk.DiscoveredDevice{}}
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	return sdk.DeviceListResponse{Total: total, List: devices[start:end]}
}

func jsonString(value any) string {
	raw, _ := json.Marshal(value)
	return string(raw)
}
