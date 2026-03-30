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
	bridgecontrol "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/control"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/web"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Runtime wires the bridge runtime subsystems together.
type Runtime struct {
	cfg               Config
	ingressHTTPServer *http.Server
	ingressGRPCServer *http.Server
	adminServer       *http.Server
	controlServer     *controlPlaneServer
	dataPlane         *runtimeDataPlane
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	cfg.NormalizeCompatibility()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	sharedMetrics := obs.NewMetrics()
	// 先初始化数据面主链路依赖，确保控制面可复用同一份注册表真相源。
	dataPlane, err := newRuntimeDataPlaneWithDependencies(cfg, runtimeDataPlaneDependencies{
		connectorMetrics: sharedMetrics,
	})
	if err != nil {
		return nil, err
	}
	connectorAuthRuntime, err := buildConnectorAuthRuntime(cfg, dataPlane.sessionRegistry, sharedMetrics)
	if err != nil {
		return nil, err
	}
	// 保存 Agent tunnel 池上报快照，供 Admin 观测页展示“Agent 视角池状态”。
	tunnelPoolReportStore := bridgecontrol.NewTunnelPoolReportStore()
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
				ListLogicalServices: func() []pb.LogicalService {
					if dataPlane == nil || dataPlane.serviceRegistry == nil {
						return []pb.LogicalService{}
					}
					return dataPlane.serviceRegistry.List()
				},
				ListServiceInstances: func() []pb.ServiceInstance {
					if dataPlane == nil || dataPlane.serviceRegistry == nil {
						return []pb.ServiceInstance{}
					}
					serviceInstances := dataPlane.serviceRegistry.ListInstances()
					result := make([]pb.ServiceInstance, 0, len(serviceInstances))
					for _, serviceInstance := range serviceInstances {
						result = append(result, serviceInstance.Instance)
					}
					return result
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
				ListTunnelPoolReports: func() []adminapi.TunnelPoolReportSnapshot {
					reportItems := tunnelPoolReportStore.List()
					result := make([]adminapi.TunnelPoolReportSnapshot, 0, len(reportItems))
					for _, item := range reportItems {
						reportedAtMS := uint64(0)
						if !item.ReportedAt.IsZero() {
							reportedAtMS = uint64(item.ReportedAt.UTC().UnixMilli())
						}
						updatedAtMS := uint64(0)
						if !item.UpdatedAt.IsZero() {
							updatedAtMS = uint64(item.UpdatedAt.UTC().UnixMilli())
						}
						result = append(result, adminapi.TunnelPoolReportSnapshot{
							ConnectorID:     strings.TrimSpace(item.ConnectorID),
							SessionID:       strings.TrimSpace(item.SessionID),
							SessionEpoch:    item.SessionEpoch,
							IdleCount:       item.IdleCount,
							InUseCount:      item.InUseCount,
							TargetIdleCount: item.TargetIdleCount,
							Trigger:         strings.TrimSpace(item.Trigger),
							ReportedAtMS:    reportedAtMS,
							UpdatedAtMS:     updatedAtMS,
						})
					}
					return result
				},
				BuildConfigSnapshot: func() map[string]any {
					return adminConfigStore.snapshot()
				},
				Metrics: sharedMetrics,
				ResolveTrafficOwnership: func(trafficID string) (adminapi.TrafficOwnershipRecord, bool) {
					if dataPlane == nil {
						return adminapi.TrafficOwnershipRecord{}, false
					}
					ownership, exists := dataPlane.resolveTrafficOwnership(trafficID)
					if !exists {
						return adminapi.TrafficOwnershipRecord{}, false
					}
					updatedAtMS := uint64(0)
					if !ownership.UpdatedAt.IsZero() {
						updatedAtMS = uint64(ownership.UpdatedAt.UTC().UnixMilli())
					}
					return adminapi.TrafficOwnershipRecord{
						TrafficID:          strings.TrimSpace(ownership.TrafficID),
						RouteID:            strings.TrimSpace(ownership.RouteID),
						TargetKind:         strings.TrimSpace(ownership.TargetKind),
						IngressMode:        strings.TrimSpace(ownership.IngressMode),
						LogicalServiceID:   strings.TrimSpace(ownership.LogicalServiceID),
						ServiceName:        strings.TrimSpace(ownership.ServiceName),
						Scope:              ownership.Scope,
						RequestScope:       ownership.RequestScope,
						MatchedScope:       ownership.MatchedScope,
						IsExternalFallback: ownership.IsExternalFallback,
						InstanceID:         strings.TrimSpace(ownership.InstanceID),
						ConnectorID:        strings.TrimSpace(ownership.ConnectorID),
						SessionID:          strings.TrimSpace(ownership.SessionID),
						UpdatedAtMS:        updatedAtMS,
					}, true
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
				ListConnectorTokens: func() ([]adminapi.ConnectorTokenRecord, error) {
					return listConnectorTokensForAdmin(connectorAuthRuntime.tokenAdmin)
				},
				GetConnectorToken: func(tokenID string) (adminapi.ConnectorTokenRecord, bool, error) {
					return getConnectorTokenForAdmin(connectorAuthRuntime.tokenAdmin, tokenID)
				},
				CreateConnectorToken: func(
					now time.Time,
					request adminapi.ConnectorTokenCreateRequest,
					actor string,
				) (adminapi.ConnectorTokenIssueResult, error) {
					_ = now
					_ = actor
					return createConnectorTokenForAdmin(connectorAuthRuntime.tokenAdmin, request)
				},
				RotateConnectorToken: func(
					now time.Time,
					tokenID string,
					actor string,
				) (adminapi.ConnectorTokenIssueResult, error) {
					_ = now
					_ = actor
					return rotateConnectorTokenForAdmin(connectorAuthRuntime.tokenAdmin, tokenID)
				},
				RevokeConnectorToken: func(
					now time.Time,
					tokenID string,
					actor string,
				) (adminapi.ConnectorTokenRecord, error) {
					_ = now
					_ = actor
					return revokeConnectorTokenForAdmin(connectorAuthRuntime.tokenAdmin, tokenID)
				},
			},
			AuthProviders:     buildAdminAuthProviders(cfg.Admin.AuthProviders),
			SessionCookieName: cfg.Admin.SessionCookieName,
			CSRFCookieName:    cfg.Admin.CSRFCookieName,
			CSRFHeaderName:    cfg.Admin.CSRFHeaderName,
			AllowedOrigins:    append([]string(nil), cfg.Admin.AllowedOrigins...),
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
		sessionRegistry:       dataPlane.sessionRegistry,
		serviceRegistry:       dataPlane.serviceRegistry,
		routeRegistry:         dataPlane.routeRegistry,
		tunnelRegistry:        dataPlane.tunnelRegistry,
		tunnelPoolReportStore: tunnelPoolReportStore,
		authCoordinator:       connectorAuthRuntime.coordinator,
		metrics:               sharedMetrics,
		hostDerivationDomain:  cfg.Ingress.BaseDomain,
	})
	if err != nil {
		return nil, err
	}
	runtime := &Runtime{
		cfg:           cfg,
		controlServer: controlServer,
		dataPlane:     dataPlane,
		adminServer:   adminServer,
	}
	runtime.ingressHTTPServer = newIngressHTTPServer(runtime, cfg.Ingress.HTTPAddr)
	runtime.ingressGRPCServer = newIngressGRPCServer(runtime, cfg.Ingress.GRPCAddr)
	return runtime, nil
}

