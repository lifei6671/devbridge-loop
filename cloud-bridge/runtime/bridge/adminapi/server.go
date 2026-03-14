package adminapi

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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

// BearerToken 定义静态 Bearer Token 与角色映射。
type BearerToken struct {
	Name  string
	Token string
	Role  Role
}

// Dependencies 定义管理后台只读 API 所需依赖。
type Dependencies struct {
	// Now 允许测试注入当前时间。
	Now func() time.Time
	// ListRoutes 返回当前路由快照列表。
	ListRoutes func() []pb.Route
	// ListServices 返回当前服务快照列表。
	ListServices func() []pb.Service
	// ListSessions 返回当前会话快照列表。
	ListSessions func() []registry.SessionRuntime
	// ListTunnels 返回当前 tunnel 运行态列表。
	ListTunnels func() []registry.TunnelRuntime
	// TunnelSnapshot 返回 tunnel 汇总快照。
	TunnelSnapshot func() registry.TunnelSnapshot
	// BuildConfigSnapshot 返回脱敏后的配置快照。
	BuildConfigSnapshot func() map[string]any
	// Metrics 指向 Bridge 指标容器。
	Metrics *obs.Metrics
	// ReloadConfig 执行配置重载操作（受控写接口）。
	ReloadConfig func(now time.Time, actor string) (ReloadConfigResult, error)
	// DrainSession 把指定 session 标记为 DRAINING，并触发生命周期收敛副作用。
	DrainSession func(now time.Time, sessionID string, reason string, actor string) (DrainResult, error)
	// DrainConnector 按 connector 当前会话执行 drain 操作。
	DrainConnector func(now time.Time, connectorID string, reason string, actor string) (DrainResult, error)
	// UpdateConfig 执行带版本并发控制的配置更新。
	UpdateConfig func(now time.Time, request ConfigUpdateRequest, actor string) (ConfigUpdateResult, error)
}

// ServerOptions 定义管理后台 API 服务构造参数。
type ServerOptions struct {
	Dependencies  Dependencies
	BearerTokens  []BearerToken
	MaxPageLimit  int
	AuditLogLimit int
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
	name string
	role Role
}

type contextKey string

const principalContextKey contextKey = "adminapi.principal"

// Server 定义 Bridge 管理后台只读 API 服务。
type Server struct {
	dependencies Dependencies
	tokenLookup  map[string]principal
	maxPageLimit int
	auditLogs    *auditLogStore
	exportStore  *diagnoseExportStore
}

// NewServer 创建管理后台 API 服务实例。
func NewServer(options ServerOptions) (*Server, error) {
	tokenLookup := make(map[string]principal, len(options.BearerTokens))
	for _, tokenConfig := range options.BearerTokens {
		normalizedToken := strings.TrimSpace(tokenConfig.Token)
		if normalizedToken == "" {
			return nil, fmt.Errorf("new admin api server: empty bearer token")
		}
		normalizedRole, ok := normalizeRole(string(tokenConfig.Role))
		if !ok {
			return nil, fmt.Errorf("new admin api server: unsupported role=%s", tokenConfig.Role)
		}
		tokenLookup[normalizedToken] = principal{
			name: strings.TrimSpace(tokenConfig.Name),
			role: normalizedRole,
		}
	}
	maxPageLimit := options.MaxPageLimit
	if maxPageLimit <= 0 {
		maxPageLimit = defaultMaxPageLimit
	}
	auditLogLimit := options.AuditLogLimit
	if auditLogLimit <= 0 {
		auditLogLimit = defaultAuditLogLimit
	}
	server := &Server{
		dependencies: options.Dependencies,
		tokenLookup:  tokenLookup,
		maxPageLimit: maxPageLimit,
		auditLogs:    newAuditLogStore(auditLogLimit),
		exportStore:  newDiagnoseExportStore(defaultDiagnoseExportLimit, defaultDiagnoseExportTTL),
	}
	return server, nil
}

