package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	sdk "github.com/thingspanel/device-connector-sdk-go"
)

func main() {
	info := sdk.FromEnv()
	if info.ServiceIdentifier == "" {
		slog.Error("CONNECTOR_SERVICE_IDENTIFIER is required")
		os.Exit(1)
	}
	if info.InstanceID == "" {
		slog.Error("CONNECTOR_INSTANCE_ID is required")
		os.Exit(1)
	}

	handler := newHomeAssistantServiceHandler()
	server := sdk.NewServer(info, handler)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	startMQTTCommandBridge(ctx, info, handler)
	startHomeAssistantTelemetry(ctx, handler)
	startStartupSync(ctx, info, handler)

	if err := server.Run(ctx); err != nil {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}
