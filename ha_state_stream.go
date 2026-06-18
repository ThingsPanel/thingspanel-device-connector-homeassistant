package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	sdk "github.com/thingspanel/device-connector-sdk-go"
)

type haStreamMessage struct {
	ID      int             `json:"id"`
	Type    string          `json:"type"`
	Success *bool           `json:"success"`
	Event   json.RawMessage `json:"event"`
}

type haStateChangedEvent struct {
	EventType string `json:"event_type"`
	Data      struct {
		EntityID string             `json:"entity_id"`
		NewState homeAssistantState `json:"new_state"`
		OldState homeAssistantState `json:"old_state"`
	} `json:"data"`
}

func runHomeAssistantStateStream(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	backoff := 2 * time.Second
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		baseURL, token, ok := handler.haStreamCredentials()
		if !ok {
			slog.Info("home assistant state stream waiting for HA credentials and bound devices")
			select {
			case <-ctx.Done():
				return
			case <-time.After(5 * time.Second):
				continue
			}
		}

		err := runHomeAssistantStateStreamSession(ctx, handler, cfg, baseURL, token)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			slog.Warn("home assistant state stream disconnected", "err", err, "retry_in", backoff)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		if backoff < 30*time.Second {
			backoff += 2 * time.Second
		}
	}
}

func runHomeAssistantStateStreamSession(
	ctx context.Context,
	handler *homeAssistantServiceHandler,
	cfg homeAssistantTelemetryConfig,
	baseURL, token string,
) error {
	wsURL, err := homeAssistantWebSocketURL(baseURL)
	if err != nil {
		return err
	}

	dialer := websocket.Dialer{
		Proxy:            http.ProxyFromEnvironment,
		HandshakeTimeout: 10 * time.Second,
	}
	conn, _, err := dialer.DialContext(ctx, wsURL, nil)
	if err != nil {
		return fmt.Errorf("dial websocket: %w", err)
	}
	defer conn.Close()

	if err := readUntilAuthRequired(conn); err != nil {
		return err
	}
	// Home Assistant auth must NOT include "id" — extra keys are rejected (auth_invalid).
	if err := conn.WriteJSON(map[string]any{
		"type":         "auth",
		"access_token": token,
	}); err != nil {
		return fmt.Errorf("auth: %w", err)
	}
	if err := readUntilAuthOK(conn); err != nil {
		return fmt.Errorf("auth result: %w", err)
	}

	subID := 1
	if err := conn.WriteJSON(map[string]any{
		"id":         subID,
		"type":       "subscribe_events",
		"event_type": "state_changed",
	}); err != nil {
		return fmt.Errorf("subscribe_events: %w", err)
	}
	if err := waitForResult(conn, subID); err != nil {
		return fmt.Errorf("subscribe result: %w", err)
	}
	slog.Info("home assistant state stream subscribed", "endpoint", wsURL)

	lastPublished := struct {
		mu     sync.Mutex
		states map[string]string
	}{states: map[string]string{}}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if err := conn.SetReadDeadline(time.Now().Add(90 * time.Second)); err != nil {
			return err
		}
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var msg haStreamMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type != "event" || len(msg.Event) == 0 {
			continue
		}

		var event haStateChangedEvent
		if err := json.Unmarshal(msg.Event, &event); err != nil {
			continue
		}
		if event.EventType != "state_changed" {
			continue
		}
		entityID := strings.TrimSpace(event.Data.EntityID)
		newState := strings.TrimSpace(event.Data.NewState.State)
		if entityID == "" || newState == "" {
			continue
		}

		device, ok := handler.boundDeviceForEntityID(entityID)
		if !ok {
			continue
		}

		lastPublished.mu.Lock()
		if lastPublished.states[entityID] == newState {
			lastPublished.mu.Unlock()
			continue
		}
		lastPublished.states[entityID] = newState
		lastPublished.mu.Unlock()

		publishHomeAssistantDeviceTelemetryFromState(ctx, handler, cfg, device, event.Data.NewState)
	}
}

func (h *homeAssistantServiceHandler) boundDeviceForEntityID(entityID string) (sdk.DeviceAddRequest, bool) {
	target := strings.TrimSpace(entityID)
	if target == "" {
		return sdk.DeviceAddRequest{}, false
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, req := range h.devices {
		if configString(req.DeviceConfig, "entity_id", "") == target {
			return req, true
		}
	}
	return sdk.DeviceAddRequest{}, false
}

func (h *homeAssistantServiceHandler) haStreamCredentials() (baseURL, token string, ok bool) {
	for _, device := range h.boundDevices() {
		cfg := h.configFor(device.DeviceID)
		base := configString(cfg, "base_url", envAny("HA_BASE_URL", "HOME_ASSISTANT_BASE_URL"))
		tok := configString(cfg, "token", envAny("HA_ACCESS_TOKEN", "HOME_ASSISTANT_TOKEN"))
		if strings.TrimSpace(base) != "" && strings.TrimSpace(tok) != "" {
			return base, tok, true
		}
	}
	return "", "", false
}

func homeAssistantWebSocketURL(baseURL string) (string, error) {
	trimmed := strings.TrimSpace(baseURL)
	if trimmed == "" {
		return "", fmt.Errorf("base_url is required for websocket")
	}
	if !strings.Contains(trimmed, "://") {
		trimmed = "http://" + trimmed
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", err
	}
	switch parsed.Scheme {
	case "https":
		parsed.Scheme = "wss"
	case "http":
		parsed.Scheme = "ws"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("unsupported HA base_url scheme %q", parsed.Scheme)
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/api/websocket"
	parsed.RawQuery = ""
	parsed.Fragment = ""
	return parsed.String(), nil
}

func readUntilAuthRequired(conn *websocket.Conn) error {
	for {
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type == "auth_required" {
			return nil
		}
		if msg.Type == "auth_ok" {
			return nil
		}
	}
}

func readUntilAuthOK(conn *websocket.Conn) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg struct {
			Type    string `json:"type"`
			Success *bool  `json:"success"`
		}
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		switch msg.Type {
		case "auth_ok":
			return nil
		case "auth_invalid":
			return fmt.Errorf("home assistant rejected access token")
		case "result":
			if msg.Success != nil && *msg.Success {
				return nil
			}
			if msg.Success != nil && !*msg.Success {
				return fmt.Errorf("home assistant auth command failed")
			}
		}
	}
	return fmt.Errorf("timed out waiting for auth_ok")
}

func waitForResult(conn *websocket.Conn, id int) error {
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		_ = conn.SetReadDeadline(deadline)
		_, raw, err := conn.ReadMessage()
		if err != nil {
			return err
		}
		var msg haStreamMessage
		if err := json.Unmarshal(raw, &msg); err != nil {
			continue
		}
		if msg.Type != "result" || msg.ID != id {
			continue
		}
		if msg.Success != nil && !*msg.Success {
			return fmt.Errorf("home assistant websocket command %d failed", id)
		}
		return nil
	}
	return fmt.Errorf("timed out waiting for websocket result id=%d", id)
}
