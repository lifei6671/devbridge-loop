package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/adminview"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// defaultPageLimit 定义查询分页默认条数。
	defaultPageLimit = 50
	// defaultMaxPageLimit 定义服务端分页硬上限，避免无界返回。
	defaultMaxPageLimit = 200
	// defaultAuditLogLimit 定义审计日志环形缓冲容量。
	defaultAuditLogLimit = 512
	// maxTimeWindow 定义 logs/metrics 查询窗口上限（24 小时）。
	maxTimeWindow = 24 * time.Hour
	// defaultCSRFCookieName 定义 cookie 鉴权模式 csrf cookie 默认名称。
	defaultCSRFCookieName = "devbridge_admin_csrf"
	// defaultCSRFHeaderName 定义 cookie 鉴权模式 csrf header 默认名称。
	defaultCSRFHeaderName = "X-CSRF-Token"
)

// Role 定义管理后台权限角色。
type Role string

const (
	// RoleViewer 只读角色。
	RoleViewer Role = "viewer"
	// RoleOperator 只读 + 受控运维角色。
	RoleOperator Role = "operator"
	// RoleAdmin 配置管理 + 运维角色。
	RoleAdmin Role = "admin"
)

// TunnelPoolReportSnapshot 定义 Agent tunnel 池上报的只读快照模型。
type TunnelPoolReportSnapshot struct {
	ConnectorID     string `json:"connector_id"`
	SessionID       string `json:"session_id"`
	SessionEpoch    uint64 `json:"session_epoch"`
	IdleCount       int    `json:"idle_count"`
	InUseCount      int    `json:"in_use_count"`
	TargetIdleCount int    `json:"target_idle_count"`
	Trigger         string `json:"trigger,omitempty"`
	ReportedAtMS    uint64 `json:"reported_at_ms"`
	UpdatedAtMS     uint64 `json:"updated_at_ms"`
}

// AgentTunnelPoolSummary 定义 Agent 侧 tunnel 池聚合视图。
type AgentTunnelPoolSummary struct {
	Idle        int    `json:"idle"`
	InUse       int    `json:"in_use"`
	Connected   int    `json:"connected"`
	TargetIdle  int    `json:"target_idle"`
	ReportCount int    `json:"report_count"`
	UpdatedAtMS uint64 `json:"updated_at_ms"`
}

// TrafficOwnershipRecord 定义按 traffic_id 反查得到的服务归属快照。
type TrafficOwnershipRecord struct {
	TrafficID          string   `json:"traffic_id"`
	RouteID            string   `json:"route_id"`
	TargetKind         string   `json:"target_kind"`
	IngressMode        string   `json:"ingress_mode"`
	LogicalServiceID   string   `json:"logical_service_id"`
	ServiceName        string   `json:"service_name"`
	Scope              pb.Scope `json:"scope"`
	RequestScope       pb.Scope `json:"request_scope"`
	MatchedScope       pb.Scope `json:"matched_scope"`
	IsExternalFallback bool     `json:"is_external_fallback"`
	InstanceID         string   `json:"instance_id"`
	ConnectorID        string   `json:"connector_id"`
	SessionID          string   `json:"session_id"`
	UpdatedAtMS        uint64   `json:"updated_at_ms"`
}

// Dependencies 定义管理后台只读 API 所需依赖。
type Dependencies struct {
	// Now 允许测试注入当前时间。
	Now func() time.Time
	// ListRoutes 返回当前路由快照列表。
	ListRoutes func() []pb.Route
	// ListLogicalServices 返回当前逻辑服务快照列表。
	ListLogicalServices func() []pb.LogicalService
	// ListServiceInstances 返回当前服务实例快照列表。
	ListServiceInstances func() []pb.ServiceInstance
	// ListSessions 返回当前会话快照列表。
	ListSessions func() []registry.SessionRuntime
	// ListTunnels 返回当前 tunnel 运行态列表。
	ListTunnels func() []registry.TunnelRuntime
	// TunnelSnapshot 返回 tunnel 汇总快照。
	TunnelSnapshot func() registry.TunnelSnapshot
	// ListTunnelPoolReports 返回 Agent tunnel 池上报快照。
	ListTunnelPoolReports func() []TunnelPoolReportSnapshot
	// BuildConfigSnapshot 返回脱敏后的配置快照。
	BuildConfigSnapshot func() map[string]any
	// Metrics 指向 Bridge 指标容器。
	Metrics *obs.Metrics
	// ResolveTrafficOwnership 按 traffic_id 反查服务归属。
	ResolveTrafficOwnership func(trafficID string) (TrafficOwnershipRecord, bool)
	// ReloadConfig 执行配置重载操作（受控写接口）。
	ReloadConfig func(now time.Time, actor string) (ReloadConfigResult, error)
	// DrainSession 把指定 session 标记为 DRAINING，并触发生命周期收敛副作用。
	DrainSession func(now time.Time, sessionID string, reason string, actor string) (DrainResult, error)
	// DrainConnector 按 connector 当前会话执行 drain 操作。
	DrainConnector func(now time.Time, connectorID string, reason string, actor string) (DrainResult, error)
	// UpdateConfig 执行带版本并发控制的配置更新。
	UpdateConfig func(now time.Time, request ConfigUpdateRequest, actor string) (ConfigUpdateResult, error)
	// ListConnectorTokens 返回 connector token 元数据列表。
	ListConnectorTokens func() ([]ConnectorTokenRecord, error)
	// GetConnectorToken 返回单个 connector token 元数据。
	GetConnectorToken func(tokenID string) (ConnectorTokenRecord, bool, error)
	// CreateConnectorToken 创建新的 connector token，并返回一次性明文 token。
	CreateConnectorToken func(now time.Time, request ConnectorTokenCreateRequest, actor string) (ConnectorTokenIssueResult, error)
	// RotateConnectorToken 轮换指定 token，并返回新的明文 token。
	RotateConnectorToken func(now time.Time, tokenID string, actor string) (ConnectorTokenIssueResult, error)
	// RevokeConnectorToken 吊销指定 token。
	RevokeConnectorToken func(now time.Time, tokenID string, actor string) (ConnectorTokenRecord, error)
}