// RegisterRoutes 把管理后台只读 API 注册到 mux。
func (server *Server) RegisterRoutes(mux *http.ServeMux) {
	if server == nil || mux == nil {
		return
	}
	mux.Handle("/api/admin/bridge/overview", server.wrapHandler(RoleViewer, "bridge", "overview", server.handleBridgeOverview))
	mux.Handle("/api/admin/routes", server.wrapHandler(RoleViewer, "routes", "list", server.handleRoutesList))
	mux.Handle("/api/admin/connectors", server.wrapHandler(RoleViewer, "connectors", "list", server.handleConnectorsList))
	mux.Handle("/api/admin/sessions", server.wrapHandler(RoleViewer, "sessions", "list", server.handleSessionsList))
	mux.Handle("/api/admin/tunnels/summary", server.wrapHandler(RoleViewer, "tunnels", "summary", server.handleTunnelSummary))
	mux.Handle("/api/admin/tunnels", server.wrapHandler(RoleViewer, "tunnels", "list", server.handleTunnelsList))
	mux.Handle("/api/admin/traffic/summary", server.wrapHandler(RoleViewer, "traffic", "summary", server.handleTrafficSummary))
	mux.Handle("/api/admin/config/snapshot", server.wrapHandler(RoleViewer, "config", "snapshot", server.handleConfigSnapshot))
	mux.Handle("/api/admin/config", server.wrapHandler(RoleAdmin, "config", "update", server.handleConfigUpdate))
	mux.Handle("/api/admin/logs/search", server.wrapHandler(RoleViewer, "logs", "search", server.handleLogsSearch))
	mux.Handle("/api/admin/metrics/query", server.wrapHandler(RoleViewer, "metrics", "query", server.handleMetricsQuery))
	mux.Handle("/api/admin/diagnose/summary", server.wrapHandler(RoleViewer, "diagnose", "summary", server.handleDiagnoseSummary))
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
			server.auditLogs.append(AuditRecord{
				TSMS:      uint64(startAt.UnixMilli()),
				Actor:     "",
				Role:      "",
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
			server.auditLogs.append(AuditRecord{
				TSMS:      uint64(startAt.UnixMilli()),
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
		server.auditLogs.append(AuditRecord{
			TSMS:         uint64(startAt.UnixMilli()),
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
	authorization := strings.TrimSpace(request.Header.Get("Authorization"))
	if !strings.HasPrefix(strings.ToLower(authorization), "bearer ") {
		return principal{}, fmt.Errorf("missing bearer token")
	}
	rawToken := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(authorization, "Bearer "), "bearer "))
	if rawToken == "" {
		return principal{}, fmt.Errorf("missing bearer token")
	}
	if len(server.tokenLookup) == 0 {
		return principal{}, fmt.Errorf("admin api bearer token is not configured")
	}
	authPrincipal, exists := server.tokenLookup[rawToken]
	if !exists {
		return principal{}, fmt.Errorf("invalid bearer token")
	}
	if strings.TrimSpace(authPrincipal.name) == "" {
		authPrincipal.name = "token_user"
	}
	return authPrincipal, nil
}

func normalizeRole(role string) (Role, bool) {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case string(RoleViewer):
		return RoleViewer, true
	case string(RoleOperator):
		return RoleOperator, true
	case string(RoleAdmin):
		return RoleAdmin, true
	default:
		return "", false
	}
}

func roleCanAccess(currentRole Role, requiredRole Role) bool {
	priorityByRole := map[Role]int{
		RoleViewer:   1,
		RoleOperator: 2,
		RoleAdmin:    3,
	}
	return priorityByRole[currentRole] >= priorityByRole[requiredRole]
}

func (server *Server) handleBridgeOverview(writer http.ResponseWriter, request *http.Request) {
	if request.Method != http.MethodGet {
		writeError(writer, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "GET is required")
		return
	}
	sessions := safeListSessions(server.dependencies)
	services := safeListServices(server.dependencies)
	routes := safeListRoutes(server.dependencies)
	tunnelSnapshot := safeTunnelSnapshot(server.dependencies)
	overview := adminview.BuildBridgeOverview(server.now(), sessions, services, routes, tunnelSnapshot)
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
		safeListServices(server.dependencies),
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
	summary := adminview.BuildTunnelSummary(server.now(), safeTunnelSnapshot(server.dependencies))
	writeJSON(writer, http.StatusOK, map[string]any{
		"summary": summary,
		"source":  "bridge.adminapi.readonly",
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
				"ts_ms":                   uint64(server.now().UnixMilli()),
				"acquire_wait_count":      trafficSummary.AcquireWaitCount,
				"acquire_wait_total_ms":   trafficSummary.AcquireWaitTotalMS,
				"open_timeout_total":      trafficSummary.OpenTimeoutTotal,
				"open_reject_total":       trafficSummary.OpenRejectTotal,
				"open_ack_late_total":     trafficSummary.OpenAckLateTotal,
				"hybrid_fallback_total":   trafficSummary.HybridFallbackTotal,
				"endpoint_override_total": trafficSummary.EndpointOverrideTotal,
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

func safeListServices(dependencies Dependencies) []pb.Service {
	if dependencies.ListServices == nil {
		return []pb.Service{}
	}
	return dependencies.ListServices()
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

func safeTunnelSnapshot(dependencies Dependencies) registry.TunnelSnapshot {
	if dependencies.TunnelSnapshot == nil {
		return registry.TunnelSnapshot{}
	}
	return dependencies.TunnelSnapshot()
}
