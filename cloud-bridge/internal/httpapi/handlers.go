package httpapi

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"runtime/debug"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/backflow"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/config"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/discovery"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// BackflowHTTPCaller 抽象 bridge 到 agent 的回流调用能力。
type BackflowHTTPCaller interface {
	ForwardHTTP(ctx context.Context, baseURL string, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error)
	ForwardGRPC(ctx context.Context, baseURL string, request domain.BackflowGRPCRequest) (domain.BackflowGRPCResponse, error)
}

// UpstreamForwarder 抽象 bridge 直连上游能力（未命中 tunnel 时使用）。
type UpstreamForwarder interface {
	ForwardHTTP(ctx context.Context, endpoint discovery.Endpoint, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error)
	ForwardGRPC(ctx context.Context, endpoint discovery.Endpoint, request domain.BackflowGRPCRequest) (domain.BackflowGRPCResponse, error)
}

// ConfigEditor 抽象 bridge 配置文件编辑能力。
type ConfigEditor interface {
	LoadConfigFile() (path string, content string, err error)
	SaveConfigFile(content string) error
	TriggerHotRestart()
}

// Handler 提供 bridge 管理面与状态查询接口。
type Handler struct {
	pipeline                 *routing.Pipeline
	store                    *store.MemoryStore
	backflowCaller           BackflowHTTPCaller
	upstreamForwarder        UpstreamForwarder
	serviceDiscoveryResolver discovery.Resolver
	configEditor             ConfigEditor
	adminAuth                config.AdminAuthConfig
	fallbackBackflowURL      string
	httpTunnelSyncOn         bool
}

// HandlerOption 允许按需扩展 Handler 行为，不影响已有调用方。
type HandlerOption func(*Handler)

// WithTunnelEventHTTPEnabled 控制是否启用 HTTP tunnel 事件入口。
func WithTunnelEventHTTPEnabled(enabled bool) HandlerOption {
	return func(h *Handler) {
		h.httpTunnelSyncOn = enabled
	}
}

// WithServiceDiscoveryResolver 注入服务发现实现（用于 tunnel 未命中场景）。
func WithServiceDiscoveryResolver(resolver discovery.Resolver) HandlerOption {
	return func(h *Handler) {
		if resolver == nil {
			h.serviceDiscoveryResolver = discovery.NoopResolver{}
			return
		}
		h.serviceDiscoveryResolver = resolver
	}
}

// WithUpstreamForwarder 注入直连上游转发能力。
func WithUpstreamForwarder(forwarder UpstreamForwarder) HandlerOption {
	return func(h *Handler) {
		h.upstreamForwarder = forwarder
	}
}

// WithConfigEditor 注入 bridge 配置编辑器。
func WithConfigEditor(editor ConfigEditor) HandlerOption {
	return func(h *Handler) {
		h.configEditor = editor
	}
}

// WithAdminAuth 注入管理页认证配置。
func WithAdminAuth(auth config.AdminAuthConfig) HandlerOption {
	return func(h *Handler) {
		h.adminAuth = auth
	}
}

// NewHandler 创建 HTTP handler 集合。
func NewHandler(
	pipeline *routing.Pipeline,
	store *store.MemoryStore,
	backflowCaller BackflowHTTPCaller,
	fallbackBackflowURL string,
	options ...HandlerOption,
) *Handler {
	h := &Handler{
		pipeline:                 pipeline,
		store:                    store,
		backflowCaller:           backflowCaller,
		serviceDiscoveryResolver: discovery.NoopResolver{},
		fallbackBackflowURL:      strings.TrimSpace(fallbackBackflowURL),
		// 默认向后兼容：若上层未显式配置，HTTP tunnel 事件入口保持开启。
		httpTunnelSyncOn: true,
	}
	for _, option := range options {
		option(h)
	}
	return h
}

// Router 构建 cloud-bridge 路由表。
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	// Go 1.26 下 `GET /` 会与无方法前缀的 ingress 路由产生模式冲突，改为仅匹配根路径。
	mux.HandleFunc("GET /{$}", h.healthz)
	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /api/v1/state/sessions", h.sessions)
	mux.HandleFunc("GET /api/v1/state/intercepts", h.intercepts)
	mux.HandleFunc("GET /api/v1/state/routes", h.routes)
	mux.HandleFunc("GET /api/v1/state/errors", h.stateErrors)
	mux.HandleFunc("POST /api/v1/tunnel/events", h.tunnelEvent)
	mux.HandleFunc("GET /api/v1/debug/route-extract", h.debugRouteExtract)
	mux.HandleFunc("GET /admin", h.withAdminAuth(h.adminConfigPage))
	mux.HandleFunc("GET /api/v1/admin/config/file", h.withAdminAuth(h.adminGetConfigFile))
	mux.HandleFunc("POST /api/v1/admin/config/file", h.withAdminAuth(h.adminSaveConfigFile))
	mux.HandleFunc("GET /api/v1/admin/config/model", h.withAdminAuth(h.adminGetConfigModel))
	mux.HandleFunc("POST /api/v1/admin/config/model", h.withAdminAuth(h.adminSaveConfigModel))
	mux.HandleFunc("/api/v1/ingress/http", h.ingressHTTP)
	mux.HandleFunc("/api/v1/ingress/http/", h.ingressHTTP)
	mux.HandleFunc("/api/v1/ingress/grpc", h.ingressGRPC)
	mux.HandleFunc("/api/v1/ingress/grpc/", h.ingressGRPC)

	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	slog.Info("healthz", "status", "ok")
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) sessions(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListSessions())
}

func (h *Handler) intercepts(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListIntercepts())
}

func (h *Handler) routes(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListRoutes())
}

func (h *Handler) stateErrors(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListErrorStats())
}

func (h *Handler) withAdminAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !h.adminAuth.Enabled {
			next(w, r)
			return
		}
		username, password, ok := r.BasicAuth()
		if !ok || !h.validateAdminCredential(username, password) {
			h.writeAdminUnauthorized(w, r)
			return
		}
		next(w, r)
	}
}

func (h *Handler) validateAdminCredential(username string, password string) bool {
	expectedUser := strings.TrimSpace(h.adminAuth.Username)
	expectedPassword := h.adminAuth.Password
	if expectedUser == "" || strings.TrimSpace(expectedPassword) == "" {
		return false
	}
	return secureEqual(username, expectedUser) && secureEqual(password, expectedPassword)
}

func (h *Handler) writeAdminUnauthorized(w http.ResponseWriter, r *http.Request) {
	realm := strings.TrimSpace(h.adminAuth.Realm)
	if realm == "" {
		realm = "bridge-console"
	}
	w.Header().Set("WWW-Authenticate", fmt.Sprintf(`Basic realm=%q, charset="UTF-8"`, realm))

	// 管理页 API 返回 JSON，方便前端明确识别 401；页面请求保留浏览器默认 Basic Auth 提示。
	if strings.HasPrefix(strings.TrimSpace(r.URL.Path), "/api/v1/admin/") {
		respondJSON(w, http.StatusUnauthorized, map[string]any{
			"error":   "admin unauthorized",
			"message": "admin authentication is required",
		})
		return
	}
	http.Error(w, "admin authentication is required", http.StatusUnauthorized)
}