// ServerOptions 定义管理后台 API 服务构造参数。
type ServerOptions struct {
	Dependencies  Dependencies
	AuthProviders []AuthProviderConfig
	MaxPageLimit  int
	AuditLogLimit int
	// SessionCookieName 定义会话 cookie 名。
	SessionCookieName string
	// CSRFCookieName 定义 cookie 鉴权模式 CSRF cookie 名。
	CSRFCookieName string
	// CSRFHeaderName 定义 cookie 鉴权模式 CSRF 请求头名。
	CSRFHeaderName string
	// AllowedOrigins 定义 cookie 鉴权模式允许的 Origin 列表。
	AllowedOrigins []string
}

// AuditRecord 描述后台请求审计条目。
type AuditRecord struct {
	TSMS   uint64 `json:"ts_ms"`
	Actor  string `json:"actor"`
	Role   string `json:"role"`
	Method string `json:"method"`
	Path   string `json:"path"`
	Scope  string `json:"scope"`
	Action string `json:"action"`
	Status int    `json:"status"`
	Result string `json:"result"`
	// ParamSummary 记录写操作的参数摘要，便于审计追溯。
	ParamSummary string `json:"param_summary,omitempty"`
	TraceID      string `json:"trace_id,omitempty"`
	ErrorCode    string `json:"error_code,omitempty"`
}

type principal struct {
	name        string
	displayName string
	provider    string
	role        Role
}

type contextKey string

const principalContextKey contextKey = "adminapi.principal"

// Server 定义 Bridge 管理后台只读 API 服务。
type Server struct {
	dependencies        Dependencies
	authProviders       map[string]authProvider
	providerDescriptors []authProviderDescriptor
	sessionStore        *authSessionStore
	maxPageLimit        int
	auditLogs           *auditLogStore
	exportStore         *diagnoseExportStore
	sessionCookieName   string
	csrfCookieName      string
	csrfHeaderName      string
	allowedOrigins      map[string]struct{}
}

// NewServer 创建管理后台 API 服务实例。
func NewServer(options ServerOptions) (*Server, error) {
	authProviders, providerDescriptors, err := buildAuthProviders(options.AuthProviders)
	if err != nil {
		return nil, fmt.Errorf("new admin api server: %w", err)
	}
	maxPageLimit := options.MaxPageLimit
	if maxPageLimit <= 0 {
		maxPageLimit = defaultMaxPageLimit
	}
	auditLogLimit := options.AuditLogLimit
	if auditLogLimit <= 0 {
		auditLogLimit = defaultAuditLogLimit
	}
	sessionCookieName := strings.TrimSpace(options.SessionCookieName)
	if sessionCookieName == "" {
		sessionCookieName = defaultAdminSessionCookieName
	}
	csrfCookieName := strings.TrimSpace(options.CSRFCookieName)
	if csrfCookieName == "" {
		csrfCookieName = defaultCSRFCookieName
	}
	csrfHeaderName := strings.TrimSpace(options.CSRFHeaderName)
	if csrfHeaderName == "" {
		csrfHeaderName = defaultCSRFHeaderName
	}
	allowedOrigins := normalizeAllowedOrigins(options.AllowedOrigins)
	if len(allowedOrigins) == 0 {
		return nil, fmt.Errorf("new admin api server: empty allowed origins")
	}
	server := &Server{
		dependencies:        options.Dependencies,
		authProviders:       authProviders,
		providerDescriptors: providerDescriptors,
		sessionStore:        newAuthSessionStore(0),
		maxPageLimit:        maxPageLimit,
		auditLogs:           newAuditLogStore(auditLogLimit),
		exportStore:         newDiagnoseExportStore(defaultDiagnoseExportLimit, defaultDiagnoseExportTTL),
		sessionCookieName:   sessionCookieName,
		csrfCookieName:      csrfCookieName,
		csrfHeaderName:      csrfHeaderName,
		allowedOrigins:      allowedOrigins,
	}
	return server, nil
}

