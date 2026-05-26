// Package connectorsdk provides the minimal foundation for building ThingsPanel
// device connectors. It handles HTTP routing, health checks, and heartbeat so
// connector implementations focus only on device capability mapping.
package connectorsdk

import "time"

// FormConfig is the JSON form schema returned to the ThingsPanel frontend
// to render the connector's configuration UI.
type FormConfig struct {
	// Schema is a JSON Schema object describing the access-point form fields.
	// This form is rendered on the tenant access-point step before per-device
	// discovery, template selection, and binding.
	// Use map[string]any to allow arbitrary schema structure.
	Schema map[string]any `json:"schema"`
	// UISchema is an optional react-jsonschema-form ui:schema object.
	UISchema map[string]any `json:"uiSchema,omitempty"`
}

// FormConfigRequest carries the legacy ThingsPanel query parameters for form
// lookups. Connectors can use this to serve different schemas for access-point
// configuration versus per-device protocol configuration.
type FormConfigRequest struct {
	ProtocolType string `json:"protocol_type,omitempty"`
	DeviceType   string `json:"device_type,omitempty"`
	FormType     string `json:"form_type,omitempty"`
	VoucherType  string `json:"voucher_type,omitempty"`
}

// DeviceAddRequest is sent by ThingsPanel when a device is bound to this connector.
type DeviceAddRequest struct {
	DeviceID     string         `json:"device_id"`
	DeviceConfig map[string]any `json:"device_config"`
	// AccessToken is the device's MQTT credential if applicable.
	AccessToken string `json:"access_token,omitempty"`
}

// DeviceListRequest is sent by the legacy ThingsPanel backend when the user
// reaches the service access device binding step.
type DeviceListRequest struct {
	Voucher           string `json:"voucher"`
	ServiceIdentifier string `json:"service_identifier,omitempty"`
	Page              int    `json:"page"`
	PageSize          int    `json:"page_size"`
}

// DiscoveredDevice is one selectable device returned by a service connector.
type DiscoveredDevice struct {
	DeviceName     string `json:"device_name"`
	DeviceNumber   string `json:"device_number"`
	Description    string `json:"description,omitempty"`
	IsBind         bool   `json:"is_bind,omitempty"`
	DeviceConfigID string `json:"device_config_id,omitempty"`
	Voucher        string `json:"voucher,omitempty"`
	ProtocolConfig string `json:"protocol_config,omitempty"`
	AdditionalInfo string `json:"additional_info,omitempty"`
}

// DeviceListResponse is the legacy ThingsPanel list envelope payload.
type DeviceListResponse struct {
	Total int                `json:"total"`
	List  []DiscoveredDevice `json:"list"`
}

// DeviceDeleteRequest is sent when a device is removed from this connector.
type DeviceDeleteRequest struct {
	DeviceID string `json:"device_id"`
}

// ConfigUpdateRequest is sent when a device's configuration changes.
type ConfigUpdateRequest struct {
	DeviceID      string         `json:"device_id"`
	DeviceConfig  map[string]any `json:"device_config"`
	CurrentConfig map[string]any `json:"current_config,omitempty"`
}

// DisconnectRequest is sent when a device loses connectivity.
type DisconnectRequest struct {
	DeviceID string `json:"device_id"`
}

// CommandRequest carries a downlink command from ThingsPanel to the connector.
type CommandRequest struct {
	DeviceID string         `json:"device_id"`
	Command  map[string]any `json:"command"`
}

// CommandResponse is returned by the connector after processing a command.
type CommandResponse struct {
	// OK indicates whether the command was accepted.
	OK bool `json:"ok"`
	// Message is an optional human-readable result.
	Message string `json:"message,omitempty"`
}

// EventNotification is a generic platform event forwarded to the connector.
type EventNotification struct {
	EventType string         `json:"event_type"`
	DeviceID  string         `json:"device_id,omitempty"`
	Payload   map[string]any `json:"payload,omitempty"`
}

// ConnectorInfo carries the runtime identity of this connector process.
// Injected via environment variables by the K8S runner.
type ConnectorInfo struct {
	// ServiceIdentifier must match device_connectors.connector_key.
	ServiceIdentifier string
	// InstanceID is the ConnectorInstance.ID from the control plane.
	InstanceID string
	// ListenAddr is the HTTP address to bind. Default: ":9001".
	ListenAddr string
	// BackendURL is the ThingsPanel backend base URL for heartbeat calls.
	BackendURL string
	// HeartbeatInterval is how often to POST /api/v1/plugin/heartbeat.
	HeartbeatInterval time.Duration
}

// FromEnv populates ConnectorInfo from standard environment variables.
//
// Expected env vars:
//
//	CONNECTOR_SERVICE_IDENTIFIER  — required
//	CONNECTOR_INSTANCE_ID         — required
//	CONNECTOR_LISTEN_ADDR         — optional, default ":9001"
//	THINGSPANEL_BACKEND_URL       — required for heartbeat
//	CONNECTOR_HEARTBEAT_INTERVAL  — optional, default "30s"
func FromEnv() ConnectorInfo {
	return fromEnv()
}
