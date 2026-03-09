package store

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

const (
	maxErrorEntries     = 50
	maxRequestSummaries = 200
	maxProcessedEventID = 2048
)

var (
	// ErrInvalidRegistration indicates malformed registration payload.
	ErrInvalidRegistration = errors.New("invalid registration")
	// ErrInstanceNotFound indicates target instance does not exist.
	ErrInstanceNotFound = errors.New("instance not found")
	// ErrInstanceConflict indicates one instance ID attempts to bind another service/env.
	ErrInstanceConflict = errors.New("instance id already bound to another env/service")
)

type processedEvent struct {
	action      string
	instanceKey string
	processedAt time.Time
}

// MemoryStore stores agent runtime state in memory only.
type MemoryStore struct {
	mu sync.RWMutex

	registrations map[string]domain.LocalRegistration // InstanceKey.String() -> registration
	instanceIndex map[string]string                   // instanceID -> InstanceKey.String()
	serviceIndex  map[string]map[string]struct{}      // ServiceKey.String() -> set(InstanceKey.String())
	endpointIndex map[string]string                   // EndpointKey.String() -> InstanceKey.String()

	recentErrors []domain.ErrorEntry
	recentReqs   []domain.RequestSummary

	processedEvents map[string]processedEvent
	processedOrder  []string

	resourceVersion int64
	sessionEpoch    int64
	tunnelConnected bool
	lastTunnelHB    time.Time
}

// NewMemoryStore constructs an in-memory runtime state store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		registrations:   make(map[string]domain.LocalRegistration),
		instanceIndex:   make(map[string]string),
		serviceIndex:    make(map[string]map[string]struct{}),
		endpointIndex:   make(map[string]string),
		recentErrors:    make([]domain.ErrorEntry, 0, maxErrorEntries),
		recentReqs:      make([]domain.RequestSummary, 0, maxRequestSummaries),
		processedEvents: make(map[string]processedEvent),
		processedOrder:  make([]string, 0, maxProcessedEventID),
	}
}

// UpsertRegistration creates or updates one registration and bumps resourceVersion on non-duplicate events.
func (s *MemoryStore) UpsertRegistration(reg domain.LocalRegistration, eventID string) (domain.LocalRegistration, bool, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if duplicated, existing, ok := s.lookupDuplicateUpsertLocked(eventID); ok {
		return existing, duplicated, nil
	}

	reg = normalizeRegistration(reg)
	if err := validateRegistration(reg); err != nil {
		return domain.LocalRegistration{}, false, err
	}

	instanceKey := domain.NewInstanceKey(reg.Env, reg.ServiceName, reg.InstanceID).String()
	if existingInstanceKey, ok := s.instanceIndex[reg.InstanceID]; ok && existingInstanceKey != instanceKey {
		return domain.LocalRegistration{}, false, fmt.Errorf("%w: instanceId=%s existing=%s target=%s", ErrInstanceConflict, reg.InstanceID, existingInstanceKey, instanceKey)
	}

	existing, hasExisting := s.registrations[instanceKey]
	if hasExisting {
		s.removeEndpointIndexesLocked(existing)
		if reg.RegisterTime.IsZero() {
			reg.RegisterTime = existing.RegisterTime
		}
	}
	if reg.RegisterTime.IsZero() {
		reg.RegisterTime = now
	}

	reg.LastHeartbeatTime = now
	reg.Healthy = true
	reg.Endpoints = uniqueNormalizedEndpoints(reg)

	s.registrations[instanceKey] = reg
	s.instanceIndex[reg.InstanceID] = instanceKey
	s.addServiceIndexLocked(reg, instanceKey)
	s.addEndpointIndexesLocked(reg, instanceKey)

	s.resourceVersion++
	s.recordProcessedEventLocked(eventID, processedEvent{action: "upsert", instanceKey: instanceKey, processedAt: now})
	return reg, false, nil
}

// Heartbeat updates heartbeat timestamp for one registration.
func (s *MemoryStore) Heartbeat(instanceID string) (domain.LocalRegistration, error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	instanceKey, ok := s.instanceIndex[strings.TrimSpace(instanceID)]
	if !ok {
		return domain.LocalRegistration{}, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
	}

	reg := s.registrations[instanceKey]
	reg.LastHeartbeatTime = now
	reg.Healthy = true
	s.registrations[instanceKey] = reg
	return reg, nil
}

