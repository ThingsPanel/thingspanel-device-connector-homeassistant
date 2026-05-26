# ThingsPanel Device Connector SDK (Go)

Minimal Go SDK for building ThingsPanel device connectors. It handles HTTP
routing, heartbeat, and graceful shutdown so connector authors focus only on
device capability mapping.

## What the SDK does for you

- Registers all ThingsPanel HTTP callback routes (`/api/v1/form/config`,
  `/api/v1/device/add`, `/api/v1/device/disconnect`, etc.)
- Exposes `/health` for K8S readiness and liveness probes
- Sends `POST /api/v1/plugin/heartbeat` on a configurable interval
- Reads runtime identity from environment variables (injected by K8S runner)
- Handles graceful shutdown on SIGTERM

## What you write

1. **`device-connector.yaml`** — identity and runtime spec (image, port, MQTT prefix)
2. **`handler.go`** — implement `sdk.Handler` with your device's capability mapping

See `examples/device-connectors/xiaomi-plug/` for a complete example.

## Handler interface

```go
type Handler interface {
    FormConfig(ctx context.Context) (FormConfig, error)
    OnDeviceAdd(ctx context.Context, req DeviceAddRequest) error
    OnDeviceDelete(ctx context.Context, req DeviceDeleteRequest) error
    OnCommand(ctx context.Context, req CommandRequest) (CommandResponse, error)
    OnConfigUpdate(ctx context.Context, req ConfigUpdateRequest) error
    OnDisconnect(ctx context.Context, req DisconnectRequest) error
    OnEvent(ctx context.Context, ev EventNotification) error
}
```

## Environment variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `CONNECTOR_SERVICE_IDENTIFIER` | yes | — | Must match `device_connectors.connector_key` |
| `CONNECTOR_INSTANCE_ID` | yes | — | `ConnectorInstance.ID` from control plane |
| `CONNECTOR_LISTEN_ADDR` | no | `:9001` | HTTP bind address |
| `THINGSPANEL_BACKEND_URL` | yes* | — | Backend base URL for heartbeat |
| `CONNECTOR_HEARTBEAT_INTERVAL` | no | `30s` | How often to POST heartbeat |

*Heartbeat is disabled (with a warning) if `THINGSPANEL_BACKEND_URL` is empty.

## Local development

```bash
CONNECTOR_SERVICE_IDENTIFIER=my-connector \
CONNECTOR_INSTANCE_ID=local-dev \
THINGSPANEL_BACKEND_URL=http://localhost:9999 \
go run .
```

Then verify:

```bash
curl http://localhost:9001/health
curl http://localhost:9001/api/v1/form/config
```
