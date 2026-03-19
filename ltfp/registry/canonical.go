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
	Connectors       []pb.Connector
	Sessions         []pb.Session
	LogicalServices  []pb.LogicalService
	ServiceInstances []pb.ServiceInstance
	Routes           []pb.Route
	Projections      []pb.DiscoveryProjection
	GeneratedAt      time.Time
}

// CanonicalRegistry 维护低频一致性配置对象。
type CanonicalRegistry struct {
	mu sync.RWMutex

	connectors       map[string]pb.Connector
	sessions         map[string]pb.Session
	logicalServices  map[string]pb.LogicalService
	serviceInstances map[string]pb.ServiceInstance
	routes           map[string]pb.Route
	projections      map[string]pb.DiscoveryProjection

	logicalServiceIDByScopeName map[string]string
	sessionIDsByConnector       map[string]map[string]struct{}
	instanceIDsByConnector      map[string]map[string]struct{}
	instanceIDsByLogicalService map[string]map[string]struct{}
	routeIDsByLogicalService    map[string]map[string]struct{}

	audits map[string]AuditInfo
}

// NewCanonicalRegistry 创建 canonical registry 实例。
func NewCanonicalRegistry() *CanonicalRegistry {
	return &CanonicalRegistry{
		connectors:                  make(map[string]pb.Connector),
		sessions:                    make(map[string]pb.Session),
		logicalServices:             make(map[string]pb.LogicalService),
		serviceInstances:            make(map[string]pb.ServiceInstance),
		routes:                      make(map[string]pb.Route),
		projections:                 make(map[string]pb.DiscoveryProjection),
		logicalServiceIDByScopeName: make(map[string]string),
		sessionIDsByConnector:       make(map[string]map[string]struct{}),
		instanceIDsByConnector:      make(map[string]map[string]struct{}),
		instanceIDsByLogicalService: make(map[string]map[string]struct{}),
		routeIDsByLogicalService:    make(map[string]map[string]struct{}),
		audits:                      make(map[string]AuditInfo),
	}
}

// UpsertConnector 写入或更新 connector 对象。
func (registry *CanonicalRegistry) UpsertConnector(connector pb.Connector) {
	registry.UpsertConnectorWithAudit(connector, "", 0)
}

// UpsertConnectorWithAudit 写入 connector 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertConnectorWithAudit(connector pb.Connector, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	connectorID := strings.TrimSpace(connector.ConnectorID)
	registry.connectors[connectorID] = connector
	registry.touchAudit("connector", connectorID, eventID, resourceVersion)
}

// UpsertSession 写入或更新 session 对象。
func (registry *CanonicalRegistry) UpsertSession(session pb.Session) {
	registry.UpsertSessionWithAudit(session, "", 0)
}

// UpsertSessionWithAudit 写入 session 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertSessionWithAudit(session pb.Session, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	sessionID := strings.TrimSpace(session.SessionID)
	connectorID := strings.TrimSpace(session.ConnectorID)
	if old, exists := registry.sessions[sessionID]; exists {
		oldConnectorID := strings.TrimSpace(old.ConnectorID)
		if oldConnectorID != "" && oldConnectorID != connectorID {
			registry.removeIndex(registry.sessionIDsByConnector, oldConnectorID, sessionID)
		}
	}
	registry.sessions[sessionID] = session
	registry.addIndex(registry.sessionIDsByConnector, connectorID, sessionID)
	registry.touchAudit("session", sessionID, eventID, resourceVersion)
}

// UpsertLogicalService 写入或更新 logical service 对象。
func (registry *CanonicalRegistry) UpsertLogicalService(service pb.LogicalService) {
	registry.UpsertLogicalServiceWithAudit(service, "", service.ResourceVersion)
}

// UpsertLogicalServiceWithAudit 写入 logical service 并更新索引与审计元数据。
func (registry *CanonicalRegistry) UpsertLogicalServiceWithAudit(service pb.LogicalService, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	logicalServiceID := strings.TrimSpace(service.LogicalServiceID)
	scopeNameKey := logicalServiceScopeNameKey(service.ServiceName, service.Scope)
	if old, exists := registry.logicalServices[logicalServiceID]; exists {
		oldKey := logicalServiceScopeNameKey(old.ServiceName, old.Scope)
		if oldKey != "" && oldKey != scopeNameKey {
			delete(registry.logicalServiceIDByScopeName, oldKey)
		}
	}
	registry.logicalServices[logicalServiceID] = service
	if logicalServiceID != "" && scopeNameKey != "" {
		registry.logicalServiceIDByScopeName[scopeNameKey] = logicalServiceID
	}
	registry.touchAudit("logical_service", logicalServiceID, eventID, resourceVersion)
}

