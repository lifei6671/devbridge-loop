package registry

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// AuditInfo 描述 canonical 资源最近一次写入的审计元数据。
type AuditInfo struct {
	CreatedAt           time.Time
	UpdatedAt           time.Time
	LastEventID         string
	LastResourceVersion uint64
}

// CanonicalSnapshot 描述 canonical registry 的只读快照视图。
type CanonicalSnapshot struct {
	Connectors  []pb.Connector
	Sessions    []pb.Session
	Services    []pb.Service
	Routes      []pb.Route
	Projections []pb.DiscoveryProjection
	GeneratedAt time.Time
}

// CanonicalRegistry 维护低频一致性配置对象。
type CanonicalRegistry struct {
	mu sync.RWMutex

	connectors  map[string]pb.Connector
	sessions    map[string]pb.Session
	services    map[string]pb.Service
	routes      map[string]pb.Route
	projections map[string]pb.DiscoveryProjection

	serviceKeyToID        map[string]string
	sessionIDsByConnector map[string]map[string]struct{}
	serviceIDsByConnector map[string]map[string]struct{}
	routeIDsByServiceKey  map[string]map[string]struct{}

	audits map[string]AuditInfo
}

// NewCanonicalRegistry 创建 canonical registry 实例。
func NewCanonicalRegistry() *CanonicalRegistry {
	return &CanonicalRegistry{
		connectors:            make(map[string]pb.Connector),
		sessions:              make(map[string]pb.Session),
		services:              make(map[string]pb.Service),
		routes:                make(map[string]pb.Route),
		projections:           make(map[string]pb.DiscoveryProjection),
		serviceKeyToID:        make(map[string]string),
		sessionIDsByConnector: make(map[string]map[string]struct{}),
		serviceIDsByConnector: make(map[string]map[string]struct{}),
		routeIDsByServiceKey:  make(map[string]map[string]struct{}),
		audits:                make(map[string]AuditInfo),
	}
}

// UpsertConnector 写入或更新 connector 对象。
func (registry *CanonicalRegistry) UpsertConnector(connector pb.Connector) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertConnectorWithAudit(connector, "", 0)
}

// UpsertConnectorWithAudit 写入 connector 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertConnectorWithAudit(connector pb.Connector, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	connectorID := strings.TrimSpace(connector.ConnectorID)
	// 以 connectorId 作为主键写入，覆盖旧值。
	registry.connectors[connectorID] = connector
	registry.touchAudit("connector", connectorID, eventID, resourceVersion)
}

// UpsertSession 写入或更新 session 对象。
func (registry *CanonicalRegistry) UpsertSession(session pb.Session) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertSessionWithAudit(session, "", 0)
}

// UpsertSessionWithAudit 写入 session 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertSessionWithAudit(session pb.Session, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	sessionID := strings.TrimSpace(session.SessionID)
	connectorID := strings.TrimSpace(session.ConnectorID)
	if old, exists := registry.sessions[sessionID]; exists {
		// session 迁移 connector 时先清理旧索引，避免脏引用。
		oldConnectorID := strings.TrimSpace(old.ConnectorID)
		if oldConnectorID != "" && oldConnectorID != connectorID {
			registry.removeIndex(registry.sessionIDsByConnector, oldConnectorID, sessionID)
		}
	}
	// 以 sessionId 作为主键写入，覆盖旧值。
	registry.sessions[sessionID] = session
	registry.addIndex(registry.sessionIDsByConnector, connectorID, sessionID)
	registry.touchAudit("session", sessionID, eventID, resourceVersion)
}

// UpsertService 写入或更新 service 对象。
func (registry *CanonicalRegistry) UpsertService(service pb.Service) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertServiceWithAudit(service, "", service.ResourceVersion)
}

