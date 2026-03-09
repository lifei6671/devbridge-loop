package httpapi

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

// Handler implements local management and state APIs.
type Handler struct {
	cfg   config.Config
	store *store.MemoryStore
}

// NewHandler creates API handlers.
func NewHandler(cfg config.Config, s *store.MemoryStore) *Handler {
	return &Handler{cfg: cfg, store: s}
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
		payload.InstanceID = generateInstanceID()
	}
	if strings.TrimSpace(payload.Env) == "" {
		payload.Env = h.cfg.EnvName
	}
	if payload.TTLSeconds <= 0 {
		payload.TTLSeconds = h.cfg.Registration.DefaultTTLSeconds
	}
	for i := range payload.Endpoints {
		if strings.TrimSpace(payload.Endpoints[i].TargetHost) == "" {
			payload.Endpoints[i].TargetHost = "127.0.0.1"
		}
		if strings.TrimSpace(payload.Endpoints[i].Status) == "" {
			payload.Endpoints[i].Status = "active"
		}
	}

	stored := h.store.UpsertRegistration(payload)
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
	if !h.store.DeleteRegistration(instanceID) {
		respondJSON(w, http.StatusNotFound, map[string]string{"error": fmt.Sprintf("instance %s not found", instanceID)})
		return
	}
	respondJSON(w, http.StatusOK, map[string]string{"result": "deleted", "instanceId": instanceID})
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
	result := h.store.Discover(request, h.cfg.EnvName)
	respondJSON(w, http.StatusOK, result)
}

func (h *Handler) stateSummary(w http.ResponseWriter, _ *http.Request) {
	summary := h.store.Summary(h.cfg.RDName, h.cfg.EnvName, h.cfg.Registration.DefaultTTLSeconds, h.cfg.Registration.ScanInterval)
	respondJSON(w, http.StatusOK, summary)
}

func (h *Handler) stateTunnel(w http.ResponseWriter, _ *http.Request) {
	state := h.store.TunnelState("bridge.example.internal:443")
	respondJSON(w, http.StatusOK, state)
}

func (h *Handler) stateIntercepts(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListActiveIntercepts())
}

func (h *Handler) stateErrors(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ListErrors())
}

func (h *Handler) reconnect(w http.ResponseWriter, _ *http.Request) {
	respondJSON(w, http.StatusOK, h.store.ReconnectTunnel())
}

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func generateInstanceID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("inst-%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("inst-%s", hex.EncodeToString(buf))
}
