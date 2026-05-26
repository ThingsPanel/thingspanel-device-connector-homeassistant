package connectorsdk_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

// ─── Stub handler ─────────────────────────────────────────────────────────────

type stubHandler struct {
	addCalled        bool
	deleteCalled     bool
	commandCalled    bool
	configCalled     bool
	disconnectCalled bool
	eventCalled      bool
	listCalled       bool
	lastFormRequest  sdk.FormConfigRequest
}

func (h *stubHandler) FormConfig(_ context.Context) (sdk.FormConfig, error) {
	return sdk.FormConfig{Schema: map[string]any{
		"type": "object",
		"x-order": []string{
			"listen_host",
			"ack_enabled",
			"factor_mappings",
			"mn_device_map",
		},
		"properties": map[string]any{
			"listen_host": map[string]any{
				"type":    "string",
				"title":   "监听地址",
				"default": "0.0.0.0",
			},
			"ack_enabled": map[string]any{
				"type":    "boolean",
				"title":   "启用应答",
				"default": true,
			},
			"factor_mappings": map[string]any{
				"type":  "array",
				"title": "因子映射",
				"default": []map[string]any{{
					"code": "a01001",
					"key":  "cod",
				}},
				"items": map[string]any{
					"type": "object",
					"properties": map[string]any{
						"code": map[string]any{"type": "string", "title": "因子代码"},
						"key":  map[string]any{"type": "string", "title": "遥测键名"},
					},
					"required": []string{"code", "key"},
				},
			},
			"mn_device_map": map[string]any{
				"type":    "object",
				"title":   "MN 映射",
				"default": map[string]any{"MN001": "dev-001"},
			},
		},
	}}, nil
}

func (h *stubHandler) FormConfigFor(_ context.Context, req sdk.FormConfigRequest) (sdk.FormConfig, error) {
	h.lastFormRequest = req
	if req.FormType == "CFG" {
		return sdk.FormConfig{Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"mn": map[string]any{
					"type":    "string",
					"title":   "MN",
					"default": "MN001",
				},
			},
		}}, nil
	}
	return h.FormConfig(context.Background())
}

func (h *stubHandler) RawFormDataFor(_ context.Context, req sdk.FormConfigRequest) (any, bool, error) {
	if req.FormType == "VCRT" {
		return map[string]any{
			"注册包(ASCII)": "REGISTER_ASCII",
		}, true, nil
	}
	return nil, false, nil
}
func (h *stubHandler) OnDeviceAdd(_ context.Context, _ sdk.DeviceAddRequest) error {
	h.addCalled = true
	return nil
}
func (h *stubHandler) OnDeviceDelete(_ context.Context, _ sdk.DeviceDeleteRequest) error {
	h.deleteCalled = true
	return nil
}
func (h *stubHandler) OnCommand(_ context.Context, _ sdk.CommandRequest) (sdk.CommandResponse, error) {
	h.commandCalled = true
	return sdk.CommandResponse{OK: true}, nil
}
func (h *stubHandler) OnConfigUpdate(_ context.Context, _ sdk.ConfigUpdateRequest) error {
	h.configCalled = true
	return nil
}
func (h *stubHandler) OnDisconnect(_ context.Context, _ sdk.DisconnectRequest) error {
	h.disconnectCalled = true
	return nil
}
func (h *stubHandler) OnEvent(_ context.Context, _ sdk.EventNotification) error {
	h.eventCalled = true
	return nil
}
func (h *stubHandler) ListDevices(_ context.Context, req sdk.DeviceListRequest) (sdk.DeviceListResponse, error) {
	h.listCalled = true
	return sdk.DeviceListResponse{
		Total: 1,
		List: []sdk.DiscoveredDevice{{
			DeviceName:   "Lamp",
			DeviceNumber: "lamp-001",
		}},
	}, nil
}

// ─── Test helpers ─────────────────────────────────────────────────────────────

func newTestServer(t *testing.T) (*sdk.Server, *stubHandler) {
	t.Helper()
	h := &stubHandler{}
	info := sdk.ConnectorInfo{
		ServiceIdentifier: "test-connector",
		InstanceID:        "inst-test",
		ListenAddr:        ":0", // won't actually bind in test
	}
	return sdk.NewServer(info, h), h
}

