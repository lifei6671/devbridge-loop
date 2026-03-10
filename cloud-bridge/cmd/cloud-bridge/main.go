package main

import (
	"context"
	"flag"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/app"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
)

func main() {
	var configFile string
	flag.StringVar(&configFile, "config", "", "bridge yaml config file path")
	flag.Parse()
	if strings.TrimSpace(configFile) != "" {
		_ = os.Setenv("DEVLOOP_BRIDGE_CONFIG_FILE", strings.TrimSpace(configFile))
	}

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
