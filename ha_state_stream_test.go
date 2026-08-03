package main

import (
	"testing"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

func TestHomeAssistantWebSocketURL(t *testing.T) {
	got, err := homeAssistantWebSocketURL("http://100.121.58.108:8123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "ws://100.121.58.108:8123/api/websocket"
	if got != want {
		t.Fatalf("got %q want %q", got, want)
	}

	got, err = homeAssistantWebSocketURL("https://homeassistant.local:8123")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "wss://homeassistant.local:8123/api/websocket" {
		t.Fatalf("unexpected wss url: %q", got)
	}
}

func TestBoundDeviceForEntityID(t *testing.T) {
	handler := newHomeAssistantServiceHandler()
	handler.devices["dev-1"] = sdkDeviceAdd("dev-1", "switch.esp_01sdeng", "ha-switch-esp-01sdeng")

	dev, ok := handler.boundDeviceForEntityID("switch.esp_01sdeng")
	if !ok || dev.DeviceID != "dev-1" {
		t.Fatalf("expected bound device dev-1, got ok=%v device=%+v", ok, dev)
	}
	if _, ok := handler.boundDeviceForEntityID("switch.missing"); ok {
		t.Fatalf("expected missing entity to be unbound")
	}
}

func TestHomeAssistantStateOnline(t *testing.T) {
	for _, test := range []struct {
		state  string
		online bool
	}{
		{state: "on", online: true},
		{state: "28.2", online: true},
		{state: "off", online: true},
		{state: "unknown", online: false},
		{state: "unavailable", online: false},
	} {
		if got := homeAssistantStateOnline(homeAssistantState{State: test.state}); got != test.online {
			t.Errorf("state %q: got online=%v, want %v", test.state, got, test.online)
		}
	}
}

func sdkDeviceAdd(deviceID, entityID, deviceNumber string) sdk.DeviceAddRequest {
	return sdk.DeviceAddRequest{
		DeviceID: deviceID,
		DeviceConfig: map[string]any{
			"entity_id":     entityID,
			"device_number": deviceNumber,
			"base_url":      "http://127.0.0.1:8123",
			"token":         "token",
		},
	}
}
