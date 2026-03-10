package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/forwarder"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
	grpcmetadata "google.golang.org/grpc/metadata"
	grpcstatus "google.golang.org/grpc/status"
)

// TunnelSyncPublisher 定义 HTTP 层到 tunnel 同步层的最小能力边界。
type TunnelSyncPublisher interface {
	EnqueueRegisterUpsert(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error
	EnqueueRegisterDelete(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error
	RequestReconnect(ctx context.Context) error
	CurrentProtocol() string
}

// LocalBackflowForwarder 定义 agent 回流到本地 endpoint 的转发能力。
type LocalBackflowForwarder interface {
	Forward(ctx context.Context, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error)
}

// Handler implements local management and state APIs.
type Handler struct {
	cfg               config.Config
	store             *store.MemoryStore
	syncPublisher     TunnelSyncPublisher
	backflowForwarder LocalBackflowForwarder
	egressHTTPClient  *http.Client
}

// NewHandler creates API handlers.
func NewHandler(cfg config.Config, s *store.MemoryStore, publisher TunnelSyncPublisher, backflowForwarder LocalBackflowForwarder) *Handler {
	timeout := cfg.Tunnel.RequestTimeout
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Handler{
		cfg:               cfg,
		store:             s,
		syncPublisher:     publisher,
		backflowForwarder: backflowForwarder,
		egressHTTPClient: &http.Client{
			Timeout: timeout,
		},
	}
}

// Router builds the HTTP router for agent-core APIs.
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.healthz)

	mux.HandleFunc("POST /api/v1/registrations", h.register)
	mux.HandleFunc("POST /api/v1/registrations/{instanceId}/heartbeat", h.heartbeat)
	mux.HandleFunc("DELETE /api/v1/registrations/{instanceId}", h.unregister)
	mux.HandleFunc("GET /api/v1/registrations", h.listRegistrations)
	mux.HandleFunc("POST /api/v1/discover", h.discover)
	mux.HandleFunc("POST /api/v1/egress/http", h.egressHTTP)
	mux.HandleFunc("POST /api/v1/egress/grpc", h.egressGRPC)

	mux.HandleFunc("GET /api/v1/state/summary", h.stateSummary)
	mux.HandleFunc("GET /api/v1/state/tunnel", h.stateTunnel)
	mux.HandleFunc("GET /api/v1/state/intercepts", h.stateIntercepts)
	mux.HandleFunc("GET /api/v1/state/errors", h.stateErrors)
	mux.HandleFunc("GET /api/v1/state/requests", h.stateRequests)
	mux.HandleFunc("GET /api/v1/state/diagnostics", h.stateDiagnostics)
	mux.HandleFunc("POST /api/v1/state/logs/clear", h.clearStateLogs)
	mux.HandleFunc("POST /api/v1/control/reconnect", h.reconnect)
	mux.HandleFunc("POST /api/v1/backflow/http", h.backflowHTTP)
	mux.HandleFunc("POST /api/v1/backflow/grpc", h.backflowGRPC)

	return mux
}

func (h *Handler) healthz(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (h *Handler) register(w http.ResponseWriter, r *http.Request) {
	var payload domain.LocalRegistration
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		h.store.AddError(domain.ErrorRouteExtractFailed, "invalid registration payload", map[string]string{"error": err.Error()})
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}

	if strings.TrimSpace(payload.ServiceName) == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "serviceName is required"})
		return
	}
	if strings.TrimSpace(payload.InstanceID) == "" {
		payload.InstanceID = generateID("inst")
	}
	if strings.TrimSpace(payload.Env) == "" {
		payload.Env = h.cfg.EnvName
	}
	if payload.TTLSeconds <= 0 {
		payload.TTLSeconds = h.cfg.Registration.DefaultTTLSeconds
	}

	// 记录更新前快照，用于计算“被移除的 endpoint”并同步 DELETE 给 bridge。
	previousReg, hasPreviousReg := h.store.FindRegistrationByInstanceID(payload.InstanceID)

	eventID := eventIDFromRequest(r)
	if eventID == "" {
		eventID = generateID("evt")
	}

	stored, duplicated, err := h.store.UpsertRegistration(payload, eventID)
	if err != nil {
		switch {
		case errors.Is(err, store.ErrInvalidRegistration):
			respondJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		case errors.Is(err, store.ErrInstanceConflict):
			respondJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			h.store.AddError(domain.ErrorRouteExtractFailed, "failed to upsert registration", map[string]string{"error": err.Error()})
			respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to upsert registration"})
		}
		return
	}

	// 本地注册成功后异步投递到 tunnel 同步队列。
	if !duplicated && h.syncPublisher != nil {
		// 先删除“这次更新中被移除的 endpoint”，避免 bridge 保留过期路由。
		if hasPreviousReg {
			removedEndpoints := diffRemovedRegistrationEndpoints(previousReg.Endpoints, stored.Endpoints)
			if len(removedEndpoints) > 0 {
				if err := h.syncPublisher.EnqueueRegisterDelete(r.Context(), domain.LocalRegistration{
					ServiceName: previousReg.ServiceName,
					Env:         previousReg.Env,
					InstanceID:  previousReg.InstanceID,
					Endpoints:   removedEndpoints,
				}, eventID); err != nil {
					h.store.AddError(domain.ErrorTunnelOffline, "enqueue register delete for removed endpoint failed", map[string]string{
						"eventId":     eventID,
						"serviceName": previousReg.ServiceName,
						"instanceId":  previousReg.InstanceID,
						"error":       err.Error(),
					})
				}
			}
		}

		// 再发送当前注册全量 endpoint 的 UPSERT，保证 bridge 与本地注册最终一致。
		if err := h.syncPublisher.EnqueueRegisterUpsert(r.Context(), stored, eventID); err != nil {
			h.store.AddError(domain.ErrorTunnelOffline, "enqueue register upsert failed", map[string]string{
				"eventId":     eventID,
				"serviceName": stored.ServiceName,
				"instanceId":  stored.InstanceID,
				"error":       err.Error(),
			})
		}
	}

	setEventHeaders(w, eventID, duplicated)
	respondJSON(w, http.StatusOK, stored)
}