// Run 启动 Bridge 运行时，当前阶段负责托管内嵌管理页面。
func (r *Runtime) Run(ctx context.Context) error {
	normalizedContext := ctx
	if normalizedContext == nil {
		// 外部未传上下文时回落 Background，避免空指针路径。
		normalizedContext = context.Background()
	}
	log.Printf(
		"bridge runtime starting ingress_http_addr=%s ingress_grpc_addr=%s control_addr=%s control_grpc_addr=%s control_tls_mode=%s admin_enabled=%t admin_addr=%s admin_ui_enabled=%t admin_ui_base_path=%s admin_ui_version=%s",
		r.cfg.Ingress.HTTPAddr,
		r.cfg.Ingress.GRPCAddr,
		r.cfg.ControlPlane.ListenAddr,
		r.cfg.ControlPlane.GRPCH2ListenAddr,
		r.cfg.ControlPlane.TLSMode,
		r.cfg.Admin.Enabled,
		r.cfg.Admin.ListenAddr,
		r.cfg.Admin.UIEnabled,
		normalizeAdminUIBasePath(r.cfg.Admin.BasePath),
		web.EmbeddedVersion(),
	)

	serverErrChannel := make(chan error, 4)
	go func() {
		if r.ingressHTTPServer == nil {
			return
		}
		if err := r.ingressHTTPServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrChannel <- fmt.Errorf("run bridge runtime: listen ingress http server: %w", err)
		}
	}()
	go func() {
		if r.ingressGRPCServer == nil {
			return
		}
		if err := r.ingressGRPCServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErrChannel <- fmt.Errorf("run bridge runtime: listen ingress grpc server: %w", err)
		}
	}()
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

