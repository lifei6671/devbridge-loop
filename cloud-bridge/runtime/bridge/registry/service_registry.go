package registry

import (
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// ServiceSnapshot 描述服务注册表中的一条记录。
type ServiceSnapshot struct {
	Service   pb.Service
	UpdatedAt time.Time
}

// ServiceRegistry 存储已发布服务与健康状态。
type ServiceRegistry struct {
	mu           sync.RWMutex
	byServiceID  map[string]*ServiceSnapshot
	byServiceKey map[string]string
}

// NewServiceRegistry 创建服务注册表。
func NewServiceRegistry() *ServiceRegistry {
	return &ServiceRegistry{
		byServiceID:  make(map[string]*ServiceSnapshot),
		byServiceKey: make(map[string]string),
	}
}

// Upsert 写入或更新服务快照。
func (registry *ServiceRegistry) Upsert(now time.Time, service pb.Service) {
	normalizedServiceID := strings.TrimSpace(service.ServiceID)
	normalizedServiceKey := strings.TrimSpace(service.ServiceKey)
	if normalizedServiceID == "" && normalizedServiceKey == "" {
		// serviceId/serviceKey 都为空时无法建立索引，直接忽略。
		return
	}
	if normalizedServiceID == "" {
		// 无 serviceId 时退化为使用 serviceKey 作为稳定主键。
		normalizedServiceID = normalizedServiceKey
	}
	service.ServiceID = normalizedServiceID
	service.ServiceKey = normalizedServiceKey

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if existingSnapshot, exists := registry.byServiceID[normalizedServiceID]; exists {
		oldServiceKey := strings.TrimSpace(existingSnapshot.Service.ServiceKey)
		if oldServiceKey != "" && oldServiceKey != normalizedServiceKey {
			// serviceKey 变更时移除旧别名，避免脏索引删除新服务。
			if mappedServiceID, mapped := registry.byServiceKey[oldServiceKey]; mapped && mappedServiceID == normalizedServiceID {
				delete(registry.byServiceKey, oldServiceKey)
			}
		}
	}
	// 同 serviceId 覆盖旧快照，保证状态单值。
	registry.byServiceID[normalizedServiceID] = &ServiceSnapshot{
		Service:   service,
		UpdatedAt: now,
	}
	if normalizedServiceKey != "" {
		// 建立 serviceKey -> serviceId 的反查索引。
		registry.byServiceKey[normalizedServiceKey] = normalizedServiceID
	}
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
	record, exists := registry.byServiceID[normalizedServiceID]
	if !exists {
		return false
	}
	if record.Service.ServiceKey != "" {
		// 删除主记录时同步删除反查索引。
		delete(registry.byServiceKey, record.Service.ServiceKey)
	}
	delete(registry.byServiceID, normalizedServiceID)
	return true
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
	delete(registry.byServiceKey, normalizedServiceKey)
	delete(registry.byServiceID, serviceID)
	return true
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
	record, exists := registry.byServiceID[normalizedServiceID]
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
	record, exists := registry.byServiceID[serviceID]
	if !exists {
		return pb.Service{}, false
	}
	return record.Service, true
}

// CurrentVersion 返回服务当前资源版本。
func (registry *ServiceRegistry) CurrentVersion(serviceID string, serviceKey string) uint64 {
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	if normalizedServiceID != "" {
		if record, exists := registry.byServiceID[normalizedServiceID]; exists {
			// 优先按 serviceId 返回当前版本。
			return record.Service.ResourceVersion
		}
	}
	if normalizedServiceKey != "" {
		if resolvedServiceID, exists := registry.byServiceKey[normalizedServiceKey]; exists {
			if record, ok := registry.byServiceID[resolvedServiceID]; ok {
				return record.Service.ResourceVersion
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
	registry.byServiceID = make(map[string]*ServiceSnapshot, len(services))
	registry.byServiceKey = make(map[string]string, len(services))
	for _, service := range services {
		normalizedServiceID := strings.TrimSpace(service.ServiceID)
		normalizedServiceKey := strings.TrimSpace(service.ServiceKey)
		if normalizedServiceID == "" && normalizedServiceKey == "" {
			// 无法索引的脏数据直接跳过。
			continue
		}
		if normalizedServiceID == "" {
			normalizedServiceID = normalizedServiceKey
		}
		service.ServiceID = normalizedServiceID
		service.ServiceKey = normalizedServiceKey
		registry.byServiceID[normalizedServiceID] = &ServiceSnapshot{
			Service:   service,
			UpdatedAt: now,
		}
		if normalizedServiceKey != "" {
			registry.byServiceKey[normalizedServiceKey] = normalizedServiceID
		}
	}
}

// List 返回当前所有服务快照。
func (registry *ServiceRegistry) List() []pb.Service {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]pb.Service, 0, len(registry.byServiceID))
	for _, record := range registry.byServiceID {
		// 返回副本，避免调用方篡改内部状态。
		result = append(result, record.Service)
	}
	return result
}
