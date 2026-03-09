package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/forwarder"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

// TunnelSyncPublisher 定义 HTTP 层到 tunnel 同步层的最小能力边界。
type TunnelSyncPublisher interface {
	EnqueueRegisterUpsert(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error
	EnqueueRegisterDelete(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error
	RequestReconnect(ctx context.Context) error
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
}

// NewHandler creates API handlers.
func NewHandler(cfg config.Config, s *store.MemoryStore, publisher TunnelSyncPublisher, backflowForwarder LocalBackflowForwarder) *Handler {
	return &Handler{
		cfg:               cfg,
		store:             s,
		syncPublisher:     publisher,
		backflowForwarder: backflowForwarder,
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

	mux.HandleFunc("GET /api/v1/state/summary", h.stateSummary)
	mux.HandleFunc("GET /api/v1/state/tunnel", h.stateTunnel)
	mux.HandleFunc("GET /api/v1/state/intercepts", h.stateIntercepts)
	mux.HandleFunc("GET /api/v1/state/errors", h.stateErrors)
	mux.HandleFunc("POST /api/v1/control/reconnect", h.reconnect)
	mux.HandleFunc("POST /api/v1/backflow/http", h.backflowHTTP)

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
	request.Env = resolveDiscoverEnv(r, request.Env)
	result := h.store.Discover(request, h.cfg.EnvName)
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) stateSummary(w http.ResponseWriter, _ *http.Request) {
	summary := h.store.Summary(h.cfg.RDName, h.cfg.EnvName, h.cfg.Registration.DefaultTTLSeconds, h.cfg.Registration.ScanInterval)
	respondJSON(w, http.StatusOK, summary)
}

func (h *Handler) stateTunnel(w http.ResponseWriter, _ *http.Request) {
	state := h.store.TunnelState(h.cfg.Tunnel.BridgeAddress)
	respondJSON(w, http.StatusOK, state)
}

func (h *Handler) stateIntercepts(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListActiveIntercepts())
}

func (h *Handler) stateErrors(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListErrors())
}

func (h *Handler) reconnect(w http.ResponseWriter, r *http.Request) {
	if h.syncPublisher == nil {
		state := h.store.ReconnectTunnel()
		respondJSON(w, http.StatusOK, state)
		return
	}

	if err := h.syncPublisher.RequestReconnect(r.Context()); err != nil {
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

func resolveDiscoverEnv(r *http.Request, payloadEnv string) string {
	headerEnv := strings.TrimSpace(r.Header.Get("x-env"))
	if headerEnv == "" {
		headerEnv = strings.TrimSpace(r.Header.Get("X-Env"))
	}
	if headerEnv != "" {
		return headerEnv
	}
	return strings.TrimSpace(payloadEnv)
}

func setEventHeaders(w http.ResponseWriter, eventID string, duplicated bool) {
	w.Header().Set("X-Event-Id", eventID)
	if duplicated {
		w.Header().Set("X-Event-Deduplicated", "true")
		return
	}
	w.Header().Set("X-Event-Deduplicated", "false")
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

func firstNonBlank(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
