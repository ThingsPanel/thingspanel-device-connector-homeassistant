# Home Assistant DeviceConnector

ThingsPanel `DeviceConnector` for Home Assistant. This is the new connector runtime implementation. The local directory keeps the historical `homeassistant-service` name, but the standalone repository should be `thingspanel-device-connector-homeassistant`.

## Publish identity

- `connectorKey`: `homeassistant`
- Standalone repo: `github.com/thingspanel/thingspanel-device-connector-homeassistant`
- Suggested visibility: public

## Scope

- Discover Home Assistant entities through the HA REST API
- Control standard switch/light style entities through the internal MQTT command bridge
- Publish state/telemetry back to ThingsPanel on Home Assistant `state_changed` events
- Keep Home Assistant and Xiaomi connectors separated: HA manages entities inside Home Assistant, while `xiaomi` talks to Xiaomi cloud/LAN directly

## Runtime configuration

Required environment variables:

- `CONNECTOR_SERVICE_IDENTIFIER=homeassistant`
- `CONNECTOR_INSTANCE_ID=<connector-instance-id>`
- `THINGSPANEL_BACKEND_URL=http://127.0.0.1:4000`

When installed by ThingsPanel, each tenant configures its Home Assistant address and token in its own service access point. They are not plugin installation fields. `HA_BASE_URL` and `HA_ACCESS_TOKEN` are only for a standalone local smoke run.

MQTT bridge and telemetry:

- `TP_MQTT_BROKER=tcp://127.0.0.1:1883`
- `HA_TP_DEVICE_ID=<thingspanel-device-id>`
- `HA_MQTT_USERNAME=<thingspanel-device-number>`
- `HA_MQTT_PASSWORD=<optional>`
- `HA_ENTITY_ID=<entity-id-used-for-local-smoke-test>`
- Telemetry uses Home Assistant's authenticated `/api/websocket` `state_changed` subscription for real-time events.
- Home Assistant REST `/api/states/{entity_id}` remains available for discovery and control queries, but is not used as a telemetry poller.
- `ha_observed_at` is copied from Home Assistant's `last_updated`; ThingsPanel's MQTT ingestion timestamp and `devices/status/{device_id}` online state are maintained separately.

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

The connector publishes a command ACK to `devices/command/response/<message_id>` with top-level `result`, `message`, and `ts` fields. A successful ACK means the Home Assistant service call succeeded; it does not confirm the physical device reached the requested state.

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

## Device synchronization

The connector pulls `service/access/list` through the SDK platform client at startup and then every
`HA_DEVICE_SYNC_INTERVAL_SECONDS` (default 60, minimum 5, `0` = startup only). Devices created on the platform
afterwards are picked up on the next pull; changed device configs are applied. A missing, expired, revoked or
out-of-scope runtime identity (`sdk.IsAuthFailure`) stops the sync with one error log; temporary startup failures
retry with a delay capped at 60 seconds, and later errors retry at the normal interval. Status (counts and device numbers only) is served on
`HA_STATUS_ADDR` (default `:9002`, `off` disables) at `GET /sync-status`.
