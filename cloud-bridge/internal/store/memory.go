package store

import (
	"sort"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

// MemoryStore stores sessions/intercepts/routes in memory.
type MemoryStore struct {
	mu sync.RWMutex

	sessions   map[string]domain.TunnelSession
	intercepts map[string]domain.ActiveIntercept
	routes     map[string]domain.BridgeRoute
	events     []domain.TunnelEvent
}

// NewMemoryStore constructs bridge runtime store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		sessions:   map[string]domain.TunnelSession{},
		intercepts: map[string]domain.ActiveIntercept{},
		routes:     map[string]domain.BridgeRoute{},
		events:     make([]domain.TunnelEvent, 0, 128),
	}
}

// UpsertSession writes one tunnel session.
func (s *MemoryStore) UpsertSession(session domain.TunnelSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.TunnelID] = session
}

// ListSessions returns sessions sorted by tunnel ID.
func (s *MemoryStore) ListSessions() []domain.TunnelSession {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.TunnelSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].TunnelID < result[j].TunnelID })
	return result
}

// ListIntercepts returns active intercepts.
func (s *MemoryStore) ListIntercepts() []domain.ActiveIntercept {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.ActiveIntercept, 0, len(s.intercepts))
	for _, intercept := range s.intercepts {
		result = append(result, intercept)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			return result[i].Env < result[j].Env
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// ListRoutes returns bridge routes.
func (s *MemoryStore) ListRoutes() []domain.BridgeRoute {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make([]domain.BridgeRoute, 0, len(s.routes))
	for _, route := range s.routes {
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			return result[i].Env < result[j].Env
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// RecordEvent appends one tunnel event and updates placeholder state.
func (s *MemoryStore) RecordEvent(event domain.TunnelEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
	if len(s.events) > 256 {
		s.events = s.events[len(s.events)-256:]
	}

	// Phase-one scaffold: keep one synthetic session active on incoming events.
	s.sessions["local-dev-tunnel"] = domain.TunnelSession{
		TunnelID:        "local-dev-tunnel",
		RDName:          "unknown-rd",
		ConnID:          event.EventID,
		SessionEpoch:    event.SessionEpoch,
		Status:          "connected",
		LastHeartbeatAt: time.Now().UTC(),
		ConnectedAt:     time.Now().UTC(),
	}
}