func (h *Handler) heartbeat(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	if strings.TrimSpace(instanceID) == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "instanceId is required"})
		return
	}
	reg, err := h.store.Heartbeat(instanceID)
	if err != nil {
		h.store.AddError(domain.ErrorRouteNotFound, err.Error(), map[string]string{"instanceId": instanceID})
		respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
		return
	}
	respondJSON(w, http.StatusOK, reg)
}

func (h *Handler) unregister(w http.ResponseWriter, r *http.Request) {
	instanceID := r.PathValue("instanceId")
	if strings.TrimSpace(instanceID) == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "instanceId is required"})
		return
	}

	eventID := eventIDFromRequest(r)
	if eventID == "" {
		eventID = generateID("evt")
	}

	removedReg, regExists := h.store.FindRegistrationByInstanceID(instanceID)
	deleted, duplicated, err := h.store.DeleteRegistration(instanceID, eventID)
	if err != nil {
		if errors.Is(err, store.ErrInstanceNotFound) {
			respondJSON(w, http.StatusNotFound, map[string]string{"error": err.Error()})
			return
		}
		h.store.AddError(domain.ErrorRouteExtractFailed, "failed to delete registration", map[string]string{"error": err.Error(), "instanceId": instanceID})
		respondJSON(w, http.StatusInternalServerError, map[string]string{"error": "failed to delete registration"})
		return
	}

	// 注销成功后向 bridge 投递删除事件，保持两侧接管关系收敛。
	if deleted && !duplicated && regExists && h.syncPublisher != nil {
		if err := h.syncPublisher.EnqueueRegisterDelete(r.Context(), removedReg, eventID); err != nil {
			h.store.AddError(domain.ErrorTunnelOffline, "enqueue register delete failed", map[string]string{
				"eventId":    eventID,
				"instanceId": removedReg.InstanceID,
				"error":      err.Error(),
			})
		}
	}

	setEventHeaders(w, eventID, duplicated)
	if deleted {
		respondJSON(w, http.StatusOK, map[string]string{"result": "deleted", "instanceId": instanceID})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"result": "duplicate-ignored", "instanceId": instanceID})
}

func (h *Handler) listRegistrations(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListRegistrations())
}

