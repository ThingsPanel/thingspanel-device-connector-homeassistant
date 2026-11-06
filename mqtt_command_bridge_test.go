package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestMQTTCommandResponsePayload(t *testing.T) {
	tests := []struct {
		name        string
		err         error
		result      float64
		message     string
		wantErrcode bool
	}{
		{name: "success", result: 0, message: "success"},
		{name: "failure", err: errors.New("command failed"), result: 1, message: "command failed", wantErrcode: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := marshalMQTTCommandResponse(tt.err)
			if err != nil {
				t.Fatalf("marshal response: %v", err)
			}

			var got map[string]any
			if err := json.Unmarshal(payload, &got); err != nil {
				t.Fatalf("decode response: %v", err)
			}
			if got["result"] != tt.result {
				t.Errorf("result = %v, want %v", got["result"], tt.result)
			}
			if got["message"] != tt.message {
				t.Errorf("message = %v, want %q", got["message"], tt.message)
			}
			if _, ok := got["ts"].(float64); !ok {
				t.Errorf("ts should be a JSON number, got %T", got["ts"])
			}
			if _, ok := got["values"]; ok {
				t.Error("response must not wrap fields in values")
			}
			if _, ok := got["device_id"]; ok {
				t.Error("response must not contain device_id")
			}
			if tt.wantErrcode {
				if got["errcode"] != "000" {
					t.Errorf("errcode = %v, want %q", got["errcode"], "000")
				}
			} else if _, ok := got["errcode"]; ok {
				t.Error("successful response must not contain errcode")
			}
		})
	}
}

func TestMQTTCommandResponseTopic(t *testing.T) {
	if got, want := mqttCommandResponseTopic("abc-123"), "devices/command/response/abc-123"; got != want {
		t.Fatalf("response topic = %q, want %q", got, want)
	}
}