// DeleteRegistration removes one registration and bumps resourceVersion on non-duplicate events.
func (s *MemoryStore) DeleteRegistration(instanceID, eventID string) (deleted bool, duplicated bool, err error) {
	now := time.Now().UTC()

	s.mu.Lock()
	defer s.mu.Unlock()

	if duplicated, ok := s.lookupDuplicateDeleteLocked(eventID); ok {
		return false, duplicated, nil
	}

	instanceKey, ok := s.instanceIndex[strings.TrimSpace(instanceID)]
	if !ok {
		return false, false, fmt.Errorf("%w: %s", ErrInstanceNotFound, instanceID)
	}

	s.deleteByInstanceKeyLocked(instanceKey)
	s.resourceVersion++
	s.recordProcessedEventLocked(eventID, processedEvent{action: "delete", instanceKey: instanceKey, processedAt: now})
	return true, false, nil
}

// FindRegistrationByInstanceID 根据 instanceID 查询注册项。
func (s *MemoryStore) FindRegistrationByInstanceID(instanceID string) (domain.LocalRegistration, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	instanceKey, ok := s.instanceIndex[strings.TrimSpace(instanceID)]
	if !ok {
		return domain.LocalRegistration{}, false
	}
	reg, exists := s.registrations[instanceKey]
	if !exists {
		return domain.LocalRegistration{}, false
	}
	return reg, true
}

// SnapshotRegistrations 返回当前注册表快照，用于 full-sync。
func (s *MemoryStore) SnapshotRegistrations() []domain.LocalRegistration {
	return s.ListRegistrations()
}

// ListRegistrations returns registrations sorted by env, serviceName, then instanceID.
func (s *MemoryStore) ListRegistrations() []domain.LocalRegistration {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.LocalRegistration, 0, len(s.registrations))
	for _, reg := range s.registrations {
		result = append(result, reg)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Env == result[j].Env {
			if result[i].ServiceName == result[j].ServiceName {
				return result[i].InstanceID < result[j].InstanceID
			}
			return result[i].ServiceName < result[j].ServiceName
		}
		return result[i].Env < result[j].Env
	})
	return result
}

// Discover resolves dev-first route and base fallback metadata.
func (s *MemoryStore) Discover(req domain.DiscoverRequest, runtimeEnv string) domain.DiscoverResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()

	requestedEnv := strings.TrimSpace(req.Env)
	if requestedEnv == "" {
		requestedEnv = strings.TrimSpace(runtimeEnv)
	}
	if requestedEnv == "" {
		requestedEnv = "base"
	}

	protocol := strings.ToLower(strings.TrimSpace(req.Protocol))
	lookupOrder := []string{requestedEnv}
	if !strings.EqualFold(requestedEnv, "base") {
		lookupOrder = append(lookupOrder, "base")
	}

	for _, env := range lookupOrder {
		serviceKey := domain.NewServiceKey(env, req.ServiceName).String()
		instances, ok := s.serviceIndex[serviceKey]
		if !ok || len(instances) == 0 {
			continue
		}

		instanceKeys := make([]string, 0, len(instances))
		for instanceKey := range instances {
			instanceKeys = append(instanceKeys, instanceKey)
		}
		sort.Strings(instanceKeys)

		for _, instanceKey := range instanceKeys {
			reg, exists := s.registrations[instanceKey]
			if !exists || !reg.Healthy {
				continue
			}
			for _, endpoint := range reg.Endpoints {
				if strings.ToLower(endpoint.Protocol) != protocol {
					continue
				}
				endpointKey := domain.NewEndpointKey(reg.Env, reg.ServiceName, reg.InstanceID, endpoint.Protocol, endpoint.ListenPort).String()
				return domain.DiscoverResponse{
					Matched:      true,
					ResolvedEnv:  env,
					Resolution:   resolutionForEnv(env, requestedEnv),
					RouteTarget:  buildRouteTarget(reg, endpoint),
					ResourceHint: fmt.Sprintf("resourceVersion=%d", s.resourceVersion),
					ServiceKey:   serviceKey,
					InstanceKey:  instanceKey,
					EndpointKey:  endpointKey,
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
			ServiceName: strings.TrimSpace(req.ServiceName),
			Protocol:    protocol,
			TargetHost:  "",
			TargetPort:  0,
		},
		ResourceHint: fmt.Sprintf("resourceVersion=%d", s.resourceVersion),
		ServiceKey:   domain.NewServiceKey("base", req.ServiceName).String(),
	}
}

func resolutionForEnv(resolved, requested string) string {
	if strings.EqualFold(resolved, requested) {
		return "dev-priority"
	}
	return "base-fallback"
}

// Summary 返回 UI 所需的状态摘要。
func (s *MemoryStore) Summary(rdName, envName string, defaultTTLSeconds int, scanInterval time.Duration) domain.StateSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	return s.buildSummaryLocked(time.Now().UTC(), rdName, envName, defaultTTLSeconds, scanInterval)
}

