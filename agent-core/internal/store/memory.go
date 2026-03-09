package store

import (
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

const maxErrorEntries = 50

// MemoryStore stores agent runtime state in memory only.
type MemoryStore struct {
	mu sync.RWMutex

	registrations   map[string]domain.LocalRegistration
	recentErrors    []domain.ErrorEntry
	resourceVersion int64
	sessionEpoch    int64
	tunnelConnected bool
	lastTunnelHB    time.Time
}

// NewMemoryStore constructs an in-memory runtime state store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		registrations: make(map[string]domain.LocalRegistration),
		recentErrors:  make([]domain.ErrorEntry, 0, maxErrorEntries),
	}
}

// UpsertRegistration creates or updates one registration and bumps resourceVersion.
func (s *MemoryStore) UpsertRegistration(reg domain.LocalRegistration) domain.LocalRegistration {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if reg.RegisterTime.IsZero() {
		reg.RegisterTime = now
	}
	reg.LastHeartbeatTime = now
	if reg.TTLSeconds <= 0 {
		reg.TTLSeconds = 30
	}
	if reg.Metadata == nil {
		reg.Metadata = map[string]string{}
	}
	if len(reg.Endpoints) == 0 {
		reg.Endpoints = []domain.LocalEndpoint{}
	}
	reg.Healthy = true

	s.registrations[reg.InstanceID] = reg
	s.resourceVersion++
	s.lastTunnelHB = now
	return reg
}

// Heartbeat updates heartbeat timestamp for one registration.
func (s *MemoryStore) Heartbeat(instanceID string) (domain.LocalRegistration, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	reg, ok := s.registrations[instanceID]
	if !ok {
		return domain.LocalRegistration{}, fmt.Errorf("instance %s not found", instanceID)
	}
	reg.LastHeartbeatTime = now
	reg.Healthy = true
	s.registrations[instanceID] = reg
	return reg, nil
}

// DeleteRegistration removes one registration and bumps resourceVersion.
func (s *MemoryStore) DeleteRegistration(instanceID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.registrations[instanceID]; !ok {
		return false
	}
	delete(s.registrations, instanceID)
	s.resourceVersion++
	return true
}

// ListRegistrations returns registrations sorted by serviceName then instanceID.
func (s *MemoryStore) ListRegistrations() []domain.LocalRegistration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.LocalRegistration, 0, len(s.registrations))
	for _, reg := range s.registrations {
		result = append(result, reg)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			return result[i].InstanceID < result[j].InstanceID
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// Discover resolves dev-first route and base fallback metadata.
func (s *MemoryStore) Discover(req domain.DiscoverRequest, runtimeEnv string) domain.DiscoverResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	requestedEnv := strings.TrimSpace(req.Env)
	if requestedEnv == "" {
		requestedEnv = runtimeEnv
	}
	if requestedEnv == "" {
		requestedEnv = "base"
	}

	lookupOrder := []string{requestedEnv}
	if !strings.EqualFold(requestedEnv, "base") {
		lookupOrder = append(lookupOrder, "base")
	}

	for _, env := range lookupOrder {
		for _, reg := range s.registrations {
			if !reg.Healthy || reg.ServiceName != req.ServiceName || reg.Env != env {
				continue
			}
			for _, endpoint := range reg.Endpoints {
				if !strings.EqualFold(endpoint.Protocol, req.Protocol) {
					continue
				}
				return domain.DiscoverResponse{
					Matched:     true,
					ResolvedEnv: env,
					Resolution:  resolutionForEnv(env, requestedEnv),
					RouteTarget: domain.RouteTarget{
						Env:         env,
						ServiceName: reg.ServiceName,
						Protocol:    endpoint.Protocol,
						TargetHost:  endpoint.TargetHost,
						TargetPort:  endpoint.TargetPort,
					},
					ResourceHint: fmt.Sprintf("resourceVersion=%d", s.resourceVersion),
				}
			}
		}
	}

	return domain.DiscoverResponse{
		Matched:     false,
		ResolvedEnv: "base",
		Resolution:  "base-fallback",
		RouteTarget: domain.RouteTarget{
			Env:         "base",
			ServiceName: req.ServiceName,
			Protocol:    req.Protocol,
			TargetHost:  "",
			TargetPort:  0,
		},
		ResourceHint: fmt.Sprintf("resourceVersion=%d", s.resourceVersion),
	}
}

