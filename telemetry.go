package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	sdk "github.com/thingspanel/device-connector-sdk-go"
)

var haTelemetryMQTTClients = struct {
	mu      sync.Mutex
	clients map[string]mqtt.Client
}{clients: map[string]mqtt.Client{}}

type homeAssistantTelemetryConfig struct {
	Broker   string
	Password string
}

func startHomeAssistantTelemetry(ctx context.Context, handler *homeAssistantServiceHandler) {
	cfg := homeAssistantTelemetryConfig{
		Broker:   strings.TrimSpace(envAny("TP_MQTT_BROKER", "MQTT_BROKER")),
		Password: envAny("HA_MQTT_PASSWORD", "HOMEASSISTANT_MQTT_PASSWORD"),
	}
	if cfg.Broker == "" {
		slog.Info("home assistant telemetry disabled: missing TP_MQTT_BROKER")
		return
	}

	slog.Info("home assistant telemetry mode: HA state_changed websocket stream")
	go runHomeAssistantStateStream(ctx, handler, cfg)
	go shutdownHomeAssistantMQTTClientsOnCancel(ctx)
}

func publishHomeAssistantDeviceTelemetryFromState(
	ctx context.Context,
	handler *homeAssistantServiceHandler,
	cfg homeAssistantTelemetryConfig,
	device sdk.DeviceAddRequest,
	state homeAssistantState,
) {
	accessToken := strings.TrimSpace(device.AccessToken)
	if accessToken == "" {
		return
	}

	deviceCfg := handler.configFor(device.DeviceID)
	entityID := configString(deviceCfg, "entity_id", "")
	if entityID == "" {
		entityID = strings.TrimSpace(state.EntityID)
	}
	if entityID == "" {
		return
	}

	mqttClient, err := homeAssistantMQTTClient(cfg.Broker, device.DeviceID, accessToken, cfg.Password)
	if err != nil {
		slog.Warn("home assistant telemetry MQTT connect failed", "deviceID", device.DeviceID, "err", err)
		return
	}

	observedAt := time.Now().UTC().Format(time.RFC3339)
	if raw := strings.TrimSpace(state.LastUpdated); raw != "" {
		observedAt = raw
	}

	payload := map[string]any{
		"ha_domain":      entityDomain(entityID),
		"ha_entity_id":   entityID,
		"ha_state":       haStateValue(state.State),
		"ha_source":      "homeassistant-service",
		"ha_observed_at": observedAt,
	}
	if brightness, ok := state.Attributes["brightness"]; ok {
		payload["ha_brightness"] = brightness
		if pct, ok := brightnessPercent(brightness); ok {
			payload["ha_brightness_pct"] = pct
		}
	}
	appendHomeAssistantLightAttributes(payload, state.Attributes)

	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("marshal home assistant telemetry failed", "deviceID", device.DeviceID, "err", err)
		return
	}

	token := mqttClient.Publish("devices/telemetry", 0, false, raw)
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		slog.Warn("publish home assistant telemetry failed", "deviceID", device.DeviceID, "err", token.Error())
		return
	}
	slog.Info(
		"home assistant telemetry published",
		"deviceID", device.DeviceID,
		"entityID", entityID,
		"state", state.State,
	)
}

// haStateValue converts a Home Assistant state string to a float64 when it
// represents a number (e.g. sensor readings like "69.3"), so ThingsPanel
// stores it in number_v and can render it on a trend/curve chart. HA's API
// always reports state as a string, even for numeric sensors, so non-numeric
// states (e.g. "on"/"off"/"unavailable") are left as-is.
func haStateValue(raw string) any {
	if f, err := strconv.ParseFloat(raw, 64); err == nil {
		return f
	}
	return raw
}

