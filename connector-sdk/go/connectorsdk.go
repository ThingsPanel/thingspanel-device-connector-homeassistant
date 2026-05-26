package connectorsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

// Handler is implemented by the connector author.
// Each method maps to one ThingsPanel HTTP callback route.
// Methods may return an error; the SDK translates that to a 500 response.
type Handler interface {
	// FormConfig returns the JSON form schema for the connector's config UI.
	FormConfig(ctx context.Context) (FormConfig, error)

	// OnDeviceAdd is called when a device is first bound to this connector.
	OnDeviceAdd(ctx context.Context, req DeviceAddRequest) error

	// OnDeviceDelete is called when a device is unbound/deleted.
	OnDeviceDelete(ctx context.Context, req DeviceDeleteRequest) error

	// OnCommand is called when ThingsPanel sends a downlink command.
	OnCommand(ctx context.Context, req CommandRequest) (CommandResponse, error)

	// OnConfigUpdate is called when a device's configuration is updated.
	OnConfigUpdate(ctx context.Context, req ConfigUpdateRequest) error

	// OnDisconnect is called when a device goes offline.
	OnDisconnect(ctx context.Context, req DisconnectRequest) error

	// OnEvent is called for generic platform event notifications.
	// Connectors that do not handle events can return nil without error.
	OnEvent(ctx context.Context, ev EventNotification) error
}

// FormConfigProvider can be implemented by connectors that need to return
// different forms for access points, vouchers, and per-device protocol config.
type FormConfigProvider interface {
	FormConfigFor(ctx context.Context, req FormConfigRequest) (FormConfig, error)
}

// RawFormDataProvider can be implemented by connectors that need to return
// non-schema payloads for legacy form types such as VCRT (voucher-type map).
type RawFormDataProvider interface {
	RawFormDataFor(ctx context.Context, req FormConfigRequest) (data any, handled bool, err error)
}

// DeviceLister can be implemented by service connectors that discover devices
// from a service-access voucher. The legacy ThingsPanel backend calls this
// during the common device selection and template binding step.
type DeviceLister interface {
	ListDevices(ctx context.Context, req DeviceListRequest) (DeviceListResponse, error)
}

// Server wires HTTP routes and manages the heartbeat goroutine.
// Construct with NewServer; start with Run.
type Server struct {
	info    ConnectorInfo
	handler Handler
	mux     *http.ServeMux
	logger  *slog.Logger
}

// NewServer creates a server with the given identity and handler.
func NewServer(info ConnectorInfo, handler Handler) *Server {
	s := &Server{
		info:    info,
		handler: handler,
		mux:     http.NewServeMux(),
		logger:  slog.Default(),
	}
	s.registerRoutes()
	return s
}

// ServeHTTP implements http.Handler so the server can be used in httptest without
// starting the full TCP listener. This is used in unit tests.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.mux.ServeHTTP(w, r)
}

// Run starts the HTTP server and heartbeat loop. It blocks until ctx is done.
func (s *Server) Run(ctx context.Context) error {
	addr := s.info.ListenAddr
	if addr == "" {
		addr = ":9001"
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// Start heartbeat in background.
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go s.runHeartbeat(hbCtx)

	// Start HTTP server; shut down gracefully when ctx is cancelled.
	errCh := make(chan error, 1)
	go func() {
		s.logger.Info("connector listening",
			"addr", addr,
			"serviceIdentifier", s.info.ServiceIdentifier,
			"instanceID", s.info.InstanceID,
		)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		shutCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		return srv.Shutdown(shutCtx)
	case err := <-errCh:
		return err
	}
}

// ─── Routes ──────────────────────────────────────────────────────────────────

func (s *Server) registerRoutes() {
	// Health — checked by K8S readiness/liveness probe.
	s.mux.HandleFunc("GET /health", s.handleHealth)

	// ThingsPanel HTTP callback routes (from backend-integration-contract.md).
	s.mux.HandleFunc("GET /api/v1/form/config", s.handleFormConfig)
	s.mux.HandleFunc("GET /api/v1/plugin/device/list", s.handlePluginDeviceList)
	s.mux.HandleFunc("POST /api/v1/device/add", s.handleDeviceAdd)
	s.mux.HandleFunc("POST /api/v1/device/delete", s.handleDeviceDelete)
	s.mux.HandleFunc("POST /api/v1/device/command", s.handleCommand)
	s.mux.HandleFunc("POST /api/v1/device/config/update", s.handleConfigUpdate)
	s.mux.HandleFunc("POST /api/v1/device/disconnect", s.handleDisconnect)
	s.mux.HandleFunc("POST /api/v1/notify/event", s.handleEvent)
}

func (s *Server) handlePluginDeviceList(w http.ResponseWriter, r *http.Request) {
	lister, ok := s.handler.(DeviceLister)
	if !ok {
		writeJSON(w, http.StatusOK, legacyEnvelope(DeviceListResponse{List: []DiscoveredDevice{}}))
		return
	}
	page := parsePositiveInt(r.URL.Query().Get("page"), 1)
	pageSize := parsePositiveInt(r.URL.Query().Get("page_size"), 10)
	resp, err := lister.ListDevices(r.Context(), DeviceListRequest{
		Voucher:           r.URL.Query().Get("voucher"),
		ServiceIdentifier: r.URL.Query().Get("service_identifier"),
		Page:              page,
		PageSize:          pageSize,
	})
	if err != nil {
		s.logger.Error("ListDevices error", "err", err)
		writeJSON(w, http.StatusOK, map[string]any{
			"code":    500,
			"message": err.Error(),
			"data":    DeviceListResponse{List: []DiscoveredDevice{}},
		})
		return
	}
	if resp.List == nil {
		resp.List = []DiscoveredDevice{}
	}
	if resp.Total == 0 {
		resp.Total = len(resp.List)
	}
	writeJSON(w, http.StatusOK, legacyEnvelope(resp))
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{
		"status":            "ok",
		"serviceIdentifier": s.info.ServiceIdentifier,
		"instanceID":        s.info.InstanceID,
	})
}

