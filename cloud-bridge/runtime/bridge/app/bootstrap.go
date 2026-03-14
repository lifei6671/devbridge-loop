package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminapi"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
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
	// 先初始化数据面主链路依赖，确保控制面可复用同一份注册表真相源。
	dataPlane, err := newRuntimeDataPlane(cfg)
	if err != nil {
		return nil, err
	}
	var adminServer *http.Server
	if cfg.Admin.Enabled {
		// 管理面启用时才初始化 mux 与 server，关闭时保持零开销。
		adminMux := http.NewServeMux()
		adminUIBasePath := normalizeAdminUIBasePath(cfg.Admin.BasePath)
		adminConfigStore := newAdminRuntimeConfigStore(cfg)
		if cfg.Admin.UIEnabled {
			// 管理页面默认挂载到 /admin 前缀，保持后续 API 路径可扩展。
			RegisterAdminUIRoutes(adminMux, adminUIBasePath, UIHandler())
		}
		adminAPIServer, err := adminapi.NewServer(adminapi.ServerOptions{
			Dependencies: adminapi.Dependencies{
				ListRoutes: func() []pb.Route {
					if dataPlane == nil || dataPlane.routeRegistry == nil {
						return []pb.Route{}
					}
					return dataPlane.routeRegistry.List()
				},
				ListServices: func() []pb.Service {
					if dataPlane == nil || dataPlane.serviceRegistry == nil {
						return []pb.Service{}
					}
					return dataPlane.serviceRegistry.List()
				},
				ListSessions: func() []registry.SessionRuntime {
					if dataPlane == nil || dataPlane.sessionRegistry == nil {
						return []registry.SessionRuntime{}
					}
					return dataPlane.sessionRegistry.List()
				},
				ListTunnels: func() []registry.TunnelRuntime {
					if dataPlane == nil || dataPlane.tunnelRegistry == nil {
						return []registry.TunnelRuntime{}
					}
					return dataPlane.tunnelRegistry.List()
				},
				TunnelSnapshot: func() registry.TunnelSnapshot {
					if dataPlane == nil || dataPlane.tunnelRegistry == nil {
						return registry.TunnelSnapshot{}
					}
					return dataPlane.tunnelRegistry.Snapshot()
				},
				BuildConfigSnapshot: func() map[string]any {
					return adminConfigStore.snapshot()
				},
				ReloadConfig: func(now time.Time, actor string) (adminapi.ReloadConfigResult, error) {
					return adminConfigStore.reload(now, actor)
				},
				DrainSession: func(
					now time.Time,
					sessionID string,
					reason string,
					actor string,
				) (adminapi.DrainResult, error) {
					_ = actor
					return drainSessionForAdmin(now, dataPlane, sessionID, reason)
				},
				DrainConnector: func(
					now time.Time,
					connectorID string,
					reason string,
					actor string,
				) (adminapi.DrainResult, error) {
					_ = actor
					return drainConnectorForAdmin(now, dataPlane, connectorID, reason)
				},
				UpdateConfig: func(
					now time.Time,
					request adminapi.ConfigUpdateRequest,
					actor string,
				) (adminapi.ConfigUpdateResult, error) {
					return adminConfigStore.update(now, request, actor)
				},
			},
			BearerTokens: buildAdminBearerTokens(cfg.Admin.AuthTokens),
		})
		if err != nil {
			return nil, fmt.Errorf("initialize admin api server: %w", err)
		}
		adminAPIServer.RegisterRoutes(adminMux)
		adminServer = &http.Server{
			Addr:    cfg.Admin.ListenAddr,
			Handler: adminMux,
		}
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
		adminServer:   adminServer,
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
		"bridge runtime starting control_addr=%s control_grpc_addr=%s admin_enabled=%t admin_addr=%s admin_ui_enabled=%t admin_ui_base_path=%s admin_ui_version=%s",
		r.cfg.ControlPlane.ListenAddr,
		r.cfg.ControlPlane.GRPCH2ListenAddr,
		r.cfg.Admin.Enabled,
		r.cfg.Admin.ListenAddr,
		r.cfg.Admin.UIEnabled,
		normalizeAdminUIBasePath(r.cfg.Admin.BasePath),
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

// buildAdminBearerTokens 把 app 配置中的 token 映射转换为 adminapi 可消费结构。
func buildAdminBearerTokens(tokens []AdminAuthTokenConfig) []adminapi.BearerToken {
	result := make([]adminapi.BearerToken, 0, len(tokens))
	for _, tokenConfig := range tokens {
		normalizedRole, ok := normalizeAdminRoleForAPI(tokenConfig.Role)
		if !ok {
			// Validate 已保证角色合法，这里仍做兜底防御。
			continue
		}
		result = append(result, adminapi.BearerToken{
			Name:  strings.TrimSpace(tokenConfig.Name),
			Token: strings.TrimSpace(tokenConfig.Token),
			Role:  normalizedRole,
		})
	}
	return result
}

// normalizeAdminRoleForAPI 把字符串角色映射为 adminapi.Role。
func normalizeAdminRoleForAPI(role string) (adminapi.Role, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "viewer":
		return adminapi.RoleViewer, true
	case "operator":
		return adminapi.RoleOperator, true
	case "admin":
		return adminapi.RoleAdmin, true
	default:
		return "", false
	}
}

// maskAdminToken 对配置快照中的 token 进行脱敏，避免管理接口泄露密钥材料。
func maskAdminToken(rawToken string) string {
	normalizedToken := strings.TrimSpace(rawToken)
	if normalizedToken == "" {
		return ""
	}
	if len(normalizedToken) <= 4 {
		return "****"
	}
	return "****" + normalizedToken[len(normalizedToken)-4:]
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