// UpsertServiceWithAudit 写入 service 并更新 service_key 索引与审计元数据。
func (registry *CanonicalRegistry) UpsertServiceWithAudit(service pb.Service, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	serviceID := strings.TrimSpace(service.ServiceID)
	serviceKey := strings.TrimSpace(service.ServiceKey)
	connectorID := strings.TrimSpace(service.ConnectorID)
	if old, exists := registry.services[serviceID]; exists {
		// service 变更 serviceKey 时清理旧映射，避免 lookup 错指。
		oldKey := strings.TrimSpace(old.ServiceKey)
		if oldKey != "" && oldKey != serviceKey {
			delete(registry.serviceKeyToID, oldKey)
		}
		// service 迁移 connector 时清理旧索引，避免脏引用。
		oldConnectorID := strings.TrimSpace(old.ConnectorID)
		if oldConnectorID != "" && oldConnectorID != connectorID {
			registry.removeIndex(registry.serviceIDsByConnector, oldConnectorID, serviceID)
		}
	}
	// 以 serviceId 作为主键存储，确保 identity 稳定。
	registry.services[serviceID] = service
	if serviceID != "" && serviceKey != "" {
		// 同步维护 lookup key 到 identity 的映射。
		registry.serviceKeyToID[serviceKey] = serviceID
	}
	registry.addIndex(registry.serviceIDsByConnector, connectorID, serviceID)
	registry.touchAudit("service", serviceID, eventID, resourceVersion)
}

// DeleteService 删除 service 对象及其映射关系。
func (registry *CanonicalRegistry) DeleteService(serviceID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	normalizedServiceID := strings.TrimSpace(serviceID)
	service, exists := registry.services[normalizedServiceID]
	if !exists {
		return
	}
	delete(registry.services, normalizedServiceID)
	if key := strings.TrimSpace(service.ServiceKey); key != "" {
		delete(registry.serviceKeyToID, key)
	}
	if connectorID := strings.TrimSpace(service.ConnectorID); connectorID != "" {
		registry.removeIndex(registry.serviceIDsByConnector, connectorID, normalizedServiceID)
	}
	delete(registry.audits, registry.auditKey("service", normalizedServiceID))
}

// UpsertRoute 写入或更新 route 对象。
func (registry *CanonicalRegistry) UpsertRoute(route pb.Route) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertRouteWithAudit(route, "", route.ResourceVersion)
}

// UpsertRouteWithAudit 写入 route 并更新最小索引与审计元数据。
func (registry *CanonicalRegistry) UpsertRouteWithAudit(route pb.Route, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	routeID := strings.TrimSpace(route.RouteID)
	if old, exists := registry.routes[routeID]; exists {
		// route 目标 serviceKey 变更时需要清理旧索引。
		oldServiceKey := routeServiceKey(old)
		if oldServiceKey != "" {
			registry.removeIndex(registry.routeIDsByServiceKey, oldServiceKey, routeID)
		}
	}
	// 以 routeId 作为主键写入，覆盖旧值。
	registry.routes[routeID] = route
	if serviceKey := routeServiceKey(route); serviceKey != "" {
		// route 到 serviceKey 的索引用于快速反查受影响 route。
		registry.addIndex(registry.routeIDsByServiceKey, serviceKey, routeID)
	}
	registry.touchAudit("route", routeID, eventID, resourceVersion)
}

// UpsertProjection 写入或更新 discovery projection 对象。
func (registry *CanonicalRegistry) UpsertProjection(projection pb.DiscoveryProjection) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertProjectionWithAudit(projection, "", 0)
}

// UpsertProjectionWithAudit 写入 projection 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertProjectionWithAudit(projection pb.DiscoveryProjection, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	projectionID := strings.TrimSpace(projection.ProjectionID)
	// 以 projectionId 作为主键写入，覆盖旧值。
	registry.projections[projectionID] = projection
	registry.touchAudit("projection", projectionID, eventID, resourceVersion)
}

// GetServiceByID 按 serviceId 查询 service。
func (registry *CanonicalRegistry) GetServiceByID(serviceID string) (pb.Service, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	service, exists := registry.services[strings.TrimSpace(serviceID)]
	return service, exists
}

// GetServiceByKey 按 serviceKey 查询 service。
func (registry *CanonicalRegistry) GetServiceByKey(serviceKey string) (pb.Service, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	serviceID, exists := registry.serviceKeyToID[strings.TrimSpace(serviceKey)]
	if !exists {
		return pb.Service{}, false
	}
	service, exists := registry.services[serviceID]
	return service, exists
}

// GetServiceIDByKey 按 serviceKey 查询 serviceId。
func (registry *CanonicalRegistry) GetServiceIDByKey(serviceKey string) (string, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	serviceID, exists := registry.serviceKeyToID[strings.TrimSpace(serviceKey)]
	return serviceID, exists
}

