package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/app"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
)

func main() {
	cfg := config.LoadFromEnv()
	a := app.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	slog.Info("cloud-bridge process started", "httpAddr", cfg.HTTPAddr)
	if err := a.Run(ctx); err != nil {
		slog.Error("cloud-bridge exited with error", "error", err)
		os.Exit(1)
	}
}
