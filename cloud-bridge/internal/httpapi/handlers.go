package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/backflow"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// BackflowHTTPCaller 抽象 bridge 到 agent 的回流调用能力。
type BackflowHTTPCaller interface {
	ForwardHTTP(ctx context.Context, baseURL string, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error)
}

// Handler 提供 bridge 管理面与状态查询接口。
type Handler struct {
	pipeline            *routing.Pipeline
	store               *store.MemoryStore
	backflowCaller      BackflowHTTPCaller
	fallbackBackflowURL string
}

// NewHandler 创建 HTTP handler 集合。
func NewHandler(pipeline *routing.Pipeline, store *store.MemoryStore, backflowCaller BackflowHTTPCaller, fallbackBackflowURL string) *Handler {
	return &Handler{
		pipeline:            pipeline,
		store:               store,
		backflowCaller:      backflowCaller,
		fallbackBackflowURL: strings.TrimSpace(fallbackBackflowURL),
	}
}

// Router 构建 cloud-bridge 路由表。
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /api/v1/state/sessions", h.sessions)
	mux.HandleFunc("GET /api/v1/state/intercepts", h.intercepts)
	mux.HandleFunc("GET /api/v1/state/routes", h.routes)
	mux.HandleFunc("POST /api/v1/tunnel/events", h.tunnelEvent)
	mux.HandleFunc("GET /api/v1/debug/route-extract", h.debugRouteExtract)
	mux.HandleFunc("/api/v1/ingress/http", h.ingressHTTP)
	mux.HandleFunc("/api/v1/ingress/http/", h.ingressHTTP)
	mux.HandleFunc("/api/v1/ingress/grpc", h.ingressGRPC)
	mux.HandleFunc("/api/v1/ingress/grpc/", h.ingressGRPC)

	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
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

func (h *Handler) tunnelEvent(w http.ResponseWriter, r *http.Request) {
	var event domain.TunnelEvent
	if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
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
		writeIngressError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, err.Error())
		return
	}

	route, session, ok := h.store.ResolveRouteForIngress(result.Env, result.ServiceName, "http")
	if !ok {
		writeIngressError(
			w,
			http.StatusNotFound,
			domain.IngressErrorRouteNotFound,
			"route not found for env/service/protocol",
		)
		return
	}

	baseURL := strings.TrimSpace(session.BackflowBaseURL)
	if baseURL == "" {
		baseURL = h.fallbackBackflowURL
	}
	if baseURL == "" {
		writeIngressError(w, http.StatusServiceUnavailable, domain.IngressErrorTunnelOffline, "backflow endpoint is unavailable")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeIngressError(w, http.StatusBadRequest, domain.IngressErrorRouteExtractFailed, "read ingress request body failed")
		return
	}

	forwardResp, err := h.backflowCaller.ForwardHTTP(r.Context(), baseURL, domain.BackflowHTTPRequest{
		Method:     r.Method,
		Path:       normalizeIngressPath(r.URL.Path),
		RawQuery:   r.URL.RawQuery,
		Host:       r.Host,
		Headers:    cloneHeaders(r.Header),
		Body:       body,
		TargetHost: "127.0.0.1",
		TargetPort: route.TargetPort,
		Protocol:   "http",
	})
	if err != nil {
		var backflowErr *backflow.Error
		if errors.As(err, &backflowErr) {
			writeIngressError(
				w,
				statusOrDefault(backflowErr.StatusCode, http.StatusBadGateway),
				firstNonEmpty(backflowErr.ErrorCode, domain.IngressErrorTunnelOffline),
				firstNonEmpty(backflowErr.Message, "call agent backflow failed"),
			)
			return
		}
		writeIngressError(w, http.StatusBadGateway, domain.IngressErrorTunnelOffline, err.Error())
		return
	}

	// 透传目标服务返回头时过滤 hop-by-hop 头，避免代理链路语义冲突。
	copyResponseHeaders(w, forwardResp.Headers)
	w.Header().Set("X-DevLoop-Route-Env", route.Env)
	w.Header().Set("X-DevLoop-Route-Service", route.ServiceName)
	w.Header().Set("X-DevLoop-Route-Protocol", route.Protocol)
	w.Header().Set("X-DevLoop-Route-Tunnel", route.TunnelID)

	statusCode := statusOrDefault(forwardResp.StatusCode, http.StatusOK)
	w.WriteHeader(statusCode)
	if len(forwardResp.Body) > 0 {
		_, _ = w.Write(forwardResp.Body)
	}
}

func (h *Handler) ingressGRPC(w http.ResponseWriter, _ *http.Request) {
	writeIngressError(
		w,
		http.StatusNotImplemented,
		domain.IngressErrorRouteNotFound,
		"grpc ingress scaffold is not implemented yet",
	)
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
