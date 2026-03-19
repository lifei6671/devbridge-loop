package registry

import (
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// LogicalServiceSnapshot 描述逻辑服务快照。
type LogicalServiceSnapshot struct {
	LogicalService pb.LogicalService
	UpdatedAt      time.Time
}

// ServiceInstanceSnapshot 描述逻辑服务下的单实例快照。
type ServiceInstanceSnapshot struct {
	LogicalService pb.LogicalService
	Instance       pb.ServiceInstance
	UpdatedAt      time.Time
}

// ServiceRegistry 存储逻辑服务与实例状态。
type ServiceRegistry struct {
	mu sync.RWMutex

	logicalServices             map[string]*LogicalServiceSnapshot
	instances                   map[string]*ServiceInstanceSnapshot
	logicalServiceIDByScopeName map[string]string
	instanceIDsByLogicalService map[string]map[string]struct{}
	instanceIDsByConnector      map[string]map[string]struct{}
	instanceIDByRuntime         map[string]string
}

// NewServiceRegistry 创建服务注册表。
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		logicalServices:             make(map[string]*LogicalServiceSnapshot),
		instances:                   make(map[string]*ServiceInstanceSnapshot),
		logicalServiceIDByScopeName: make(map[string]string),
		instanceIDsByLogicalService: make(map[string]map[string]struct{}),
		instanceIDsByConnector:      make(map[string]map[string]struct{}),
		instanceIDByRuntime:         make(map[string]string),
	}
}

// Upsert 写入或更新逻辑服务与实例快照。
func (registry *ServiceRegistry) Upsert(now time.Time, logicalService pb.LogicalService, instance pb.ServiceInstance) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.upsertLocked(now, logicalService, instance)
}

// RemoveLogicalService 删除逻辑服务及其全部实例。
func (registry *ServiceRegistry) RemoveLogicalService(logicalServiceID string) bool {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.removeLogicalServiceLocked(normalizedLogicalServiceID)
}

// RemoveInstanceByID 删除指定实例。
func (registry *ServiceRegistry) RemoveInstanceByID(instanceID string) bool {
	normalizedInstanceID := strings.TrimSpace(instanceID)
	if normalizedInstanceID == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.removeInstanceLocked(normalizedInstanceID)
}

// RemoveInstanceByLogicalServiceAndRuntime 按逻辑服务与 runtime 删除实例。
func (registry *ServiceRegistry) RemoveInstanceByLogicalServiceAndRuntime(
	logicalServiceID string,
	connectorID string,
	sessionID string,
) bool {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return false
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	removed := false
	for _, instance := range registry.instancesByLogicalServiceLocked(normalizedLogicalServiceID) {
		if normalizedConnectorID != "" && strings.TrimSpace(instance.Instance.ConnectorID) != normalizedConnectorID {
			continue
		}
		if normalizedSessionID != "" && strings.TrimSpace(instance.Instance.SessionID) != normalizedSessionID {
			continue
		}
		if registry.removeInstanceLocked(instance.Instance.InstanceID) {
			removed = true
		}
	}
	return removed
}

