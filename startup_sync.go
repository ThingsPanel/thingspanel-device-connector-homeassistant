package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

// startStartupSync polls the backend service-access list and replays OnDeviceAdd
// for every bound device. Service-access devices have no device_config_id, so the
// backend heartbeat bootstrap sync does not push them to the connector.
func startStartupSync(ctx context.Context, info sdk.ConnectorInfo, handler *homeAssistantServiceHandler) {
	backendURL := strings.TrimSpace(info.BackendURL)
	if backendURL == "" {
		slog.Warn("startup sync disabled: THINGSPANEL_BACKEND_URL not set")
		return
	}
	go func() {
		for attempt := 1; ; attempt++ {
			select {
			case <-ctx.Done():
				return
			case <-time.After(time.Duration(attempt) * 2 * time.Second):
			}
			if err := runStartupSync(ctx, backendURL, info.ServiceIdentifier, handler); err != nil {
				if attempt <= 5 {
					slog.Warn("startup sync failed, retrying", "attempt", attempt, "err", err)
					continue
				}
				slog.Error("startup sync gave up", "err", err)
			}
			return
		}
	}()
}

func runStartupSync(ctx context.Context, backendURL, serviceIdentifier string, handler *homeAssistantServiceHandler) error {
	url := backendURL + "/api/v1/plugin/service/access/list"
	body, _ := json.Marshal(map[string]string{"service_identifier": serviceIdentifier})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", url, err)
	}
	defer resp.Body.Close()

	var envelope struct {
		Code int             `json:"code"`
		Data json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	if envelope.Code != 200 || len(envelope.Data) == 0 || string(envelope.Data) == "null" {
		slog.Info("startup sync: no bound devices yet", "code", envelope.Code)
		return nil
	}

	var serviceAccesses []map[string]any
	if err := json.Unmarshal(envelope.Data, &serviceAccesses); err != nil {
		return fmt.Errorf("decode service accesses: %w", err)
	}

	synced := 0
	for _, sa := range serviceAccesses {
		svcVoucher := parseJSONStringField(sa["voucher"])
		devicesRaw, _ := sa["devices"].([]any)
		for _, devAny := range devicesRaw {
			dev, ok := devAny.(map[string]any)
			if !ok {
				continue
			}
			deviceID, _ := dev["id"].(string)
			if deviceID == "" {
				continue
			}
			deviceNumber, _ := dev["device_number"].(string)

			deviceCfg := make(map[string]any, len(svcVoucher)+4)
			for k, v := range svcVoucher {
				deviceCfg[k] = v
			}
			for k, v := range parseJSONStringField(dev["protocol_config"]) {
				deviceCfg[k] = v
			}
			if deviceNumber != "" {
				deviceCfg["device_number"] = deviceNumber
			}

			devVoucher := parseJSONStringField(dev["voucher"])
			accessToken, _ := devVoucher["username"].(string)

			if err := handler.OnDeviceAdd(ctx, sdk.DeviceAddRequest{
				DeviceID:     deviceID,
				DeviceConfig: deviceCfg,
				AccessToken:  accessToken,
			}); err != nil {
				slog.Error("startup sync: OnDeviceAdd failed", "deviceID", deviceID, "err", err)
				continue
			}
			synced++
		}
	}
	slog.Info("home assistant startup sync complete", "devicesSynced", synced)
	return nil
}

func parseJSONStringField(v any) map[string]any {
	s, ok := v.(string)
	if !ok || strings.TrimSpace(s) == "" {
		return map[string]any{}
	}
	var result map[string]any
	if err := json.Unmarshal([]byte(s), &result); err != nil {
		return map[string]any{}
	}
	return result
}