func (h *Handler) discover(w http.ResponseWriter, r *http.Request) {
	var request domain.DiscoverRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if strings.TrimSpace(request.ServiceName) == "" || strings.TrimSpace(request.Protocol) == "" {
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "serviceName and protocol are required"})
		return
	}
	request.Env = h.resolveDiscoverEnv(r, request.Env)
	result := h.store.Discover(request, h.cfg.EnvName)
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) egressHTTP(w http.ResponseWriter, r *http.Request) {
	var request domain.EgressHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, domain.EgressHTTPResponse{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  domain.ErrorRouteExtractFailed,
			Message:    "invalid egress payload",
		})
		return
	}
	if strings.TrimSpace(request.ServiceName) == "" {
		respondJSON(w, http.StatusBadRequest, domain.EgressHTTPResponse{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  domain.ErrorRouteExtractFailed,
			Message:    "serviceName is required",
		})
		return
	}

	request.Env = h.resolveDiscoverEnv(r, request.Env)
	discoverResult := h.store.Discover(domain.DiscoverRequest{
		ServiceName: request.ServiceName,
		Env:         request.Env,
		Protocol:    "http",
	}, h.cfg.EnvName)

	resolvedEnv := firstNonBlank(discoverResult.ResolvedEnv, "base")
	resolution := firstNonBlank(discoverResult.Resolution, "base-fallback")
	startedAt := time.Now().UTC()

	headers := cloneHeadersForEgress(request.Headers)
	// 出口代理统一透传 env，保证 bridge 与上游都拿到一致上下文。
	headers.Set("x-env", resolvedEnv)
	headers.Set("X-Env", resolvedEnv)

	method := strings.ToUpper(strings.TrimSpace(request.Method))
	if method == "" {
		method = http.MethodGet
	}
	path := normalizeProxyPath(request.Path)

	targetURL := ""
	hostOverride := ""
	upstream := "base"

	// dev 命中走 bridge ingress；否则走 base fallback（通过 baseUrl）。
	if discoverResult.Matched && strings.EqualFold(discoverResult.Resolution, "dev-priority") {
		upstream = "bridge"
		headers.Set("x-service-name", strings.TrimSpace(request.ServiceName))
		headers.Set("X-Service-Name", strings.TrimSpace(request.ServiceName))
		hostOverride = fmt.Sprintf("%s.%s.internal", sanitizeHostSegment(request.ServiceName), sanitizeHostSegment(resolvedEnv))

		urlValue, err := buildBridgeIngressURL(h.cfg.Tunnel.BridgeAddress, path, request.RawQuery)
		if err != nil {
			respondJSON(w, http.StatusBadRequest, domain.EgressHTTPResponse{
				StatusCode:  http.StatusBadRequest,
				ResolvedEnv: resolvedEnv,
				Resolution:  resolution,
				Upstream:    upstream,
				ErrorCode:   domain.ErrorRouteExtractFailed,
				Message:     err.Error(),
			})
			return
		}
		targetURL = urlValue
	} else {
		urlValue, err := buildDirectTargetURL(request.BaseURL, path, request.RawQuery)
		if err != nil {
			message := "base fallback requires baseUrl"
			recordEgressSummary(h.store, domain.RequestSummary{
				Direction:    "egress",
				Protocol:     "http",
				ServiceName:  strings.TrimSpace(request.ServiceName),
				RequestedEnv: strings.TrimSpace(request.Env),
				ResolvedEnv:  resolvedEnv,
				Resolution:   resolution,
				Upstream:     upstream,
				Result:       "error",
				ErrorCode:    domain.ErrorRouteNotFound,
				Message:      message,
				LatencyMs:    elapsedMS(startedAt),
				OccurredAt:   time.Now().UTC(),
			})
			respondJSON(w, http.StatusNotFound, domain.EgressHTTPResponse{
				StatusCode:  http.StatusNotFound,
				ResolvedEnv: resolvedEnv,
				Resolution:  resolution,
				Upstream:    upstream,
				ErrorCode:   domain.ErrorRouteNotFound,
				Message:     message,
			})
			return
		}
		targetURL = urlValue
	}

	result, err := h.executeEgressHTTP(r.Context(), method, targetURL, headers, request.Body, hostOverride)
	if err != nil {
		statusCode, errorCode, message := classifyEgressError(err, upstream)
		recordEgressSummary(h.store, domain.RequestSummary{
			Direction:    "egress",
			Protocol:     "http",
			ServiceName:  strings.TrimSpace(request.ServiceName),
			RequestedEnv: strings.TrimSpace(request.Env),
			ResolvedEnv:  resolvedEnv,
			Resolution:   resolution,
			Upstream:     upstream,
			StatusCode:   statusCode,
			Result:       "error",
			ErrorCode:    errorCode,
			Message:      message,
			LatencyMs:    elapsedMS(startedAt),
			OccurredAt:   time.Now().UTC(),
		})
		h.store.AddError(errorCode, "egress proxy failed", map[string]string{
			"serviceName": strings.TrimSpace(request.ServiceName),
			"resolution":  resolution,
			"upstream":    upstream,
			"error":       message,
		})
		respondJSON(w, statusCode, domain.EgressHTTPResponse{
			StatusCode:  statusCode,
			ResolvedEnv: resolvedEnv,
			Resolution:  resolution,
			Upstream:    upstream,
			ErrorCode:   errorCode,
			Message:     message,
		})
		return
	}

	recordEgressSummary(h.store, domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "http",
		ServiceName:  strings.TrimSpace(request.ServiceName),
		RequestedEnv: strings.TrimSpace(request.Env),
		ResolvedEnv:  resolvedEnv,
		Resolution:   resolution,
		Upstream:     upstream,
		StatusCode:   result.StatusCode,
		Result:       "success",
		LatencyMs:    elapsedMS(startedAt),
		OccurredAt:   time.Now().UTC(),
	})

	// 返回真实上游状态码，同时通过头部暴露命中语义给调用方。
	copyHTTPHeaders(w, result.Headers)
	w.Header().Set("X-DevLoop-Resolution", resolution)
	w.Header().Set("X-DevLoop-Resolved-Env", resolvedEnv)
	w.Header().Set("X-DevLoop-Upstream", upstream)
	w.WriteHeader(result.StatusCode)
	if len(result.Body) > 0 {
		_, _ = w.Write(result.Body)
	}
}