// RegisterRoutes 把管理后台只读 API 注册到 mux。
func (server *Server) RegisterRoutes(mux *http.ServeMux) {
	if server == nil || mux == nil {
		return
	}
	mux.HandleFunc("/api/admin/auth/providers", server.handleAuthProviders)
	mux.HandleFunc("/api/admin/auth/session", server.handleAuthSession)
	mux.HandleFunc("/api/admin/auth/login", server.handleAuthLogin)
	mux.HandleFunc("/api/admin/auth/logout", server.handleAuthLogout)
	mux.Handle("/api/admin/bridge/overview", server.wrapHandler(RoleViewer, "bridge", "overview", server.handleBridgeOverview))
	mux.Handle("/api/admin/routes", server.wrapHandler(RoleViewer, "routes", "list", server.handleRoutesList))
	mux.Handle("/api/admin/services", server.wrapHandler(RoleViewer, "services", "list", server.handleServicesList))
	mux.Handle("/api/admin/connectors", server.wrapHandler(RoleViewer, "connectors", "list", server.handleConnectorsList))
	mux.Handle("/api/admin/sessions", server.wrapHandler(RoleViewer, "sessions", "list", server.handleSessionsList))
	mux.Handle("/api/admin/tunnels/summary", server.wrapHandler(RoleViewer, "tunnels", "summary", server.handleTunnelSummary))
	mux.Handle("/api/admin/tunnels", server.wrapHandler(RoleViewer, "tunnels", "list", server.handleTunnelsList))
	mux.Handle("/api/admin/traffic/summary", server.wrapHandler(RoleViewer, "traffic", "summary", server.handleTrafficSummary))
	mux.Handle("/api/admin/traffic/ownership", server.wrapHandler(RoleViewer, "traffic", "ownership", server.handleTrafficOwnership))
	mux.Handle("/api/admin/config/snapshot", server.wrapHandler(RoleViewer, "config", "snapshot", server.handleConfigSnapshot))
	mux.Handle("/api/admin/connector-tokens", server.wrapHandler(RoleViewer, "connector_tokens", "dispatch", server.handleConnectorTokens))
	mux.Handle("/api/admin/connector-tokens/", server.wrapHandler(RoleViewer, "connector_tokens", "dispatch", server.handleConnectorTokens))
	mux.Handle("/api/admin/config", server.wrapHandler(RoleAdmin, "config", "update", server.handleConfigUpdate))
	mux.Handle("/api/admin/logs/search", server.wrapHandler(RoleViewer, "logs", "search", server.handleLogsSearch))
	mux.Handle("/api/admin/metrics/query", server.wrapHandler(RoleViewer, "metrics", "query", server.handleMetricsQuery))
	mux.Handle("/api/admin/diagnose/summary", server.wrapHandler(RoleViewer, "diagnose", "summary", server.handleDiagnoseSummary))
	mux.Handle("/api/admin/events/stream", server.wrapHandler(RoleViewer, "events", "stream", server.handleEventsStream))
	mux.Handle("/api/admin/ops/config/reload", server.wrapHandler(RoleOperator, "ops", "config_reload", server.handleOpsConfigReload))
	mux.Handle("/api/admin/ops/session/", server.wrapHandler(RoleOperator, "ops", "session_drain", server.handleOpsSessionDrain))
	mux.Handle("/api/admin/ops/connector/", server.wrapHandler(RoleOperator, "ops", "connector_drain", server.handleOpsConnectorDrain))
	mux.Handle("/api/admin/ops/diagnose/export", server.wrapHandler(RoleAdmin, "ops", "diagnose_export", server.handleOpsDiagnoseExport))
	mux.Handle("/api/admin/ops/diagnose/export/", server.wrapHandler(RoleAdmin, "ops", "diagnose_export_download", server.handleOpsDiagnoseExportDownload))
}

func (server *Server) now() time.Time {
	if server != nil && server.dependencies.Now != nil {
		return server.dependencies.Now().UTC()
	}
	return time.Now().UTC()
}

func (server *Server) wrapHandler(
	requiredRole Role,
	scope string,
	action string,
	handlerFunc http.HandlerFunc,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startAt := server.now()
		actor, authErr := server.authenticateRequest(request)
		if authErr != nil {
			writeError(writer, http.StatusUnauthorized, "UNAUTHORIZED", authErr.Error())
			server.appendAuditRecord(startAt, AuditRecord{
				Method:    request.Method,
				Path:      request.URL.Path,
				Scope:     scope,
				Action:    action,
				Status:    http.StatusUnauthorized,
				Result:    "rejected",
				TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
				ErrorCode: "UNAUTHORIZED",
			})
			return
		}
		if !roleCanAccess(actor.role, requiredRole) {
			writeError(writer, http.StatusForbidden, "FORBIDDEN", "permission denied for role")
			server.appendAuditRecord(startAt, AuditRecord{
				Actor:     actor.name,
				Role:      string(actor.role),
				Method:    request.Method,
				Path:      request.URL.Path,
				Scope:     scope,
				Action:    action,
				Status:    http.StatusForbidden,
				Result:    "rejected",
				TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
				ErrorCode: "FORBIDDEN",
			})
			return
		}
		if securityErr := server.enforceWriteRequestSecurity(request); securityErr != nil {
			writeError(writer, http.StatusForbidden, "FORBIDDEN", securityErr.Error())
			server.appendAuditRecord(startAt, AuditRecord{
				Actor:     actor.name,
				Role:      string(actor.role),
				Method:    request.Method,
				Path:      request.URL.Path,
				Scope:     scope,
				Action:    action,
				Status:    http.StatusForbidden,
				Result:    "rejected",
				TraceID:   strings.TrimSpace(request.Header.Get("X-Request-Id")),
				ErrorCode: "FORBIDDEN",
			})
			return
		}

		recorder := &statusCodeRecorder{ResponseWriter: writer}
		requestWithPrincipal := request.WithContext(context.WithValue(request.Context(), principalContextKey, actor))
		handlerFunc(recorder, requestWithPrincipal)
		statusCode := recorder.statusCode
		if statusCode == 0 {
			statusCode = http.StatusOK
		}
		result := "success"
		if statusCode >= http.StatusBadRequest {
			result = "failed"
		}
		server.appendAuditRecord(startAt, AuditRecord{
			Actor:        actor.name,
			Role:         string(actor.role),
			Method:       request.Method,
			Path:         request.URL.Path,
			Scope:        scope,
			Action:       action,
			Status:       statusCode,
			Result:       result,
			ParamSummary: sanitizeAuditParamSummary(recorder.paramSummary),
			TraceID:      strings.TrimSpace(request.Header.Get("X-Request-Id")),
			ErrorCode:    strings.TrimSpace(recorder.errorCode),
		})
	})
}