func (s *Server) handleFormConfig(w http.ResponseWriter, r *http.Request) {
	req := FormConfigRequest{
		ProtocolType: strings.TrimSpace(r.URL.Query().Get("protocol_type")),
		DeviceType:   strings.TrimSpace(r.URL.Query().Get("device_type")),
		FormType:     strings.TrimSpace(r.URL.Query().Get("form_type")),
		VoucherType:  strings.TrimSpace(r.URL.Query().Get("voucher_type")),
	}

	if req.FormType != "" {
		if provider, ok := s.handler.(RawFormDataProvider); ok {
			data, handled, err := provider.RawFormDataFor(r.Context(), req)
			if err != nil {
				s.logger.Error("RawFormDataFor error", "err", err, "formType", req.FormType)
				writeError(w, http.StatusInternalServerError, err)
				return
			}
			if handled {
				writeJSON(w, http.StatusOK, map[string]any{
					"code":    200,
					"message": "success",
					"data":    data,
				})
				return
			}
		}
	}

	var (
		cfg FormConfig
		err error
	)
	if provider, ok := s.handler.(FormConfigProvider); ok {
		cfg, err = provider.FormConfigFor(r.Context(), req)
	} else {
		cfg, err = s.handler.FormConfig(r.Context())
	}
	if err != nil {
		s.logger.Error("FormConfig error", "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if req.FormType != "" {
		writeJSON(w, http.StatusOK, map[string]any{
			"code":    200,
			"message": "success",
			"data":    legacyFormElements(cfg.Schema),
		})
		return
	}
	writeJSON(w, http.StatusOK, cfg)
}

func (s *Server) handleDeviceAdd(w http.ResponseWriter, r *http.Request) {
	var req DeviceAddRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.handler.OnDeviceAdd(r.Context(), req); err != nil {
		s.logger.Error("OnDeviceAdd error", "deviceID", req.DeviceID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDeviceDelete(w http.ResponseWriter, r *http.Request) {
	var req DeviceDeleteRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.handler.OnDeviceDelete(r.Context(), req); err != nil {
		s.logger.Error("OnDeviceDelete error", "deviceID", req.DeviceID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleCommand(w http.ResponseWriter, r *http.Request) {
	var req CommandRequest
	if !decodeBody(w, r, &req) {
		return
	}
	resp, err := s.handler.OnCommand(r.Context(), req)
	if err != nil {
		s.logger.Error("OnCommand error", "deviceID", req.DeviceID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleConfigUpdate(w http.ResponseWriter, r *http.Request) {
	var req ConfigUpdateRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.handler.OnConfigUpdate(r.Context(), req); err != nil {
		s.logger.Error("OnConfigUpdate error", "deviceID", req.DeviceID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleDisconnect(w http.ResponseWriter, r *http.Request) {
	var req DisconnectRequest
	if !decodeBody(w, r, &req) {
		return
	}
	if err := s.handler.OnDisconnect(r.Context(), req); err != nil {
		s.logger.Error("OnDisconnect error", "deviceID", req.DeviceID, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func (s *Server) handleEvent(w http.ResponseWriter, r *http.Request) {
	var raw map[string]any
	if !decodeBody(w, r, &raw) {
		return
	}
	ev := normalizeEventNotification(raw)
	if err := s.handler.OnEvent(r.Context(), ev); err != nil {
		s.logger.Error("OnEvent error", "eventType", ev.EventType, "err", err)
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func normalizeEventNotification(raw map[string]any) EventNotification {
	if len(raw) == 0 {
		return EventNotification{}
	}

	if messageType, ok := raw["message_type"].(string); ok {
		ev := EventNotification{
			EventType: normalizeLegacyEventType(messageType),
			Payload:   map[string]any{},
		}
		if message, ok := raw["message"].(string); ok && strings.TrimSpace(message) != "" {
			var payload map[string]any
			if err := json.Unmarshal([]byte(message), &payload); err == nil {
				ev.Payload = payload
				if deviceID, ok := payload["device_id"].(string); ok {
					ev.DeviceID = deviceID
				}
			}
		}
		return ev
	}

	ev := EventNotification{}
	if eventType, ok := raw["event_type"].(string); ok {
		ev.EventType = eventType
	}
	if deviceID, ok := raw["device_id"].(string); ok {
		ev.DeviceID = deviceID
	}
	if payload, ok := raw["payload"].(map[string]any); ok {
		ev.Payload = payload
	}
	return ev
}

func normalizeLegacyEventType(messageType string) string {
	switch strings.TrimSpace(messageType) {
	case "1":
		return "service_access.updated"
	default:
		return strings.TrimSpace(messageType)
	}
}

// ─── Heartbeat ────────────────────────────────────────────────────────────────

func (s *Server) runHeartbeat(ctx context.Context) {
	interval := s.info.HeartbeatInterval
	if interval == 0 {
		interval = 30 * time.Second
	}
	if s.info.BackendURL == "" || s.info.ServiceIdentifier == "" {
		s.logger.Warn("heartbeat disabled: BackendURL or ServiceIdentifier not set")
		return
	}

	s.logger.Info("heartbeat started",
		"interval", interval,
		"backendURL", s.info.BackendURL,
	)

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	// Send once immediately so the backend sees the connector as alive on startup.
	s.sendHeartbeat(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.sendHeartbeat(ctx)
		}
	}
}

func (s *Server) sendHeartbeat(ctx context.Context) {
	url := s.info.BackendURL + "/api/v1/plugin/heartbeat"
	body, _ := json.Marshal(map[string]string{
		"service_identifier": s.info.ServiceIdentifier,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		s.logger.Error("heartbeat: build request", "err", err)
		return
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		s.logger.Warn("heartbeat: POST failed", "url", url, "err", err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		s.logger.Warn("heartbeat: unexpected status", "status", resp.StatusCode)
	}
}

// ─── HTTP helpers ─────────────────────────────────────────────────────────────

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, err error) {
	writeJSON(w, status, map[string]string{"error": err.Error()})
}

func legacyEnvelope(data any) map[string]any {
	return map[string]any{
		"code":    200,
		"message": "success",
		"data":    data,
	}
}

func parsePositiveInt(value string, fallback int) int {
	n, err := strconv.Atoi(value)
	if err != nil || n < 1 {
		return fallback
	}
	return n
}

func legacyFormElements(schema map[string]any) []map[string]any {
	properties, _ := schema["properties"].(map[string]any)
	required := make(map[string]bool)
	for _, key := range stringSlice(schema["required"]) {
		required[key] = true
	}

	elements := make([]map[string]any, 0, len(properties))
	for _, key := range orderedPropertyKeys(schema, properties) {
		raw := properties[key]
		prop, _ := raw.(map[string]any)
		typ := fmt.Sprint(prop["type"])
		elementType := "input"
		validateType := "string"
		switch {
		case typ == "boolean":
			elementType = "switch"
			validateType = "boolean"
		case typ == "array":
			if tableElement := legacyArrayElement(key, prop, required[key]); tableElement != nil {
				elements = append(elements, tableElement)
				continue
			}
		case typ == "object":
			if kvElement := legacyObjectElement(key, prop, required[key]); kvElement != nil {
				elements = append(elements, kvElement)
				continue
			}
		case prop["enum"] != nil:
			elementType = "select"
		}
		if typ == "integer" || typ == "number" {
			validateType = "number"
		}

		element := map[string]any{
			"dataKey":     key,
			"label":       stringValue(prop, "title", key),
			"type":        elementType,
			"placeholder": stringValue(prop, "description", ""),
			"validate": map[string]any{
				"required": required[key],
				"type":     validateType,
			},
		}
		if inputType := stringValue(prop, "inputType", ""); inputType != "" {
			element["inputType"] = inputType
		}
		copyOptionalSchemaValue(element, prop, "visibleWhen")
		copyOptionalSchemaValue(element, prop, "showWhen")
		if def, ok := prop["default"]; ok {
			element["default"] = def
		}
		if values := stringSlice(prop["enum"]); len(values) > 0 {
			options := make([]map[string]string, 0, len(values))
			for _, value := range values {
				options = append(options, map[string]string{"label": value, "value": value})
			}
			element["options"] = options
		}
		elements = append(elements, element)
	}
	return elements
}

func orderedPropertyKeys(schema map[string]any, properties map[string]any) []string {
	keys := make([]string, 0, len(properties))
	seen := make(map[string]bool, len(properties))

	for _, key := range stringSlice(schema["x-order"]) {
		if _, ok := properties[key]; ok {
			keys = append(keys, key)
			seen[key] = true
		}
	}

	remaining := make([]string, 0, len(properties)-len(keys))
	for key := range properties {
		if !seen[key] {
			remaining = append(remaining, key)
		}
	}
	sort.Strings(remaining)
	return append(keys, remaining...)
}

func legacyArrayElement(key string, prop map[string]any, required bool) map[string]any {
	items, _ := prop["items"].(map[string]any)
	if fmt.Sprint(items["type"]) != "object" {
		return nil
	}
	itemProps, _ := items["properties"].(map[string]any)
	itemRequired := make(map[string]bool)
	for _, reqKey := range stringSlice(items["required"]) {
		itemRequired[reqKey] = true
	}

	arrayElements := make([]map[string]any, 0, len(itemProps))
	for _, itemKey := range orderedPropertyKeys(items, itemProps) {
		itemProp, _ := itemProps[itemKey].(map[string]any)
		subType := fmt.Sprint(itemProp["type"])
		subElementType := "input"
		validateType := "string"
		if itemProp["enum"] != nil {
			subElementType = "select"
		}
		if subType == "integer" || subType == "number" {
			validateType = "number"
		}

		subElement := map[string]any{
			"dataKey":     itemKey,
			"label":       stringValue(itemProp, "title", itemKey),
			"type":        subElementType,
			"placeholder": stringValue(itemProp, "description", ""),
			"validate": map[string]any{
				"required": itemRequired[itemKey],
				"type":     validateType,
			},
		}
		if def, ok := itemProp["default"]; ok {
			subElement["default"] = def
		}
		if values := stringSlice(itemProp["enum"]); len(values) > 0 {
			options := make([]map[string]string, 0, len(values))
			for _, value := range values {
				options = append(options, map[string]string{"label": value, "value": value})
			}
			subElement["options"] = options
		}
		arrayElements = append(arrayElements, subElement)
	}

	return map[string]any{
		"dataKey":  key,
		"label":    stringValue(prop, "title", key),
		"type":     "table",
		"array":    arrayElements,
		"default":  prop["default"],
		"validate": map[string]any{"required": required},
	}
}

func legacyObjectElement(key string, prop map[string]any, required bool) map[string]any {
	return map[string]any{
		"dataKey":     key,
		"label":       stringValue(prop, "title", key),
		"type":        "kv-table",
		"placeholder": stringValue(prop, "description", ""),
		"default":     prop["default"],
		"validate": map[string]any{
			"required": required,
			"type":     "object",
		},
	}
}

func stringSlice(v any) []string {
	raw, ok := v.([]any)
	if !ok {
		if typed, ok := v.([]string); ok {
			return typed
		}
		return nil
	}
	values := make([]string, 0, len(raw))
	for _, item := range raw {
		values = append(values, fmt.Sprint(item))
	}
	return values
}

func stringValue(values map[string]any, key, fallback string) string {
	if value := fmt.Sprint(values[key]); value != "" && value != "<nil>" {
		return value
	}
	return fallback
}

func copyOptionalSchemaValue(dst map[string]any, src map[string]any, key string) {
	if value, ok := src[key]; ok {
		dst[key] = value
	}
}

func decodeBody(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": fmt.Sprintf("invalid request body: %v", err),
		})
		return false
	}
	return true
}
