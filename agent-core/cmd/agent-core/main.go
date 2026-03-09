package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/app"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
)

func main() {
	cfg := config.LoadFromEnv()
	a := app.New(cfg)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("agent-core starting on %s (rdName=%s envName=%s)", cfg.HTTPAddr, cfg.RDName, cfg.EnvName)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("agent-core exited with error: %v", err)
	}
}