func (h *Handler) egressGRPC(w http.ResponseWriter, r *http.Request) {
	var request domain.EgressGRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, domain.EgressGRPCResponse{
			Status:       "error",
			ErrorCode:    domain.ErrorRouteExtractFailed,
			Message:      "invalid egress grpc payload",
			ObservedTime: time.Now().UTC(),
		})
		return
	}
	if strings.TrimSpace(request.ServiceName) == "" {
		respondJSON(w, http.StatusBadRequest, domain.EgressGRPCResponse{
			Status:       "error",
			ErrorCode:    domain.ErrorRouteExtractFailed,
			Message:      "serviceName is required",
			ObservedTime: time.Now().UTC(),
		})
		return
	}

	request.Env = h.resolveDiscoverEnv(r, request.Env)
	discoverResult := h.store.Discover(domain.DiscoverRequest{
		ServiceName: request.ServiceName,
		Env:         request.Env,
		Protocol:    "grpc",
	}, h.cfg.EnvName)
	resolvedEnv := firstNonBlank(discoverResult.ResolvedEnv, "base")
	resolution := firstNonBlank(discoverResult.Resolution, "base-fallback")
	startedAt := time.Now().UTC()

	target, upstream, err := resolveGRPCTarget(discoverResult, request.BaseAddress)
	if err != nil {
		latency := elapsedMS(startedAt)
		now := time.Now().UTC()
		recordEgressSummary(h.store, domain.RequestSummary{
			Direction:    "egress",
			Protocol:     "grpc",
			ServiceName:  strings.TrimSpace(request.ServiceName),
			RequestedEnv: strings.TrimSpace(request.Env),
			ResolvedEnv:  resolvedEnv,
			Resolution:   resolution,
			Upstream:     "base",
			StatusCode:   http.StatusNotFound,
			Result:       "error",
			ErrorCode:    domain.ErrorRouteNotFound,
			Message:      err.Error(),
			LatencyMs:    latency,
			OccurredAt:   now,
		})
		respondJSON(w, http.StatusNotFound, domain.EgressGRPCResponse{
			Status:       "error",
			ResolvedEnv:  resolvedEnv,
			Resolution:   resolution,
			Upstream:     "base",
			Target:       "",
			LatencyMs:    latency,
			ErrorCode:    domain.ErrorRouteNotFound,
			Message:      err.Error(),
			ObservedTime: now,
		})
		return
	}

	statusValue, grpcErr := h.executeGRPCHealthCheck(r.Context(), target, resolvedEnv, request)
	latency := elapsedMS(startedAt)
	now := time.Now().UTC()

	if grpcErr != nil {
		errorCode, message := classifyGRPCError(grpcErr, upstream)
		recordEgressSummary(h.store, domain.RequestSummary{
			Direction:    "egress",
			Protocol:     "grpc",
			ServiceName:  strings.TrimSpace(request.ServiceName),
			RequestedEnv: strings.TrimSpace(request.Env),
			ResolvedEnv:  resolvedEnv,
			Resolution:   resolution,
			Upstream:     upstream,
			StatusCode:   http.StatusBadGateway,
			Result:       "error",
			ErrorCode:    errorCode,
			Message:      message,
			LatencyMs:    latency,
			OccurredAt:   now,
		})
		h.store.AddError(errorCode, "grpc egress proxy failed", map[string]string{
			"serviceName": strings.TrimSpace(request.ServiceName),
			"target":      target,
			"upstream":    upstream,
			"error":       message,
		})
		respondJSON(w, http.StatusBadGateway, domain.EgressGRPCResponse{
			Status:       "error",
			ResolvedEnv:  resolvedEnv,
			Resolution:   resolution,
			Upstream:     upstream,
			Target:       target,
			LatencyMs:    latency,
			ErrorCode:    errorCode,
			Message:      message,
			ObservedTime: now,
		})
		return
	}

	recordEgressSummary(h.store, domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "grpc",
		ServiceName:  strings.TrimSpace(request.ServiceName),
		RequestedEnv: strings.TrimSpace(request.Env),
		ResolvedEnv:  resolvedEnv,
		Resolution:   resolution,
		Upstream:     upstream,
		StatusCode:   http.StatusOK,
		Result:       "success",
		Message:      statusValue,
		LatencyMs:    latency,
		OccurredAt:   now,
	})

	respondJSON(w, http.StatusOK, domain.EgressGRPCResponse{
		Status:       statusValue,
		ResolvedEnv:  resolvedEnv,
		Resolution:   resolution,
		Upstream:     upstream,
		Target:       target,
		LatencyMs:    latency,
		ObservedTime: now,
	})
}