func (server *Server) authenticateRequest(request *http.Request) (principal, error) {
	if server == nil {
		return principal{}, fmt.Errorf("admin api server unavailable")
	}
	if request == nil {
		return principal{}, fmt.Errorf("missing request")
	}
	session, exists := server.resolveSession(request)
	if !exists {
		return principal{}, fmt.Errorf("missing or expired session")
	}
	authPrincipal := session.principal
	if strings.TrimSpace(authPrincipal.name) == "" {
		authPrincipal.name = "admin_user"
	}
	return authPrincipal, nil
}

// extractTokenFromCookie 从指定 cookie 读取鉴权 token（仅 cookie 鉴权模式使用）。
func extractTokenFromCookie(request *http.Request, cookieName string) string {
	if request == nil {
		return ""
	}
	tokenCookie, err := request.Cookie(strings.TrimSpace(cookieName))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(tokenCookie.Value)
}

// normalizeAllowedOrigins 归一化允许来源列表为集合结构，便于 O(1) 校验。
func normalizeAllowedOrigins(rawOrigins []string) map[string]struct{} {
	originSet := make(map[string]struct{}, len(rawOrigins))
	for _, rawOrigin := range rawOrigins {
		normalizedOrigin, ok := normalizeOrigin(rawOrigin)
		if !ok {
			continue
		}
		originSet[normalizedOrigin] = struct{}{}
	}
	return originSet
}

// normalizeOrigin 把 Origin 统一归一化为 scheme://host 形式。
func normalizeOrigin(rawOrigin string) (string, bool) {
	trimmedOrigin := strings.TrimSpace(rawOrigin)
	if trimmedOrigin == "" {
		return "", false
	}
	parsedOrigin, err := url.Parse(trimmedOrigin)
	if err != nil {
		return "", false
	}
	if strings.TrimSpace(parsedOrigin.Scheme) == "" || strings.TrimSpace(parsedOrigin.Host) == "" {
		return "", false
	}
	// Origin 语义仅接受 scheme://host，不允许拼接 path/query/fragment。
	if strings.TrimSpace(parsedOrigin.Path) != "" && strings.TrimSpace(parsedOrigin.Path) != "/" {
		return "", false
	}
	if strings.TrimSpace(parsedOrigin.RawQuery) != "" || strings.TrimSpace(parsedOrigin.Fragment) != "" {
		return "", false
	}
	return strings.ToLower(parsedOrigin.Scheme) + "://" + strings.ToLower(parsedOrigin.Host), true
}

// originFromReferer 从 Referer 派生 Origin，用于兼容某些浏览器不发送 Origin 的场景。
func originFromReferer(rawReferer string) string {
	parsedReferer, err := url.Parse(strings.TrimSpace(rawReferer))
	if err != nil {
		return ""
	}
	if strings.TrimSpace(parsedReferer.Scheme) == "" || strings.TrimSpace(parsedReferer.Host) == "" {
		return ""
	}
	return parsedReferer.Scheme + "://" + parsedReferer.Host
}

// isMutationMethod 判断是否为会修改状态的请求方法。
func isMutationMethod(method string) bool {
	switch strings.ToUpper(strings.TrimSpace(method)) {
	case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		return true
	default:
		return false
	}
}

// enforceWriteRequestSecurity 对写请求启用会话 + CSRF + 来源校验。
func (server *Server) enforceWriteRequestSecurity(request *http.Request) error {
	if server == nil || !isMutationMethod(request.Method) {
		return nil
	}
	if request == nil {
		return fmt.Errorf("invalid request")
	}
	if strings.HasPrefix(request.URL.Path, "/api/admin/auth/login") {
		return server.enforcePublicOriginSecurity(request)
	}
	rawOrigin := strings.TrimSpace(request.Header.Get("Origin"))
	if rawOrigin == "" {
		rawOrigin = originFromReferer(request.Header.Get("Referer"))
	}
	normalizedOrigin, ok := normalizeOrigin(rawOrigin)
	if !ok {
		return fmt.Errorf("csrf check failed: missing or invalid origin")
	}
	if _, allowed := server.allowedOrigins[normalizedOrigin]; !allowed {
		return fmt.Errorf("csrf check failed: origin is not allowed")
	}
	csrfHeaderValue := strings.TrimSpace(request.Header.Get(server.csrfHeaderName))
	if csrfHeaderValue == "" {
		return fmt.Errorf("csrf check failed: missing csrf header")
	}
	csrfCookie, err := request.Cookie(server.csrfCookieName)
	if err != nil {
		return fmt.Errorf("csrf check failed: missing csrf cookie")
	}
	if strings.TrimSpace(csrfCookie.Value) == "" || strings.TrimSpace(csrfCookie.Value) != csrfHeaderValue {
		return fmt.Errorf("csrf check failed: csrf token mismatch")
	}
	return nil
}

// enforcePublicOriginSecurity 对登录等公开写接口仅执行来源校验。
func (server *Server) enforcePublicOriginSecurity(request *http.Request) error {
	if server == nil {
		return nil
	}
	if request == nil {
		return fmt.Errorf("invalid request")
	}
	rawOrigin := strings.TrimSpace(request.Header.Get("Origin"))
	if rawOrigin == "" {
		rawOrigin = originFromReferer(request.Header.Get("Referer"))
	}
	normalizedOrigin, ok := normalizeOrigin(rawOrigin)
	if !ok {
		return fmt.Errorf("origin check failed: missing or invalid origin")
	}
	if _, allowed := server.allowedOrigins[normalizedOrigin]; !allowed {
		return fmt.Errorf("origin check failed: origin is not allowed")
	}
	return nil
}

func roleCanAccess(currentRole Role, requiredRole Role) bool {
	priorityByRole := map[Role]int{
		RoleViewer:   1,
		RoleOperator: 2,
		RoleAdmin:    3,
	}
	return priorityByRole[currentRole] >= priorityByRole[requiredRole]
}

