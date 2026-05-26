package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
	sdk "github.com/thingspanel/device-connector-sdk-go"
)

type mqttCommandBridge struct {
	info         sdk.ConnectorInfo
	handler      *homeAssistantServiceHandler
	broker       string
	deviceID     string
	deviceNumber string
	logger       *slog.Logger
}

type mqttCommandPayload struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func startMQTTCommandBridge(ctx context.Context, info sdk.ConnectorInfo, handler *homeAssistantServiceHandler) {
	bridge, err := newMQTTCommandBridge(info, handler)
	if err != nil {
		slog.Warn("mqtt command bridge disabled", "err", err)
		return
	}
	go bridge.run(ctx)
}

func newMQTTCommandBridge(info sdk.ConnectorInfo, handler *homeAssistantServiceHandler) (*mqttCommandBridge, error) {
	broker := envAny("TP_MQTT_BROKER", "MQTT_BROKER")
	deviceID := envAny("HA_TP_DEVICE_ID", "HOMEASSISTANT_TP_DEVICE_ID")
	deviceNumber := envAny("HA_MQTT_USERNAME", "HOMEASSISTANT_MQTT_USERNAME")
	if broker == "" || deviceID == "" || deviceNumber == "" {
		return nil, fmt.Errorf("TP_MQTT_BROKER, HA_TP_DEVICE_ID, and HA_MQTT_USERNAME are required")
	}
	return &mqttCommandBridge{info: info, handler: handler, broker: broker, deviceID: deviceID, deviceNumber: deviceNumber, logger: slog.Default()}, nil
}

func (b *mqttCommandBridge) run(ctx context.Context) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(b.broker)
	opts.SetClientID(fmt.Sprintf("%s-homeassistant-command-%d", b.info.InstanceID, time.Now().UnixNano()))
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	if user := envAny("TP_MQTT_USER", "MQTT_USER", "MQTT_USERNAME"); user != "" {
		opts.SetUsername(user)
	}
	if pass := envAny("TP_MQTT_PASS", "MQTT_PASS", "MQTT_PASSWORD"); pass != "" {
		opts.SetPassword(pass)
	}
	commandTopic := fmt.Sprintf("plugin/%s/devices/command/%s/+", b.info.ServiceIdentifier, b.deviceNumber)
	opts.OnConnect = func(client mqtt.Client) {
		token := client.Subscribe(commandTopic, 1, b.handleCommandMessage)
		token.Wait()
		if err := token.Error(); err != nil {
			b.logger.Error("subscribe failed", "topic", commandTopic, "err", err)
			return
		}
		b.logger.Info("mqtt command bridge subscribed", "topic", commandTopic)
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		b.logger.Error("mqtt connect failed", "err", err)
		return
	}
	defer client.Disconnect(250)
	<-ctx.Done()
}

func (b *mqttCommandBridge) handleCommandMessage(client mqtt.Client, msg mqtt.Message) {
	messageID := messageIDFromTopic(msg.Topic())
	req, err := b.decodeCommand(msg.Payload())
	if err != nil {
		b.publishResponse(client, messageID, err)
		return
	}
	resp, err := b.handler.OnCommand(context.Background(), req)
	if err != nil {
		b.publishResponse(client, messageID, err)
		return
	}
	b.logger.Info("command handled", "message", resp.Message)
	b.publishResponse(client, messageID, nil)
}

func (b *mqttCommandBridge) decodeCommand(payload []byte) (sdk.CommandRequest, error) {
	var in mqttCommandPayload
	if err := json.Unmarshal(payload, &in); err != nil {
		return sdk.CommandRequest{}, err
	}
	var params any
	if len(in.Params) > 0 && string(in.Params) != "null" {
		if err := json.Unmarshal(in.Params, &params); err != nil {
			return sdk.CommandRequest{}, err
		}
	}
	return sdk.CommandRequest{DeviceID: b.deviceID, Command: map[string]any{in.Method: params}}, nil
}

func (b *mqttCommandBridge) publishResponse(client mqtt.Client, messageID string, err error) {
	values := map[string]any{"result": 0, "message": "success", "ts": time.Now().Unix()}
	if err != nil {
		values["result"] = 1
		values["errcode"] = "000"
		values["message"] = err.Error()
	}
	payload, _ := json.Marshal(map[string]any{"device_id": b.deviceID, "values": values})
	token := client.Publish("devices/command/response/"+messageID, 1, false, payload)
	token.Wait()
}

func messageIDFromTopic(topic string) string {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