func brightnessPercent(value any) (int, bool) {
	switch raw := value.(type) {
	case float64:
		return brightnessPercentFromFloat(raw)
	case int:
		return brightnessPercentFromFloat(float64(raw))
	case int64:
		return brightnessPercentFromFloat(float64(raw))
	case json.Number:
		parsed, err := raw.Float64()
		if err != nil {
			return 0, false
		}
		return brightnessPercentFromFloat(parsed)
	default:
		return 0, false
	}
}

func brightnessPercentFromFloat(value float64) (int, bool) {
	if value < 0 || value > 255 {
		return 0, false
	}
	return int((value / 255.0 * 100.0) + 0.5), true
}

// appendHomeAssistantLightAttributes copies common HA light state attributes into
// telemetry using an ha_* prefix. Works for any light entity (WLED, Yeelight, etc.).
func appendHomeAssistantLightAttributes(payload map[string]any, attrs map[string]any) {
	if payload == nil || attrs == nil {
		return
	}
	for _, key := range []string{
		"effect",
		"color_mode",
		"color_temp",
		"color_temp_kelvin",
		"rgb_color",
		"hs_color",
		"xy_color",
		"color_name",
		"supported_color_modes",
		"effect_list",
		"min_color_temp_kelvin",
		"max_color_temp_kelvin",
	} {
		value, ok := attrs[key]
		if !ok || value == nil {
			continue
		}
		payload["ha_"+key] = value
	}
}

func publishHomeAssistantStatusOnly(cfg homeAssistantTelemetryConfig, deviceID, accessToken string, online bool) {
	if strings.TrimSpace(accessToken) == "" {
		return
	}
	client, err := homeAssistantMQTTClient(cfg.Broker, deviceID, accessToken, cfg.Password)
	if err != nil {
		return
	}
	publishHomeAssistantStatus(client, deviceID, online)
}

func publishHomeAssistantStatus(client mqtt.Client, deviceID string, online bool) {
	val := []byte("0")
	if online {
		val = []byte("1")
	}
	token := client.Publish("devices/status/"+deviceID, 1, true, val)
	token.WaitTimeout(3 * time.Second)
}

func homeAssistantMQTTClient(broker, deviceID, accessToken, password string) (mqtt.Client, error) {
	key := deviceID + ":" + accessToken
	haTelemetryMQTTClients.mu.Lock()
	if c, ok := haTelemetryMQTTClients.clients[key]; ok && c != nil && c.IsConnected() {
		haTelemetryMQTTClients.mu.Unlock()
		return c, nil
	}
	haTelemetryMQTTClients.mu.Unlock()

	opts := mqtt.NewClientOptions()
	opts.AddBroker(broker)
	opts.SetClientID("homeassistant-telemetry-" + deviceID)
	opts.SetUsername(accessToken)
	if password != "" {
		opts.SetPassword(password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetWill("devices/status/"+deviceID, "0", 1, true)

	client := mqtt.NewClient(opts)
	token := client.Connect()
	if !token.WaitTimeout(5 * time.Second) {
		return nil, context.DeadlineExceeded
	}
	if err := token.Error(); err != nil {
		return nil, err
	}

	haTelemetryMQTTClients.mu.Lock()
	haTelemetryMQTTClients.clients[key] = client
	haTelemetryMQTTClients.mu.Unlock()
	return client, nil
}

func shutdownHomeAssistantMQTTClientsOnCancel(ctx context.Context) {
	<-ctx.Done()

	haTelemetryMQTTClients.mu.Lock()
	clients := make(map[string]mqtt.Client, len(haTelemetryMQTTClients.clients))
	for key, client := range haTelemetryMQTTClients.clients {
		clients[key] = client
	}
	haTelemetryMQTTClients.mu.Unlock()

	for key, client := range clients {
		deviceID := strings.SplitN(key, ":", 2)[0]
		if client == nil || !client.IsConnected() {
			continue
		}
		token := client.Publish("devices/status/"+deviceID, 1, true, []byte("0"))
		token.WaitTimeout(3 * time.Second)
		client.Disconnect(250)
	}
}
