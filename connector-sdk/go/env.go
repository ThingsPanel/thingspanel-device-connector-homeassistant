package connectorsdk

import (
	"os"
	"time"
)

// fromEnv reads ConnectorInfo from well-known environment variables injected
// by the K8S runner's ConfigMap.
//
// Variable mapping:
//
//	CONNECTOR_SERVICE_IDENTIFIER → ConnectorInfo.ServiceIdentifier
//	CONNECTOR_INSTANCE_ID        → ConnectorInfo.InstanceID
//	CONNECTOR_LISTEN_ADDR        → ConnectorInfo.ListenAddr    (default ":9001")
//	THINGSPANEL_BACKEND_URL      → ConnectorInfo.BackendURL
//	CONNECTOR_HEARTBEAT_INTERVAL → ConnectorInfo.HeartbeatInterval (default 30s)
func fromEnv() ConnectorInfo {
	interval := 30 * time.Second
	if raw := os.Getenv("CONNECTOR_HEARTBEAT_INTERVAL"); raw != "" {
		if d, err := time.ParseDuration(raw); err == nil {
			interval = d
		}
	}

	listenAddr := os.Getenv("CONNECTOR_LISTEN_ADDR")
	if listenAddr == "" {
		listenAddr = ":9001"
	}

	return ConnectorInfo{
		ServiceIdentifier: os.Getenv("CONNECTOR_SERVICE_IDENTIFIER"),
		InstanceID:        os.Getenv("CONNECTOR_INSTANCE_ID"),
		ListenAddr:        listenAddr,
		BackendURL:        os.Getenv("THINGSPANEL_BACKEND_URL"),
		HeartbeatInterval: interval,
	}
}