// GetConnectorByID 按 connectorId 查询 connector。
func (registry *CanonicalRegistry) GetConnectorByID(connectorID string) (pb.Connector, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	connector, exists := registry.connectors[strings.TrimSpace(connectorID)]
	return connector, exists
}

// GetSessionByID 按 sessionId 查询 session。
func (registry *CanonicalRegistry) GetSessionByID(sessionID string) (pb.Session, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	session, exists := registry.sessions[strings.TrimSpace(sessionID)]
	return session, exists
}

// ListSessionsByConnector 返回 connector 关联的全部 session。
func (registry *CanonicalRegistry) ListSessionsByConnector(connectorID string) []pb.Session {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedConnectorID := strings.TrimSpace(connectorID)
	sessionIDs := registry.sessionIDsByConnector[normalizedConnectorID]
	result := make([]pb.Session, 0, len(sessionIDs))
	for sessionID := range sessionIDs {
		if session, exists := registry.sessions[sessionID]; exists {
			// 只返回当前仍在 registry 中存在的 session。
			result = append(result, session)
		}
	}
	return result
}

// FindActiveSessionByConnector 按 connector 查询 ACTIVE 会话。
func (registry *CanonicalRegistry) FindActiveSessionByConnector(connectorID string) (pb.Session, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedConnectorID := strings.TrimSpace(connectorID)
	for _, session := range registry.sessions {
		// 仅返回同 connector 且状态为 ACTIVE 的会话。
		if session.ConnectorID == normalizedConnectorID && session.State == pb.SessionStateActive {
			return session, true
		}
	}
	return pb.Session{}, false
}

// ListServicesByConnector 返回 connector 关联的全部 service。
func (registry *CanonicalRegistry) ListServicesByConnector(connectorID string) []pb.Service {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedConnectorID := strings.TrimSpace(connectorID)
	serviceIDs := registry.serviceIDsByConnector[normalizedConnectorID]
	result := make([]pb.Service, 0, len(serviceIDs))
	for serviceID := range serviceIDs {
		if service, exists := registry.services[serviceID]; exists {
			// 只返回当前仍在 registry 中存在的 service。
			result = append(result, service)
		}
	}
	return result
}

// ListServicesByScope 返回指定 scope 下的服务集合。
func (registry *CanonicalRegistry) ListServicesByScope(namespace string, environment string) []pb.Service {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedNamespace := strings.TrimSpace(namespace)
	normalizedEnvironment := strings.TrimSpace(environment)
	services := make([]pb.Service, 0)
	for _, service := range registry.services {
		// 仅返回同 scope 的 service，供 route resolve 使用。
		if strings.TrimSpace(service.Namespace) == normalizedNamespace && strings.TrimSpace(service.Environment) == normalizedEnvironment {
			services = append(services, service)
		}
	}
	return services
}

// GetRouteByID 按 routeId 查询 route。
func (registry *CanonicalRegistry) GetRouteByID(routeID string) (pb.Route, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	route, exists := registry.routes[strings.TrimSpace(routeID)]
	return route, exists
}

// ListRoutesByScope 返回指定 scope 下的路由集合。
func (registry *CanonicalRegistry) ListRoutesByScope(namespace string, environment string) []pb.Route {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedNamespace := strings.TrimSpace(namespace)
	normalizedEnvironment := strings.TrimSpace(environment)
	routes := make([]pb.Route, 0)
	for _, route := range registry.routes {
		// 仅返回同 scope route，供 ingress 和 resolver 消费。
		if strings.TrimSpace(route.Namespace) == normalizedNamespace && strings.TrimSpace(route.Environment) == normalizedEnvironment {
			routes = append(routes, route)
		}
	}
	return routes
}

// ListRoutesByServiceKey 返回指向指定 serviceKey 的路由集合。
func (registry *CanonicalRegistry) ListRoutesByServiceKey(serviceKey string) []pb.Route {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	routeIDs := registry.routeIDsByServiceKey[normalizedServiceKey]
	routes := make([]pb.Route, 0, len(routeIDs))
	for routeID := range routeIDs {
		if route, exists := registry.routes[routeID]; exists {
			// 仅返回当前仍在 registry 中存在的 route。
			routes = append(routes, route)
		}
	}
	return routes
}