func doRequest(t *testing.T, srv *sdk.Server, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequest(method, path, bodyReader)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.ServeHTTP(rr, req)
	return rr
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestServer_Health(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/health", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /health: expected 200, got %d", rr.Code)
	}
	var resp map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse health response: %v", err)
	}
	if resp["status"] != "ok" {
		t.Errorf("expected status=ok, got %q", resp["status"])
	}
	if resp["serviceIdentifier"] != "test-connector" {
		t.Errorf("expected serviceIdentifier=test-connector, got %q", resp["serviceIdentifier"])
	}
}

func TestServer_FormConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/form/config", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/form/config: expected 200, got %d", rr.Code)
	}
	var cfg sdk.FormConfig
	if err := json.Unmarshal(rr.Body.Bytes(), &cfg); err != nil {
		t.Fatalf("parse form config: %v", err)
	}
	if cfg.Schema == nil {
		t.Error("expected non-nil schema")
	}
}

func TestServer_LegacyFormConfig(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/form/config?form_type=SVCR", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET legacy form config: expected 200, got %d", rr.Code)
	}
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			DataKey string `json:"dataKey"`
			Type    string `json:"type"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse legacy form config: %v", err)
	}
	if resp.Code != 200 {
		t.Fatalf("expected code=200, got %d", resp.Code)
	}
	if len(resp.Data) != 4 {
		t.Fatalf("expected 4 legacy form elements, got %d", len(resp.Data))
	}
	expected := []struct {
		key string
		typ string
	}{
		{"listen_host", "input"},
		{"ack_enabled", "switch"},
		{"factor_mappings", "table"},
		{"mn_device_map", "kv-table"},
	}
	for i, want := range expected {
		if resp.Data[i].DataKey != want.key || resp.Data[i].Type != want.typ {
			t.Fatalf("element %d = (%s, %s), want (%s, %s)", i, resp.Data[i].DataKey, resp.Data[i].Type, want.key, want.typ)
		}
	}
}

func TestServer_FormConfigByType(t *testing.T) {
	srv, h := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/form/config?protocol_type=hj212&device_type=2&form_type=CFG&voucher_type=device", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET typed form config: expected 200, got %d", rr.Code)
	}
	if h.lastFormRequest.FormType != "CFG" || h.lastFormRequest.ProtocolType != "hj212" || h.lastFormRequest.DeviceType != "2" || h.lastFormRequest.VoucherType != "device" {
		t.Fatalf("unexpected form request: %#v", h.lastFormRequest)
	}
	var resp struct {
		Code int `json:"code"`
		Data []struct {
			DataKey string `json:"dataKey"`
			Label   string `json:"label"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse typed form config: %v", err)
	}
	if resp.Code != 200 || len(resp.Data) != 1 || resp.Data[0].DataKey != "mn" {
		t.Fatalf("unexpected typed form response: %#v", resp)
	}
}

func TestServer_RawVoucherTypeForm(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/form/config?protocol_type=hj212&device_type=2&form_type=VCRT", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET raw voucher type form: expected 200, got %d", rr.Code)
	}
	var resp struct {
		Code int            `json:"code"`
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse raw voucher type response: %v", err)
	}
	if resp.Code != 200 || resp.Data["注册包(ASCII)"] != "REGISTER_ASCII" {
		t.Fatalf("unexpected raw voucher type response: %#v", resp)
	}
}

