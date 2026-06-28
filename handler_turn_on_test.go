package main

import "testing"

func TestParseTurnOnParams(t *testing.T) {
	params, err := parseTurnOnParams(map[string]any{"effect": "Rainbow"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if params["effect"] != "Rainbow" {
		t.Fatalf("unexpected params: %#v", params)
	}
}

func TestParseTurnOnParamsRejectsEmpty(t *testing.T) {
	if _, err := parseTurnOnParams(map[string]any{}); err == nil {
		t.Fatal("expected error for empty params")
	}
}

func TestAppendHomeAssistantLightAttributes(t *testing.T) {
	payload := map[string]any{"ha_state": "on"}
	appendHomeAssistantLightAttributes(payload, map[string]any{
		"effect":      "Solid",
		"effect_list": []any{"Solid", "Rainbow"},
		"color_mode":  "rgb",
	})
	if payload["ha_effect"] != "Solid" {
		t.Fatalf("expected ha_effect, got %#v", payload["ha_effect"])
	}
	if _, ok := payload["ha_effect_list"]; !ok {
		t.Fatalf("expected ha_effect_list in payload: %#v", payload)
	}
}
