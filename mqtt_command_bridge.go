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
	info    sdk.ConnectorInfo
	handler *homeAssistantServiceHandler
	broker  string
	logger  *slog.Logger
}

type mqttCommandPayload struct {
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func startMQTTCommandBridge(ctx context.Context, info sdk.ConnectorInfo, handler *homeAssistantServiceHandler) {
	broker := envAny("TP_MQTT_BROKER", "MQTT_BROKER")
	if broker == "" {
		slog.Warn("mqtt command bridge disabled: TP_MQTT_BROKER not set")
		return
	}
	bridge := &mqttCommandBridge{
		info:    info,
		handler: handler,
		broker:  broker,
		logger:  slog.Default(),
	}
	go bridge.run(ctx)
}

func (b *mqttCommandBridge) run(ctx context.Context) {
	opts := mqtt.NewClientOptions()
	opts.AddBroker(b.broker)
	opts.SetClientID(fmt.Sprintf("%s-homeassistant-command-%d", b.info.InstanceID, time.Now().UnixNano()))
	opts.SetCleanSession(true)
	opts.SetAutoReconnect(true)
	opts.SetConnectRetry(true)
	opts.SetKeepAlive(30 * time.Second)
	opts.SetDefaultPublishHandler(b.handleCommandMessage)
	if user := envAny("TP_MQTT_USER", "MQTT_USER", "MQTT_USERNAME"); user != "" {
		opts.SetUsername(user)
	}
	if pass := envAny("TP_MQTT_PASS", "MQTT_PASS", "MQTT_PASSWORD"); pass != "" {
		opts.SetPassword(pass)
	}

	pluginTopic := fmt.Sprintf("plugin/%s/devices/command/+/+", b.info.ServiceIdentifier)
	opts.SetConnectionLostHandler(func(_ mqtt.Client, err error) {
		b.logger.Warn("mqtt command bridge connection lost", "err", err)
	})
	opts.OnConnect = func(client mqtt.Client) {
		filters := map[string]byte{pluginTopic: 1}
		token := client.SubscribeMultiple(filters, nil)
		token.Wait()
		if err := token.Error(); err != nil {
			b.logger.Error("subscribe failed", "err", err)
			return
		}
		b.logger.Info("mqtt command bridge subscribed", "topics", []string{pluginTopic})
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
	if strings.HasPrefix(strings.Trim(msg.Topic(), "/"), "devices/command/response/") {
		return
	}
	messageID := haMessageIDFromTopic(msg.Topic())
	deviceNumber := haDeviceNumberFromTopic(msg.Topic())
	b.logger.Info(
		"mqtt command received",
		"topic", msg.Topic(),
		"deviceNumber", deviceNumber,
		"messageID", messageID,
		"payload", string(msg.Payload()),
	)

	req, err := b.decodeCommand(deviceNumber, msg.Payload())
	if err != nil {
		b.logger.Warn(
			"mqtt command decode failed",
			"topic", msg.Topic(),
			"deviceNumber", deviceNumber,
			"messageID", messageID,
			"err", err,
		)
		b.publishResponse(client, "", messageID, err)
		return
	}

	resp, err := b.handler.OnCommand(context.Background(), req)
	if err != nil {
		b.publishResponse(client, req.DeviceID, messageID, err)
		return
	}
	b.logger.Info("command handled", "deviceID", req.DeviceID, "message", resp.Message)
	b.publishResponse(client, req.DeviceID, messageID, nil)
}

func (b *mqttCommandBridge) decodeCommand(deviceNumber string, payload []byte) (sdk.CommandRequest, error) {
	deviceID := strings.TrimSpace(b.handler.deviceIDForNumber(deviceNumber))
	if deviceID == "" {
		return sdk.CommandRequest{}, fmt.Errorf("device number %s is not bound in connector", deviceNumber)
	}

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
	return sdk.CommandRequest{DeviceID: deviceID, Command: map[string]any{in.Method: params}}, nil
}

func (b *mqttCommandBridge) publishResponse(client mqtt.Client, deviceID, messageID string, err error) {
	values := map[string]any{"result": 0, "message": "success", "ts": time.Now().Unix()}
	if err != nil {
		values["result"] = 1
		values["errcode"] = "000"
		values["message"] = err.Error()
	}
	payload, _ := json.Marshal(map[string]any{"device_id": deviceID, "values": values})
	token := client.Publish("devices/command/response/"+messageID, 1, false, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		b.logger.Warn("mqtt command response publish failed", "messageID", messageID, "err", err)
	}
}

func haDeviceNumberFromTopic(topic string) string {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) < 2 {
		return ""
	}
	return parts[len(parts)-2]
}

func haMessageIDFromTopic(topic string) string {
	parts := strings.Split(strings.Trim(topic, "/"), "/")
	if len(parts) == 0 {
		return ""
	}
	return parts[len(parts)-1]
}
