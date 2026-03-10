package httpapi

import (
	"context"
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

// Handler 提供 bridge 管理面与状态查询接口。
type Handler struct {
	pipeline                 *routing.Pipeline
	store                    *store.MemoryStore
	backflowCaller           BackflowHTTPCaller
	upstreamForwarder        UpstreamForwarder
	serviceDiscoveryResolver discovery.Resolver
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
