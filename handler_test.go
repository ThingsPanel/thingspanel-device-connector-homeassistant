package main

import "testing"

func TestNormalizedDeviceNumberWithinLimit(t *testing.T) {
	got := normalizedDeviceNumber("ha", "light.yeelink_mono1_d9b1")
	if len(got) > 36 {
		t.Fatalf("device_number exceeds 36 chars: %q", got)
	}
	if got != "ha-light-yeelink-mono1-d9b1" {
		t.Fatalf("unexpected short device_number: %q", got)
	}
}

func TestNormalizedDeviceNumberLongEntitiesAreUnique(t *testing.T) {
	entities := []string{
		"sensor.zhang_jun_hong_s_iphone_17_ssid",
		"sensor.zhang_jun_hong_s_iphone_17_pressure",
		"sensor.zhang_jun_hong_s_iphone_17_battery_state",
		"sensor.zhang_jun_hong_s_iphone_17_average_active_pace",
	}
	seen := map[string]string{}
	for _, entityID := range entities {
		got := normalizedDeviceNumber("ha", entityID)
		if len(got) > 36 {
			t.Fatalf("device_number exceeds 36 chars for %q: %q", entityID, got)
		}
		if other, ok := seen[got]; ok {
			t.Fatalf("collision for %q and %q: %q", entityID, other, got)
		}
		seen[got] = entityID
	}
}

func TestNormalizedDeviceNumberIsDeterministic(t *testing.T) {
	entityID := "sensor.zhang_jun_hong_s_iphone_17_ssid"
	first := normalizedDeviceNumber("ha", entityID)
	second := normalizedDeviceNumber("ha", entityID)
	if first != second {
		t.Fatalf("expected deterministic device_number, got %q and %q", first, second)
	}
}

func TestBrightnessPercentFromFloat(t *testing.T) {
	got, ok := brightnessPercentFromFloat(128)
	if !ok {
		t.Fatal("expected brightness conversion ok")
	}
	if got != 50 {
		t.Fatalf("expected 128 to map to 50%%, got %d", got)
	}
}