func (server *Server) appendAuditRecord(startAt time.Time, record AuditRecord) {
	if server == nil || server.auditLogs == nil {
		return
	}
	if startAt.IsZero() {
		startAt = server.now()
	}
	record.TSMS = uint64(startAt.UnixMilli())
	server.auditLogs.append(record)
}

func (server *Server) handleBridgeOverview(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	sessions := safeListSessions(server.dependencies)
	logicalServices := safeListLogicalServices(server.dependencies)
	routes := safeListRoutes(server.dependencies)
	tunnelSnapshot := safeTunnelSnapshot(server.dependencies)
	overview := adminview.BuildBridgeOverview(
		server.now(),
		sessions,
		logicalServices,
		routes,
		tunnelSnapshot,
		safeBuildConfigSnapshot(server.dependencies),
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		"overview": overview,
		"source":   "bridge.adminapi.readonly",
	})
}

func (server *Server) handleRoutesList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	items := adminview.BuildRouteItems(safeListRoutes(server.dependencies))
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(items),
		"source":      "bridge.adminapi.readonly",
	})
}

func (server *Server) handleServicesList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	connectorFilter := strings.TrimSpace(request.URL.Query().Get("connector_id"))
	sessionStateFilter := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("session_state")))
	if sessionStateFilter == "ALL" {
		sessionStateFilter = ""
	}
	statusFilter := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("status")))
	if statusFilter == "ALL" {
		statusFilter = ""
	}
	healthFilter := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("health_status")))
	if healthFilter == "ALL" {
		healthFilter = ""
	}
	serviceTypeFilter := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("service_type")))
	if serviceTypeFilter == "all" {
		serviceTypeFilter = ""
	}
	queryText := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("q")))

	items := adminview.BuildServiceItems(
		server.now(),
		safeListLogicalServices(server.dependencies),
		safeListServiceInstances(server.dependencies),
		safeListSessions(server.dependencies),
	)
	if connectorFilter != "" ||
		sessionStateFilter != "" ||
		statusFilter != "" ||
		healthFilter != "" ||
		serviceTypeFilter != "" ||
		queryText != "" {
		filteredItems := make([]adminview.ServiceItem, 0, len(items))
		for _, item := range items {
			if connectorFilter != "" && strings.TrimSpace(item.ConnectorID) != connectorFilter {
				continue
			}
			if sessionStateFilter != "" && strings.ToUpper(strings.TrimSpace(item.SessionState)) != sessionStateFilter {
				continue
			}
			if statusFilter != "" && strings.ToUpper(strings.TrimSpace(item.Status)) != statusFilter {
				continue
			}
			if healthFilter != "" && strings.ToUpper(strings.TrimSpace(item.HealthStatus)) != healthFilter {
				continue
			}
			if serviceTypeFilter != "" && strings.ToLower(strings.TrimSpace(item.ServiceType)) != serviceTypeFilter {
				continue
			}
			if queryText != "" {
				searchText := strings.ToLower(
					strings.Join([]string{
						item.LogicalServiceID,
						item.InstanceID,
						item.ServiceName,
						item.ConnectorID,
						item.SessionID,
						item.EndpointAddress,
						item.SNIName,
					}, " "),
				)
				if !strings.Contains(searchText, queryText) {
					continue
				}
			}
			filteredItems = append(filteredItems, item)
		}
		items = filteredItems
	}
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":                 pagedItems,
		"next_cursor":           nextCursor,
		"limit":                 page.limit,
		"total":                 len(items),
		"connector_id_filter":   normalizeAuditFilterValue(connectorFilter),
		"session_state_filter":  normalizeAuditFilterValue(sessionStateFilter),
		"service_status_filter": normalizeAuditFilterValue(statusFilter),
		"health_status_filter":  normalizeAuditFilterValue(healthFilter),
		"service_type_filter":   normalizeAuditFilterValue(serviceTypeFilter),
		"q_filter":              normalizeAuditFilterValue(queryText),
		"source":                "bridge.adminapi.readonly",
	})
}

func (server *Server) handleConnectorsList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	items := adminview.BuildConnectorItems(
		safeListSessions(server.dependencies),
		safeListServiceInstances(server.dependencies),
	)
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(items),
		"source":      "bridge.adminapi.readonly",
	})
}

func (server *Server) handleSessionsList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	sessionStateFilter := strings.ToUpper(strings.TrimSpace(request.URL.Query().Get("state")))
	items := adminview.BuildSessionItems(safeListSessions(server.dependencies))
	if sessionStateFilter != "" {
		filteredItems := make([]adminview.SessionItem, 0, len(items))
		for _, item := range items {
			if strings.ToUpper(strings.TrimSpace(item.State)) != sessionStateFilter {
				continue
			}
			filteredItems = append(filteredItems, item)
		}
		items = filteredItems
	}
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(items),
		"source":      "bridge.adminapi.readonly",
	})
}

func (server *Server) handleTunnelSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	connectorFilter := strings.TrimSpace(request.URL.Query().Get("connector_id"))
	summary := adminview.BuildTunnelSummary(server.now(), safeTunnelSnapshot(server.dependencies))
	if connectorFilter != "" {
		// connector 过滤场景下改为按 tunnel 列表聚合，避免误用全局快照统计。
		filteredTunnels := filterTunnelsByConnector(safeListTunnels(server.dependencies), connectorFilter)
		summary = adminview.BuildTunnelSummaryFromRuntimes(server.now(), filteredTunnels)
	}
	agentPoolReports := filterTunnelPoolReportsByConnector(
		safeListTunnelPoolReports(server.dependencies),
		connectorFilter,
	)
	agentPoolSummary := buildAgentTunnelPoolSummary(server.now(), agentPoolReports)
	writeJSON(writer, http.StatusOK, map[string]any{
		"summary":             summary,
		"agent_pool_summary":  agentPoolSummary,
		"connector_id_filter": normalizeAuditFilterValue(connectorFilter),
		"source":              "bridge.adminapi.readonly",
	})
}

