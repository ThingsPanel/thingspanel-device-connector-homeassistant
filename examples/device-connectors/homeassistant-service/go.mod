module github.com/thingspanel/thingspanel-device-connector-homeassistant

go 1.22

require (
	github.com/eclipse/paho.mqtt.golang v1.5.0
	github.com/thingspanel/device-connector-sdk-go v0.0.0
)

require (
	github.com/gorilla/websocket v1.5.3 // indirect
	golang.org/x/net v0.27.0 // indirect
	golang.org/x/sync v0.7.0 // indirect
)

replace github.com/thingspanel/device-connector-sdk-go => ../../../connector-sdk/go