// Diagnostics 返回聚合诊断快照，避免 UI 侧多接口拼装造成时间窗不一致。
func (s *MemoryStore) Diagnostics(rdName, envName string, defaultTTLSeconds int, scanInterval time.Duration) domain.DiagnosticsSnapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()

	now := time.Now().UTC()
	recentErrors := slices.Clone(s.recentErrors)
	recentRequests := slices.Clone(s.recentReqs)

	// 统一在 store 层做倒序，保证所有调用方拿到的都是“最近优先”语义。
	slices.Reverse(recentErrors)
	slices.Reverse(recentRequests)

	return domain.DiagnosticsSnapshot{
		Summary:        s.buildSummaryLocked(now, rdName, envName, defaultTTLSeconds, scanInterval),
		RecentErrors:   recentErrors,
		RecentRequests: recentRequests,
		GeneratedAt:    now,
	}
}

// buildSummaryLocked 在持锁上下文内构建状态摘要，避免重复拼装逻辑。
func (s *MemoryStore) buildSummaryLocked(now time.Time, rdName, envName string, defaultTTLSeconds int, scanInterval time.Duration) domain.StateSummary {
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
		ActiveIntercepts:    len(s.endpointIndex),
		LastUpdateAt:        now,
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

	result := make([]domain.ActiveIntercept, 0, len(s.endpointIndex))
	now := time.Now().UTC()
	for instanceKey := range s.registrations {
		reg := s.registrations[instanceKey]
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
			if result[i].Env == result[j].Env {
				return result[i].InstanceID < result[j].InstanceID
			}
			return result[i].Env < result[j].Env
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

// ListRequestSummaries 返回最近请求摘要，按时间倒序。
func (s *MemoryStore) ListRequestSummaries() []domain.RequestSummary {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := slices.Clone(s.recentReqs)
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

// AddRequestSummary 记录一条请求摘要，超过上限时淘汰最旧数据。
func (s *MemoryStore) AddRequestSummary(summary domain.RequestSummary) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.recentReqs = append(s.recentReqs, summary)
	if len(s.recentReqs) > maxRequestSummaries {
		s.recentReqs = s.recentReqs[len(s.recentReqs)-maxRequestSummaries:]
	}
}

// ExpireRegistrations 删除超过 TTL 的注册项，并返回被删除的快照列表。
func (s *MemoryStore) ExpireRegistrations(now time.Time) []domain.LocalRegistration {
	s.mu.Lock()
	defer s.mu.Unlock()

	removed := make([]domain.LocalRegistration, 0)
	for instanceKey, reg := range s.registrations {
		expiresAt := reg.LastHeartbeatTime.Add(time.Duration(reg.TTLSeconds) * time.Second)
		if now.After(expiresAt) {
			s.deleteByInstanceKeyLocked(instanceKey)
			removed = append(removed, reg)
		}
	}
	if len(removed) > 0 {
		s.resourceVersion++
	}
	sort.Slice(removed, func(i, j int) bool {
		if removed[i].ServiceName == removed[j].ServiceName {
			return removed[i].InstanceID < removed[j].InstanceID
		}
		return removed[i].ServiceName < removed[j].ServiceName
	})
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

// BeginTunnelReconnect 在重连前开启新 session epoch。
func (s *MemoryStore) BeginTunnelReconnect() domain.TunnelState {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionEpoch++
	s.tunnelConnected = false
	s.lastTunnelHB = time.Now().UTC()

	return domain.TunnelState{
		Connected:         s.tunnelConnected,
		SessionEpoch:      s.sessionEpoch,
		ResourceVersion:   s.resourceVersion,
		LastHeartbeatAt:   s.lastTunnelHB,
		BridgeAddress:     "",
		ReconnectBackoffM: []int{500, 1000, 2000, 5000},
	}
}

// MarkTunnelConnected 更新 tunnel 为已连接状态。
func (s *MemoryStore) MarkTunnelConnected() domain.TunnelState {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.tunnelConnected = true
	s.lastTunnelHB = time.Now().UTC()
	return domain.TunnelState{
		Connected:         s.tunnelConnected,
		SessionEpoch:      s.sessionEpoch,
		ResourceVersion:   s.resourceVersion,
		LastHeartbeatAt:   s.lastTunnelHB,
		BridgeAddress:     "",
		ReconnectBackoffM: []int{500, 1000, 2000, 5000},
	}
}

// MarkTunnelDisconnected 更新 tunnel 为断开状态。
func (s *MemoryStore) MarkTunnelDisconnected() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tunnelConnected = false
}

// MarkTunnelHeartbeat 刷新 tunnel 心跳时间。
func (s *MemoryStore) MarkTunnelHeartbeat(at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lastTunnelHB = at.UTC()
}

// CurrentSyncMeta 返回当前 sessionEpoch 和 resourceVersion。
func (s *MemoryStore) CurrentSyncMeta() (sessionEpoch int64, resourceVersion int64) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sessionEpoch, s.resourceVersion
}

func (s *MemoryStore) lookupDuplicateUpsertLocked(eventID string) (bool, domain.LocalRegistration, bool) {
	if eventID == "" {
		return false, domain.LocalRegistration{}, false
	}
	event, ok := s.processedEvents[eventID]
	if !ok {
		return false, domain.LocalRegistration{}, false
	}
	if event.action != "upsert" {
		return true, domain.LocalRegistration{}, true
	}
	reg, exists := s.registrations[event.instanceKey]
	if !exists {
		return true, domain.LocalRegistration{}, true
	}
	return true, reg, true
}

func (s *MemoryStore) lookupDuplicateDeleteLocked(eventID string) (bool, bool) {
	if eventID == "" {
		return false, false
	}
	if _, ok := s.processedEvents[eventID]; !ok {
		return false, false
	}
	return true, true
}

func (s *MemoryStore) addServiceIndexLocked(reg domain.LocalRegistration, instanceKey string) {
	serviceKey := domain.NewServiceKey(reg.Env, reg.ServiceName).String()
	instances, ok := s.serviceIndex[serviceKey]
	if !ok {
		instances = make(map[string]struct{})
		s.serviceIndex[serviceKey] = instances
	}
	instances[instanceKey] = struct{}{}
}

func (s *MemoryStore) addEndpointIndexesLocked(reg domain.LocalRegistration, instanceKey string) {
	for _, endpoint := range reg.Endpoints {
		endpointKey := domain.NewEndpointKey(reg.Env, reg.ServiceName, reg.InstanceID, endpoint.Protocol, endpoint.ListenPort).String()
		s.endpointIndex[endpointKey] = instanceKey
	}
}

func (s *MemoryStore) removeEndpointIndexesLocked(reg domain.LocalRegistration) {
	for _, endpoint := range reg.Endpoints {
		endpointKey := domain.NewEndpointKey(reg.Env, reg.ServiceName, reg.InstanceID, endpoint.Protocol, endpoint.ListenPort).String()
		delete(s.endpointIndex, endpointKey)
	}
}

func (s *MemoryStore) deleteByInstanceKeyLocked(instanceKey string) {
	reg, ok := s.registrations[instanceKey]
	if !ok {
		return
	}

	s.removeEndpointIndexesLocked(reg)

	serviceKey := domain.NewServiceKey(reg.Env, reg.ServiceName).String()
	if instances, exists := s.serviceIndex[serviceKey]; exists {
		delete(instances, instanceKey)
		if len(instances) == 0 {
			delete(s.serviceIndex, serviceKey)
		}
	}

	delete(s.instanceIndex, reg.InstanceID)
	delete(s.registrations, instanceKey)
}

func (s *MemoryStore) recordProcessedEventLocked(eventID string, event processedEvent) {
	eventID = strings.TrimSpace(eventID)
	if eventID == "" {
		return
	}
	if _, exists := s.processedEvents[eventID]; !exists {
		s.processedOrder = append(s.processedOrder, eventID)
	}
	s.processedEvents[eventID] = event

	if len(s.processedOrder) <= maxProcessedEventID {
		return
	}
	oldest := s.processedOrder[0]
	s.processedOrder = s.processedOrder[1:]
	delete(s.processedEvents, oldest)
}

func validateRegistration(reg domain.LocalRegistration) error {
	if strings.TrimSpace(reg.ServiceName) == "" || strings.TrimSpace(reg.Env) == "" || strings.TrimSpace(reg.InstanceID) == "" {
		return fmt.Errorf("%w: serviceName/env/instanceId are required", ErrInvalidRegistration)
	}
	for _, endpoint := range reg.Endpoints {
		if strings.TrimSpace(endpoint.Protocol) == "" {
			return fmt.Errorf("%w: endpoint.protocol is required", ErrInvalidRegistration)
		}
		if endpoint.TargetPort <= 0 {
			return fmt.Errorf("%w: endpoint.targetPort must be positive", ErrInvalidRegistration)
		}
	}
	return nil
}

func normalizeRegistration(reg domain.LocalRegistration) domain.LocalRegistration {
	reg.ServiceName = strings.TrimSpace(reg.ServiceName)
	reg.Env = strings.TrimSpace(reg.Env)
	reg.InstanceID = strings.TrimSpace(reg.InstanceID)
	if reg.Metadata == nil {
		reg.Metadata = map[string]string{}
	}
	if reg.TTLSeconds <= 0 {
		reg.TTLSeconds = 30
	}
	if reg.Endpoints == nil {
		reg.Endpoints = []domain.LocalEndpoint{}
	}
	return reg
}

func uniqueNormalizedEndpoints(reg domain.LocalRegistration) []domain.LocalEndpoint {
	if len(reg.Endpoints) == 0 {
		return []domain.LocalEndpoint{}
	}
	result := make([]domain.LocalEndpoint, 0, len(reg.Endpoints))
	seen := make(map[string]struct{})
	for _, endpoint := range reg.Endpoints {
		normalized := normalizeEndpoint(endpoint)
		key := fmt.Sprintf("%s|%d", normalized.Protocol, normalized.ListenPort)
		if _, exists := seen[key]; exists {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, normalized)
	}

	sort.Slice(result, func(i, j int) bool {
		if result[i].Protocol == result[j].Protocol {
			return result[i].ListenPort < result[j].ListenPort
		}
		return result[i].Protocol < result[j].Protocol
	})
	return result
}

func normalizeEndpoint(endpoint domain.LocalEndpoint) domain.LocalEndpoint {
	endpoint.Protocol = strings.ToLower(strings.TrimSpace(endpoint.Protocol))
	endpoint.ListenHost = strings.TrimSpace(endpoint.ListenHost)
	endpoint.TargetHost = strings.TrimSpace(endpoint.TargetHost)
	endpoint.Status = strings.TrimSpace(endpoint.Status)

	if endpoint.TargetHost == "" {
		endpoint.TargetHost = "127.0.0.1"
	}
	if endpoint.ListenHost == "" {
		endpoint.ListenHost = "127.0.0.1"
	}
	if endpoint.ListenPort <= 0 {
		endpoint.ListenPort = endpoint.TargetPort
	}
	if endpoint.Status == "" {
		endpoint.Status = "active"
	}
	return endpoint
}

func buildRouteTarget(reg domain.LocalRegistration, endpoint domain.LocalEndpoint) domain.RouteTarget {
	return domain.RouteTarget{
		Env:         reg.Env,
		ServiceName: reg.ServiceName,
		Protocol:    endpoint.Protocol,
		TargetHost:  endpoint.TargetHost,
		TargetPort:  endpoint.TargetPort,
	}
}
