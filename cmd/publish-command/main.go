package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"time"

	mqtt "github.com/eclipse/paho.mqtt.golang"
)

func main() {
	var broker, service, deviceNumber, method, params, user, pass string
	flag.StringVar(&broker, "broker", "tcp://127.0.0.1:1883", "MQTT broker")
	flag.StringVar(&service, "service", "homeassistant", "connector service identifier")
	flag.StringVar(&deviceNumber, "device", "ha-yeelight-f0b4290ed9b1", "ThingsPanel device number")
	flag.StringVar(&method, "method", "switch", "command method")
	flag.StringVar(&params, "params", `"on"`, "raw JSON params")
	flag.StringVar(&user, "user", "", "MQTT username")
	flag.StringVar(&pass, "pass", "", "MQTT password")
	flag.Parse()

	var raw json.RawMessage = []byte(params)
	payload, _ := json.Marshal(map[string]any{"method": method, "params": raw})
	messageID := fmt.Sprintf("codex-%d", time.Now().UnixNano())
	topic := fmt.Sprintf("plugin/%s/devices/command/%s/%s", service, deviceNumber, messageID)

	opts := mqtt.NewClientOptions().AddBroker(broker).SetClientID(messageID)
	if user != "" {
		opts.SetUsername(user)
	}
	if pass != "" {
		opts.SetPassword(pass)
	}
	client := mqtt.NewClient(opts)
	token := client.Connect()
	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatal(err)
	}
	defer client.Disconnect(250)
	token = client.Publish(topic, 1, false, payload)
	token.Wait()
	if err := token.Error(); err != nil {
		log.Fatal(err)
	}
	fmt.Println(topic)
	fmt.Println(string(payload))
}
