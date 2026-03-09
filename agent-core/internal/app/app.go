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
	"github.com/lifei6671/devbridge-loop/agent-core/internal/forwarder"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/httpapi"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/tunnel"
)

// App 负责组装配置、状态存储和传输层。
type App struct {
	cfg         config.Config
	stateStore  *store.MemoryStore
	syncManager *tunnel.SyncManager
	server      *http.Server
}

// New 构建 App 运行时依赖。
func New(cfg config.Config) *App {
	stateStore := store.NewMemoryStore()
	syncManager := tunnel.NewSyncManager(cfg, stateStore)
	backflowForwarder := forwarder.NewHTTPForwarder(cfg.Tunnel.RequestTimeout)
	h := httpapi.NewHandler(cfg, stateStore, syncManager, backflowForwarder)

	return &App{
		cfg:         cfg,
		stateStore:  stateStore,
		syncManager: syncManager,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           h.Router(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Run 启动 HTTP API、TTL 扫描与 tunnel 同步循环，直到上下文取消。
func (a *App) Run(ctx context.Context) error {
	errorCh := make(chan error, 1)

	go a.runTTLScanner(ctx)
	go a.syncManager.Run(ctx)

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

			// TTL 触发的摘除也需要同步给 bridge，避免远端残留脏接管。
			for _, reg := range expired {
				a.stateStore.AddError(domain.ErrorRouteNotFound, "registration expired by ttl scanner", map[string]string{
					"instanceId": reg.InstanceID,
				})
				if err := a.syncManager.EnqueueRegisterDelete(ctx, reg, fmt.Sprintf("evt-ttl-%s-%d", reg.InstanceID, now.UTC().UnixNano())); err != nil {
					a.stateStore.AddError(domain.ErrorTunnelOffline, "enqueue ttl delete sync failed", map[string]string{
						"instanceId": reg.InstanceID,
						"error":      err.Error(),
					})
				}
				log.Printf("registration expired due to ttl timeout: %s", reg.InstanceID)
			}
		}
	}
}
