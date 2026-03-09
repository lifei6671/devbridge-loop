package main

import (
	"context"
	"log"
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

	log.Printf("cloud-bridge starting on %s", cfg.HTTPAddr)
	if err := a.Run(ctx); err != nil {
		log.Fatalf("cloud-bridge exited with error: %v", err)
	}
}