func (h *Handler) adminConfigPage(w http.ResponseWriter, _ *http.Request) {
	if h.configEditor == nil {
		http.Error(w, "config editor is unavailable", http.StatusServiceUnavailable)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(adminConfigPageHTML))
}

func (h *Handler) adminGetConfigFile(w http.ResponseWriter, _ *http.Request) {
	if h.configEditor == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config editor is unavailable",
			"message": "bridge config editor is unavailable",
		})
		return
	}
	path, content, err := h.configEditor.LoadConfigFile()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "load config failed",
			"message": err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"path":    path,
		"content": content,
	})
}

func (h *Handler) adminSaveConfigFile(w http.ResponseWriter, r *http.Request) {
	if h.configEditor == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config editor is unavailable",
			"message": "bridge config editor is unavailable",
		})
		return
	}
	var request struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid payload",
			"message": err.Error(),
		})
		return
	}
	if err := h.configEditor.SaveConfigFile(request.Content); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "save config failed",
			"message": err.Error(),
		})
		return
	}

	// 配置保存成功后异步触发热重启，确保当前请求可以稳定回包给前端。
	h.configEditor.TriggerHotRestart()
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"restarting": true,
		"message":    "config saved, bridge is restarting",
	})
}

func (h *Handler) adminGetConfigModel(w http.ResponseWriter, _ *http.Request) {
	if h.configEditor == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config editor is unavailable",
			"message": "bridge config editor is unavailable",
		})
		return
	}
	path, content, err := h.configEditor.LoadConfigFile()
	if err != nil {
		respondJSON(w, http.StatusInternalServerError, map[string]any{
			"error":   "load config failed",
			"message": err.Error(),
		})
		return
	}

	model, err := config.ParseConfigYAMLToAdminModel(path, []byte(content))
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "parse config failed",
			"message": err.Error(),
		})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"path":  path,
		"model": model,
	})
}