// UpsertServiceInstance 写入或更新 service instance 对象。
func (registry *CanonicalRegistry) UpsertServiceInstance(instance pb.ServiceInstance) {
	registry.UpsertServiceInstanceWithAudit(instance, "", instance.ResourceVersion)
}

// UpsertServiceInstanceWithAudit 写入 service instance 并更新索引与审计元数据。
func (registry *CanonicalRegistry) UpsertServiceInstanceWithAudit(instance pb.ServiceInstance, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	instanceID := strings.TrimSpace(instance.InstanceID)
	connectorID := strings.TrimSpace(instance.ConnectorID)
	logicalServiceID := strings.TrimSpace(instance.LogicalServiceID)
	if old, exists := registry.serviceInstances[instanceID]; exists {
		oldConnectorID := strings.TrimSpace(old.ConnectorID)
		if oldConnectorID != "" && oldConnectorID != connectorID {
			registry.removeIndex(registry.instanceIDsByConnector, oldConnectorID, instanceID)
		}
		oldLogicalServiceID := strings.TrimSpace(old.LogicalServiceID)
		if oldLogicalServiceID != "" && oldLogicalServiceID != logicalServiceID {
			registry.removeIndex(registry.instanceIDsByLogicalService, oldLogicalServiceID, instanceID)
		}
	}
	registry.serviceInstances[instanceID] = instance
	registry.addIndex(registry.instanceIDsByConnector, connectorID, instanceID)
	registry.addIndex(registry.instanceIDsByLogicalService, logicalServiceID, instanceID)
	registry.touchAudit("service_instance", instanceID, eventID, resourceVersion)
}

// UpsertRoute 写入或更新 route 对象。
func (registry *CanonicalRegistry) UpsertRoute(route pb.Route) {
	registry.UpsertRouteWithAudit(route, "", route.ResourceVersion)
}

// UpsertRouteWithAudit 写入 route 并更新最小索引与审计元数据。
func (registry *CanonicalRegistry) UpsertRouteWithAudit(route pb.Route, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	routeID := strings.TrimSpace(route.RouteID)
	if old, exists := registry.routes[routeID]; exists {
		oldLogicalServiceID := routeLogicalServiceID(old)
		if oldLogicalServiceID != "" {
			registry.removeIndex(registry.routeIDsByLogicalService, oldLogicalServiceID, routeID)
		}
	}
	registry.routes[routeID] = route
	if logicalServiceID := routeLogicalServiceID(route); logicalServiceID != "" {
		registry.addIndex(registry.routeIDsByLogicalService, logicalServiceID, routeID)
	}
	registry.touchAudit("route", routeID, eventID, resourceVersion)
}

// UpsertProjection 写入或更新 discovery projection 对象。
func (registry *CanonicalRegistry) UpsertProjection(projection pb.DiscoveryProjection) {
	registry.UpsertProjectionWithAudit(projection, "", 0)
}

// UpsertProjectionWithAudit 写入 projection 并更新审计元数据。
func (registry *CanonicalRegistry) UpsertProjectionWithAudit(projection pb.DiscoveryProjection, eventID string, resourceVersion uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	projectionID := strings.TrimSpace(projection.ProjectionID)
	registry.projections[projectionID] = projection
	registry.touchAudit("projection", projectionID, eventID, resourceVersion)
}

// GetLogicalServiceByID 按 logicalServiceId 查询 logical service。
func (registry *CanonicalRegistry) GetLogicalServiceByID(logicalServiceID string) (pb.LogicalService, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	service, exists := registry.logicalServices[strings.TrimSpace(logicalServiceID)]
	return service, exists
}

// FindLogicalServiceByNameScope 按 serviceName + scope 查询 logical service。
func (registry *CanonicalRegistry) FindLogicalServiceByNameScope(serviceName string, scope pb.Scope) (pb.LogicalService, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	logicalServiceID, exists := registry.logicalServiceIDByScopeName[logicalServiceScopeNameKey(serviceName, scope)]
	if !exists {
		return pb.LogicalService{}, false
	}
	service, exists := registry.logicalServices[logicalServiceID]
	return service, exists
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
		if session.ConnectorID == normalizedConnectorID && session.State == pb.SessionStateActive {
			return session, true
		}
	}
	return pb.Session{}, false
}

