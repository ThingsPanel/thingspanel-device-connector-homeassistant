package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

type homeAssistantTelemetryConfig struct {
	DeviceID     string
	DeviceNumber string
	Password     string
	Broker       string
	EntityID     string
	Interval     time.Duration
}

func startHomeAssistantTelemetry(ctx context.Context, handler *homeAssistantServiceHandler) {
	cfg := homeAssistantTelemetryConfig{
		DeviceID:     strings.TrimSpace(os.Getenv("HA_TP_DEVICE_ID")),
		DeviceNumber: strings.TrimSpace(os.Getenv("HA_MQTT_USERNAME")),
		Password:     os.Getenv("HA_MQTT_PASSWORD"),
		Broker:       strings.TrimSpace(os.Getenv("TP_MQTT_BROKER")),
		EntityID:     envAny("HA_ENTITY_ID", "HA_LIGHT_ENTITY_ID", "HA_YEELIGHT_ENTITY_ID"),
		Interval:     30 * time.Second,
	}
	if raw := strings.TrimSpace(os.Getenv("HA_TELEMETRY_INTERVAL_SECONDS")); raw != "" {
		if seconds, err := strconv.Atoi(raw); err == nil && seconds > 0 {
			cfg.Interval = time.Duration(seconds) * time.Second
		}
	}
	if handler.ha == nil || cfg.DeviceID == "" || cfg.DeviceNumber == "" || cfg.Broker == "" || cfg.EntityID == "" {
		slog.Info("home assistant telemetry disabled: missing HA client, HA_TP_DEVICE_ID, HA_MQTT_USERNAME, TP_MQTT_BROKER, or HA_ENTITY_ID")
		return
	}
	go runHomeAssistantTelemetry(ctx, handler, cfg)
}

func runHomeAssistantTelemetry(ctx context.Context, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(cfg.Broker)
	opts.SetClientID("homeassistant-telemetry-" + cfg.DeviceID)
	opts.SetUsername(cfg.DeviceNumber)
	if cfg.Password != "" {
		opts.SetPassword(cfg.Password)
	}
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)

	client := mqtt.NewClient(opts)
	if token := client.Connect(); !token.WaitTimeout(5*time.Second) || token.Error() != nil {
		if token.Error() != nil {
			slog.Warn("home assistant telemetry MQTT connect failed", "err", token.Error())
		} else {
			slog.Warn("home assistant telemetry MQTT connect timeout")
		}
		return
	}
	defer client.Disconnect(250)

	publishHomeAssistantTelemetry(ctx, client, handler, cfg)
	ticker := time.NewTicker(cfg.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			publishHomeAssistantTelemetry(ctx, client, handler, cfg)
		}
	}
}

func publishHomeAssistantTelemetry(ctx context.Context, client mqtt.Client, handler *homeAssistantServiceHandler, cfg homeAssistantTelemetryConfig) {
	queryCtx, cancel := context.WithTimeout(ctx, homeAssistantCommandTimeout)
	defer cancel()
	state, err := handler.ha.GetState(queryCtx, cfg.EntityID)
	if err != nil {
		slog.Warn("home assistant telemetry state query failed", "err", err)
		return
	}

	statusToken := client.Publish("devices/status/"+cfg.DeviceID, 0, false, []byte("1"))
	if !statusToken.WaitTimeout(3*time.Second) || statusToken.Error() != nil {
		slog.Warn("publish home assistant status failed", "err", statusToken.Error())
	}

	payload := map[string]any{
		"ha_online":      true,
		"ha_entity_id":   cfg.EntityID,
		"ha_state":       state.State,
		"ha_source":      "homeassistant-service",
		"ha_observed_at": time.Now().UTC().Format(time.RFC3339),
	}
	if brightness, ok := state.Attributes["brightness"]; ok {
		payload["ha_brightness"] = brightness
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		slog.Warn("marshal home assistant telemetry failed", "err", err)
		return
	}
	token := client.Publish("devices/telemetry", 0, false, raw)
	if !token.WaitTimeout(3*time.Second) || token.Error() != nil {
		slog.Warn("publish home assistant telemetry failed", "err", token.Error())
		return
	}
	slog.Info("home assistant telemetry published", "deviceID", cfg.DeviceID, "entityID", cfg.EntityID, "state", state.State)
}