func (h *Handler) stateSummary(w http.ResponseWriter, _ *http.Request) {
	summary := h.store.Summary(h.cfg.RDName, h.cfg.EnvName, h.cfg.Registration.DefaultTTLSeconds, h.cfg.Registration.ScanInterval)
	respondJSON(w, http.StatusOK, summary)
}

func (h *Handler) stateTunnel(w http.ResponseWriter, _ *http.Request) {
	state := h.store.TunnelState(h.cfg.Tunnel.BridgeAddress)
	// tunnel 卡片展示“当前实际传输协议”：优先使用同步管理器运行态，其次退回配置值。
	state.Protocol = h.currentTunnelProtocol()
	respondJSON(w, http.StatusOK, state)
}

func (h *Handler) stateIntercepts(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListActiveIntercepts())
}

func (h *Handler) stateErrors(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListErrors())
}

func (h *Handler) stateRequests(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListRequestSummaries())
}

func (h *Handler) stateDiagnostics(w http.ResponseWriter, _ *http.Request) {
	diagnostics := h.store.Diagnostics(
		h.cfg.RDName,
		h.cfg.EnvName,
		h.cfg.Registration.DefaultTTLSeconds,
		h.cfg.Registration.ScanInterval,
	)
	respondJSON(w, http.StatusOK, diagnostics)
}

func (h *Handler) clearStateLogs(w http.ResponseWriter, _ *http.Request) {
	clearedErrors, clearedRequests := h.store.ClearDiagnostics()
	respondJSON(w, http.StatusOK, map[string]int{
		"clearedErrors":   clearedErrors,
		"clearedRequests": clearedRequests,
	})
}

func (h *Handler) currentTunnelProtocol() string {
	protocol := strings.ToLower(strings.TrimSpace(h.cfg.Tunnel.SyncProtocol))
	if h.syncPublisher != nil {
		protocol = strings.ToLower(strings.TrimSpace(h.syncPublisher.CurrentProtocol()))
	}
	if protocol == "masque" {
		return "masque"
	}
	return "http"
}

func (h *Handler) reconnect(w http.ResponseWriter, r *http.Request) {
	if h.syncPublisher == nil {
		state := h.store.ReconnectTunnel()
		respondJSON(w, http.StatusOK, state)
		return
	}

	// 手动重连接口需要尽快返回前端，避免在 bridge 长时间不可达时页面“无响应”。
	reconnectCtx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()

	if err := h.syncPublisher.RequestReconnect(reconnectCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			state := h.store.TunnelState(h.cfg.Tunnel.BridgeAddress)
			respondJSON(w, http.StatusAccepted, state)
			return
		}
		h.store.AddError(domain.ErrorTunnelOffline, "manual reconnect failed", map[string]string{"error": err.Error()})
		respondJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
		return
	}

	state := h.store.TunnelState(h.cfg.Tunnel.BridgeAddress)
	respondJSON(w, http.StatusOK, state)
}

func (h *Handler) backflowHTTP(w http.ResponseWriter, r *http.Request) {
	if h.backflowForwarder == nil {
		respondJSON(w, http.StatusNotImplemented, domain.BackflowHTTPResponse{
			StatusCode: http.StatusNotImplemented,
			ErrorCode:  domain.ErrorRouteExtractFailed,
			Message:    "local backflow forwarder is not configured",
		})
		return
	}

	var request domain.BackflowHTTPRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, domain.BackflowHTTPResponse{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  domain.ErrorRouteExtractFailed,
			Message:    "invalid backflow payload",
		})
		return
	}

	if strings.TrimSpace(request.TargetHost) == "" || request.TargetPort <= 0 {
		respondJSON(w, http.StatusBadRequest, domain.BackflowHTTPResponse{
			StatusCode: http.StatusBadRequest,
			ErrorCode:  domain.ErrorRouteExtractFailed,
			Message:    "targetHost/targetPort are required",
		})
		return
	}

	response, err := h.backflowForwarder.Forward(r.Context(), request)
	if err != nil {
		statusCode, errorCode, message := classifyBackflowError(err)
		respondJSON(w, statusCode, domain.BackflowHTTPResponse{
			StatusCode: statusCode,
			ErrorCode:  errorCode,
			Message:    message,
		})
		return
	}

	// 对 bridge 来说，调用成功即返回 200，真实业务状态码放在 payload.statusCode 中。
	respondJSON(w, http.StatusOK, response)
}