// ListServiceInstancesByConnector 返回 connector 关联的全部 service instance。
func (registry *CanonicalRegistry) ListServiceInstancesByConnector(connectorID string) []pb.ServiceInstance {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedConnectorID := strings.TrimSpace(connectorID)
	instanceIDs := registry.instanceIDsByConnector[normalizedConnectorID]
	result := make([]pb.ServiceInstance, 0, len(instanceIDs))
	for instanceID := range instanceIDs {
		if instance, exists := registry.serviceInstances[instanceID]; exists {
			result = append(result, instance)
		}
	}
	return result
}

// ListServiceInstancesByLogicalService 返回 logical service 关联的全部 service instance。
func (registry *CanonicalRegistry) ListServiceInstancesByLogicalService(logicalServiceID string) []pb.ServiceInstance {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	instanceIDs := registry.instanceIDsByLogicalService[normalizedLogicalServiceID]
	result := make([]pb.ServiceInstance, 0, len(instanceIDs))
	for instanceID := range instanceIDs {
		if instance, exists := registry.serviceInstances[instanceID]; exists {
			result = append(result, instance)
		}
	}
	return result
}

// ListLogicalServicesByScope 返回指定 scope 下的逻辑服务集合。
func (registry *CanonicalRegistry) ListLogicalServicesByScope(scope pb.Scope) []pb.LogicalService {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]pb.LogicalService, 0)
	for _, service := range registry.logicalServices {
		if sameScope(service.Scope, scope) {
			result = append(result, service)
		}
	}
	return result
}

// GetRouteByID 按 routeId 查询 route。
func (registry *CanonicalRegistry) GetRouteByID(routeID string) (pb.Route, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	route, exists := registry.routes[strings.TrimSpace(routeID)]
	return route, exists
}

// ListRoutesByScope 返回指定 scope 下的路由集合。
func (registry *CanonicalRegistry) ListRoutesByScope(scope pb.Scope) []pb.Route {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	routes := make([]pb.Route, 0)
	for _, route := range registry.routes {
		if sameScope(route.Scope, scope) {
			routes = append(routes, route)
		}
	}
	return routes
}

// ListRoutesByLogicalService 返回指向指定 logical service 的路由集合。
func (registry *CanonicalRegistry) ListRoutesByLogicalService(logicalServiceID string) []pb.Route {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	routeIDs := registry.routeIDsByLogicalService[normalizedLogicalServiceID]
	routes := make([]pb.Route, 0, len(routeIDs))
	for routeID := range routeIDs {
		if route, exists := registry.routes[routeID]; exists {
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
		Connectors:       make([]pb.Connector, 0, len(registry.connectors)),
		Sessions:         make([]pb.Session, 0, len(registry.sessions)),
		LogicalServices:  make([]pb.LogicalService, 0, len(registry.logicalServices)),
		ServiceInstances: make([]pb.ServiceInstance, 0, len(registry.serviceInstances)),
		Routes:           make([]pb.Route, 0, len(registry.routes)),
		Projections:      make([]pb.DiscoveryProjection, 0, len(registry.projections)),
		GeneratedAt:      time.Now().UTC(),
	}
	for _, connector := range registry.connectors {
		snapshot.Connectors = append(snapshot.Connectors, connector)
	}
	for _, session := range registry.sessions {
		snapshot.Sessions = append(snapshot.Sessions, session)
	}
	for _, service := range registry.logicalServices {
		snapshot.LogicalServices = append(snapshot.LogicalServices, service)
	}
	for _, instance := range registry.serviceInstances {
		snapshot.ServiceInstances = append(snapshot.ServiceInstances, instance)
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

// logicalServiceScopeNameKey 构造逻辑服务唯一索引键。
func logicalServiceScopeNameKey(serviceName string, scope pb.Scope) string {
	return strings.Join([]string{
		strings.TrimSpace(scope.Namespace),
		strings.TrimSpace(scope.Environment),
		strings.TrimSpace(serviceName),
	}, "|")
}

// routeLogicalServiceID 提取 route 目标对应的 logical service ID。
func routeLogicalServiceID(route pb.Route) string {
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		if route.Target.ConnectorService == nil {
			return ""
		}
		return strings.TrimSpace(route.Target.ConnectorService.Selector.LogicalServiceID)
	default:
		return ""
	}
}

// sameScope 判断两个 scope 是否完全一致。
func sameScope(left pb.Scope, right pb.Scope) bool {
	return strings.TrimSpace(left.Namespace) == strings.TrimSpace(right.Namespace) &&
		strings.TrimSpace(left.Environment) == strings.TrimSpace(right.Environment)
}
