# Home Assistant DeviceConnector

ThingsPanel `DeviceConnector` for Home Assistant. This is the new connector runtime implementation. The local directory keeps the historical `homeassistant-service` name, but the standalone repository should be `thingspanel-device-connector-homeassistant`.

## Install in ThingsPanel

> This is a third-party **service plugin for ThingsPanel**. It is not a Home Assistant add-on or a HACS integration, and nothing is installed into Home Assistant.

[![Install the Home Assistant plugin in your ThingsPanel.](./docs/images/install-in-thingspanel.svg)](https://r.thingspanel.cn/redirect/_change?redirect=apply%2Fplugin%3Ftab%3Dinstall%26plugin%3Dhomeassistant)

Click the button, enter the address of your ThingsPanel platform, and you are taken to **Application management → Plugin management → Plugin install**, with Home Assistant selected.

1. A **platform super administrator** installs the plugin once. The installation asks for no Home Assistant address or token.
2. Each **tenant** then adds its own connection point under **Device access → Third-party access → Home Assistant**, entering its own Home Assistant address and long-lived access token, and picks the entities to create as devices.

Other entry points: [plugin page in the Resource Center](https://r.thingspanel.cn/marketplace/plugins/homeassistant) · [signed plugin packages (GitHub Release `plugin-ha-v1.0.0`)](https://github.com/ThingsPanel/thingspanel-installer/releases/tag/plugin-ha-v1.0.0). The release assets are installed by the ThingsPanel installer and are not meant to be downloaded and run by hand. Your platform must run a ThingsPanel version that includes the plugin-install page.

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
- `HA_BASE_URL=http://127.0.0.1:8123`
- `HA_ACCESS_TOKEN=<home-assistant-long-lived-token>`

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
