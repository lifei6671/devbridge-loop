package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
)

// Runtime wires the bridge runtime subsystems together.
type Runtime struct {
	cfg         Config
	adminServer *http.Server
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	_ = ctx
	adminMux := http.NewServeMux()
	if cfg.Admin.UIEnabled {
		// 管理页面默认挂载到 /admin 前缀，保持后续 API 路径可扩展。
		RegisterAdminUIRoutes(adminMux, AdminUIBasePath(), UIHandler())
	}
	return &Runtime{
		cfg: cfg,
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
		"bridge runtime starting admin_addr=%s admin_ui_enabled=%t admin_ui_base_path=%s admin_ui_version=%s",
		r.cfg.Admin.ListenAddr,
		r.cfg.Admin.UIEnabled,
		AdminUIBasePath(),
		web.EmbeddedVersion(),
	)

	serverErrChannel := make(chan error, 1)
	go func() {
		if r.adminServer == nil {
			close(serverErrChannel)
			return
		}
		if err := r.adminServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			// 管理端口启动失败直接上抛，阻止半启动状态。
			serverErrChannel <- fmt.Errorf("run bridge runtime: listen admin server: %w", err)
		}
		close(serverErrChannel)
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
		return runErr
	}
}

// Shutdown 执行管理端口优雅关闭。
func (r *Runtime) Shutdown(ctx context.Context) error {
	if r == nil || r.adminServer == nil {
		return nil
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		// 兜底上下文，确保可被调用方直接复用。
		normalizedContext = context.Background()
	}
	if err := r.adminServer.Shutdown(normalizedContext); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("shutdown bridge runtime: %w", err)
	}
	return nil
}