// buildAdminAuthProviders 把 app 配置中的 provider 描述转换为 adminapi 可消费结构。
func buildAdminAuthProviders(providers []AdminAuthProviderConfig) []adminapi.AuthProviderConfig {
	result := make([]adminapi.AuthProviderConfig, 0, len(providers))
	for _, providerConfig := range providers {
		provider := adminapi.AuthProviderConfig{
			Name:    strings.TrimSpace(providerConfig.Name),
			Type:    strings.TrimSpace(providerConfig.Type),
			Label:   strings.TrimSpace(providerConfig.Label),
			Enabled: providerConfig.Enabled,
		}
		if strings.EqualFold(strings.TrimSpace(providerConfig.Type), "password") {
			provider.Password.Accounts = make([]adminapi.PasswordAccountConfig, 0, len(providerConfig.Password.Accounts))
			for _, accountConfig := range providerConfig.Password.Accounts {
				normalizedRole, ok := normalizeAdminRoleForAPI(accountConfig.Role)
				if !ok {
					continue
				}
				provider.Password.Accounts = append(provider.Password.Accounts, adminapi.PasswordAccountConfig{
					Username:    strings.TrimSpace(accountConfig.Username),
					Password:    strings.TrimSpace(accountConfig.Password),
					DisplayName: strings.TrimSpace(accountConfig.DisplayName),
					Role:        normalizedRole,
				})
			}
		}
		result = append(result, provider)
	}
	return result
}

// buildAdminAuthProviderSnapshot 生成管理面配置快照中可安全展示的 provider 结构。
func buildAdminAuthProviderSnapshot(providers []AdminAuthProviderConfig) []map[string]any {
	result := make([]map[string]any, 0, len(providers))
	for _, providerConfig := range providers {
		providerSnapshot := map[string]any{
			"name":    strings.TrimSpace(providerConfig.Name),
			"type":    strings.TrimSpace(providerConfig.Type),
			"label":   strings.TrimSpace(providerConfig.Label),
			"enabled": providerConfig.Enabled,
		}
		if strings.EqualFold(strings.TrimSpace(providerConfig.Type), "password") {
			accountSnapshots := make([]map[string]any, 0, len(providerConfig.Password.Accounts))
			for _, accountConfig := range providerConfig.Password.Accounts {
				accountSnapshots = append(accountSnapshots, map[string]any{
					"username":     strings.TrimSpace(accountConfig.Username),
					"display_name": strings.TrimSpace(accountConfig.DisplayName),
					"role":         strings.TrimSpace(accountConfig.Role),
					"password":     maskAdminSecret(accountConfig.Password),
				})
			}
			providerSnapshot["password"] = map[string]any{
				"accounts": accountSnapshots,
			}
		}
		result = append(result, providerSnapshot)
	}
	return result
}

// maskAdminSecret 对配置快照中的敏感凭据做脱敏，避免管理接口泄露密钥材料。
func maskAdminSecret(rawSecret string) string {
	normalizedSecret := strings.TrimSpace(rawSecret)
	if normalizedSecret == "" {
		return ""
	}
	if len(normalizedSecret) <= 4 {
		return "****"
	}
	return "****" + normalizedSecret[len(normalizedSecret)-4:]
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
	if r.ingressHTTPServer != nil {
		if err := r.ingressHTTPServer.Shutdown(normalizedContext); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown bridge ingress http server: %w", err)
		}
	}
	if r.ingressGRPCServer != nil {
		if err := r.ingressGRPCServer.Shutdown(normalizedContext); err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("shutdown bridge ingress grpc server: %w", err)
		}
	}
	if r.controlServer != nil {
		if err := r.controlServer.shutdown(); err != nil && !errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("shutdown bridge control plane: %w", err)
		}
	}
	return nil
}