func (h *Handler) backflowGRPC(w http.ResponseWriter, r *http.Request) {
	var request domain.BackflowGRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		respondJSON(w, http.StatusBadRequest, domain.BackflowGRPCResponse{
			Status:    "error",
			ErrorCode: domain.ErrorRouteExtractFailed,
			Message:   "invalid backflow grpc payload",
		})
		return
	}

	targetHost := strings.TrimSpace(request.TargetHost)
	if targetHost == "" || request.TargetPort <= 0 {
		respondJSON(w, http.StatusBadRequest, domain.BackflowGRPCResponse{
			Status:    "error",
			ErrorCode: domain.ErrorRouteExtractFailed,
			Message:   "targetHost/targetPort are required",
		})
		return
	}

	target := net.JoinHostPort(targetHost, strconv.Itoa(request.TargetPort))
	resolvedEnv := firstNonBlank(strings.TrimSpace(request.Env), strings.TrimSpace(h.cfg.EnvName), "base")
	startedAt := time.Now().UTC()

	// gRPC 回流与出口代理共用同一执行器，保持超时和 metadata 透传行为一致。
	statusValue, err := h.executeGRPCHealthCheck(r.Context(), target, resolvedEnv, domain.EgressGRPCRequest{
		HealthService: strings.TrimSpace(request.HealthService),
		TimeoutMs:     request.TimeoutMs,
	})
	if err != nil {
		statusCode, errorCode, message := classifyBackflowGRPCError(err)
		respondJSON(w, statusCode, domain.BackflowGRPCResponse{
			Status:    "error",
			Target:    target,
			LatencyMs: elapsedMS(startedAt),
			ErrorCode: errorCode,
			Message:   message,
		})
		return
	}

	respondJSON(w, http.StatusOK, domain.BackflowGRPCResponse{
		Status:    statusValue,
		Target:    target,
		LatencyMs: elapsedMS(startedAt),
	})
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func generateID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf))
}

func eventIDFromRequest(r *http.Request) string {
	eventID := strings.TrimSpace(r.Header.Get("x-event-id"))
	if eventID == "" {
		eventID = strings.TrimSpace(r.Header.Get("X-Event-Id"))
	}
	return eventID
}

func (h *Handler) resolveDiscoverEnv(r *http.Request, payloadEnv string) string {
	headerEnv := firstNonBlank(
		strings.TrimSpace(r.Header.Get("x-env")),
		strings.TrimSpace(r.Header.Get("X-Env")),
	)
	payloadEnv = strings.TrimSpace(payloadEnv)
	runtimeEnv := strings.TrimSpace(h.cfg.EnvName)
	order := h.cfg.EnvResolve.Order
	if len(order) == 0 {
		order = []string{"requestHeader", "payload", "runtimeDefault", "baseFallback"}
	}

	// 统一按配置优先级解析 env，HTTP 与 gRPC 出口、discover 共用同一策略。
	for _, item := range order {
		switch strings.TrimSpace(item) {
		case "requestHeader":
			if headerEnv != "" {
				return headerEnv
			}
		case "payload":
			if payloadEnv != "" {
				return payloadEnv
			}
		case "runtimeDefault":
			if runtimeEnv != "" {
				return runtimeEnv
			}
		case "baseFallback":
			return "base"
		}
	}

	return firstNonBlank(headerEnv, payloadEnv, runtimeEnv, "base")
}

func setEventHeaders(w http.ResponseWriter, eventID string, duplicated bool) {
	w.Header().Set("X-Event-Id", eventID)
	if duplicated {
		w.Header().Set("X-Event-Deduplicated", "true")
		return
	}
	w.Header().Set("X-Event-Deduplicated", "false")
}

func diffRemovedRegistrationEndpoints(previous, current []domain.LocalEndpoint) []domain.LocalEndpoint {
	if len(previous) == 0 {
		return nil
	}

	currentSet := make(map[string]struct{}, len(current))
	for _, endpoint := range current {
		currentSet[endpointSyncKey(endpoint)] = struct{}{}
	}

	removed := make([]domain.LocalEndpoint, 0, len(previous))
	for _, endpoint := range previous {
		if _, exists := currentSet[endpointSyncKey(endpoint)]; exists {
			continue
		}
		removed = append(removed, endpoint)
	}
	return removed
}

func endpointSyncKey(endpoint domain.LocalEndpoint) string {
	return fmt.Sprintf("%s|%d", strings.ToLower(strings.TrimSpace(endpoint.Protocol)), endpoint.TargetPort)
}