func resolutionForEnv(resolved, requested string) string {
	if resolved == requested {
		return "dev-priority"
	}
	return "base-fallback"
}

// Summary returns aggregate state for UI.
func (s *MemoryStore) Summary(rdName, envName string, defaultTTLSeconds int, scanInterval time.Duration) domain.StateSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	tunnelStatus := "disconnected"
	bridgeStatus := "offline"
	if s.tunnelConnected {
		tunnelStatus = "connected"
		bridgeStatus = "online"
	}

	return domain.StateSummary{
		AgentStatus:         "running",
		BridgeStatus:        bridgeStatus,
		TunnelStatus:        tunnelStatus,
		CurrentEnv:          envName,
		RDName:              rdName,
		RegistrationCount:   len(s.registrations),
		ActiveIntercepts:    len(s.registrations),
		LastUpdateAt:        time.Now().UTC(),
		DefaultTTLSeconds:   defaultTTLSeconds,
		ScanIntervalSeconds: int(scanInterval / time.Second),
	}
}

// TunnelState returns current tunnel status.
func (s *MemoryStore) TunnelState(bridgeAddress string) domain.TunnelState {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return domain.TunnelState{
		Connected:         s.tunnelConnected,
		SessionEpoch:      s.sessionEpoch,
		ResourceVersion:   s.resourceVersion,
		LastHeartbeatAt:   s.lastTunnelHB,
		BridgeAddress:     bridgeAddress,
		ReconnectBackoffM: []int{500, 1000, 2000, 5000},
	}
}

// ListActiveIntercepts provides derived active intercept list.
func (s *MemoryStore) ListActiveIntercepts() []domain.ActiveIntercept {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.ActiveIntercept, 0, len(s.registrations))
	now := time.Now().UTC()
	for _, reg := range s.registrations {
		for _, endpoint := range reg.Endpoints {
			result = append(result, domain.ActiveIntercept{
				Env:         reg.Env,
				ServiceName: reg.ServiceName,
				Protocol:    endpoint.Protocol,
				TunnelID:    "local-dev-tunnel",
				InstanceID:  reg.InstanceID,
				TargetPort:  endpoint.TargetPort,
				Status:      endpoint.Status,
				UpdatedAt:   now,
			})
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			return result[i].InstanceID < result[j].InstanceID
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// ListErrors returns recent errors newest first.
func (s *MemoryStore) ListErrors() []domain.ErrorEntry {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := slices.Clone(s.recentErrors)
	slices.Reverse(result)
	return result
}

// AddError stores one error entry.
func (s *MemoryStore) AddError(code domain.ErrorCode, message string, context map[string]string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recentErrors = append(s.recentErrors, domain.ErrorEntry{
		Code:       code,
		Message:    message,
		Context:    context,
		OccurredAt: time.Now().UTC(),
	})
	if len(s.recentErrors) > maxErrorEntries {
		s.recentErrors = s.recentErrors[len(s.recentErrors)-maxErrorEntries:]
	}
}

// ExpireRegistrations removes registrations whose heartbeat exceeded TTL.
func (s *MemoryStore) ExpireRegistrations(now time.Time) []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := make([]string, 0)
	for id, reg := range s.registrations {
		expiresAt := reg.LastHeartbeatTime.Add(time.Duration(reg.TTLSeconds) * time.Second)
		if now.After(expiresAt) {
			delete(s.registrations, id)
			removed = append(removed, id)
		}
	}
	if len(removed) > 0 {
		s.resourceVersion++
	}
	return removed
}

// ReconnectTunnel simulates reconnect lifecycle and increments sessionEpoch.
func (s *MemoryStore) ReconnectTunnel() domain.TunnelState {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionEpoch++
	s.tunnelConnected = true
	s.lastTunnelHB = time.Now().UTC()

	return domain.TunnelState{
		Connected:         s.tunnelConnected,
		SessionEpoch:      s.sessionEpoch,
		ResourceVersion:   s.resourceVersion,
		LastHeartbeatAt:   s.lastTunnelHB,
		BridgeAddress:     "bridge.example.internal:443",
		ReconnectBackoffM: []int{500, 1000, 2000, 5000},
	}
}
