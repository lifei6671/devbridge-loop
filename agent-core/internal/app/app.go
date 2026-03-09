package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/httpapi"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

// App wires configuration, stores, and transport.
type App struct {
	cfg        config.Config
	stateStore *store.MemoryStore
	server     *http.Server
}

// New constructs an App with runtime dependencies.
func New(cfg config.Config) *App {
	stateStore := store.NewMemoryStore()
	h := httpapi.NewHandler(cfg, stateStore)

	return &App{
		cfg:        cfg,
		stateStore: stateStore,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           h.Router(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Run starts API server and TTL scanner until context cancellation.
func (a *App) Run(ctx context.Context) error {
	errorCh := make(chan error, 1)

	go a.runTTLScanner(ctx)

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

func (a *App) runTTLScanner(ctx context.Context) {
	ticker := time.NewTicker(a.cfg.Registration.ScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			expired := a.stateStore.ExpireRegistrations(now.UTC())
			if len(expired) == 0 {
				continue
			}
			for _, instanceID := range expired {
				a.stateStore.AddError(domain.ErrorRouteNotFound, "registration expired by ttl scanner", map[string]string{
					"instanceId": instanceID,
				})
				log.Printf("registration expired due to ttl timeout: %s", instanceID)
			}
		}
	}
}
