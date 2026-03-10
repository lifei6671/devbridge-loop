package app

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/backflow"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/httpapi"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// App 负责组装 cloud-bridge 的传输层和运行态依赖。
type App struct {
	cfg              config.Config
	server           *http.Server
	tunnelProtocols  []string
	protocolRuntimes []TunnelProtocolRuntime
	startupErr       error
}

// New 构造 cloud-bridge 应用实例。
func New(cfg config.Config) *App {
	tunnelProtocols := effectiveTunnelProtocols(cfg)
	pipeline := routing.NewPipeline(cfg.RouteExtractorOrder)
	stateStore := store.NewMemoryStoreWithBridge(cfg.BridgePublicHost, cfg.BridgePublicPort)
	backflowClient := backflow.NewClient(cfg.IngressTimeout)
	httpTunnelSyncEnabled := tunnelProtocolEnabled(tunnelProtocols, tunnelProtocolHTTP)
	h := httpapi.NewHandler(
		pipeline,
		stateStore,
		backflowClient,
		cfg.FallbackBackflowURL,
		httpapi.WithTunnelEventHTTPEnabled(httpTunnelSyncEnabled),
	)

	protocolRuntimes, startupErr := newTunnelProtocolRuntimes(cfg, stateStore)
	if startupErr != nil {
		startupErr = fmt.Errorf("init tunnel runtimes failed: %w", startupErr)
	}

	slog.Info("bridge routing pipeline", "order", pipeline.DebugString())
	return &App{
		cfg:              cfg,
		tunnelProtocols:  tunnelProtocols,
		protocolRuntimes: protocolRuntimes,
		startupErr:       startupErr,
		server: &http.Server{
			Addr:              cfg.HTTPAddr,
			Handler:           h.Router(),
			ReadHeaderTimeout: 5 * time.Second,
		},
	}
}

// Run 启动 cloud-bridge，并在收到退出信号后平滑关闭。
func (a *App) Run(ctx context.Context) error {
	if a.startupErr != nil {
		return a.startupErr
	}

	// 启动时明确打印 tunnel 协议配置，便于联调快速判断生效结果。
	slog.Info("cloud-bridge startup",
		"httpAddr", a.cfg.HTTPAddr,
		"enabledTunnelProtocols", a.tunnelProtocols,
		"httpTunnelSyncEnabled", tunnelProtocolEnabled(a.tunnelProtocols, tunnelProtocolHTTP),
		"masqueEnabled", tunnelProtocolEnabled(a.tunnelProtocols, tunnelProtocolMASQUE),
		"masqueAddr", a.cfg.MasqueAddr,
		"masqueTunnelUDPAddr", a.cfg.MasqueTunnelUDPAddr,
	)

	type runError struct {
		source string
		err    error
	}

	errorCh := make(chan runError, 1+len(a.protocolRuntimes))
	go func() {
		if err := a.server.ListenAndServe(); err != nil {
			errorCh <- runError{source: "http-api", err: err}
		}
	}()

	for _, runtime := range a.protocolRuntimes {
		currentRuntime := runtime
		go func() {
			if err := currentRuntime.Run(ctx); err != nil {
				errorCh <- runError{source: currentRuntime.Protocol(), err: err}
			}
		}()
	}

	select {
	case <-ctx.Done():
		return a.shutdown()
	case runtimeErr := <-errorCh:
		if runtimeErr.source == "http-api" && errors.Is(runtimeErr.err, http.ErrServerClosed) {
			return nil
		}
		// 任一协议运行时异常退出时，主动关闭其余组件，避免僵尸 goroutine 与端口泄露。
		_ = a.shutdown()
		return fmt.Errorf("run %s server: %w", runtimeErr.source, runtimeErr.err)
	}
}

func (a *App) shutdown() error {
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// 先停主 HTTP 服务，再依次回收各 tunnel runtime。
	httpErr := a.server.Shutdown(shutdownCtx)
	runtimeErr := a.closeTunnelRuntimes()
	if errors.Is(httpErr, http.ErrServerClosed) {
		httpErr = nil
	}
	return errors.Join(httpErr, runtimeErr)
}

func (a *App) closeTunnelRuntimes() error {
	var joinedErr error
	for _, runtime := range a.protocolRuntimes {
		if err := runtime.Close(); err != nil {
			joinedErr = errors.Join(joinedErr, fmt.Errorf("close %s runtime failed: %w", runtime.Protocol(), err))
		}
	}
	return joinedErr
}