func (server *Server) handleTunnelsList(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	stateFilter := strings.ToLower(strings.TrimSpace(request.URL.Query().Get("state")))
	connectorFilter := strings.TrimSpace(request.URL.Query().Get("connector_id"))
	items := adminview.BuildTunnelItems(safeListTunnels(server.dependencies))
	if stateFilter != "" || connectorFilter != "" {
		filteredItems := make([]adminview.TunnelItem, 0, len(items))
		for _, item := range items {
			if stateFilter != "" && strings.ToLower(strings.TrimSpace(item.State)) != stateFilter {
				continue
			}
			if connectorFilter != "" && strings.TrimSpace(item.ConnectorID) != connectorFilter {
				continue
			}
			filteredItems = append(filteredItems, item)
		}
		items = filteredItems
	}
	pagedItems, nextCursor := paginate(items, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(items),
		"source":      "bridge.adminapi.readonly",
	})
}

func (server *Server) handleTrafficSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	summary := adminview.BuildTrafficSummary(server.now(), server.dependencies.Metrics)
	writeJSON(writer, http.StatusOK, map[string]any{
		"summary": summary,
		"source":  "bridge.adminapi.readonly",
	})
}

// handleTrafficOwnership 按 traffic_id 查询服务归属快照。
func (server *Server) handleTrafficOwnership(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	trafficID := strings.TrimSpace(request.URL.Query().Get("traffic_id"))
	if trafficID == "" {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", "traffic_id is required")
		return
	}
	ownership, exists := safeResolveTrafficOwnership(server.dependencies, trafficID)
	if !exists {
		writeError(
			writer,
			http.StatusNotFound,
			"RESOURCE_NOT_FOUND",
			fmt.Sprintf("traffic ownership not found for traffic_id=%s", trafficID),
		)
		return
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"ownership": ownership,
		"source":    "bridge.adminapi.readonly",
	})
}

func (server *Server) handleConfigSnapshot(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	configSnapshot := map[string]any{}
	if server.dependencies.BuildConfigSnapshot != nil {
		configSnapshot = server.dependencies.BuildConfigSnapshot()
	}
	writeJSON(writer, http.StatusOK, map[string]any{
		"snapshot": configSnapshot,
		"source":   "bridge.adminapi.readonly",
	})
}

func (server *Server) handleLogsSearch(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	timeRange, rangeErr := parseTimeRangeQuery(request)
	if rangeErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", rangeErr.Error())
		return
	}
	page, pageErr := parsePageQuery(request, server.maxPageLimit)
	if pageErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", pageErr.Error())
		return
	}
	logItems := server.auditLogs.query(timeRange.fromMS, timeRange.toMS)
	pagedItems, nextCursor := paginate(logItems, page)
	writeJSON(writer, http.StatusOK, map[string]any{
		"items":       pagedItems,
		"next_cursor": nextCursor,
		"limit":       page.limit,
		"total":       len(logItems),
		"source":      "bridge.adminapi.audit",
	})
}

func (server *Server) handleMetricsQuery(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	timeRange, rangeErr := parseTimeRangeQuery(request)
	if rangeErr != nil {
		writeError(writer, http.StatusBadRequest, "INVALID_ARGUMENT", rangeErr.Error())
		return
	}
	trafficSummary := adminview.BuildTrafficSummary(server.now(), server.dependencies.Metrics)
	writeJSON(writer, http.StatusOK, map[string]any{
		"from_ms": timeRange.fromMS,
		"to_ms":   timeRange.toMS,
		"points": []map[string]any{
			{
				"ts_ms":                               uint64(server.now().UnixMilli()),
				"acquire_wait_count":                  trafficSummary.AcquireWaitCount,
				"acquire_wait_total_ms":               trafficSummary.AcquireWaitTotalMS,
				"open_timeout_total":                  trafficSummary.OpenTimeoutTotal,
				"open_reject_total":                   trafficSummary.OpenRejectTotal,
				"open_ack_late_total":                 trafficSummary.OpenAckLateTotal,
				"scope_fallback_total":                trafficSummary.ScopeFallbackTotal,
				"route_conflict_rejection_total":      trafficSummary.RouteConflictRejectionTotal,
				"host_derive_success_total":           trafficSummary.HostDeriveSuccessTotal,
				"host_derive_failure_total":           trafficSummary.HostDeriveFailureTotal,
				"endpoint_override_total":             trafficSummary.EndpointOverrideTotal,
				"quic_connection_accept_total":        trafficSummary.QUICConnectionAcceptTotal,
				"quic_connection_active":              trafficSummary.QUICConnectionActive,
				"quic_connection_authenticated_total": trafficSummary.QUICConnectionAuthenticatedTotal,
				"quic_tunnel_registered_total":        trafficSummary.QUICTunnelRegisteredTotal,
			},
		},
		"source": "bridge.adminapi.readonly",
	})
}

func (server *Server) handleDiagnoseSummary(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	summary := adminview.BuildDiagnoseSummary(
		server.now(),
		safeListSessions(server.dependencies),
		safeTunnelSnapshot(server.dependencies),
		server.dependencies.Metrics,
	)
	writeJSON(writer, http.StatusOK, map[string]any{
		"summary": summary,
		"source":  "bridge.adminapi.readonly",
	})
}

type auditLogStore struct {
	mutex sync.RWMutex
	limit int
	items []AuditRecord
}

