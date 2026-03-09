package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// Handler provides bridge admin/status endpoints.
type Handler struct {
	pipeline *routing.Pipeline
	store    *store.MemoryStore
}

// NewHandler creates bridge handlers.
func NewHandler(pipeline *routing.Pipeline, store *store.MemoryStore) *Handler {
	return &Handler{pipeline: pipeline, store: store}
}

// Router builds cloud-bridge API router.
func (h *Handler) Router() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", h.healthz)
	mux.HandleFunc("GET /api/v1/state/sessions", h.sessions)
	mux.HandleFunc("GET /api/v1/state/intercepts", h.intercepts)
	mux.HandleFunc("GET /api/v1/state/routes", h.routes)
	mux.HandleFunc("POST /api/v1/tunnel/events", h.tunnelEvent)
	mux.HandleFunc("GET /api/v1/debug/route-extract", h.debugRouteExtract)

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
		respondJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid payload"})
		return
	}
	if event.SentAt.IsZero() {
		event.SentAt = time.Now().UTC()
	}
	h.store.RecordEvent(event)
	respondJSON(w, http.StatusAccepted, map[string]string{"status": "accepted"})
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

func respondJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}
