package store

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

const (
	maxEventHistory          = 256
	maxProcessedEventHistory = 4096
)

// EventRejectError 表示 tunnel 事件被业务规则拒绝。
type EventRejectError struct {
	Code            string
	Message         string
	StatusCode      int
	SessionEpoch    int64
	ResourceVersion int64
}

func (e *EventRejectError) Error() string {
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type processedEventRecord struct {
	Key          string
	TunnelID     string
	SessionEpoch int64
	EventID      string
	MessageType  string
	ProcessedAt  time.Time
}

// MemoryStore 保存 bridge 运行时内存态。
type MemoryStore struct {
	mu sync.RWMutex

	bridgeHost string
	bridgePort int

	sessions   map[string]domain.TunnelSession
	intercepts map[string]domain.ActiveIntercept
	routes     map[string]domain.BridgeRoute
	events     []domain.TunnelEvent

	processedEvents map[string]processedEventRecord
	processedOrder  []string
}

// NewMemoryStore 创建默认配置的内存存储。
func NewMemoryStore() *MemoryStore {
	return NewMemoryStoreWithBridge("bridge.example.internal", 443)
}

// NewMemoryStoreWithBridge 创建可指定 bridge 出口地址的内存存储。
func NewMemoryStoreWithBridge(bridgeHost string, bridgePort int) *MemoryStore {
	host := strings.TrimSpace(bridgeHost)
	if host == "" {
		host = "bridge.example.internal"
	}
	if bridgePort <= 0 {
		bridgePort = 443
	}
	return &MemoryStore{
		bridgeHost:      host,
		bridgePort:      bridgePort,
		sessions:        map[string]domain.TunnelSession{},
		intercepts:      map[string]domain.ActiveIntercept{},
		routes:          map[string]domain.BridgeRoute{},
		events:          make([]domain.TunnelEvent, 0, maxEventHistory),
		processedEvents: map[string]processedEventRecord{},
		processedOrder:  make([]string, 0, maxProcessedEventHistory),
	}
}

// UpsertSession 写入或更新一条 tunnel session。
func (s *MemoryStore) UpsertSession(session domain.TunnelSession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[session.TunnelID] = session
}

// ListSessions 返回按 tunnelId 排序的会话列表。
func (s *MemoryStore) ListSessions() []domain.TunnelSession {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.TunnelSession, 0, len(s.sessions))
	for _, session := range s.sessions {
		result = append(result, session)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].TunnelID < result[j].TunnelID
	})
	return result
}

// ListIntercepts 返回按 service/env/protocol 排序的接管列表。
func (s *MemoryStore) ListIntercepts() []domain.ActiveIntercept {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.ActiveIntercept, 0, len(s.intercepts))
	for _, intercept := range s.intercepts {
		result = append(result, intercept)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			if result[i].Env == result[j].Env {
				return result[i].Protocol < result[j].Protocol
			}
			return result[i].Env < result[j].Env
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// ListRoutes 返回按 service/env/protocol 排序的路由列表。
func (s *MemoryStore) ListRoutes() []domain.BridgeRoute {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := make([]domain.BridgeRoute, 0, len(s.routes))
	for _, route := range s.routes {
		result = append(result, route)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].ServiceName == result[j].ServiceName {
			if result[i].Env == result[j].Env {
				return result[i].Protocol < result[j].Protocol
			}
			return result[i].Env < result[j].Env
		}
		return result[i].ServiceName < result[j].ServiceName
	})
	return result
}

// ResolveRouteForIngress 按 (env, serviceName, protocol) 查找一条可用路由与会话。
func (s *MemoryStore) ResolveRouteForIngress(env, serviceName, protocol string) (domain.BridgeRoute, domain.TunnelSession, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	env = strings.TrimSpace(env)
	serviceName = strings.TrimSpace(serviceName)
	protocol = sanitizeProtocol(protocol)
	if env == "" || serviceName == "" {
		return domain.BridgeRoute{}, domain.TunnelSession{}, false
	}

	// 先筛选出候选路由，再按 tunnelId/targetPort 排序，保证回流行为可预测。
	candidates := make([]domain.BridgeRoute, 0)
	for _, route := range s.routes {
		if !strings.EqualFold(route.Env, env) {
			continue
		}
		if !strings.EqualFold(route.ServiceName, serviceName) {
			continue
		}
		if sanitizeProtocol(route.Protocol) != protocol {
			continue
		}
		candidates = append(candidates, route)
	}
	if len(candidates) == 0 {
		return domain.BridgeRoute{}, domain.TunnelSession{}, false
	}

	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].TunnelID == candidates[j].TunnelID {
			return candidates[i].TargetPort < candidates[j].TargetPort
		}
		return candidates[i].TunnelID < candidates[j].TunnelID
	})

	for _, candidate := range candidates {
		session, ok := s.sessions[candidate.TunnelID]
		if !ok {
			continue
		}
		return candidate, session, true
	}
	return domain.BridgeRoute{}, domain.TunnelSession{}, false
}

func (s *MemoryStore) appendEventLocked(event domain.TunnelEvent) {
	s.events = append(s.events, event)
	if len(s.events) > maxEventHistory {
		s.events = s.events[len(s.events)-maxEventHistory:]
	}
}

func (s *MemoryStore) eventDedupKey(tunnelID string, sessionEpoch int64, eventID string) string {
	return fmt.Sprintf("%s|%d|%s", strings.ToLower(strings.TrimSpace(tunnelID)), sessionEpoch, strings.TrimSpace(eventID))
}

func (s *MemoryStore) findProcessedLocked(dedupKey string) (processedEventRecord, bool) {
	record, ok := s.processedEvents[dedupKey]
	return record, ok
}

func (s *MemoryStore) rememberProcessedLocked(record processedEventRecord) {
	if _, exists := s.processedEvents[record.Key]; !exists {
		s.processedOrder = append(s.processedOrder, record.Key)
	}
	s.processedEvents[record.Key] = record

	if len(s.processedOrder) <= maxProcessedEventHistory {
		return
	}

	oldest := s.processedOrder[0]
	s.processedOrder = s.processedOrder[1:]
	delete(s.processedEvents, oldest)
}

func (s *MemoryStore) clearTunnelStateLocked(tunnelID string) {
	for key, intercept := range s.intercepts {
		if intercept.TunnelID == tunnelID {
			delete(s.intercepts, key)
		}
	}
	for key, route := range s.routes {
		if route.TunnelID == tunnelID {
			delete(s.routes, key)
		}
	}
}

func activeInterceptKey(tunnelID, env, serviceName, protocol, instanceID string, targetPort int) string {
	return fmt.Sprintf(
		"%s|%s|%s|%s|%s|%d",
		strings.ToLower(strings.TrimSpace(tunnelID)),
		strings.ToLower(strings.TrimSpace(env)),
		strings.ToLower(strings.TrimSpace(serviceName)),
		strings.ToLower(strings.TrimSpace(protocol)),
		strings.TrimSpace(instanceID),
		targetPort,
	)
}

func bridgeRouteKey(tunnelID, env, serviceName, protocol, instanceID string, targetPort int) string {
	return activeInterceptKey(tunnelID, env, serviceName, protocol, instanceID, targetPort)
}

func sanitizeProtocol(protocol string) string {
	value := strings.ToLower(strings.TrimSpace(protocol))
	if value == "" {
		return "http"
	}
	return value
}

func sanitizeStatus(status string) string {
	value := strings.TrimSpace(status)
	if value == "" {
		return "active"
	}
	return value
}

func maxInt64(a, b int64) int64 {
	if a >= b {
		return a
	}
	return b
}
