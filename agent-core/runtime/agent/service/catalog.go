package service

import (
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Record 描述 Agent 本地 service 运行态记录。
type Record struct {
	Registration     adapter.LocalRegistration
	HealthStatus     pb.HealthStatus
	EndpointStatuses []pb.EndpointHealthStatus
	UpdatedAt        time.Time
}

// Catalog 维护 Agent 本地 service 注册与健康真相源。
type Catalog struct {
	mu           sync.RWMutex
	byServiceID  map[string]*Record
	byServiceKey map[string]string
}

// NewCatalog 创建本地服务目录。
func NewCatalog() *Catalog {
	return &Catalog{
		byServiceID:  make(map[string]*Record),
		byServiceKey: make(map[string]string),
	}
}

// Upsert 写入或更新本地 service 注册。
func (catalog *Catalog) Upsert(now time.Time, registration adapter.LocalRegistration) Record {
	normalizedRegistration := normalizeRegistration(registration)
	normalizedServiceID := strings.TrimSpace(normalizedRegistration.ServiceID)
	normalizedServiceKey := strings.TrimSpace(normalizedRegistration.ServiceKey)
	if normalizedServiceID == "" && normalizedServiceKey == "" {
		// service_id 与 service_key 同时为空时无法索引，直接返回空记录。
		return Record{}
	}

	catalog.mu.Lock()
	defer catalog.mu.Unlock()

	if normalizedServiceID == "" && normalizedServiceKey != "" {
		// service_id 为空时尝试复用已有 key->id 映射，保持 identity 稳定。
		if mappedServiceID, exists := catalog.byServiceKey[normalizedServiceKey]; exists {
			normalizedServiceID = mappedServiceID
		}
	}
	if normalizedServiceID == "" {
		// 无显式 service_id 时回退到 service_key，保证记录可追踪。
		normalizedServiceID = normalizedServiceKey
	}
	normalizedRegistration.ServiceID = normalizedServiceID
	normalizedRegistration.ServiceKey = normalizedServiceKey

	var previousHealthStatus pb.HealthStatus = pb.HealthStatusUnknown
	var previousEndpointStatuses []pb.EndpointHealthStatus
	if existingRecord, exists := catalog.byServiceID[normalizedServiceID]; exists {
		previousHealthStatus = normalizeHealthStatus(existingRecord.HealthStatus)
		previousEndpointStatuses = cloneEndpointStatuses(existingRecord.EndpointStatuses)
		oldServiceKey := strings.TrimSpace(existingRecord.Registration.ServiceKey)
		if oldServiceKey != "" && oldServiceKey != normalizedServiceKey {
			// service_key 发生变更时移除旧反查索引，避免脏映射误命中。
			delete(catalog.byServiceKey, oldServiceKey)
		}
	}

	record := &Record{
		Registration:     normalizedRegistration,
		HealthStatus:     previousHealthStatus,
		EndpointStatuses: previousEndpointStatuses,
		UpdatedAt:        normalizeUpdatedAt(now),
	}
	catalog.byServiceID[normalizedServiceID] = record
	if normalizedServiceKey != "" {
		// 建立 service_key -> service_id 映射，供 lookup 与 ack 回写复用。
		catalog.byServiceKey[normalizedServiceKey] = normalizedServiceID
	}
	return cloneRecord(*record)
}

// SetServiceIDByKey 用 service_key 回写稳定 service_id（用于 PublishServiceAck 场景）。
func (catalog *Catalog) SetServiceIDByKey(now time.Time, serviceKey string, serviceID string) bool {
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceKey == "" || normalizedServiceID == "" {
		return false
	}

	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	currentServiceID, exists := catalog.byServiceKey[normalizedServiceKey]
	if !exists {
		return false
	}
	if currentServiceID == normalizedServiceID {
		return true
	}

	record, exists := catalog.byServiceID[currentServiceID]
	if !exists {
		return false
	}
	delete(catalog.byServiceID, currentServiceID)

	record.Registration.ServiceID = normalizedServiceID
	record.UpdatedAt = normalizeUpdatedAt(now)
	catalog.byServiceID[normalizedServiceID] = record
	catalog.byServiceKey[normalizedServiceKey] = normalizedServiceID
	return true
}

// UpdateHealth 更新本地 service 聚合健康状态。
func (catalog *Catalog) UpdateHealth(
	now time.Time,
	serviceID string,
	serviceKey string,
	serviceHealthStatus pb.HealthStatus,
	endpointStatuses []pb.EndpointHealthStatus,
) bool {
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceKey := strings.TrimSpace(serviceKey)
	normalizedHealthStatus := normalizeHealthStatus(serviceHealthStatus)

	catalog.mu.Lock()
	defer catalog.mu.Unlock()

	record := catalog.lookupRecordLocked(normalizedServiceID, normalizedServiceKey)
	if record == nil {
		// 未找到对应服务时不创建空壳记录，避免污染目录。
		return false
	}
	record.HealthStatus = normalizedHealthStatus
	record.EndpointStatuses = cloneEndpointStatuses(endpointStatuses)
	record.UpdatedAt = normalizeUpdatedAt(now)
	return true
}

// RemoveByServiceID 按 service_id 删除本地 service。
func (catalog *Catalog) RemoveByServiceID(serviceID string) bool {
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return false
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	record, exists := catalog.byServiceID[normalizedServiceID]
	if !exists {
		return false
	}
	normalizedServiceKey := strings.TrimSpace(record.Registration.ServiceKey)
	if normalizedServiceKey != "" {
		delete(catalog.byServiceKey, normalizedServiceKey)
	}
	delete(catalog.byServiceID, normalizedServiceID)
	return true
}

// List 返回本地 service 记录快照。
func (catalog *Catalog) List() []Record {
	catalog.mu.RLock()
	defer catalog.mu.RUnlock()
	result := make([]Record, 0, len(catalog.byServiceID))
	for _, record := range catalog.byServiceID {
		result = append(result, cloneRecord(*record))
	}
	sort.Slice(result, func(left int, right int) bool {
		leftKey := strings.TrimSpace(result[left].Registration.ServiceKey)
		rightKey := strings.TrimSpace(result[right].Registration.ServiceKey)
		if leftKey == rightKey {
			return result[left].Registration.ServiceID < result[right].Registration.ServiceID
		}
		return leftKey < rightKey
	})
	return result
}

// lookupRecordLocked 在已加锁上下文中按 service_id / service_key 查询记录。
func (catalog *Catalog) lookupRecordLocked(serviceID string, serviceKey string) *Record {
	if serviceID != "" {
		if record, exists := catalog.byServiceID[serviceID]; exists {
			return record
		}
	}
	if serviceKey != "" {
		if resolvedServiceID, exists := catalog.byServiceKey[serviceKey]; exists {
			if record, ok := catalog.byServiceID[resolvedServiceID]; ok {
				return record
			}
		}
	}
	return nil
}

// normalizeRegistration 归一化并深拷贝注册对象。
func normalizeRegistration(registration adapter.LocalRegistration) adapter.LocalRegistration {
	normalized := adapter.LocalRegistration{
		ServiceID:       strings.TrimSpace(registration.ServiceID),
		ServiceKey:      strings.TrimSpace(registration.ServiceKey),
		Namespace:       strings.TrimSpace(registration.Namespace),
		Environment:     strings.TrimSpace(registration.Environment),
		ServiceName:     strings.TrimSpace(registration.ServiceName),
		ServiceType:     strings.TrimSpace(registration.ServiceType),
		Endpoints:       cloneEndpoints(registration.Endpoints),
		Exposure:        registration.Exposure,
		HealthCheck:     registration.HealthCheck,
		DiscoveryPolicy: registration.DiscoveryPolicy,
		Labels:          cloneStringMap(registration.Labels),
		Metadata:        cloneStringMap(registration.Metadata),
	}
	if normalized.ServiceKey == "" {
		// 未显式提供 key 时按协议规则自动构造。
		normalized.ServiceKey = adapter.BuildServiceKey(
			normalized.Namespace,
			normalized.Environment,
			normalized.ServiceName,
		)
	}
	return normalized
}

// normalizeHealthStatus 将非法健康状态回落为 UNKNOWN。
func normalizeHealthStatus(status pb.HealthStatus) pb.HealthStatus {
	switch status {
	case pb.HealthStatusHealthy, pb.HealthStatusUnhealthy, pb.HealthStatusUnknown:
		return status
	default:
		return pb.HealthStatusUnknown
	}
}

// normalizeUpdatedAt 归一化更新时间，缺失时回填 UTC now。
func normalizeUpdatedAt(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}

// cloneRecord 深拷贝目录记录，避免外部持有内部可变引用。
func cloneRecord(record Record) Record {
	return Record{
		Registration: adapter.LocalRegistration{
			ServiceID:       record.Registration.ServiceID,
			ServiceKey:      record.Registration.ServiceKey,
			Namespace:       record.Registration.Namespace,
			Environment:     record.Registration.Environment,
			ServiceName:     record.Registration.ServiceName,
			ServiceType:     record.Registration.ServiceType,
			Endpoints:       cloneEndpoints(record.Registration.Endpoints),
			Exposure:        record.Registration.Exposure,
			HealthCheck:     record.Registration.HealthCheck,
			DiscoveryPolicy: record.Registration.DiscoveryPolicy,
			Labels:          cloneStringMap(record.Registration.Labels),
			Metadata:        cloneStringMap(record.Registration.Metadata),
		},
		HealthStatus:     normalizeHealthStatus(record.HealthStatus),
		EndpointStatuses: cloneEndpointStatuses(record.EndpointStatuses),
		UpdatedAt:        record.UpdatedAt,
	}
}

// cloneEndpoints 深拷贝 endpoint 切片。
func cloneEndpoints(endpoints []pb.ServiceEndpoint) []pb.ServiceEndpoint {
	cloned := make([]pb.ServiceEndpoint, len(endpoints))
	copy(cloned, endpoints)
	return cloned
}

// cloneEndpointStatuses 深拷贝 endpoint 健康状态切片。
func cloneEndpointStatuses(statuses []pb.EndpointHealthStatus) []pb.EndpointHealthStatus {
	cloned := make([]pb.EndpointHealthStatus, len(statuses))
	copy(cloned, statuses)
	return cloned
}

// cloneStringMap 深拷贝字符串 map。
func cloneStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}