func newAuditLogStore(limit int) *auditLogStore {
	normalizedLimit := limit
	if normalizedLimit <= 0 {
		normalizedLimit = defaultAuditLogLimit
	}
	return &auditLogStore{
		limit: normalizedLimit,
		items: make([]AuditRecord, 0, normalizedLimit),
	}
}

func (store *auditLogStore) append(record AuditRecord) {
	if store == nil {
		return
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	if len(store.items) >= store.limit {
		// 环形覆盖策略：超限时丢弃最老记录，保留最近窗口。
		copy(store.items, store.items[1:])
		store.items[len(store.items)-1] = record
		return
	}
	store.items = append(store.items, record)
}

func (store *auditLogStore) query(fromMS uint64, toMS uint64) []AuditRecord {
	if store == nil {
		return []AuditRecord{}
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	items := make([]AuditRecord, 0, len(store.items))
	for _, item := range store.items {
		if item.TSMS < fromMS || item.TSMS > toMS {
			continue
		}
		items = append(items, item)
	}
	return items
}

type statusCodeRecorder struct {
	http.ResponseWriter
	statusCode   int
	errorCode    string
	paramSummary string
}

func (recorder *statusCodeRecorder) WriteHeader(statusCode int) {
	recorder.statusCode = statusCode
	recorder.ResponseWriter.WriteHeader(statusCode)
}

// Flush 透传底层 flusher 能力，确保 SSE 等流式接口可用。
func (recorder *statusCodeRecorder) Flush() {
	if recorder == nil {
		return
	}
	flusher, ok := recorder.ResponseWriter.(http.Flusher)
	if !ok {
		return
	}
	flusher.Flush()
}

// setParamSummary 保存写操作参数摘要供审计日志读取。
func (recorder *statusCodeRecorder) setParamSummary(summary string) {
	if recorder == nil {
		return
	}
	recorder.paramSummary = strings.TrimSpace(summary)
}

type pageQuery struct {
	offset int
	limit  int
}

func parsePageQuery(request *http.Request, maxPageLimit int) (pageQuery, error) {
	normalizedLimit := defaultPageLimit
	rawLimit := strings.TrimSpace(request.URL.Query().Get("limit"))
	if rawLimit != "" {
		parsedLimit, err := strconv.Atoi(rawLimit)
		if err != nil || parsedLimit <= 0 {
			return pageQuery{}, fmt.Errorf("invalid limit")
		}
		normalizedLimit = parsedLimit
	}
	if normalizedLimit > maxPageLimit {
		normalizedLimit = maxPageLimit
	}

	offset := 0
	rawCursor := strings.TrimSpace(request.URL.Query().Get("cursor"))
	if rawCursor != "" {
		parsedCursor, err := strconv.Atoi(rawCursor)
		if err != nil || parsedCursor < 0 {
			return pageQuery{}, fmt.Errorf("invalid cursor")
		}
		offset = parsedCursor
	}
	return pageQuery{
		offset: offset,
		limit:  normalizedLimit,
	}, nil
}

func paginate[T any](items []T, page pageQuery) ([]T, string) {
	if page.offset >= len(items) {
		return []T{}, ""
	}
	endIndex := page.offset + page.limit
	if endIndex > len(items) {
		endIndex = len(items)
	}
	pagedItems := make([]T, endIndex-page.offset)
	copy(pagedItems, items[page.offset:endIndex])
	nextCursor := ""
	if endIndex < len(items) {
		nextCursor = strconv.Itoa(endIndex)
	}
	return pagedItems, nextCursor
}

type timeRangeQuery struct {
	fromMS uint64
	toMS   uint64
}

func parseTimeRangeQuery(request *http.Request) (timeRangeQuery, error) {
	rawFromMS := strings.TrimSpace(request.URL.Query().Get("from"))
	rawToMS := strings.TrimSpace(request.URL.Query().Get("to"))
	if rawFromMS == "" || rawToMS == "" {
		return timeRangeQuery{}, fmt.Errorf("from/to is required")
	}
	fromMS, fromErr := strconv.ParseUint(rawFromMS, 10, 64)
	if fromErr != nil {
		return timeRangeQuery{}, fmt.Errorf("invalid from")
	}
	toMS, toErr := strconv.ParseUint(rawToMS, 10, 64)
	if toErr != nil {
		return timeRangeQuery{}, fmt.Errorf("invalid to")
	}
	if toMS < fromMS {
		return timeRangeQuery{}, fmt.Errorf("invalid time range")
	}
	if toMS-fromMS > uint64(maxTimeWindow.Milliseconds()) {
		return timeRangeQuery{}, fmt.Errorf("time window exceeds 24h")
	}
	return timeRangeQuery{
		fromMS: fromMS,
		toMS:   toMS,
	}, nil
}

func writeJSON(writer http.ResponseWriter, statusCode int, payload any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(statusCode)
	_ = json.NewEncoder(writer).Encode(payload)
}

func writeError(writer http.ResponseWriter, statusCode int, code string, message string) {
	if recorder, ok := writer.(*statusCodeRecorder); ok {
		recorder.errorCode = strings.TrimSpace(code)
	}
	writeJSON(writer, statusCode, map[string]any{
		"error": map[string]any{
			"code":    strings.TrimSpace(code),
			"message": strings.TrimSpace(message),
		},
	})
}

// principalFromRequest 提取请求上下文中的鉴权主体。
func principalFromRequest(request *http.Request) principal {
	if request == nil {
		return principal{}
	}
	value := request.Context().Value(principalContextKey)
	resolved, ok := value.(principal)
	if !ok {
		return principal{}
	}
	return resolved
}

// setAuditParamSummary 把参数摘要写入审计记录缓冲。
func setAuditParamSummary(writer http.ResponseWriter, summary string) {
	recorder, ok := writer.(*statusCodeRecorder)
	if !ok {
		return
	}
	recorder.setParamSummary(summary)
}

// sanitizeAuditParamSummary 统一裁剪审计参数摘要，避免日志被超长文本污染。
func sanitizeAuditParamSummary(summary string) string {
	normalizedSummary := strings.TrimSpace(summary)
	if normalizedSummary == "" {
		return ""
	}
	const maxAuditSummaryLength = 256
	if len(normalizedSummary) <= maxAuditSummaryLength {
		return normalizedSummary
	}
	return normalizedSummary[:maxAuditSummaryLength]
}

func safeListRoutes(dependencies Dependencies) []pb.Route {
	if dependencies.ListRoutes == nil {
		return []pb.Route{}
	}
	return dependencies.ListRoutes()
}

func safeListLogicalServices(dependencies Dependencies) []pb.LogicalService {
	if dependencies.ListLogicalServices == nil {
		return []pb.LogicalService{}
	}
	return dependencies.ListLogicalServices()
}

func safeListServiceInstances(dependencies Dependencies) []pb.ServiceInstance {
	if dependencies.ListServiceInstances == nil {
		return []pb.ServiceInstance{}
	}
	return dependencies.ListServiceInstances()
}

func safeListSessions(dependencies Dependencies) []registry.SessionRuntime {
	if dependencies.ListSessions == nil {
		return []registry.SessionRuntime{}
	}
	return dependencies.ListSessions()
}

func safeListTunnels(dependencies Dependencies) []registry.TunnelRuntime {
	if dependencies.ListTunnels == nil {
		return []registry.TunnelRuntime{}
	}
	return dependencies.ListTunnels()
}

func safeResolveTrafficOwnership(
	dependencies Dependencies,
	trafficID string,
) (TrafficOwnershipRecord, bool) {
	if dependencies.ResolveTrafficOwnership == nil {
		return TrafficOwnershipRecord{}, false
	}
	return dependencies.ResolveTrafficOwnership(strings.TrimSpace(trafficID))
}

func safeTunnelSnapshot(dependencies Dependencies) registry.TunnelSnapshot {
	if dependencies.TunnelSnapshot == nil {
		return registry.TunnelSnapshot{}
	}
	return dependencies.TunnelSnapshot()
}

func safeListTunnelPoolReports(dependencies Dependencies) []TunnelPoolReportSnapshot {
	if dependencies.ListTunnelPoolReports == nil {
		return []TunnelPoolReportSnapshot{}
	}
	return dependencies.ListTunnelPoolReports()
}

func safeListConnectorTokens(dependencies Dependencies) []ConnectorTokenRecord {
	if dependencies.ListConnectorTokens == nil {
		return []ConnectorTokenRecord{}
	}
	items, err := dependencies.ListConnectorTokens()
	if err != nil {
		return []ConnectorTokenRecord{}
	}
	return items
}

func safeBuildConfigSnapshot(dependencies Dependencies) map[string]any {
	if dependencies.BuildConfigSnapshot == nil {
		return map[string]any{}
	}
	snapshot := dependencies.BuildConfigSnapshot()
	if snapshot == nil {
		return map[string]any{}
	}
	return snapshot
}

// filterTunnelsByConnector 仅保留指定 connector 的 tunnel 运行态。
func filterTunnelsByConnector(
	tunnels []registry.TunnelRuntime,
	connectorID string,
) []registry.TunnelRuntime {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return tunnels
	}
	filteredTunnels := make([]registry.TunnelRuntime, 0, len(tunnels))
	for _, tunnelRuntime := range tunnels {
		if strings.TrimSpace(tunnelRuntime.ConnectorID) != normalizedConnectorID {
			continue
		}
		filteredTunnels = append(filteredTunnels, tunnelRuntime)
	}
	return filteredTunnels
}

// filterTunnelPoolReportsByConnector 仅保留指定 connector 的 tunnel 池上报快照。
func filterTunnelPoolReportsByConnector(
	reports []TunnelPoolReportSnapshot,
	connectorID string,
) []TunnelPoolReportSnapshot {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return reports
	}
	filteredReports := make([]TunnelPoolReportSnapshot, 0, len(reports))
	for _, report := range reports {
		if strings.TrimSpace(report.ConnectorID) != normalizedConnectorID {
			continue
		}
		filteredReports = append(filteredReports, report)
	}
	return filteredReports
}

