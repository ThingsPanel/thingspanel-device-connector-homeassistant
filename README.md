# Home Assistant DeviceConnector

ThingsPanel `DeviceConnector` for Home Assistant. This is the new connector runtime implementation. The local directory keeps the historical `homeassistant-service` name, but the standalone repository should be `thingspanel-device-connector-homeassistant`.

## Publish identity

- `connectorKey`: `homeassistant`
- Standalone repo: `github.com/thingspanel/thingspanel-device-connector-homeassistant`
- Suggested visibility: public

## Scope

- Discover Home Assistant entities through the HA REST API
- Control standard switch/light style entities through the internal MQTT command bridge
- Publish state/telemetry back to ThingsPanel on HA `state_changed` events (passthrough frequency)
- Keep Home Assistant and Xiaomi connectors separated: HA manages entities inside Home Assistant, while `xiaomi` talks to Xiaomi cloud/LAN directly

## Runtime configuration

Required environment variables:

- `CONNECTOR_SERVICE_IDENTIFIER=homeassistant`
- `CONNECTOR_INSTANCE_ID=<connector-instance-id>`
- `THINGSPANEL_BACKEND_URL=http://127.0.0.1:4000`
- `HA_BASE_URL=http://127.0.0.1:8123`
- `HA_ACCESS_TOKEN=<home-assistant-long-lived-token>`

MQTT bridge and telemetry:

- `TP_MQTT_BROKER=tcp://127.0.0.1:1883`
- `HA_TP_DEVICE_ID=<thingspanel-device-id>`
- `HA_MQTT_USERNAME=<thingspanel-device-number>`
- `HA_MQTT_PASSWORD=<optional>`
- `HA_ENTITY_ID=<entity-id-used-for-local-smoke-test>`
- `HA_TELEMETRY_MODE=stream` — default; subscribe to Home Assistant `state_changed` websocket and publish immediately (same frequency as HA)
- `HA_TELEMETRY_MODE=poll` — legacy fixed-interval REST polling (only if you explicitly need it)
- `HA_TELEMETRY_MODE=both` — websocket stream plus optional poll fallback
- `HA_TELEMETRY_INTERVAL_SECONDS=30` — only used when mode is `poll` or `both`; leave unset in stream mode

## Local run

```bash
cd examples/device-connectors/homeassistant-service
go run .
curl http://127.0.0.1:9001/health
curl "http://127.0.0.1:9001/api/v1/form/config?form_type=SVCR"
```

## Standard commands

Topic:

```text
plugin/homeassistant/devices/command/<device_number>/<message_id>
```

Examples:

- `{"method":"switch","params":"on"}`
- `{"method":"switch","params":"off"}`
- `{"method":"brightness","params":35}`
- `{"method":"turn_on","params":{"effect":"Rainbow"}}`
- `{"method":"turn_on","params":{"rgb_color":[255,0,0],"brightness_pct":80}}`
- `{"method":"query","params":"state"}`

Local helper:

```bash
go run ./cmd/publish-command -service homeassistant -method query -params '"state"'
```

## Verification and deploy

```bash
go test ./...
```

Deploy helper in this monorepo:

```bash
cd encore-api
go run ./cmd/deploy-homeassistant-service
```
