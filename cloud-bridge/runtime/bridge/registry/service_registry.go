package registry

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// ServicePoolSnapshot 描述逻辑服务池快照。
type ServicePoolSnapshot struct {
	Service   pb.Service
	UpdatedAt time.Time
}

// ServiceInstanceSnapshot 描述服务池内单实例快照（内部模型）。
type ServiceInstanceSnapshot struct {
	Service           pb.Service
	ServiceInstanceID string
	SessionID         string
	UpdatedAt         time.Time
}

// ServiceRegistry 存储逻辑服务池与实例状态。
type ServiceRegistry struct {
	mu                 sync.RWMutex
	byServiceID        map[string]map[string]*ServiceInstanceSnapshot
	byServiceKey       map[string]string
	instanceIdentityID map[string]string
}

// NewServiceRegistry 创建服务注册表。
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		byServiceID:        make(map[string]map[string]*ServiceInstanceSnapshot),
		byServiceKey:       make(map[string]string),
		instanceIdentityID: make(map[string]string),
	}
}

// Upsert 写入或更新服务快照。
func (registry *ServiceRegistry) Upsert(now time.Time, service pb.Service) {
	// 兼容旧调用路径：未提供 session_id 时按空会话写入实例。
	_ = registry.UpsertWithRuntime(now, service, "")
}

// UpsertWithRuntime 写入或更新服务实例快照，并返回内部 service_instance_id。
func (registry *ServiceRegistry) UpsertWithRuntime(now time.Time, service pb.Service, sessionID string) string {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.upsertLocked(now, service, sessionID)
}

// RemoveByServiceID 按 serviceId 删除服务。
func (registry *ServiceRegistry) RemoveByServiceID(serviceID string) bool {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		// 空 serviceId 直接返回 false。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.removeServicePoolLocked(normalizedServiceID)
}

