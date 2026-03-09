package httpapi

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

// Handler 提供 bridge 管理面与状态查询接口。
type Handler struct {
	pipeline *routing.Pipeline
	store    *store.MemoryStore
}

// NewHandler 创建 HTTP handler 集合。
func NewHandler(pipeline *routing.Pipeline, store *store.MemoryStore) *Handler {
	return &Handler{pipeline: pipeline, store: store}
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
