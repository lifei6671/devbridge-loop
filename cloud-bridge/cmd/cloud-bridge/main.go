package main

import (
	"context"
	"errors"
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
		_ = os.Setenv(config.BridgeConfigFileEnv, strings.TrimSpace(configFile))
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	restartSignal := make(chan struct{}, 1)
	for {
		cfg := config.LoadFromEnv()
		editor := app.NewBridgeConfigEditor(config.ResolveConfigFilePath(), cfg, restartSignal)
		a := app.New(
			cfg,
			app.WithHotRestartSignal(restartSignal),
			app.WithConfigEditor(editor),
		)

		slog.Info("cloud-bridge process started", "httpAddr", cfg.HTTPAddr, "configFile", config.ResolveConfigFilePath())
		err := a.Run(ctx)
		if err == nil {
			return
		}
		if errors.Is(err, app.ErrHotRestartRequested) {
			slog.Info("cloud-bridge hot restart requested, restarting...")
			if ctx.Err() != nil {
				return
			}
			continue
		}
		slog.Error("cloud-bridge exited with error", "error", err)
		os.Exit(1)
	}
}