// GetLogicalServiceByID 读取指定 logicalServiceID 的快照。
func (registry *ServiceRegistry) GetLogicalServiceByID(logicalServiceID string) (pb.LogicalService, bool) {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return pb.LogicalService{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := registry.logicalServices[normalizedLogicalServiceID]
	if !exists || record == nil {
		return pb.LogicalService{}, false
	}
	return record.LogicalService, true
}

// FindLogicalServiceByNameScope 按 serviceName + scope 查询逻辑服务。
func (registry *ServiceRegistry) FindLogicalServiceByNameScope(serviceName string, scope pb.Scope) (pb.LogicalService, bool) {
	normalizedKey := buildLogicalServiceScopeNameKey(serviceName, scope)
	if normalizedKey == "" {
		return pb.LogicalService{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	logicalServiceID, exists := registry.logicalServiceIDByScopeName[normalizedKey]
	if !exists {
		return pb.LogicalService{}, false
	}
	record, exists := registry.logicalServices[logicalServiceID]
	if !exists || record == nil {
		return pb.LogicalService{}, false
	}
	return record.LogicalService, true
}

// GetInstanceByID 读取指定 instanceID 的快照。
func (registry *ServiceRegistry) GetInstanceByID(instanceID string) (ServiceInstanceSnapshot, bool) {
	normalizedInstanceID := strings.TrimSpace(instanceID)
	if normalizedInstanceID == "" {
		return ServiceInstanceSnapshot{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := registry.instances[normalizedInstanceID]
	if !exists || record == nil {
		return ServiceInstanceSnapshot{}, false
	}
	return cloneServiceInstanceSnapshot(*record), true
}

// FindInstanceByConnectorLogicalService 查询同 connector + logicalService 的实例。
func (registry *ServiceRegistry) FindInstanceByConnectorLogicalService(connectorID string, logicalServiceID string) (ServiceInstanceSnapshot, bool) {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedConnectorID == "" || normalizedLogicalServiceID == "" {
		return ServiceInstanceSnapshot{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	for _, record := range registry.instancesByLogicalServiceLocked(normalizedLogicalServiceID) {
		if strings.TrimSpace(record.Instance.ConnectorID) == normalizedConnectorID {
			return cloneServiceInstanceSnapshot(*record), true
		}
	}
	return ServiceInstanceSnapshot{}, false
}

// ListInstancesByLogicalServiceID 返回指定 logicalServiceID 的所有实例快照。
func (registry *ServiceRegistry) ListInstancesByLogicalServiceID(logicalServiceID string) []ServiceInstanceSnapshot {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	instances := registry.instancesByLogicalServiceLocked(normalizedLogicalServiceID)
	return cloneServiceInstances(instances)
}

// ListInstances 返回当前全部实例快照。
func (registry *ServiceRegistry) ListInstances() []ServiceInstanceSnapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	instances := make([]*ServiceInstanceSnapshot, 0, len(registry.instances))
	for _, record := range registry.instances {
		if record == nil {
			continue
		}
		instances = append(instances, record)
	}
	return cloneServiceInstances(instances)
}

// ListLogicalServiceIDsByRuntime 返回指定 connector/session 命中的 logicalServiceID 列表。
func (registry *ServiceRegistry) ListLogicalServiceIDsByRuntime(connectorID string, sessionID string) []string {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return nil
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	logicalServiceIDs := make([]string, 0, len(registry.logicalServices))
	for logicalServiceID, record := range registry.logicalServices {
		if record == nil {
			continue
		}
		matched := false
		for _, instance := range registry.instancesByLogicalServiceLocked(logicalServiceID) {
			if strings.TrimSpace(instance.Instance.ConnectorID) != normalizedConnectorID {
				continue
			}
			if normalizedSessionID != "" && strings.TrimSpace(instance.Instance.SessionID) != normalizedSessionID {
				continue
			}
			matched = true
			break
		}
		if matched {
			logicalServiceIDs = append(logicalServiceIDs, logicalServiceID)
		}
	}
	return logicalServiceIDs
}

// CurrentVersion 返回逻辑服务当前资源版本。
func (registry *ServiceRegistry) CurrentVersion(logicalServiceID string, instanceID string) uint64 {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	normalizedInstanceID := strings.TrimSpace(instanceID)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if normalizedInstanceID != "" {
		if record, exists := registry.instances[normalizedInstanceID]; exists && record != nil {
			return record.Instance.ResourceVersion
		}
	}
	if normalizedLogicalServiceID != "" {
		if record, exists := registry.logicalServices[normalizedLogicalServiceID]; exists && record != nil {
			return record.LogicalService.ResourceVersion
		}
	}
	return 0
}

// ReplaceAll 用 full-sync 快照覆盖全部服务视图。
func (registry *ServiceRegistry) ReplaceAll(now time.Time, logicalServices []pb.LogicalService, instances []pb.ServiceInstance) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.logicalServices = make(map[string]*LogicalServiceSnapshot, len(logicalServices))
	registry.instances = make(map[string]*ServiceInstanceSnapshot, len(instances))
	registry.logicalServiceIDByScopeName = make(map[string]string, len(logicalServices))
	registry.instanceIDsByLogicalService = make(map[string]map[string]struct{}, len(logicalServices))
	registry.instanceIDsByConnector = make(map[string]map[string]struct{}, len(instances))
	registry.instanceIDByRuntime = make(map[string]string, len(instances))
	normalizedNow := normalizeRegistryTime(now)
	for _, logicalService := range logicalServices {
		registry.logicalServices[strings.TrimSpace(logicalService.LogicalServiceID)] = &LogicalServiceSnapshot{
			LogicalService: logicalService,
			UpdatedAt:      normalizedNow,
		}
		registry.logicalServiceIDByScopeName[buildLogicalServiceScopeNameKey(logicalService.ServiceName, logicalService.Scope)] =
			strings.TrimSpace(logicalService.LogicalServiceID)
	}
	for _, instance := range instances {
		registry.upsertInstanceLocked(normalizedNow, instance)
	}
	registry.recalculateAllLogicalServicesLocked()
}

// List 返回当前所有逻辑服务快照。
func (registry *ServiceRegistry) List() []pb.LogicalService {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]pb.LogicalService, 0, len(registry.logicalServices))
	for _, record := range registry.logicalServices {
		if record == nil {
			continue
		}
		result = append(result, record.LogicalService)
	}
	return result
}

// MarkLifecycleByConnector 按 connector 批量更新实例生命周期状态。
func (registry *ServiceRegistry) MarkLifecycleByConnector(
	now time.Time,
	connectorID string,
	status pb.ServiceStatus,
	healthStatus pb.HealthStatus,
) int {
	return registry.MarkLifecycleByConnectorAndSession(now, connectorID, "", status, healthStatus)
}

// MarkLifecycleByConnectorAndSession 按 connector+session 更新实例生命周期状态。
func (registry *ServiceRegistry) MarkLifecycleByConnectorAndSession(
	now time.Time,
	connectorID string,
	sessionID string,
	status pb.ServiceStatus,
	healthStatus pb.HealthStatus,
) int {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return 0
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	normalizedNow := normalizeRegistryTime(now)
	registry.mu.Lock()
	defer registry.mu.Unlock()
	updatedCount := 0
	affectedLogicalServiceIDs := make(map[string]struct{})
	for instanceID, record := range registry.instances {
		if record == nil {
			continue
		}
		if strings.TrimSpace(record.Instance.ConnectorID) != normalizedConnectorID {
			continue
		}
		if normalizedSessionID != "" && strings.TrimSpace(record.Instance.SessionID) != normalizedSessionID {
			continue
		}
		record.Instance.InstanceStatus = status
		record.Instance.HealthStatus = healthStatus
		record.UpdatedAt = normalizedNow
		registry.instances[instanceID] = record
		affectedLogicalServiceIDs[strings.TrimSpace(record.Instance.LogicalServiceID)] = struct{}{}
		updatedCount++
	}
	for logicalServiceID := range affectedLogicalServiceIDs {
		registry.recalculateLogicalServiceLocked(logicalServiceID)
	}
	return updatedCount
}

// upsertLocked 在持锁上下文写入逻辑服务与实例快照。
func (registry *ServiceRegistry) upsertLocked(now time.Time, logicalService pb.LogicalService, instance pb.ServiceInstance) {
	normalizedNow := normalizeRegistryTime(now)
	normalizedLogicalServiceID := strings.TrimSpace(logicalService.LogicalServiceID)
	registry.logicalServices[normalizedLogicalServiceID] = &LogicalServiceSnapshot{
		LogicalService: logicalService,
		UpdatedAt:      normalizedNow,
	}
	registry.logicalServiceIDByScopeName[buildLogicalServiceScopeNameKey(logicalService.ServiceName, logicalService.Scope)] =
		normalizedLogicalServiceID
	registry.upsertInstanceLocked(normalizedNow, instance)
	registry.recalculateLogicalServiceLocked(normalizedLogicalServiceID)
}

// upsertInstanceLocked 在持锁上下文写入实例快照。
func (registry *ServiceRegistry) upsertInstanceLocked(now time.Time, instance pb.ServiceInstance) {
	normalizedInstanceID := strings.TrimSpace(instance.InstanceID)
	if normalizedInstanceID == "" {
		return
	}
	normalizedLogicalServiceID := strings.TrimSpace(instance.LogicalServiceID)
	logicalService := pb.LogicalService{}
	if record, exists := registry.logicalServices[normalizedLogicalServiceID]; exists && record != nil {
		logicalService = record.LogicalService
	}
	if previous, exists := registry.instances[normalizedInstanceID]; exists && previous != nil {
		registry.removeIndex(registry.instanceIDsByConnector, previous.Instance.ConnectorID, normalizedInstanceID)
		registry.removeIndex(registry.instanceIDsByLogicalService, previous.Instance.LogicalServiceID, normalizedInstanceID)
		delete(registry.instanceIDByRuntime, buildRuntimeInstanceKey(previous.Instance.ConnectorID, previous.Instance.SessionID, previous.Instance.LogicalServiceID))
	}
	registry.instances[normalizedInstanceID] = &ServiceInstanceSnapshot{
		LogicalService: logicalService,
		Instance:       instance,
		UpdatedAt:      now,
	}
	registry.addIndex(registry.instanceIDsByConnector, instance.ConnectorID, normalizedInstanceID)
	registry.addIndex(registry.instanceIDsByLogicalService, instance.LogicalServiceID, normalizedInstanceID)
	registry.instanceIDByRuntime[buildRuntimeInstanceKey(instance.ConnectorID, instance.SessionID, instance.LogicalServiceID)] = normalizedInstanceID
}

// removeLogicalServiceLocked 删除逻辑服务及其全部实例。
func (registry *ServiceRegistry) removeLogicalServiceLocked(logicalServiceID string) bool {
	record, exists := registry.logicalServices[logicalServiceID]
	if !exists || record == nil {
		return false
	}
	delete(registry.logicalServices, logicalServiceID)
	delete(registry.logicalServiceIDByScopeName, buildLogicalServiceScopeNameKey(record.LogicalService.ServiceName, record.LogicalService.Scope))
	for _, instance := range registry.instancesByLogicalServiceLocked(logicalServiceID) {
		registry.removeInstanceLocked(instance.Instance.InstanceID)
	}
	return true
}

// removeInstanceLocked 删除实例，并在最后一个实例被删除后收敛逻辑服务状态。
func (registry *ServiceRegistry) removeInstanceLocked(instanceID string) bool {
	record, exists := registry.instances[instanceID]
	if !exists || record == nil {
		return false
	}
	logicalServiceID := strings.TrimSpace(record.Instance.LogicalServiceID)
	delete(registry.instances, instanceID)
	registry.removeIndex(registry.instanceIDsByConnector, record.Instance.ConnectorID, instanceID)
	registry.removeIndex(registry.instanceIDsByLogicalService, logicalServiceID, instanceID)
	delete(registry.instanceIDByRuntime, buildRuntimeInstanceKey(record.Instance.ConnectorID, record.Instance.SessionID, logicalServiceID))
	registry.recalculateLogicalServiceLocked(logicalServiceID)
	return true
}

// instancesByLogicalServiceLocked 返回逻辑服务下的实例记录。
func (registry *ServiceRegistry) instancesByLogicalServiceLocked(logicalServiceID string) []*ServiceInstanceSnapshot {
	instanceIDs := registry.instanceIDsByLogicalService[strings.TrimSpace(logicalServiceID)]
	result := make([]*ServiceInstanceSnapshot, 0, len(instanceIDs))
	for instanceID := range instanceIDs {
		if record, exists := registry.instances[instanceID]; exists && record != nil {
			result = append(result, record)
		}
	}
	return result
}

// recalculateAllLogicalServicesLocked 重算全部逻辑服务聚合状态。
func (registry *ServiceRegistry) recalculateAllLogicalServicesLocked() {
	for logicalServiceID := range registry.logicalServices {
		registry.recalculateLogicalServiceLocked(logicalServiceID)
	}
}

// recalculateLogicalServiceLocked 根据实例状态重算逻辑服务状态。
func (registry *ServiceRegistry) recalculateLogicalServiceLocked(logicalServiceID string) {
	record, exists := registry.logicalServices[strings.TrimSpace(logicalServiceID)]
	if !exists || record == nil {
		return
	}
	activeInstanceCount := int32(0)
	healthyInstanceCount := int32(0)
	for _, instance := range registry.instancesByLogicalServiceLocked(logicalServiceID) {
		instance.LogicalService = record.LogicalService
		if instance.Instance.InstanceStatus == pb.ServiceStatusActive {
			activeInstanceCount++
		}
		if instance.Instance.InstanceStatus == pb.ServiceStatusActive && instance.Instance.HealthStatus == pb.HealthStatusHealthy {
			healthyInstanceCount++
		}
	}
	record.LogicalService.ActiveInstanceCount = activeInstanceCount
	record.LogicalService.HealthyInstanceCount = healthyInstanceCount
	if activeInstanceCount == 0 {
		record.LogicalService.Status = pb.ServiceStatusInactive
	} else {
		record.LogicalService.Status = pb.ServiceStatusActive
	}
	registry.logicalServices[strings.TrimSpace(logicalServiceID)] = record
}

// addIndex 为二级索引插入一条关系。
func (registry *ServiceRegistry) addIndex(index map[string]map[string]struct{}, key string, value string) {
	normalizedKey := strings.TrimSpace(key)
	normalizedValue := strings.TrimSpace(value)
	if normalizedKey == "" || normalizedValue == "" {
		return
	}
	if _, exists := index[normalizedKey]; !exists {
		index[normalizedKey] = make(map[string]struct{})
	}
	index[normalizedKey][normalizedValue] = struct{}{}
}

// removeIndex 从二级索引删除关系。
func (registry *ServiceRegistry) removeIndex(index map[string]map[string]struct{}, key string, value string) {
	normalizedKey := strings.TrimSpace(key)
	normalizedValue := strings.TrimSpace(value)
	if normalizedKey == "" || normalizedValue == "" {
		return
	}
	values, exists := index[normalizedKey]
	if !exists {
		return
	}
	delete(values, normalizedValue)
	if len(values) == 0 {
		delete(index, normalizedKey)
	}
}

// buildLogicalServiceScopeNameKey 构造逻辑服务唯一索引键。
func buildLogicalServiceScopeNameKey(serviceName string, scope pb.Scope) string {
	normalizedServiceName := strings.TrimSpace(serviceName)
	normalizedNamespace := strings.TrimSpace(scope.Namespace)
	normalizedEnvironment := strings.TrimSpace(scope.Environment)
	if normalizedServiceName == "" || normalizedNamespace == "" || normalizedEnvironment == "" {
		return ""
	}
	return normalizedNamespace + "|" + normalizedEnvironment + "|" + normalizedServiceName
}

// buildRuntimeInstanceKey 构造 connector/session/logicalService 维度实例索引键。
func buildRuntimeInstanceKey(connectorID string, sessionID string, logicalServiceID string) string {
	return strings.TrimSpace(connectorID) + "|" + strings.TrimSpace(sessionID) + "|" + strings.TrimSpace(logicalServiceID)
}

// cloneServiceInstances 复制实例切片，避免调用方持有内部可变引用。
func cloneServiceInstances(instances []*ServiceInstanceSnapshot) []ServiceInstanceSnapshot {
	if len(instances) == 0 {
		return nil
	}
	result := make([]ServiceInstanceSnapshot, 0, len(instances))
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		result = append(result, cloneServiceInstanceSnapshot(*instance))
	}
	return result
}

// cloneServiceInstanceSnapshot 深拷贝实例快照。
func cloneServiceInstanceSnapshot(snapshot ServiceInstanceSnapshot) ServiceInstanceSnapshot {
	return ServiceInstanceSnapshot{
		LogicalService: snapshot.LogicalService,
		Instance:       snapshot.Instance,
		UpdatedAt:      snapshot.UpdatedAt,
	}
}

// normalizeRegistryTime 归一化服务注册表更新时间。
func normalizeRegistryTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