// buildAgentTunnelPoolSummary 基于 Agent 上报快照聚合 tunnel 池视图。
func buildAgentTunnelPoolSummary(
	now time.Time,
	reports []TunnelPoolReportSnapshot,
) AgentTunnelPoolSummary {
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	summary := AgentTunnelPoolSummary{
		UpdatedAtMS: uint64(normalizedNow.UnixMilli()),
	}
	latestUpdatedAtMS := uint64(0)
	for _, report := range reports {
		summary.Idle += maxInt(report.IdleCount, 0)
		summary.InUse += maxInt(report.InUseCount, 0)
		summary.TargetIdle += maxInt(report.TargetIdleCount, 0)
		summary.ReportCount++
		reportUpdatedAtMS := maxUint64(report.UpdatedAtMS, report.ReportedAtMS)
		if reportUpdatedAtMS > latestUpdatedAtMS {
			latestUpdatedAtMS = reportUpdatedAtMS
		}
	}
	if latestUpdatedAtMS == 0 {
		latestUpdatedAtMS = summary.UpdatedAtMS
	}
	summary.Connected = summary.Idle + summary.InUse
	summary.UpdatedAtMS = latestUpdatedAtMS
	return summary
}

func maxInt(value int, fallback int) int {
	if value < fallback {
		return fallback
	}
	return value
}

func maxUint64(left uint64, right uint64) uint64 {
	if left >= right {
		return left
	}
	return right
}