func TestServer_PluginDeviceList(t *testing.T) {
	srv, h := newTestServer(t)
	rr := doRequest(t, srv, http.MethodGet, "/api/v1/plugin/device/list?voucher=%7B%7D&page=1&page_size=10", "")

	if rr.Code != http.StatusOK {
		t.Fatalf("GET plugin device list: expected 200, got %d", rr.Code)
	}
	if !h.listCalled {
		t.Fatal("expected ListDevices to be called")
	}
	var resp struct {
		Code int `json:"code"`
		Data struct {
			Total int `json:"total"`
			List  []struct {
				DeviceName   string `json:"device_name"`
				DeviceNumber string `json:"device_number"`
			} `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse plugin device list: %v", err)
	}
	if resp.Code != 200 || resp.Data.Total != 1 || resp.Data.List[0].DeviceNumber != "lamp-001" {
		t.Fatalf("unexpected response: %#v", resp)
	}
}

func TestServer_DeviceAdd(t *testing.T) {
	srv, h := newTestServer(t)
	body := `{"device_id":"dev-001","device_config":{}}`
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/add", body)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/device/add: expected 200, got %d (body: %s)", rr.Code, rr.Body)
	}
	if !h.addCalled {
		t.Error("expected OnDeviceAdd to be called")
	}
}

func TestServer_DeviceDelete(t *testing.T) {
	srv, h := newTestServer(t)
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/delete", `{"device_id":"dev-001"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/device/delete: expected 200, got %d", rr.Code)
	}
	if !h.deleteCalled {
		t.Error("expected OnDeviceDelete to be called")
	}
}

func TestServer_Command(t *testing.T) {
	srv, h := newTestServer(t)
	body := `{"device_id":"dev-001","command":{"switch":"on"}}`
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/command", body)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/device/command: expected 200, got %d (body: %s)", rr.Code, rr.Body)
	}
	if !h.commandCalled {
		t.Error("expected OnCommand to be called")
	}
	var resp sdk.CommandResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse command response: %v", err)
	}
	if !resp.OK {
		t.Error("expected command response OK=true")
	}
}

func TestServer_Disconnect(t *testing.T) {
	srv, h := newTestServer(t)
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/disconnect", `{"device_id":"dev-001"}`)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/device/disconnect: expected 200, got %d", rr.Code)
	}
	if !h.disconnectCalled {
		t.Error("expected OnDisconnect to be called")
	}
}

func TestServer_ConfigUpdate(t *testing.T) {
	srv, h := newTestServer(t)
	body := `{"device_id":"dev-001","device_config":{"token":"abc"}}`
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/config/update", body)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/device/config/update: expected 200, got %d", rr.Code)
	}
	if !h.configCalled {
		t.Error("expected OnConfigUpdate to be called")
	}
}

func TestServer_Event(t *testing.T) {
	srv, h := newTestServer(t)
	body := `{"event_type":"test","device_id":"dev-001"}`
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/notify/event", body)

	if rr.Code != http.StatusOK {
		t.Fatalf("POST /api/v1/notify/event: expected 200, got %d", rr.Code)
	}
	if !h.eventCalled {
		t.Error("expected OnEvent to be called")
	}
}

func TestServer_BadJSON_Returns400(t *testing.T) {
	srv, _ := newTestServer(t)
	rr := doRequest(t, srv, http.MethodPost, "/api/v1/device/add", "not-json")

	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400 for bad JSON, got %d", rr.Code)
	}
}

// ─── ConnectorInfo env loading ────────────────────────────────────────────────

func TestFromEnv_Defaults(t *testing.T) {
	// No env vars set — check defaults.
	t.Setenv("CONNECTOR_SERVICE_IDENTIFIER", "")
	t.Setenv("CONNECTOR_INSTANCE_ID", "")
	t.Setenv("CONNECTOR_LISTEN_ADDR", "")
	t.Setenv("THINGSPANEL_BACKEND_URL", "")
	t.Setenv("CONNECTOR_HEARTBEAT_INTERVAL", "")

	info := sdk.FromEnv()
	if info.ListenAddr != ":9001" {
		t.Errorf("expected default ListenAddr :9001, got %q", info.ListenAddr)
	}
	if info.HeartbeatInterval.Seconds() != 30 {
		t.Errorf("expected default 30s heartbeat, got %v", info.HeartbeatInterval)
	}
}

func TestFromEnv_CustomValues(t *testing.T) {
	t.Setenv("CONNECTOR_SERVICE_IDENTIFIER", "xiaomi-plug")
	t.Setenv("CONNECTOR_INSTANCE_ID", "inst-abc")
	t.Setenv("CONNECTOR_LISTEN_ADDR", ":8080")
	t.Setenv("THINGSPANEL_BACKEND_URL", "http://backend:9999")
	t.Setenv("CONNECTOR_HEARTBEAT_INTERVAL", "10s")

	info := sdk.FromEnv()
	if info.ServiceIdentifier != "xiaomi-plug" {
		t.Errorf("expected xiaomi-plug, got %q", info.ServiceIdentifier)
	}
	if info.ListenAddr != ":8080" {
		t.Errorf("expected :8080, got %q", info.ListenAddr)
	}
	if info.HeartbeatInterval.Seconds() != 10 {
		t.Errorf("expected 10s, got %v", info.HeartbeatInterval)
	}
}