// GetProjectionByID 按 projectionId 查询 projection。
func (registry *CanonicalRegistry) GetProjectionByID(projectionID string) (pb.DiscoveryProjection, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	projection, exists := registry.projections[strings.TrimSpace(projectionID)]
	return projection, exists
}

// GetAuditInfo 查询资源审计信息。
func (registry *CanonicalRegistry) GetAuditInfo(resourceType string, resourceID string) (AuditInfo, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	audit, exists := registry.audits[registry.auditKey(resourceType, resourceID)]
	return audit, exists
}

// Snapshot 生成 canonical registry 的最小状态快照。
func (registry *CanonicalRegistry) Snapshot() CanonicalSnapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	snapshot := CanonicalSnapshot{
		Connectors:  make([]pb.Connector, 0, len(registry.connectors)),
		Sessions:    make([]pb.Session, 0, len(registry.sessions)),
		Services:    make([]pb.Service, 0, len(registry.services)),
		Routes:      make([]pb.Route, 0, len(registry.routes)),
		Projections: make([]pb.DiscoveryProjection, 0, len(registry.projections)),
		GeneratedAt: time.Now().UTC(),
	}
	for _, connector := range registry.connectors {
		snapshot.Connectors = append(snapshot.Connectors, connector)
	}
	for _, session := range registry.sessions {
		snapshot.Sessions = append(snapshot.Sessions, session)
	}
	for _, service := range registry.services {
		snapshot.Services = append(snapshot.Services, service)
	}
	for _, route := range registry.routes {
		snapshot.Routes = append(snapshot.Routes, route)
	}
	for _, projection := range registry.projections {
		snapshot.Projections = append(snapshot.Projections, projection)
	}
	return snapshot
}

// addIndex 为二级索引插入一条关系。
func (registry *CanonicalRegistry) addIndex(index map[string]map[string]struct{}, key string, value string) {
	normalizedKey := strings.TrimSpace(key)
	normalizedValue := strings.TrimSpace(value)
	if normalizedKey == "" || normalizedValue == "" {
		return
	}
	values, exists := index[normalizedKey]
	if !exists {
		// 索引桶不存在时先创建，避免 map nil 写入 panic。
		values = make(map[string]struct{})
		index[normalizedKey] = values
	}
	values[normalizedValue] = struct{}{}
}

// removeIndex 从二级索引删除关系，value 为空时删除整桶。
func (registry *CanonicalRegistry) removeIndex(index map[string]map[string]struct{}, key string, value string) {
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return
	}
	values, exists := index[normalizedKey]
	if !exists {
		return
	}
	normalizedValue := strings.TrimSpace(value)
	if normalizedValue == "" {
		// value 为空表示清理整个索引桶。
		delete(index, normalizedKey)
		return
	}
	delete(values, normalizedValue)
	if len(values) == 0 {
		delete(index, normalizedKey)
	}
}

// touchAudit 更新资源审计元数据。
func (registry *CanonicalRegistry) touchAudit(resourceType string, resourceID string, eventID string, resourceVersion uint64) {
	key := registry.auditKey(resourceType, resourceID)
	now := time.Now().UTC()
	current, exists := registry.audits[key]
	if !exists {
		// 首次写入时同时记录创建与更新时间。
		current.CreatedAt = now
	}
	current.UpdatedAt = now
	current.LastEventID = strings.TrimSpace(eventID)
	current.LastResourceVersion = resourceVersion
	registry.audits[key] = current
}

// auditKey 构造审计存储键。
func (registry *CanonicalRegistry) auditKey(resourceType string, resourceID string) string {
	return fmt.Sprintf("%s|%s", strings.TrimSpace(resourceType), strings.TrimSpace(resourceID))
}

// routeServiceKey 提取 route 目标对应的 connector serviceKey。
func routeServiceKey(route pb.Route) string {
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		if route.Target.ConnectorService == nil {
			return ""
		}
		return strings.TrimSpace(route.Target.ConnectorService.ServiceKey)
	case pb.RouteTargetTypeHybridGroup:
		if route.Target.HybridGroup == nil {
			return ""
		}
		return strings.TrimSpace(route.Target.HybridGroup.PrimaryConnectorService.ServiceKey)
	default:
		return ""
	}
}