func (h *Handler) adminSaveConfigModel(w http.ResponseWriter, r *http.Request) {
	if h.configEditor == nil {
		respondJSON(w, http.StatusServiceUnavailable, map[string]any{
			"error":   "config editor is unavailable",
			"message": "bridge config editor is unavailable",
		})
		return
	}
	var request struct {
		Model config.AdminConfigModel `json:"model"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "invalid payload",
			"message": err.Error(),
		})
		return
	}

	content, err := config.MarshalAdminModelYAML(request.Model)
	if err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "render config failed",
			"message": err.Error(),
		})
		return
	}
	if err := h.configEditor.SaveConfigFile(string(content)); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]any{
			"error":   "save config failed",
			"message": err.Error(),
		})
		return
	}
	h.configEditor.TriggerHotRestart()
	respondJSON(w, http.StatusOK, map[string]any{
		"ok":         true,
		"restarting": true,
		"message":    "config saved, bridge is restarting",
	})
}

func (h *Handler) tunnelEvent(w http.ResponseWriter, r *http.Request) {
	// 当 bridge 未启用 HTTP tunnel 协议时，显式拒绝该入口请求。
	if !h.httpTunnelSyncOn {
		h.store.AddError(domain.SyncErrorProtocolDisabled, "http tunnel sync protocol is disabled", map[string]string{
			"path": strings.TrimSpace(r.URL.Path),
		})
		respondJSON(w, http.StatusServiceUnavailable, domain.TunnelEventReply{
			Type:         domain.TunnelMessageError,
			Status:       domain.EventStatusRejected,
			ErrorCode:    domain.SyncErrorProtocolDisabled,
			Message:      "http tunnel sync protocol is disabled",
			EventID:      "",
			SessionEpoch: 0,
		})
		return
	}

	var event domain.TunnelEvent
	defer func() {
		// 避免单次异常导致连接被强制关闭，尽量返回可诊断错误给 agent。
		if recovered := recover(); recovered != nil {
			h.store.AddError(domain.SyncErrorInvalidPayload, "panic while processing tunnel event", map[string]string{
				"eventId":      strings.TrimSpace(event.EventID),
				"type":         strings.TrimSpace(event.Type),
				"sessionEpoch": strconv.FormatInt(event.SessionEpoch, 10),
				"panic":        fmt.Sprintf("%v", recovered),
			})
			slog.Error("panic while processing tunnel event", "panic", recovered, "stack", string(debug.Stack()))
			respondJSON(w, http.StatusInternalServerError, domain.TunnelEventReply{
				Type:         domain.TunnelMessageError,
				Status:       domain.EventStatusRejected,
				EventID:      strings.TrimSpace(event.EventID),
				SessionEpoch: event.SessionEpoch,
				ErrorCode:    domain.SyncErrorInvalidPayload,
				Message:      fmt.Sprintf("bridge panic: %v", recovered),
			})
		}
	}()

	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
		h.store.AddError(domain.SyncErrorInvalidPayload, "invalid tunnel event payload", map[string]string{
			"error": err.Error(),
		})
		respondJSON(w, http.StatusBadRequest, domain.TunnelEventReply{
			Type:         domain.TunnelMessageError,
			Status:       domain.EventStatusRejected,
			ErrorCode:    domain.SyncErrorInvalidPayload,
			Message:      "invalid payload",
			EventID:      "",
			SessionEpoch: 0,
		})
		return
	}
	if event.SentAt.IsZero() {
		event.SentAt = time.Now().UTC()
	}

	reply, err := h.store.ProcessTunnelEvent(event)
	if err != nil {
		var reject *store.EventRejectError
		if errors.As(err, &reject) {
			h.store.AddError(reject.Code, reject.Message, map[string]string{
				"eventId":      strings.TrimSpace(event.EventID),
				"type":         strings.TrimSpace(event.Type),
				"sessionEpoch": strconv.FormatInt(event.SessionEpoch, 10),
			})
			respondJSON(w, reject.StatusCode, domain.TunnelEventReply{
				Type:            domain.TunnelMessageError,
				Status:          domain.EventStatusRejected,
				EventID:         event.EventID,
				SessionEpoch:    selectSessionEpoch(event.SessionEpoch, reject.SessionEpoch),
				ResourceVersion: reject.ResourceVersion,
				Deduplicated:    false,
				ErrorCode:       reject.Code,
				Message:         reject.Message,
			})
			return
		}

		h.store.AddError(domain.SyncErrorInvalidPayload, "process tunnel event failed", map[string]string{
			"eventId":      strings.TrimSpace(event.EventID),
			"type":         strings.TrimSpace(event.Type),
			"sessionEpoch": strconv.FormatInt(event.SessionEpoch, 10),
			"error":        err.Error(),
		})
		respondJSON(w, http.StatusInternalServerError, domain.TunnelEventReply{
			Type:         domain.TunnelMessageError,
			Status:       domain.EventStatusRejected,
			EventID:      event.EventID,
			SessionEpoch: event.SessionEpoch,
			ErrorCode:    domain.SyncErrorInvalidPayload,
			Message:      err.Error(),
		})
		return
	}

	statusCode := http.StatusAccepted
	if reply.Status == domain.EventStatusDuplicate {
		statusCode = http.StatusOK
	}
	respondJSON(w, statusCode, reply)
}

func (h *Handler) debugRouteExtract(w http.ResponseWriter, r *http.Request) {
	result, err := h.pipeline.Resolve(r.Context(), r)
	if err != nil {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, map[string]any{
		"result": result,
		"order":  h.pipeline.DebugString(),
	})
}

func (h *Handler) ingressHTTP(w http.ResponseWriter, r *http.Request) {
	result, err := h.pipeline.Resolve(r.Context(), r)
	if err != nil {
		h.store.AddError(domain.IngressErrorRouteExtractFailed, "resolve ingress route failed", map[string]string{
			"host":  strings.TrimSpace(r.Host),
			"path":  strings.TrimSpace(r.URL.Path),
			"error": err.Error(),
		})
		writeIngressError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, err.Error())
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		h.store.AddError(domain.IngressErrorRouteExtractFailed, "read ingress request body failed", map[string]string{
			"path":  strings.TrimSpace(r.URL.Path),
			"error": err.Error(),
		})
		writeIngressError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, "read ingress request body failed")
		return
	}

	backflowRequest := domain.BackflowHTTPRequest{
		Method:   r.Method,
		Path:     normalizeIngressPath(r.URL.Path),
		RawQuery: r.URL.RawQuery,
		Host:     r.Host,
		Headers:  cloneHeaders(r.Header),
		Body:     body,
		Protocol: "http",
	}

	if route, session, localMatched := h.store.ResolveRouteForIngress(result.Env, result.ServiceName, "http"); localMatched {
		baseURL := strings.TrimSpace(session.BackflowBaseURL)
		if baseURL == "" {
			baseURL = h.fallbackBackflowURL
		}
		if baseURL == "" {
			h.store.AddError(domain.IngressErrorTunnelOffline, "backflow endpoint is unavailable", map[string]string{
				"env":         strings.TrimSpace(route.Env),
				"serviceName": strings.TrimSpace(route.ServiceName),
				"tunnelId":    strings.TrimSpace(route.TunnelID),
			})
			writeIngressError(w, http.StatusServiceUnavailable, domain.IngressErrorTunnelOffline, "backflow endpoint is unavailable")
			return
		}

		forwardResp, err := h.backflowCaller.ForwardHTTP(r.Context(), baseURL, domain.BackflowHTTPRequest{
			Method:     backflowRequest.Method,
			Path:       backflowRequest.Path,
			RawQuery:   backflowRequest.RawQuery,
			Host:       backflowRequest.Host,
			Headers:    backflowRequest.Headers,
			Body:       backflowRequest.Body,
			TargetHost: "127.0.0.1",
			TargetPort: route.TargetPort,
			Protocol:   "http",
		})
		if err != nil {
			var backflowErr *backflow.Error
			if errors.As(err, &backflowErr) {
				h.store.AddError(firstNonEmpty(backflowErr.ErrorCode, domain.IngressErrorTunnelOffline), firstNonEmpty(backflowErr.Message, "call agent backflow failed"), map[string]string{
					"env":         strings.TrimSpace(route.Env),
					"serviceName": strings.TrimSpace(route.ServiceName),
					"tunnelId":    strings.TrimSpace(route.TunnelID),
					"baseURL":     strings.TrimSpace(baseURL),
				})
				writeIngressError(
					w,
					statusOrDefault(backflowErr.StatusCode, http.StatusBadGateway),
					firstNonEmpty(backflowErr.ErrorCode, domain.IngressErrorTunnelOffline),
					firstNonEmpty(backflowErr.Message, "call agent backflow failed"),
				)
				return
			}
			h.store.AddError(domain.IngressErrorTunnelOffline, "call agent backflow failed", map[string]string{
				"env":         strings.TrimSpace(route.Env),
				"serviceName": strings.TrimSpace(route.ServiceName),
				"tunnelId":    strings.TrimSpace(route.TunnelID),
				"baseURL":     strings.TrimSpace(baseURL),
				"error":       err.Error(),
			})
			writeIngressError(w, http.StatusBadGateway, domain.IngressErrorTunnelOffline, err.Error())
			return
		}

		// 透传目标服务返回头时过滤 hop-by-hop 头，避免代理链路语义冲突。
		copyResponseHeaders(w, forwardResp.Headers)
		w.Header().Set("X-DevLoop-Route-Env", route.Env)
		w.Header().Set("X-DevLoop-Route-Service", route.ServiceName)
		w.Header().Set("X-DevLoop-Route-Protocol", route.Protocol)
		w.Header().Set("X-DevLoop-Route-Tunnel", route.TunnelID)
		w.Header().Set("X-DevLoop-Route-Source", "tunnel")

		statusCode := statusOrDefault(forwardResp.StatusCode, http.StatusOK)
		w.WriteHeader(statusCode)
		if len(forwardResp.Body) > 0 {
			_, _ = w.Write(forwardResp.Body)
		}
		return
	}

	endpoint, matched, discoveryErr := h.resolveDiscoveredEndpoint(r.Context(), result.Env, result.ServiceName, "http")
	if discoveryErr != nil {
		h.store.AddError(domain.IngressErrorServiceDiscoveryFailed, "service discovery lookup failed", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "http",
			"error":       discoveryErr.Error(),
		})
		writeIngressError(w, http.StatusBadGateway, domain.IngressErrorServiceDiscoveryFailed, discoveryErr.Error())
		return
	}
	if !matched {
		h.store.AddError(domain.IngressErrorRouteNotFound, "ingress route not found", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "http",
		})
		writeIngressError(w, http.StatusNotFound, domain.IngressErrorRouteNotFound, "route not found for env/service/protocol")
		return
	}
	if h.upstreamForwarder == nil {
		h.store.AddError(domain.IngressErrorLocalEndpointDown, "upstream forwarder is unavailable", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "http",
			"target":      endpoint.Target(),
		})
		writeIngressError(w, http.StatusServiceUnavailable, domain.IngressErrorLocalEndpointDown, "upstream forwarder is unavailable")
		return
	}

	forwardResp, err := h.upstreamForwarder.ForwardHTTP(r.Context(), endpoint, domain.BackflowHTTPRequest{
		Method:   backflowRequest.Method,
		Path:     backflowRequest.Path,
		RawQuery: backflowRequest.RawQuery,
		Host:     backflowRequest.Host,
		Headers:  backflowRequest.Headers,
		Body:     backflowRequest.Body,
		Protocol: backflowRequest.Protocol,
	})
	if err != nil {
		h.store.AddError(domain.IngressErrorLocalEndpointDown, "forward request to discovered endpoint failed", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "http",
			"target":      endpoint.Target(),
			"source":      endpoint.Source,
			"error":       err.Error(),
		})
		writeIngressError(w, http.StatusBadGateway, domain.IngressErrorLocalEndpointDown, err.Error())
		return
	}

	copyResponseHeaders(w, forwardResp.Headers)
	w.Header().Set("X-DevLoop-Route-Env", strings.TrimSpace(result.Env))
	w.Header().Set("X-DevLoop-Route-Service", strings.TrimSpace(result.ServiceName))
	w.Header().Set("X-DevLoop-Route-Protocol", "http")
	w.Header().Set("X-DevLoop-Route-Source", "discovery:"+endpoint.Source)
	w.Header().Set("X-DevLoop-Route-Target", endpoint.Target())

	statusCode := statusOrDefault(forwardResp.StatusCode, http.StatusOK)
	w.WriteHeader(statusCode)
	if len(forwardResp.Body) > 0 {
		_, _ = w.Write(forwardResp.Body)
	}
}

func (h *Handler) ingressGRPC(w http.ResponseWriter, r *http.Request) {
	result, err := h.pipeline.Resolve(r.Context(), r)
	if err != nil {
		h.store.AddError(domain.IngressErrorRouteExtractFailed, "resolve grpc ingress route failed", map[string]string{
			"host":  strings.TrimSpace(r.Host),
			"path":  strings.TrimSpace(r.URL.Path),
			"error": err.Error(),
		})
		writeIngressGRPCError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, err.Error(), "")
		return
	}

	var request domain.IngressGRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil && !errors.Is(err, io.EOF) {
		h.store.AddError(domain.IngressErrorRouteExtractFailed, "decode grpc ingress payload failed", map[string]string{
			"path":  strings.TrimSpace(r.URL.Path),
			"error": err.Error(),
		})
		writeIngressGRPCError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, "invalid grpc ingress payload", "")
		return
	}

	if route, session, localMatched := h.store.ResolveRouteForIngress(result.Env, result.ServiceName, "grpc"); localMatched {
		baseURL := strings.TrimSpace(session.BackflowBaseURL)
		if baseURL == "" {
			baseURL = h.fallbackBackflowURL
		}
		if baseURL == "" {
			h.store.AddError(domain.IngressErrorTunnelOffline, "grpc backflow endpoint is unavailable", map[string]string{
				"env":         strings.TrimSpace(route.Env),
				"serviceName": strings.TrimSpace(route.ServiceName),
				"tunnelId":    strings.TrimSpace(route.TunnelID),
			})
			writeIngressGRPCError(w, http.StatusServiceUnavailable, domain.IngressErrorTunnelOffline, "backflow endpoint is unavailable", "")
			return
		}

		startedAt := time.Now().UTC()
		forwardResp, err := h.backflowCaller.ForwardGRPC(r.Context(), baseURL, domain.BackflowGRPCRequest{
			TargetHost:    "127.0.0.1",
			TargetPort:    route.TargetPort,
			Env:           route.Env,
			HealthService: strings.TrimSpace(request.HealthService),
			TimeoutMs:     request.TimeoutMs,
		})
		if err != nil {
			var backflowErr *backflow.Error
			if errors.As(err, &backflowErr) {
				errorCode := firstNonEmpty(backflowErr.ErrorCode, domain.IngressErrorTunnelOffline)
				message := firstNonEmpty(backflowErr.Message, "call agent grpc backflow failed")
				h.store.AddError(errorCode, message, map[string]string{
					"env":         strings.TrimSpace(route.Env),
					"serviceName": strings.TrimSpace(route.ServiceName),
					"tunnelId":    strings.TrimSpace(route.TunnelID),
					"baseURL":     strings.TrimSpace(baseURL),
				})
				writeIngressGRPCError(w, statusOrDefault(backflowErr.StatusCode, http.StatusBadGateway), errorCode, message, "")
				return
			}
			h.store.AddError(domain.IngressErrorTunnelOffline, "call agent grpc backflow failed", map[string]string{
				"env":         strings.TrimSpace(route.Env),
				"serviceName": strings.TrimSpace(route.ServiceName),
				"tunnelId":    strings.TrimSpace(route.TunnelID),
				"baseURL":     strings.TrimSpace(baseURL),
				"error":       err.Error(),
			})
			writeIngressGRPCError(w, http.StatusBadGateway, domain.IngressErrorTunnelOffline, err.Error(), "")
			return
		}

		// gRPC ingress 返回 JSON 结果，同时通过响应头暴露命中路由，便于调试和观测。
		w.Header().Set("X-DevLoop-Route-Env", route.Env)
		w.Header().Set("X-DevLoop-Route-Service", route.ServiceName)
		w.Header().Set("X-DevLoop-Route-Protocol", route.Protocol)
		w.Header().Set("X-DevLoop-Route-Tunnel", route.TunnelID)
		w.Header().Set("X-DevLoop-Route-Source", "tunnel")
		respondJSON(w, http.StatusOK, domain.IngressGRPCResponse{
			Status:      firstNonEmpty(forwardResp.Status, "UNKNOWN"),
			ResolvedEnv: route.Env,
			ServiceName: route.ServiceName,
			Protocol:    route.Protocol,
			TunnelID:    route.TunnelID,
			Target:      firstNonEmpty(forwardResp.Target, "127.0.0.1:"+strconv.Itoa(route.TargetPort)),
			LatencyMs:   maxInt64Value(forwardResp.LatencyMs, time.Since(startedAt).Milliseconds()),
			ErrorCode:   "",
			Message:     "",
		})
		return
	}

	endpoint, matched, discoveryErr := h.resolveDiscoveredEndpoint(r.Context(), result.Env, result.ServiceName, "grpc")
	if discoveryErr != nil {
		h.store.AddError(domain.IngressErrorServiceDiscoveryFailed, "grpc service discovery lookup failed", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "grpc",
			"error":       discoveryErr.Error(),
		})
		writeIngressGRPCError(w, http.StatusBadGateway, domain.IngressErrorServiceDiscoveryFailed, discoveryErr.Error(), "")
		return
	}
	if !matched {
		h.store.AddError(domain.IngressErrorRouteNotFound, "grpc ingress route not found", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "grpc",
		})
		writeIngressGRPCError(w, http.StatusNotFound, domain.IngressErrorRouteNotFound, "route not found for env/service/protocol", "")
		return
	}
	if h.upstreamForwarder == nil {
		h.store.AddError(domain.IngressErrorLocalEndpointDown, "upstream forwarder is unavailable", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "grpc",
			"target":      endpoint.Target(),
		})
		writeIngressGRPCError(w, http.StatusServiceUnavailable, domain.IngressErrorLocalEndpointDown, "upstream forwarder is unavailable", endpoint.Target())
		return
	}

	startedAt := time.Now().UTC()
	forwardResp, err := h.upstreamForwarder.ForwardGRPC(r.Context(), endpoint, domain.BackflowGRPCRequest{
		TargetHost:    endpoint.Host,
		TargetPort:    endpoint.Port,
		Env:           strings.TrimSpace(result.Env),
		HealthService: strings.TrimSpace(request.HealthService),
		TimeoutMs:     request.TimeoutMs,
	})
	if err != nil {
		h.store.AddError(domain.IngressErrorLocalEndpointDown, "call discovered grpc endpoint failed", map[string]string{
			"env":         strings.TrimSpace(result.Env),
			"serviceName": strings.TrimSpace(result.ServiceName),
			"protocol":    "grpc",
			"target":      endpoint.Target(),
			"source":      endpoint.Source,
			"error":       err.Error(),
		})
		writeIngressGRPCError(w, http.StatusBadGateway, domain.IngressErrorLocalEndpointDown, err.Error(), endpoint.Target())
		return
	}

	w.Header().Set("X-DevLoop-Route-Env", strings.TrimSpace(result.Env))
	w.Header().Set("X-DevLoop-Route-Service", strings.TrimSpace(result.ServiceName))
	w.Header().Set("X-DevLoop-Route-Protocol", "grpc")
	w.Header().Set("X-DevLoop-Route-Source", "discovery:"+endpoint.Source)
	w.Header().Set("X-DevLoop-Route-Target", endpoint.Target())
	respondJSON(w, http.StatusOK, domain.IngressGRPCResponse{
		Status:      firstNonEmpty(forwardResp.Status, "UNKNOWN"),
		ResolvedEnv: strings.TrimSpace(result.Env),
		ServiceName: strings.TrimSpace(result.ServiceName),
		Protocol:    "grpc",
		TunnelID:    "",
		Target:      firstNonEmpty(forwardResp.Target, endpoint.Target()),
		LatencyMs:   maxInt64Value(forwardResp.LatencyMs, time.Since(startedAt).Milliseconds()),
		ErrorCode:   "",
		Message:     "",
	})
}

func (h *Handler) resolveDiscoveredEndpoint(ctx context.Context, env string, serviceName string, protocol string) (discovery.Endpoint, bool, error) {
	if h.serviceDiscoveryResolver == nil {
		return discovery.Endpoint{}, false, nil
	}
	return h.serviceDiscoveryResolver.Resolve(ctx, discovery.Query{
		Env:         strings.TrimSpace(env),
		ServiceName: strings.TrimSpace(serviceName),
		Protocol:    strings.TrimSpace(protocol),
	})
}

const adminConfigPageHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>cloud-bridge 配置中心</title>
  <style>
    :root{
      --bg:#f7f9fc;
      --panel:#ffffff;
      --panel-soft:#f6f7fb;
      --line:#d6d9e3;
      --text:#1f2937;
      --muted:#6b7280;
      --accent:#ca5b1a;
      --accent-soft:#fff1e8;
      --ok:#15803d;
      --err:#b91c1c;
    }
    *{box-sizing:border-box}
    body{
      margin:0;
      font-family:"Source Han Sans SC","Noto Sans SC","PingFang SC","Microsoft YaHei",sans-serif;
      color:var(--text);
      background:radial-gradient(circle at 20% -10%, #fff3e6 0%, transparent 42%),linear-gradient(140deg,#f4f8ff 0%,#f9fafb 100%);
      min-height:100vh;
    }
    .layout{
      max-width:1280px;
      margin:20px auto;
      padding:0 16px 20px;
      display:grid;
      grid-template-columns:260px 1fr;
      gap:14px;
    }
    .sidebar{
      background:var(--panel);
      border:1px solid var(--line);
      border-radius:16px;
      padding:14px;
      box-shadow:0 12px 30px rgba(31,41,55,.06);
      position:sticky;
      top:16px;
      height:fit-content;
    }
    .brand{
      margin-bottom:12px;
      padding-bottom:12px;
      border-bottom:1px dashed var(--line);
    }
    .brand h1{
      font-size:22px;
      margin:0;
      letter-spacing:.4px;
      font-weight:800;
    }
    .brand p{
      margin:6px 0 0;
      color:var(--muted);
      font-size:12px;
    }
    .menu{
      display:flex;
      flex-direction:column;
      gap:6px;
    }
    .menu button{
      width:100%;
      text-align:left;
      background:#fff;
      border:1px solid var(--line);
      border-radius:10px;
      padding:10px 11px;
      font-size:14px;
      font-weight:700;
      color:var(--text);
      cursor:pointer;
      transition:.15s ease;
    }
    .menu button.active{
      background:var(--accent-soft);
      border-color:#f0b896;
      color:#8d3e12;
    }
    .menu button:hover{transform:translateY(-1px)}
    .main{
      background:var(--panel);
      border:1px solid var(--line);
      border-radius:16px;
      box-shadow:0 12px 30px rgba(31,41,55,.06);
      overflow:hidden;
    }
    .toolbar{
      display:flex;
      justify-content:space-between;
      gap:12px;
      align-items:center;
      padding:12px 14px;
      border-bottom:1px solid var(--line);
      background:var(--panel-soft);
      flex-wrap:wrap;
    }
    .toolbar .meta{
      min-width:340px;
    }
    .toolbar .meta .path{
      font-size:12px;
      color:var(--muted);
      word-break:break-all;
    }
    .toolbar .meta .status{
      margin-top:5px;
      display:inline-block;
      font-size:12px;
      padding:4px 9px;
      border:1px solid var(--line);
      border-radius:999px;
      background:#fff;
      color:var(--muted);
    }
    .toolbar .actions{
      display:flex;
      gap:8px;
      flex-wrap:wrap;
    }
    button{
      border:1px solid transparent;
      border-radius:10px;
      padding:9px 12px;
      font-size:13px;
      font-weight:700;
      cursor:pointer;
    }
    .btn-secondary{background:#fff;border-color:var(--line);color:var(--text)}
    .btn-primary{background:var(--accent);color:#fff}
    .btn-primary:disabled,.btn-secondary:disabled{opacity:.6;cursor:not-allowed}
    .content{
      padding:14px;
      display:flex;
      flex-direction:column;
      gap:12px;
    }
    .section{display:none}
    .section.active{display:block}
    .panel{
      border:1px solid var(--line);
      border-radius:14px;
      padding:12px;
      background:#fff;
    }
    .panel h3{
      margin:0 0 10px;
      font-size:15px;
      font-weight:800;
    }
    .grid{
      display:grid;
      grid-template-columns:repeat(2,minmax(0,1fr));
      gap:10px;
    }
    .field{
      display:flex;
      flex-direction:column;
      gap:5px;
    }
    .field label{
      font-size:12px;
      color:var(--muted);
      font-weight:700;
    }
    .field input,.field select,.field textarea{
      width:100%;
      border:1px solid var(--line);
      border-radius:9px;
      padding:8px 9px;
      font-size:13px;
      background:#fff;
      color:var(--text);
    }
    .field textarea{
      min-height:90px;
      font-family:"JetBrains Mono","Fira Code","Consolas",monospace;
      resize:vertical;
    }
    .field input:focus,.field select:focus,.field textarea:focus{
      outline:2px solid rgba(37,99,235,.14);
      border-color:#93c5fd;
    }
    .chips{
      display:flex;
      gap:8px;
      flex-wrap:wrap;
    }
    .chip{
      padding:5px 9px;
      border:1px solid var(--line);
      border-radius:999px;
      background:#fff;
      font-size:12px;
      color:var(--text);
      display:flex;
      align-items:center;
      gap:5px;
    }
    .chip input{margin:0}
    .routes{
      display:flex;
      flex-direction:column;
      gap:8px;
    }
    .route-row{
      display:grid;
      grid-template-columns:1.2fr 1.2fr 1fr 1.6fr .8fr auto;
      gap:8px;
      align-items:center;
      border:1px solid var(--line);
      border-radius:10px;
      padding:8px;
      background:#fff;
    }
    .route-row input,.route-row select{
      border:1px solid var(--line);
      border-radius:8px;
      padding:7px 8px;
      font-size:12px;
    }
    .route-row button{
      background:#fff;
      border:1px solid #f1b8b8;
      color:#a61b1b;
      padding:7px 8px;
      font-size:12px;
    }
    .message{
      font-size:13px;
      color:var(--muted);
      min-height:20px;
    }
    .message.ok{color:var(--ok)}
    .message.err{color:var(--err)}
    .hidden{display:none !important}
    @media (max-width:980px){
      .layout{grid-template-columns:1fr}
      .sidebar{position:static}
      .grid{grid-template-columns:1fr}
      .route-row{grid-template-columns:1fr 1fr}
    }
  </style>
</head>
<body>
  <div class="layout">
    <aside class="sidebar">
      <div class="brand">
        <h1>bridge 控制台</h1>
        <p>参数编辑后自动热重启</p>
      </div>
      <div class="menu">
        <button class="menu-item active" data-section="basic">基础设置</button>
        <button class="menu-item" data-section="auth">控制台认证</button>
        <button class="menu-item" data-section="protocol">协议设置</button>
        <button class="menu-item" data-section="discovery">服务发现</button>
        <button class="menu-item" data-section="routes">本地路由</button>
      </div>
    </aside>
    <main class="main">
      <div class="toolbar">
        <div class="meta">
          <div class="path">配置文件：<span id="configPath">加载中...</span></div>
          <span id="runtimeStatus" class="status">就绪</span>
        </div>
        <div class="actions">
          <button id="reloadBtn" class="btn-secondary" type="button">重新加载</button>
          <button id="saveBtn" class="btn-primary" type="button">保存并热重启</button>
        </div>
      </div>
      <div class="content">
        <div id="message" class="message"></div>

        <section id="section-basic" class="section active">
          <div class="panel">
            <h3>基础设置</h3>
            <div class="grid">
              <div class="field"><label>HTTP 监听地址</label><input id="httpAddr" placeholder="0.0.0.0:38080" /></div>
              <div class="field"><label>回流兜底地址</label><input id="fallbackBackflowUrl" placeholder="http://127.0.0.1:39090" /></div>
              <div class="field"><label>对外域名</label><input id="bridgePublicHost" placeholder="bridge.example.internal" /></div>
              <div class="field"><label>对外端口</label><input id="bridgePublicPort" type="number" min="1" /></div>
              <div class="field"><label>Ingress 超时(秒)</label><input id="ingressTimeoutSec" type="number" min="1" /></div>
              <div class="field"><label>路由提取顺序(逗号分隔)</label><input id="routeExtractorOrder" placeholder="host,header,sni" /></div>
            </div>
          </div>
        </section>

        <section id="section-auth" class="section">
          <div class="panel">
            <h3>控制台认证</h3>
            <div class="chips">
              <label class="chip"><input id="adminAuthEnabled" type="checkbox" />启用 Basic 认证</label>
            </div>
            <div id="adminAuthDetail" class="grid" style="margin-top:10px">
              <div class="field"><label>用户名</label><input id="adminAuthUsername" placeholder="admin" /></div>
              <div class="field"><label>密码</label><input id="adminAuthPassword" type="password" placeholder="devloop-admin" /></div>
              <div class="field"><label>认证域(Realm)</label><input id="adminAuthRealm" placeholder="bridge-console" /></div>
            </div>
          </div>
        </section>

        <section id="section-protocol" class="section">
          <div class="panel">
            <h3>同步协议</h3>
            <div class="chips">
              <label class="chip"><input id="syncHttp" type="checkbox" />HTTP</label>
              <label class="chip"><input id="syncMasque" type="checkbox" />MASQUE</label>
            </div>
          </div>
          <div id="masquePanel" class="panel">
            <h3>MASQUE 参数</h3>
            <div class="grid">
              <div class="field"><label>MASQUE 监听地址</label><input id="masqueAddr" placeholder="0.0.0.0:38080" /></div>
              <div class="field"><label>Tunnel UDP 地址</label><input id="masqueTunnelUdpAddr" placeholder="127.0.0.1:39081" /></div>
              <div class="field"><label>鉴权模式</label><select id="masqueAuthMode"><option value="psk">psk</option><option value="ecdh">ecdh</option></select></div>
              <div class="field"><label>PSK</label><input id="masquePsk" placeholder="devloop-masque-default-psk" /></div>
            </div>
          </div>
        </section>

        <section id="section-discovery" class="section">
          <div class="panel">
            <h3>发现总开关</h3>
            <div class="chips">
              <label class="chip"><input id="backendLocal" type="checkbox" />local</label>
              <label class="chip"><input id="backendNacos" type="checkbox" />nacos</label>
              <label class="chip"><input id="backendEtcd" type="checkbox" />etcd</label>
              <label class="chip"><input id="backendConsul" type="checkbox" />consul</label>
            </div>
            <div class="grid" style="margin-top:10px">
              <div class="field"><label>发现超时(ms)</label><input id="discoveryTimeoutMs" type="number" min="1" /></div>
              <div class="field"><label>local 配置文件</label><input id="discoveryLocalFile" placeholder="./bridge-config.yaml" /></div>
            </div>
          </div>
          <div id="nacosPanel" class="panel">
            <h3>Nacos</h3>
            <div class="grid">
              <div class="field"><label>地址</label><input id="nacosAddr" placeholder="127.0.0.1:8848" /></div>
              <div class="field"><label>Namespace</label><input id="nacosNamespace" /></div>
              <div class="field"><label>Group</label><input id="nacosGroup" placeholder="DEFAULT_GROUP" /></div>
              <div class="field"><label>Service Pattern</label><input id="nacosServicePattern" placeholder="${service}" /></div>
              <div class="field"><label>用户名</label><input id="nacosUsername" /></div>
              <div class="field"><label>密码</label><input id="nacosPassword" type="password" /></div>
            </div>
          </div>
          <div id="etcdPanel" class="panel">
            <h3>Etcd</h3>
            <div class="grid">
              <div class="field"><label>Endpoints(逗号分隔)</label><input id="etcdEndpoints" placeholder="127.0.0.1:2379" /></div>
              <div class="field"><label>Key Prefix</label><input id="etcdKeyPrefix" placeholder="/devloop/services" /></div>
            </div>
          </div>
          <div id="consulPanel" class="panel">
            <h3>Consul</h3>
            <div class="grid">
              <div class="field"><label>地址</label><input id="consulAddr" placeholder="127.0.0.1:8500" /></div>
              <div class="field"><label>Datacenter</label><input id="consulDatacenter" /></div>
              <div class="field"><label>Service Pattern</label><input id="consulServicePattern" placeholder="${service}" /></div>
            </div>
          </div>
        </section>

        <section id="section-routes" class="section">
          <div class="panel">
            <h3>本地路由 (local)</h3>
            <div id="routeRows" class="routes"></div>
            <div style="margin-top:10px">
              <button id="addRouteBtn" class="btn-secondary" type="button">新增路由</button>
            </div>
          </div>
        </section>
      </div>
    </main>
  </div>

  <template id="routeRowTpl">
    <div class="route-row">
      <input data-key="env" placeholder="env" />
      <input data-key="serviceName" placeholder="serviceName" />
      <select data-key="protocol"><option value="http">http</option><option value="grpc">grpc</option></select>
      <input data-key="host" placeholder="127.0.0.1" />
      <input data-key="port" type="number" min="1" placeholder="8080" />
      <button data-action="delete" type="button">删除</button>
    </div>
  </template>

  <script>
    const dom = {
      configPath: document.getElementById('configPath'),
      runtimeStatus: document.getElementById('runtimeStatus'),
      message: document.getElementById('message'),
      reloadBtn: document.getElementById('reloadBtn'),
      saveBtn: document.getElementById('saveBtn'),
      addRouteBtn: document.getElementById('addRouteBtn'),
      routeRows: document.getElementById('routeRows'),
      routeTpl: document.getElementById('routeRowTpl'),
    };
    let currentPath = '';

    function text(id){ return document.getElementById(id); }
    function checked(id){ return document.getElementById(id).checked; }
    function setChecked(id,v){ document.getElementById(id).checked = !!v; }
    function setMessage(text, type){
      dom.message.textContent = text || '';
      dom.message.className = 'message' + (type ? ' ' + type : '');
    }
    function setBusy(busy, status){
      dom.reloadBtn.disabled = busy;
      dom.saveBtn.disabled = busy;
      dom.runtimeStatus.textContent = status || (busy ? '处理中...' : '就绪');
    }

    // 切换菜单与配置区，模拟 Clash Verge 左栏导航体验。
    document.querySelectorAll('.menu-item').forEach((btn)=>{
      btn.addEventListener('click', ()=>{
        document.querySelectorAll('.menu-item').forEach((b)=>b.classList.remove('active'));
        btn.classList.add('active');
        const section = btn.dataset.section;
        document.querySelectorAll('.section').forEach((s)=>s.classList.remove('active'));
        text('section-' + section).classList.add('active');
      });
    });

    function updateProtocolVisibility(){
      const showMasque = checked('syncMasque');
      text('masquePanel').classList.toggle('hidden', !showMasque);
    }
    function updateAdminAuthVisibility(){
      const showDetail = checked('adminAuthEnabled');
      text('adminAuthDetail').classList.toggle('hidden', !showDetail);
    }
    function updateDiscoveryVisibility(){
      text('nacosPanel').classList.toggle('hidden', !checked('backendNacos'));
      text('etcdPanel').classList.toggle('hidden', !checked('backendEtcd'));
      text('consulPanel').classList.toggle('hidden', !checked('backendConsul'));
    }
    text('syncMasque').addEventListener('change', updateProtocolVisibility);
    text('adminAuthEnabled').addEventListener('change', updateAdminAuthVisibility);
    ['backendNacos','backendEtcd','backendConsul'].forEach((id)=>{
      text(id).addEventListener('change', updateDiscoveryVisibility);
    });

    function addRouteRow(route){
      const node = dom.routeTpl.content.firstElementChild.cloneNode(true);
      node.querySelector('[data-key="env"]').value = route?.env || '';
      node.querySelector('[data-key="serviceName"]').value = route?.serviceName || '';
      node.querySelector('[data-key="protocol"]').value = route?.protocol || 'http';
      node.querySelector('[data-key="host"]').value = route?.host || '';
      node.querySelector('[data-key="port"]').value = route?.port || '';
      node.querySelector('[data-action="delete"]').addEventListener('click', ()=> node.remove());
      dom.routeRows.appendChild(node);
    }
    dom.addRouteBtn.addEventListener('click', ()=> addRouteRow({ protocol:'http' }));

    function fillModel(model){
      text('httpAddr').value = model.httpAddr || '';
      text('fallbackBackflowUrl').value = model.ingress?.fallbackBackflowUrl || '';
      text('bridgePublicHost').value = model.bridgePublic?.host || '';
      text('bridgePublicPort').value = model.bridgePublic?.port || '';
      text('ingressTimeoutSec').value = model.ingress?.timeoutSec || '';
      text('routeExtractorOrder').value = (model.routeExtractorOrder || []).join(',');
      setChecked('adminAuthEnabled', !!model.adminAuth?.enabled);
      text('adminAuthUsername').value = model.adminAuth?.username || '';
      text('adminAuthPassword').value = model.adminAuth?.password || '';
      text('adminAuthRealm').value = model.adminAuth?.realm || '';

      const sync = new Set(model.tunnelSyncProtocols || []);
      setChecked('syncHttp', sync.has('http'));
      setChecked('syncMasque', sync.has('masque'));
      text('masqueAddr').value = model.masque?.addr || '';
      text('masqueTunnelUdpAddr').value = model.masque?.tunnelUdpAddr || '';
      text('masqueAuthMode').value = model.masque?.authMode || 'psk';
      text('masquePsk').value = model.masque?.psk || '';

      const backends = new Set(model.discovery?.backends || []);
      setChecked('backendLocal', backends.has('local'));
      setChecked('backendNacos', backends.has('nacos'));
      setChecked('backendEtcd', backends.has('etcd'));
      setChecked('backendConsul', backends.has('consul'));
      text('discoveryTimeoutMs').value = model.discovery?.timeoutMs || '';
      text('discoveryLocalFile').value = model.discovery?.localFile || '';

      text('nacosAddr').value = model.discovery?.nacos?.addr || '';
      text('nacosNamespace').value = model.discovery?.nacos?.namespace || '';
      text('nacosGroup').value = model.discovery?.nacos?.group || '';
      text('nacosServicePattern').value = model.discovery?.nacos?.servicePattern || '';
      text('nacosUsername').value = model.discovery?.nacos?.username || '';
      text('nacosPassword').value = model.discovery?.nacos?.password || '';

      text('etcdEndpoints').value = (model.discovery?.etcd?.endpoints || []).join(',');
      text('etcdKeyPrefix').value = model.discovery?.etcd?.keyPrefix || '';

      text('consulAddr').value = model.discovery?.consul?.addr || '';
      text('consulDatacenter').value = model.discovery?.consul?.datacenter || '';
      text('consulServicePattern').value = model.discovery?.consul?.servicePattern || '';

      dom.routeRows.innerHTML = '';
      (model.routes || []).forEach((route)=> addRouteRow(route));
      updateAdminAuthVisibility();
      updateProtocolVisibility();
      updateDiscoveryVisibility();
    }

    function splitList(input){
      return String(input || '').split(',').map(v=>v.trim()).filter(Boolean);
    }

    function collectModel(){
      const tunnelSyncProtocols = [];
      if (checked('syncHttp')) tunnelSyncProtocols.push('http');
      if (checked('syncMasque')) tunnelSyncProtocols.push('masque');
      if (tunnelSyncProtocols.length === 0) tunnelSyncProtocols.push('masque');

      const backends = [];
      if (checked('backendLocal')) backends.push('local');
      if (checked('backendNacos')) backends.push('nacos');
      if (checked('backendEtcd')) backends.push('etcd');
      if (checked('backendConsul')) backends.push('consul');
      if (backends.length === 0) backends.push('local');

      const routes = Array.from(dom.routeRows.querySelectorAll('.route-row')).map((row)=>{
        return {
          env: row.querySelector('[data-key="env"]').value.trim(),
          serviceName: row.querySelector('[data-key="serviceName"]').value.trim(),
          protocol: row.querySelector('[data-key="protocol"]').value.trim() || 'http',
          host: row.querySelector('[data-key="host"]').value.trim(),
          port: Number(row.querySelector('[data-key="port"]').value || 0),
        };
      }).filter((route)=> route.env && route.serviceName && route.host && route.port > 0);

      return {
        httpAddr: text('httpAddr').value.trim(),
        tunnelSyncProtocols,
        routeExtractorOrder: splitList(text('routeExtractorOrder').value),
        masque: {
          addr: text('masqueAddr').value.trim(),
          tunnelUdpAddr: text('masqueTunnelUdpAddr').value.trim(),
          authMode: text('masqueAuthMode').value.trim(),
          psk: text('masquePsk').value,
        },
        adminAuth: {
          enabled: checked('adminAuthEnabled'),
          username: text('adminAuthUsername').value.trim(),
          password: text('adminAuthPassword').value,
          realm: text('adminAuthRealm').value.trim(),
        },
        bridgePublic: {
          host: text('bridgePublicHost').value.trim(),
          port: Number(text('bridgePublicPort').value || 0),
        },
        ingress: {
          fallbackBackflowUrl: text('fallbackBackflowUrl').value.trim(),
          timeoutSec: Number(text('ingressTimeoutSec').value || 0),
        },
        discovery: {
          backends,
          timeoutMs: Number(text('discoveryTimeoutMs').value || 0),
          localFile: text('discoveryLocalFile').value.trim(),
          nacos: {
            addr: text('nacosAddr').value.trim(),
            namespace: text('nacosNamespace').value.trim(),
            group: text('nacosGroup').value.trim(),
            servicePattern: text('nacosServicePattern').value.trim(),
            username: text('nacosUsername').value.trim(),
            password: text('nacosPassword').value,
          },
          etcd: {
            endpoints: splitList(text('etcdEndpoints').value),
            keyPrefix: text('etcdKeyPrefix').value.trim(),
          },
          consul: {
            addr: text('consulAddr').value.trim(),
            datacenter: text('consulDatacenter').value.trim(),
            servicePattern: text('consulServicePattern').value.trim(),
          }
        },
        routes,
      };
    }

    async function waitBridgeRecovered(){
      const deadline = Date.now() + 20000;
      while(Date.now() < deadline){
        try{
          const resp = await fetch('/healthz', { cache:'no-store' });
          if(resp.ok) return true;
        }catch(_){}
        await new Promise((r)=>setTimeout(r, 600));
      }
      return false;
    }

    async function loadModel(){
      setBusy(true, '加载中...');
      setMessage('');
      try{
        const resp = await fetch('/api/v1/admin/config/model', { cache:'no-store' });
        if(resp.status === 401){
          throw new Error('认证失败，请刷新页面后重新登录。');
        }
        const data = await resp.json();
        if(!resp.ok) throw new Error(data.message || '读取配置失败');
        currentPath = data.path || '';
        dom.configPath.textContent = currentPath || '(未指定)';
        fillModel(data.model || {});
        setMessage('配置已加载。', 'ok');
      }catch(err){
        setMessage('加载失败: ' + (err.message || err), 'err');
      }finally{
        setBusy(false, '就绪');
      }
    }

    async function saveModel(){
      setBusy(true, '保存中...');
      setMessage('');
      try{
        const model = collectModel();
        const resp = await fetch('/api/v1/admin/config/model', {
          method:'POST',
          headers:{ 'Content-Type':'application/json' },
          body: JSON.stringify({ model }),
        });
        if(resp.status === 401){
          throw new Error('认证失败，请刷新页面后重新登录。');
        }
        const data = await resp.json();
        if(!resp.ok) throw new Error(data.message || '保存失败');
        setMessage('配置已保存，bridge 正在热重启...', 'ok');
        dom.runtimeStatus.textContent = '重启中...';
        const recovered = await waitBridgeRecovered();
        if(recovered){
          await loadModel();
          setMessage('bridge 已重启并恢复。', 'ok');
        }else{
          setMessage('等待 bridge 恢复超时，请手动刷新。', 'err');
          setBusy(false, '就绪');
        }
      }catch(err){
        setMessage('保存失败: ' + (err.message || err), 'err');
        setBusy(false, '就绪');
      }
    }

    dom.reloadBtn.addEventListener('click', loadModel);
    dom.saveBtn.addEventListener('click', saveModel);
    updateAdminAuthVisibility();
    loadModel();
  </script>
</body>
</html>`

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func selectSessionEpoch(requestEpoch, rejectedEpoch int64) int64 {
	if rejectedEpoch > 0 {
		return rejectedEpoch
	}
	return requestEpoch
}

func writeIngressError(w http.ResponseWriter, status int, errorCode string, message string) {
	respondJSON(w, status, domain.BackflowHTTPResponse{
		StatusCode: status,
		ErrorCode:  errorCode,
		Message:    message,
	})
}

func writeIngressGRPCError(w http.ResponseWriter, status int, errorCode string, message string, target string) {
	respondJSON(w, status, domain.IngressGRPCResponse{
		Status:      "error",
		ResolvedEnv: "",
		ServiceName: "",
		Protocol:    "grpc",
		TunnelID:    "",
		Target:      strings.TrimSpace(target),
		LatencyMs:   0,
		ErrorCode:   strings.TrimSpace(errorCode),
		Message:     strings.TrimSpace(message),
	})
}

func normalizeIngressPath(rawPath string) string {
	path := strings.TrimPrefix(rawPath, "/api/v1/ingress/http")
	if strings.TrimSpace(path) == "" {
		return "/"
	}
	if !strings.HasPrefix(path, "/") {
		return "/" + path
	}
	return path
}

func cloneHeaders(headers http.Header) map[string][]string {
	result := make(map[string][]string, len(headers))
	for name, values := range headers {
		copied := make([]string, 0, len(values))
		for _, value := range values {
			copied = append(copied, value)
		}
		result[name] = copied
	}
	return result
}

func copyResponseHeaders(w http.ResponseWriter, headers map[string][]string) {
	for name, values := range headers {
		if isHopByHopHeader(name) {
			continue
		}
		for _, value := range values {
			w.Header().Add(name, value)
		}
	}
}

func isHopByHopHeader(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "connection", "proxy-connection", "keep-alive", "proxy-authenticate", "proxy-authorization", "te", "trailer", "transfer-encoding", "upgrade":
		return true
	default:
		return false
	}
}

func statusOrDefault(statusCode int, fallback int) int {
	if statusCode <= 0 {
		return fallback
	}
	return statusCode
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func maxInt64Value(a int64, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}

func secureEqual(actual string, expected string) bool {
	if len(actual) != len(expected) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(actual), []byte(expected)) == 1
}