// RemoveByServiceKey 按 serviceKey 删除服务。
func (registry *ServiceRegistry) RemoveByServiceKey(serviceKey string) bool {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey == "" {
		// 空 serviceKey 直接返回 false。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	serviceID, exists := registry.byServiceKey[normalizedServiceKey]
	if !exists {
		return false
	}
	return registry.removeServicePoolLocked(serviceID)
}

// RemoveInstanceByServiceIDAndRuntime 按 service_id + connector/session 删除单实例。
func (registry *ServiceRegistry) RemoveInstanceByServiceIDAndRuntime(serviceID string, connectorID string, sessionID string) bool {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	return registry.removeInstanceLocked(normalizedServiceID, strings.TrimSpace(connectorID), strings.TrimSpace(sessionID))
}

// RemoveInstanceByServiceKeyAndRuntime 按 service_key + connector/session 删除单实例。
func (registry *ServiceRegistry) RemoveInstanceByServiceKeyAndRuntime(serviceKey string, connectorID string, sessionID string) bool {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey == "" {
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	serviceID, exists := registry.byServiceKey[normalizedServiceKey]
	if !exists {
		return false
	}
	return registry.removeInstanceLocked(serviceID, strings.TrimSpace(connectorID), strings.TrimSpace(sessionID))
}

// GetByServiceID 读取指定 serviceId 的快照。
func (registry *ServiceRegistry) GetByServiceID(serviceID string) (pb.Service, bool) {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		// 入参为空时返回未命中。
		return pb.Service{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := selectServicePoolSnapshotLocked(registry.byServiceID[normalizedServiceID])
	if !exists {
		return pb.Service{}, false
	}
	return record.Service, true
}

// GetByServiceKey 读取指定 serviceKey 的快照。
func (registry *ServiceRegistry) GetByServiceKey(serviceKey string) (pb.Service, bool) {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey == "" {
		// 入参为空时返回未命中。
		return pb.Service{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	serviceID, exists := registry.byServiceKey[normalizedServiceKey]
	if !exists {
		return pb.Service{}, false
	}
	record, exists := selectServicePoolSnapshotLocked(registry.byServiceID[serviceID])
	if !exists {
		return pb.Service{}, false
	}
	return record.Service, true
}

// ListInstancesByServiceID 返回指定 service_id 的所有实例快照。
func (registry *ServiceRegistry) ListInstancesByServiceID(serviceID string) []ServiceInstanceSnapshot {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return cloneServiceInstances(registry.byServiceID[normalizedServiceID])
}

// ListInstancesByServiceKey 返回指定 service_key 对应服务池的实例快照。
func (registry *ServiceRegistry) ListInstancesByServiceKey(serviceKey string) []ServiceInstanceSnapshot {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceKey == "" {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	serviceID, exists := registry.byServiceKey[normalizedServiceKey]
	if !exists {
		return nil
	}
	return cloneServiceInstances(registry.byServiceID[serviceID])
}

// ListServiceIDsByRuntime 返回指定 connector/session 命中的 service_id 列表。
func (registry *ServiceRegistry) ListServiceIDsByRuntime(connectorID string, sessionID string) []string {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return nil
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	serviceIDs := make([]string, 0, len(registry.byServiceID))
	for serviceID, instances := range registry.byServiceID {
		if len(instances) == 0 {
			continue
		}
		matched := false
		for _, instance := range instances {
			if instance == nil {
				continue
			}
			if strings.TrimSpace(instance.Service.ConnectorID) != normalizedConnectorID {
				continue
			}
			if normalizedSessionID != "" && strings.TrimSpace(instance.SessionID) != normalizedSessionID {
				continue
			}
			matched = true
			break
		}
		if matched {
			serviceIDs = append(serviceIDs, serviceID)
		}
	}
	return serviceIDs
}

// CurrentVersion 返回服务当前资源版本。
func (registry *ServiceRegistry) CurrentVersion(serviceID string, serviceKey string) uint64 {
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if normalizedServiceID != "" {
		if record, exists := registry.byServiceID[normalizedServiceID]; exists {
			// 优先按 serviceId 返回池内最大资源版本。
			return maxResourceVersionLocked(record)
		}
	}
	if normalizedServiceKey != "" {
		if resolvedServiceID, exists := registry.byServiceKey[normalizedServiceKey]; exists {
			if record, ok := registry.byServiceID[resolvedServiceID]; ok {
				return maxResourceVersionLocked(record)
			}
		}
	}
	return 0
}

// ReplaceAll 用 full-sync 快照覆盖全部服务视图。
func (registry *ServiceRegistry) ReplaceAll(now time.Time, services []pb.Service) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	// full-sync 时先清空旧快照，保证对账结果权威。
	registry.byServiceID = make(map[string]map[string]*ServiceInstanceSnapshot, len(services))
	registry.byServiceKey = make(map[string]string, len(services))
	registry.instanceIdentityID = make(map[string]string, len(services))
	for _, service := range services {
		// full-sync 场景下 session_id 可能缺失，按 connector+service 维度回放实例。
		registry.upsertLocked(now, service, "")
	}
}

// List 返回当前所有服务快照。
func (registry *ServiceRegistry) List() []pb.Service {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]pb.Service, 0, len(registry.byServiceID))
	for _, instances := range registry.byServiceID {
		record, exists := selectServicePoolSnapshotLocked(instances)
		if !exists {
			continue
		}
		// 返回池级视图，保持旧接口兼容。
		result = append(result, record.Service)
	}
	return result
}

// MarkLifecycleByConnector 按 connector 批量更新服务生命周期状态。
func (registry *ServiceRegistry) MarkLifecycleByConnector(
	now time.Time,
	connectorID string,
	status pb.ServiceStatus,
	healthStatus pb.HealthStatus,
) int {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		// connector_id 为空时无法筛选目标服务。
		return 0
	}
	normalizedNow := normalizeRegistryTime(now)
	registry.mu.Lock()
	defer registry.mu.Unlock()

	// 兼容旧语义：未指定 session_id 时按 connector 全量更新。
	return registry.markLifecycleByRuntimeLocked(
		normalizedNow,
		normalizedConnectorID,
		"",
		false,
		status,
		healthStatus,
	)
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
	if normalizedSessionID == "" {
		// session_id 缺失时回退到 connector 维度，兼容历史调用链路。
		return registry.markLifecycleByRuntimeLocked(
			normalizedNow,
			normalizedConnectorID,
			"",
			false,
			status,
			healthStatus,
		)
	}
	updatedCount := registry.markLifecycleByRuntimeLocked(
		normalizedNow,
		normalizedConnectorID,
		normalizedSessionID,
		false,
		status,
		healthStatus,
	)
	if updatedCount > 0 {
		return updatedCount
	}
	// 兼容旧数据：若找不到显式 session_id 的实例，仅收敛空 session 的遗留实例。
	return registry.markLifecycleByRuntimeLocked(
		normalizedNow,
		normalizedConnectorID,
		"",
		true,
		status,
		healthStatus,
	)
}

// upsertLocked 在持锁上下文写入服务实例快照。
func (registry *ServiceRegistry) upsertLocked(now time.Time, service pb.Service, sessionID string) string {
	normalizedServiceID := strings.TrimSpace(service.ServiceID)
	normalizedServiceKey := strings.TrimSpace(service.ServiceKey)
	if normalizedServiceID == "" && normalizedServiceKey == "" {
		return ""
	}
	if normalizedServiceID == "" {
		normalizedServiceID = normalizedServiceKey
	}
	normalizedConnectorID := strings.TrimSpace(service.ConnectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	normalizedNow := normalizeRegistryTime(now)
	service.ServiceID = normalizedServiceID
	service.ServiceKey = normalizedServiceKey
	service.ConnectorID = normalizedConnectorID
	baseResourceID := normalizeServiceBaseResourceID(normalizedServiceID, normalizedServiceKey)
	identity := buildServiceInstanceIdentity(baseResourceID, normalizedConnectorID, normalizedSessionID)
	serviceInstanceID := buildServiceInstanceID(baseResourceID, normalizedConnectorID, normalizedSessionID)
	if registry.byServiceID[normalizedServiceID] == nil {
		registry.byServiceID[normalizedServiceID] = make(map[string]*ServiceInstanceSnapshot)
	}
	instances := registry.byServiceID[normalizedServiceID]
	if normalizedSessionID != "" {
		// 会话恢复后收敛 full-sync 产生的空 session 遗留实例，避免同 runtime 重复计数。
		registry.collapseLegacyEmptySessionInstanceLocked(normalizedServiceID, normalizedConnectorID)
	}
	if previousInstanceID, exists := registry.instanceIdentityID[identity]; exists && previousInstanceID != serviceInstanceID {
		delete(instances, previousInstanceID)
	}
	previousSnapshot, hasPrevious := instances[serviceInstanceID]
	instances[serviceInstanceID] = &ServiceInstanceSnapshot{
		Service:           service,
		ServiceInstanceID: serviceInstanceID,
		SessionID:         normalizedSessionID,
		UpdatedAt:         normalizedNow,
	}
	registry.instanceIdentityID[identity] = serviceInstanceID
	if normalizedServiceKey != "" {
		registry.byServiceKey[normalizedServiceKey] = normalizedServiceID
	}
	if hasPrevious {
		previousServiceKey := strings.TrimSpace(previousSnapshot.Service.ServiceKey)
		if previousServiceKey != "" && previousServiceKey != normalizedServiceKey {
			registry.cleanupServiceKeyAliasIfUnusedLocked(normalizedServiceID, previousServiceKey)
		}
	}
	return serviceInstanceID
}

// collapseLegacyEmptySessionInstanceLocked 收敛同 connector 的空 session 历史实例。
func (registry *ServiceRegistry) collapseLegacyEmptySessionInstanceLocked(serviceID string, connectorID string) {
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedServiceID == "" || normalizedConnectorID == "" {
		return
	}
	instances, exists := registry.byServiceID[normalizedServiceID]
	if !exists {
		return
	}
	for instanceID, instance := range instances {
		if instance == nil {
			continue
		}
		if strings.TrimSpace(instance.Service.ConnectorID) != normalizedConnectorID {
			continue
		}
		if strings.TrimSpace(instance.SessionID) != "" {
			continue
		}
		identity := buildServiceInstanceIdentity(
			normalizeServiceBaseResourceID(normalizedServiceID, instance.Service.ServiceKey),
			normalizedConnectorID,
			"",
		)
		delete(registry.instanceIdentityID, identity)
		delete(instances, instanceID)
		registry.cleanupServiceKeyAliasIfUnusedLocked(normalizedServiceID, instance.Service.ServiceKey)
	}
}

// cleanupServiceKeyAliasIfUnusedLocked 在服务池中无实例引用时清理 service_key 反查索引。
func (registry *ServiceRegistry) cleanupServiceKeyAliasIfUnusedLocked(serviceID string, serviceKey string) {
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	if normalizedServiceID == "" || normalizedServiceKey == "" {
		return
	}
	if registry.poolContainsServiceKeyLocked(normalizedServiceID, normalizedServiceKey) {
		return
	}
	if mappedServiceID, mapped := registry.byServiceKey[normalizedServiceKey]; mapped && mappedServiceID == normalizedServiceID {
		delete(registry.byServiceKey, normalizedServiceKey)
	}
}

// removeServicePoolLocked 删除一个逻辑服务池及其全部实例。
func (registry *ServiceRegistry) removeServicePoolLocked(serviceID string) bool {
	instances, exists := registry.byServiceID[serviceID]
	if !exists {
		return false
	}
	for _, instance := range instances {
		identity := buildServiceInstanceIdentity(
			normalizeServiceBaseResourceID(serviceID, instance.Service.ServiceKey),
			instance.Service.ConnectorID,
			instance.SessionID,
		)
		delete(registry.instanceIdentityID, identity)
	}
	delete(registry.byServiceID, serviceID)
	for serviceKey, mappedServiceID := range registry.byServiceKey {
		if mappedServiceID == serviceID {
			delete(registry.byServiceKey, serviceKey)
		}
	}
	return true
}

// markLifecycleByRuntimeLocked 在持锁上下文按 connector/session 过滤并更新实例生命周期。
func (registry *ServiceRegistry) markLifecycleByRuntimeLocked(
	now time.Time,
	connectorID string,
	sessionID string,
	onlyEmptySession bool,
	status pb.ServiceStatus,
	healthStatus pb.HealthStatus,
) int {
	updatedCount := 0
	for _, instances := range registry.byServiceID {
		for _, record := range instances {
			if strings.TrimSpace(record.Service.ConnectorID) != connectorID {
				continue
			}
			recordSessionID := strings.TrimSpace(record.SessionID)
			if sessionID != "" && recordSessionID != sessionID {
				continue
			}
			if sessionID == "" && onlyEmptySession && recordSessionID != "" {
				// 兼容模式下仅命中历史空 session 实例，避免跨会话误伤。
				continue
			}
			record.Service.Status = status
			record.Service.HealthStatus = healthStatus
			record.UpdatedAt = now
			updatedCount++
		}
	}
	return updatedCount
}

// removeInstanceLocked 删除指定服务池中的实例。
func (registry *ServiceRegistry) removeInstanceLocked(serviceID string, connectorID string, sessionID string) bool {
	instances, exists := registry.byServiceID[serviceID]
	if !exists {
		return false
	}
	removedCount := 0
	for instanceID, instance := range instances {
		normalizedInstanceConnectorID := strings.TrimSpace(instance.Service.ConnectorID)
		normalizedInstanceSessionID := strings.TrimSpace(instance.SessionID)
		if connectorID != "" && normalizedInstanceConnectorID != connectorID {
			continue
		}
		if sessionID != "" && normalizedInstanceSessionID != sessionID {
			continue
		}
		identity := buildServiceInstanceIdentity(
			normalizeServiceBaseResourceID(serviceID, instance.Service.ServiceKey),
			normalizedInstanceConnectorID,
			normalizedInstanceSessionID,
		)
		delete(registry.instanceIdentityID, identity)
		delete(instances, instanceID)
		removedCount++
	}
	if removedCount == 0 {
		return false
	}
	if len(instances) == 0 {
		delete(registry.byServiceID, serviceID)
		for serviceKey, mappedServiceID := range registry.byServiceKey {
			if mappedServiceID == serviceID {
				delete(registry.byServiceKey, serviceKey)
			}
		}
		return true
	}
	for serviceKey, mappedServiceID := range registry.byServiceKey {
		if mappedServiceID == serviceID && !registry.poolContainsServiceKeyLocked(serviceID, serviceKey) {
			delete(registry.byServiceKey, serviceKey)
		}
	}
	return true
}

// poolContainsServiceKeyLocked 判断服务池中是否仍有实例引用指定 key。
func (registry *ServiceRegistry) poolContainsServiceKeyLocked(serviceID string, serviceKey string) bool {
	instances, exists := registry.byServiceID[serviceID]
	if !exists {
		return false
	}
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	for _, instance := range instances {
		if strings.TrimSpace(instance.Service.ServiceKey) == normalizedServiceKey {
			return true
		}
	}
	return false
}

// selectServicePoolSnapshotLocked 选择服务池代表快照（优先 ACTIVE+HEALTHY）。
func selectServicePoolSnapshotLocked(instances map[string]*ServiceInstanceSnapshot) (ServicePoolSnapshot, bool) {
	if len(instances) == 0 {
		return ServicePoolSnapshot{}, false
	}
	selected := ServicePoolSnapshot{}
	selectedScore := -1
	selectedInstanceID := ""
	for instanceID, instance := range instances {
		if instance == nil {
			continue
		}
		currentScore := serviceAvailabilityScore(instance.Service)
		if currentScore > selectedScore {
			selectedScore = currentScore
			selectedInstanceID = instanceID
			selected = ServicePoolSnapshot{
				Service:   instance.Service,
				UpdatedAt: instance.UpdatedAt,
			}
			continue
		}
		if currentScore < selectedScore {
			continue
		}
		if instance.UpdatedAt.After(selected.UpdatedAt) {
			selectedInstanceID = instanceID
			selected = ServicePoolSnapshot{
				Service:   instance.Service,
				UpdatedAt: instance.UpdatedAt,
			}
			continue
		}
		if instance.UpdatedAt.Equal(selected.UpdatedAt) && selectedInstanceID != "" && instanceID < selectedInstanceID {
			selectedInstanceID = instanceID
			selected = ServicePoolSnapshot{
				Service:   instance.Service,
				UpdatedAt: instance.UpdatedAt,
			}
		}
	}
	if selectedScore < 0 {
		return ServicePoolSnapshot{}, false
	}
	return selected, true
}

// cloneServiceInstances 复制实例切片，避免调用方持有内部可变引用。
func cloneServiceInstances(instances map[string]*ServiceInstanceSnapshot) []ServiceInstanceSnapshot {
	if len(instances) == 0 {
		return nil
	}
	result := make([]ServiceInstanceSnapshot, 0, len(instances))
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		result = append(result, ServiceInstanceSnapshot{
			Service:           instance.Service,
			ServiceInstanceID: instance.ServiceInstanceID,
			SessionID:         instance.SessionID,
			UpdatedAt:         instance.UpdatedAt,
		})
	}
	return result
}

// maxResourceVersionLocked 返回服务池内最大资源版本。
func maxResourceVersionLocked(instances map[string]*ServiceInstanceSnapshot) uint64 {
	maxVersion := uint64(0)
	for _, instance := range instances {
		if instance == nil {
			continue
		}
		if instance.Service.ResourceVersion > maxVersion {
			maxVersion = instance.Service.ResourceVersion
		}
	}
	return maxVersion
}

// serviceAvailabilityScore 对服务实例可用性打分，优先 ACTIVE+HEALTHY。
func serviceAvailabilityScore(service pb.Service) int {
	if service.Status != pb.ServiceStatusActive {
		return 0
	}
	switch service.HealthStatus {
	case pb.HealthStatusHealthy:
		return 3
	case pb.HealthStatusUnknown:
		return 2
	case pb.HealthStatusUnhealthy:
		return 1
	default:
		return 1
	}
}

// normalizeRegistryTime 归一化服务注册表更新时间。
func normalizeRegistryTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

// normalizeServiceBaseResourceID 归一化实例 identity 使用的资源基准键。
func normalizeServiceBaseResourceID(serviceID string, serviceKey string) string {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID != "" {
		return normalizedServiceID
	}
	return strings.TrimSpace(serviceKey)
}

// buildServiceInstanceIdentity 构造实例 identity（用于幂等更新）。
func buildServiceInstanceIdentity(baseResourceID string, connectorID string, sessionID string) string {
	return fmt.Sprintf(
		"%s|%s|%s",
		strings.TrimSpace(baseResourceID),
		strings.TrimSpace(connectorID),
		strings.TrimSpace(sessionID),
	)
}

// buildServiceInstanceID 构造内部 service_instance_id。
func buildServiceInstanceID(baseResourceID string, connectorID string, sessionID string) string {
	normalizedBaseID := strings.TrimSpace(baseResourceID)
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedConnectorID == "" && normalizedSessionID == "" {
		// 兼容历史单实例场景，保持 instance_id 可稳定回放。
		return "svcinst:" + normalizedBaseID
	}
	return fmt.Sprintf("svcinst:%s|%s|%s", normalizedBaseID, normalizedConnectorID, normalizedSessionID)
}
