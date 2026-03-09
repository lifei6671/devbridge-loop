package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/httpapi"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// App wires cloud-bridge transport and state.
type App struct {
	cfg    config.Config
	server *http.Server
}

// New constructs cloud-bridge app.
func New(cfg config.Config) *App {
	pipeline := routing.NewPipeline(cfg.RouteExtractorOrder)
	stateStore := store.NewMemoryStore()
	h := httpapi.NewHandler(pipeline, stateStore)

	log.Printf("%s", pipeline.DebugString())
	return &App{
		cfg: cfg,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           h.Router(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Run starts cloud-bridge until context cancellation.
func (a *App) Run(ctx context.Context) error {
	errorCh := make(chan error, 1)
	go func() {
		if err := a.server.ListenAndServe(); err != nil {
			errorCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := a.server.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("shutdown server: %w", err)
		}
		return nil
	case err := <-errorCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("run http server: %w", err)
	}
}