func classifyBackflowError(err error) (statusCode int, errorCode domain.ErrorCode, message string) {
	var forwardErr *forwarder.ForwardError
	if errors.As(err, &forwardErr) {
		switch forwardErr.Code {
		case domain.ErrorUpstreamTimeout:
			return http.StatusGatewayTimeout, domain.ErrorUpstreamTimeout, firstNonBlank(forwardErr.Message, "upstream timeout")
		case domain.ErrorLocalEndpointDown:
			return http.StatusBadGateway, domain.ErrorLocalEndpointDown, firstNonBlank(forwardErr.Message, "local endpoint unavailable")
		default:
			return http.StatusBadGateway, forwardErr.Code, firstNonBlank(forwardErr.Message, "backflow failed")
		}
	}
	return http.StatusBadGateway, domain.ErrorLocalEndpointDown, firstNonBlank(err.Error(), "backflow failed")
}

func classifyBackflowGRPCError(err error) (statusCode int, errorCode domain.ErrorCode, message string) {
	status, ok := grpcstatus.FromError(err)
	if !ok {
		return http.StatusBadGateway, domain.ErrorLocalEndpointDown, firstNonBlank(err.Error(), "grpc backflow failed")
	}

	switch status.Code() {
	case codes.DeadlineExceeded:
		return http.StatusGatewayTimeout, domain.ErrorUpstreamTimeout, firstNonBlank(status.Message(), "grpc upstream timeout")
	case codes.Unavailable:
		return http.StatusBadGateway, domain.ErrorLocalEndpointDown, firstNonBlank(status.Message(), "grpc local endpoint unavailable")
	default:
		return http.StatusBadGateway, domain.ErrorRouteExtractFailed, firstNonBlank(status.Message(), status.Code().String())
	}
}

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

type outboundHTTPResult struct {
	StatusCode int
	Headers    map[string][]string
	Body       []byte
}

func (h *Handler) executeEgressHTTP(
	ctx context.Context,
	method string,
	targetURL string,
	headers http.Header,
	body []byte,
	hostOverride string,
) (outboundHTTPResult, error) {
	request, err := http.NewRequestWithContext(ctx, method, targetURL, bytes.NewReader(body))
	if err != nil {
		return outboundHTTPResult{}, err
	}
	if strings.TrimSpace(hostOverride) != "" {
		request.Host = strings.TrimSpace(hostOverride)
	}
	for name, values := range headers {
		if isHopByHopHeader(name) {
			continue
		}
		for _, value := range values {
			request.Header.Add(name, value)
		}
	}

	response, err := h.egressHTTPClient.Do(request)
	if err != nil {
		return outboundHTTPResult{}, err
	}
	defer response.Body.Close()

	respBody, err := io.ReadAll(response.Body)
	if err != nil {
		return outboundHTTPResult{}, err
	}

	return outboundHTTPResult{
		StatusCode: response.StatusCode,
		Headers:    cloneHeaderMap(response.Header),
		Body:       respBody,
	}, nil
}

func buildBridgeIngressURL(bridgeAddress string, path string, rawQuery string) (string, error) {
	address := strings.TrimSpace(bridgeAddress)
	if address == "" {
		address = "http://127.0.0.1:38080"
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}

	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("invalid bridge address: %w", err)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + "/api/v1/ingress/http" + normalizeProxyPath(path)
	parsed.RawQuery = strings.TrimSpace(rawQuery)
	parsed.Fragment = ""
	return parsed.String(), nil
}

func buildDirectTargetURL(baseURL string, path string, rawQuery string) (string, error) {
	address := strings.TrimSpace(baseURL)
	if address == "" {
		return "", fmt.Errorf("baseUrl is required")
	}
	if !strings.Contains(address, "://") {
		address = "http://" + address
	}
	parsed, err := url.Parse(address)
	if err != nil {
		return "", fmt.Errorf("invalid baseUrl: %w", err)
	}
	basePath := strings.TrimRight(parsed.Path, "/")
	parsed.Path = basePath + normalizeProxyPath(path)
	parsed.RawQuery = strings.TrimSpace(rawQuery)
	parsed.Fragment = ""
	return parsed.String(), nil
}

func normalizeProxyPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return "/"
	}
	if !strings.HasPrefix(trimmed, "/") {
		return "/" + trimmed
	}
	return trimmed
}

func sanitizeHostSegment(value string) string {
	normalized := strings.ToLower(strings.TrimSpace(value))
	normalized = strings.ReplaceAll(normalized, "_", "-")
	normalized = strings.ReplaceAll(normalized, "/", "-")
	normalized = strings.ReplaceAll(normalized, " ", "-")
	if normalized == "" {
		return "unknown"
	}
	return normalized
}

