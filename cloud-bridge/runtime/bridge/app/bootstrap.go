package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
)

// Runtime wires the bridge runtime subsystems together.
type Runtime struct {
	cfg           Config
	adminServer   *http.Server
	controlServer *controlPlaneServer
	dataPlane     *runtimeDataPlane
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	_ = ctx
	// 先初始化数据面主链路依赖，确保控制面可复用同一份注册表真相源。
	dataPlane, err := newRuntimeDataPlane(cfg)
	if err != nil {
		return nil, err
	}
	adminMux := http.NewServeMux()
	if cfg.Admin.UIEnabled {
		// 管理页面默认挂载到 /admin 前缀，保持后续 API 路径可扩展。
		RegisterAdminUIRoutes(adminMux, AdminUIBasePath(), UIHandler())
	}
	// 控制面与数据面共享注册表，避免“控制面更新了、数据面看不到”的分裂状态。
	controlServer, err := newControlPlaneServer(cfg.ControlPlane, controlPlaneDependencies{
		sessionRegistry: dataPlane.sessionRegistry,
		serviceRegistry: dataPlane.serviceRegistry,
		routeRegistry:   dataPlane.routeRegistry,
		tunnelRegistry:  dataPlane.tunnelRegistry,
	})
	if err != nil {
		return nil, err
	}
	return &Runtime{
		cfg:           cfg,
		controlServer: controlServer,
		dataPlane:     dataPlane,
		adminServer: &http.Server{
			Addr:    cfg.Admin.ListenAddr,
			Handler: adminMux,
		},
	}, nil
}

// Run 启动 Bridge 运行时，当前阶段负责托管内嵌管理页面。
func (r *Runtime) Run(ctx context.Context) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 外部未传上下文时回落 Background，避免空指针路径。
		normalizedContext = context.Background()
	}
	log.Printf(
		"bridge runtime starting control_addr=%s control_grpc_addr=%s admin_addr=%s admin_ui_enabled=%t admin_ui_base_path=%s admin_ui_version=%s",
		r.cfg.ControlPlane.ListenAddr,
		r.cfg.ControlPlane.GRPCH2ListenAddr,
		r.cfg.Admin.ListenAddr,
		r.cfg.Admin.UIEnabled,
		AdminUIBasePath(),
		web.EmbeddedVersion(),
	)

	serverErrChannel := make(chan error, 2)
	go func() {
		if r.controlServer == nil {
			return
		}
		if err := r.controlServer.run(normalizedContext); err != nil && !errors.Is(err, context.Canceled) {
			serverErrChannel <- fmt.Errorf("run bridge runtime: control plane failed: %w", err)
		}
	}()
	go func() {
		if r.adminServer == nil {
			return
		}
		if err := r.adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 管理端口启动失败直接上抛，阻止半启动状态。
			serverErrChannel <- fmt.Errorf("run bridge runtime: listen admin server: %w", err)
		}
	}()

	select {
	case <-normalizedContext.Done():
		// 收到退出信号后优雅关闭，确保连接收敛完成。
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := r.Shutdown(shutdownContext); err != nil {
			return err
		}
		return normalizedContext.Err()
	case runErr, open := <-serverErrChannel:
		if !open {
			return nil
		}
		shutdownContext, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = r.Shutdown(shutdownContext)
		return runErr
	}
}

// Shutdown 执行管理端口优雅关闭。
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil {
		return nil
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// 兜底上下文，确保可被调用方直接复用。
		normalizedContext = context.Background()
	}
	if r.adminServer != nil {
		if err := r.adminServer.Shutdown(normalizedContext); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown bridge runtime: %w", err)
		}
	}
	if r.controlServer != nil {
		if err := r.controlServer.shutdown(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("shutdown bridge control plane: %w", err)
		}
	}
	return nil
}
