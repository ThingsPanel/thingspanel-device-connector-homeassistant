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
	Interval time.Duration
	Mode     string // stream | poll | both
}

func startHomeAssistantTelemetry(ctx context.Context, handler *homeAssistantServiceHandler) {
	mode := strings.ToLower(strings.TrimSpace(envAny("HA_TELEMETRY_MODE", "stream")))
	if mode == "" {
		mode = "stream"
	}

	cfg := homeAssistantTelemetryConfig{
		Broker:   strings.TrimSpace(envAny("TP_MQTT_BROKER", "MQTT_BROKER")),
		Password: envAny("HA_MQTT_PASSWORD", "HOMEASSISTANT_MQTT_PASSWORD"),
		Interval: 0,
		Mode:     mode,
	}
	if raw := strings.TrimSpace(envAny("HA_TELEMETRY_INTERVAL_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			cfg.Interval = time.Duration(seconds) * time.Second
		}
	}
	if cfg.Broker == "" {
		slog.Info("home assistant telemetry disabled: missing TP_MQTT_BROKER")
		return
	}

	go runHomeAssistantTelemetryCoordinator(ctx, handler, cfg)
}

func runHomeAssistantTelemetryCoordinator(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	// One-time bootstrap so ThingsPanel has current values even before the first HA event.
	publishAllHomeAssistantTelemetry(ctx, handler, cfg)

	useStream := cfg.Mode == "stream" || cfg.Mode == "both"
	usePoll := cfg.Mode == "poll" || cfg.Mode == "both"

	if useStream {
		slog.Info("home assistant telemetry mode: HA state_changed websocket stream")
		go runHomeAssistantStateStream(ctx, handler, cfg)
	}
	if usePoll && cfg.Interval > 0 {
		slog.Info("home assistant telemetry mode: periodic poll", "interval", cfg.Interval)
		go runHomeAssistantTelemetryPoll(ctx, handler, cfg)
	} else if usePoll && cfg.Interval <= 0 {
		slog.Info("home assistant telemetry poll disabled: HA_TELEMETRY_INTERVAL_SECONDS not set")
	}

	if !useStream && !usePoll {
		slog.Warn("home assistant telemetry disabled: invalid HA_TELEMETRY_MODE", "mode", cfg.Mode)
		return
	}

	for {
		select {
		case <-ctx.Done():
			return
		case device := <-handler.immediateTelemCh:
			go func(dev sdk.DeviceAddRequest) {
				select {
				case <-ctx.Done():
				case <-time.After(500 * time.Millisecond):
					publishHomeAssistantDeviceTelemetry(ctx, handler, cfg, dev)
				}
			}(device)
		}
	}
}

func runHomeAssistantTelemetryPoll(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publishAllHomeAssistantTelemetry(ctx, handler, cfg)
		}
	}
}

func publishAllHomeAssistantTelemetry(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	for _, device := range handler.boundDevices() {
		publishHomeAssistantDeviceTelemetry(ctx, handler, cfg, device)
	}
}

func publishHomeAssistantDeviceTelemetry(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig, device sdk.DeviceAddRequest) {
	accessToken := strings.TrimSpace(device.AccessToken)
	if accessToken == "" {
		return
	}

	deviceCfg := handler.configFor(device.DeviceID)
	entityID := configString(deviceCfg, "entity_id", "")
	if entityID == "" {
		return
	}

	ha, err := handler.haClientFromConfig(deviceCfg)
	if err != nil {
		slog.Warn("home assistant telemetry client unavailable", "deviceID", device.DeviceID, "err", err)
		return
	}

	queryCtx, cancel := context.WithTimeout(ctx, homeAssistantCommandTimeout)
	defer cancel()

	state, err := ha.GetState(queryCtx, entityID)
	if err != nil {
		slog.Warn("home assistant telemetry state query failed", "deviceID", device.DeviceID, "entityID", entityID, "err", err)
		publishHomeAssistantStatusOnly(cfg, device.DeviceID, accessToken, false)
		return
	}
	publishHomeAssistantDeviceTelemetryFromState(ctx, handler, cfg, device, state)
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

	publishHomeAssistantStatus(mqttClient, device.DeviceID, true)

	observedAt := time.Now().UTC().Format(time.RFC3339)
	if raw := strings.TrimSpace(state.LastUpdated); raw != "" {
		observedAt = raw
	}

	payload := map[string]any{
		"ha_online":      true,
		"ha_entity_id":   entityID,
		"ha_state":       state.State,
		"ha_source":      "homeassistant-service",
		"ha_observed_at": observedAt,
	}
	if brightness, ok := state.Attributes["brightness"]; ok {
		payload["ha_brightness"] = brightness
	}

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

func publishHomeAssistantStatusOnly(cfg homeAssistantTelemetryConfig, deviceID, accessToken string, online bool) {
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
	token := client.Publish("devices/status/"+deviceID, 0, false, val)
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
