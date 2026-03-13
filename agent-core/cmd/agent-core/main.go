package main

import (
	"context"
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/app"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	cfg := app.DefaultConfig()
	runtime, err := app.Bootstrap(ctx, cfg)
	if err != nil {
		log.Fatalf("agent bootstrap failed: %v", err)
	}

	if err := runtime.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatalf("agent runtime stopped: %v", err)
	}
}