func classifyEgressError(err error, upstream string) (int, domain.ErrorCode, string) {
	var urlErr *url.Error
	if errors.As(err, &urlErr) && urlErr.Timeout() {
		return http.StatusGatewayTimeout, domain.ErrorUpstreamTimeout, firstNonBlank(urlErr.Error(), "upstream timeout")
	}

	if strings.EqualFold(upstream, "bridge") {
		return http.StatusBadGateway, domain.ErrorTunnelOffline, firstNonBlank(err.Error(), "bridge is unavailable")
	}
	return http.StatusBadGateway, domain.ErrorLocalEndpointDown, firstNonBlank(err.Error(), "base upstream is unavailable")
}

func cloneHeadersForEgress(headers map[string][]string) http.Header {
	result := make(http.Header, len(headers))
	for name, values := range headers {
		copied := make([]string, 0, len(values))
		for _, value := range values {
			copied = append(copied, value)
		}
		result[name] = copied
	}
	return result
}

func cloneHeaderMap(headers http.Header) map[string][]string {
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

func copyHTTPHeaders(w http.ResponseWriter, headers map[string][]string) {
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

func elapsedMS(startedAt time.Time) int64 {
	return time.Since(startedAt).Milliseconds()
}

func recordEgressSummary(stateStore *store.MemoryStore, summary domain.RequestSummary) {
	// 请求摘要统一在这一处落库，避免遗漏字段导致 UI 展示不一致。
	stateStore.AddRequestSummary(summary)
}

func resolveGRPCTarget(discoverResult domain.DiscoverResponse, baseAddress string) (target string, upstream string, err error) {
	// dev 命中时直接走 discover 返回的 grpc route target；否则走 base fallback。
	if discoverResult.Matched && strings.EqualFold(discoverResult.Resolution, "dev-priority") {
		if discoverResult.RouteTarget.TargetPort <= 0 {
			return "", "", fmt.Errorf("grpc discover target is missing targetPort")
		}
		host := firstNonBlank(discoverResult.RouteTarget.TargetHost, "127.0.0.1")
		return net.JoinHostPort(host, strconv.Itoa(discoverResult.RouteTarget.TargetPort)), "dev", nil
	}

	address := strings.TrimSpace(baseAddress)
	if address == "" {
		return "", "", fmt.Errorf("base fallback requires baseAddress")
	}
	if strings.Contains(address, "://") {
		parsed, parseErr := url.Parse(address)
		if parseErr != nil {
			return "", "", fmt.Errorf("invalid baseAddress: %w", parseErr)
		}
		if strings.TrimSpace(parsed.Host) == "" {
			return "", "", fmt.Errorf("invalid baseAddress: missing host")
		}
		return parsed.Host, "base", nil
	}
	return address, "base", nil
}

func (h *Handler) executeGRPCHealthCheck(
	ctx context.Context,
	target string,
	resolvedEnv string,
	request domain.EgressGRPCRequest,
) (string, error) {
	timeout := h.cfg.Tunnel.RequestTimeout
	if request.TimeoutMs > 0 {
		timeout = time.Duration(request.TimeoutMs) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = 5 * time.Second
	}

	callCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := grpc.DialContext(
		callCtx,
		target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return "", err
	}
	defer conn.Close()

	// gRPC 出口链路与 HTTP 保持一致，统一透传 x-env 上下文。
	callCtx = grpcmetadata.NewOutgoingContext(callCtx, grpcmetadata.Pairs(
		"x-env", resolvedEnv,
		"X-Env", resolvedEnv,
	))

	client := grpc_health_v1.NewHealthClient(conn)
	reply, err := client.Check(callCtx, &grpc_health_v1.HealthCheckRequest{
		Service: strings.TrimSpace(request.HealthService),
	})
	if err != nil {
		return "", err
	}
	return reply.GetStatus().String(), nil
}

func classifyGRPCError(err error, upstream string) (domain.ErrorCode, string) {
	status, ok := grpcstatus.FromError(err)
	if !ok {
		if strings.EqualFold(upstream, "dev") {
			return domain.ErrorTunnelOffline, firstNonBlank(err.Error(), "grpc target unavailable")
		}
		return domain.ErrorLocalEndpointDown, firstNonBlank(err.Error(), "base grpc upstream unavailable")
	}

	switch status.Code() {
	case codes.DeadlineExceeded:
		return domain.ErrorUpstreamTimeout, firstNonBlank(status.Message(), "grpc upstream timeout")
	case codes.Unavailable:
		if strings.EqualFold(upstream, "dev") {
			return domain.ErrorTunnelOffline, firstNonBlank(status.Message(), "dev grpc target unavailable")
		}
		return domain.ErrorLocalEndpointDown, firstNonBlank(status.Message(), "base grpc upstream unavailable")
	default:
		return domain.ErrorRouteExtractFailed, firstNonBlank(status.Message(), status.Code().String())
	}
}
