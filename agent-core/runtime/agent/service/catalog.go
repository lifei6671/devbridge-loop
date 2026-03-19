package service

import (
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"math/big"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/tsbase62"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const ulidEncoding = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

// Record 描述 Agent 本地 service 运行态记录。
type Record struct {
	Registration     adapter.LocalRegistration
	HealthStatus     pb.HealthStatus
	EndpointStatuses []pb.EndpointHealthStatus
	UpdatedAt        time.Time
}

// Catalog 维护 Agent 本地 service 注册与健康真相源。
type Catalog struct {
	mu sync.RWMutex

	byInstanceID                 map[string]*Record
	instanceIDByScopeServiceName map[string]string
	instanceIDsByLogicalService  map[string]map[string]struct{}
}

// NewCatalog 创建本地服务目录。
func NewCatalog() *Catalog {
	return &Catalog{
		byInstanceID:                 make(map[string]*Record),
		instanceIDByScopeServiceName: make(map[string]string),
		instanceIDsByLogicalService:  make(map[string]map[string]struct{}),
	}
}

// Upsert 写入或更新本地 service 注册。
func (catalog *Catalog) Upsert(now time.Time, registration adapter.LocalRegistration) Record {
	normalizedRegistration := normalizeRegistration(registration)
	scopeServiceNameKey := buildScopeServiceNameKey(normalizedRegistration.ServiceName, normalizedRegistration.Scope)
	normalizedInstanceID := strings.TrimSpace(normalizedRegistration.InstanceID)
	if normalizedInstanceID == "" && scopeServiceNameKey == "" {
		// instance_id 与 service_name/scope 同时为空时无法索引，直接返回空记录。
		return Record{}
	}

	catalog.mu.Lock()
	defer catalog.mu.Unlock()

	if normalizedInstanceID == "" && scopeServiceNameKey != "" {
		// instance_id 缺失时优先复用同逻辑服务的既有实例标识，保持本地 identity 稳定。
		if mappedInstanceID, exists := catalog.instanceIDByScopeServiceName[scopeServiceNameKey]; exists {
			normalizedInstanceID = mappedInstanceID
		}
	}
	if normalizedInstanceID == "" {
		normalizedInstanceID = buildGeneratedInstanceID(
			now,
			normalizedRegistration.ServiceName,
			normalizedRegistration.ServiceType,
		)
	}
	normalizedRegistration.InstanceID = normalizedInstanceID

	if strings.TrimSpace(normalizedRegistration.LogicalServiceID) == "" && scopeServiceNameKey != "" {
		if existingRecord, exists := catalog.byInstanceID[normalizedInstanceID]; exists && existingRecord != nil {
			normalizedRegistration.LogicalServiceID = strings.TrimSpace(existingRecord.Registration.LogicalServiceID)
		} else if mappedInstanceID, exists := catalog.instanceIDByScopeServiceName[scopeServiceNameKey]; exists {
			if existingRecord, ok := catalog.byInstanceID[mappedInstanceID]; ok && existingRecord != nil {
				normalizedRegistration.LogicalServiceID = strings.TrimSpace(existingRecord.Registration.LogicalServiceID)
			}
		}
	}

	var previousHealthStatus pb.HealthStatus = pb.HealthStatusUnknown
	var previousEndpointStatuses []pb.EndpointHealthStatus
	if existingRecord, exists := catalog.byInstanceID[normalizedInstanceID]; exists && existingRecord != nil {
		previousHealthStatus = normalizeHealthStatus(existingRecord.HealthStatus)
		previousEndpointStatuses = cloneEndpointStatuses(existingRecord.EndpointStatuses)
		catalog.removeRecordIndexesLocked(existingRecord.Registration)
	}

	record := &Record{
		Registration:     normalizedRegistration,
		HealthStatus:     previousHealthStatus,
		EndpointStatuses: previousEndpointStatuses,
		UpdatedAt:        normalizeUpdatedAt(now),
	}
	catalog.byInstanceID[normalizedInstanceID] = record
	catalog.addRecordIndexesLocked(normalizedRegistration)
	return cloneRecord(*record)
}

// ApplyPublishIdentity 用 PublishServiceAck 回写 logical_service_id 与 instance_id。
func (catalog *Catalog) ApplyPublishIdentity(
	now time.Time,
	serviceName string,
	scope pb.Scope,
	logicalServiceID string,
	instanceID string,
) bool {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	normalizedInstanceID := strings.TrimSpace(instanceID)
	scopeServiceNameKey := buildScopeServiceNameKey(serviceName, scope)
	if normalizedLogicalServiceID == "" || (normalizedInstanceID == "" && scopeServiceNameKey == "") {
		return false
	}

	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	currentInstanceID := normalizedInstanceID
	if currentInstanceID == "" {
		mappedInstanceID, exists := catalog.instanceIDByScopeServiceName[scopeServiceNameKey]
		if !exists {
			return false
		}
		currentInstanceID = mappedInstanceID
	}
	record, exists := catalog.byInstanceID[currentInstanceID]
	if !exists || record == nil {
		if scopeServiceNameKey == "" {
			return false
		}
		mappedInstanceID, mappedExists := catalog.instanceIDByScopeServiceName[scopeServiceNameKey]
		if !mappedExists {
			return false
		}
		record, exists = catalog.byInstanceID[mappedInstanceID]
		if !exists || record == nil {
			return false
		}
		currentInstanceID = mappedInstanceID
	}
	catalog.removeRecordIndexesLocked(record.Registration)
	delete(catalog.byInstanceID, currentInstanceID)

	record.Registration.LogicalServiceID = normalizedLogicalServiceID
	if normalizedInstanceID != "" {
		record.Registration.InstanceID = normalizedInstanceID
	} else {
		normalizedInstanceID = strings.TrimSpace(record.Registration.InstanceID)
	}
	record.UpdatedAt = normalizeUpdatedAt(now)

	if normalizedInstanceID == "" {
		return false
	}
	catalog.byInstanceID[normalizedInstanceID] = record
	catalog.addRecordIndexesLocked(record.Registration)
	return true
}

// UpdateHealth 更新本地 service 聚合健康状态。
func (catalog *Catalog) UpdateHealth(
	now time.Time,
	logicalServiceID string,
	instanceID string,
	serviceHealthStatus pb.HealthStatus,
	endpointStatuses []pb.EndpointHealthStatus,
) bool {
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	normalizedInstanceID := strings.TrimSpace(instanceID)
	normalizedHealthStatus := normalizeHealthStatus(serviceHealthStatus)

	catalog.mu.Lock()
	defer catalog.mu.Unlock()

	record := catalog.lookupRecordLocked(normalizedLogicalServiceID, normalizedInstanceID)
	if record == nil {
		// 未找到对应服务时不创建空壳记录，避免污染目录。
		return false
	}
	record.HealthStatus = normalizedHealthStatus
	record.EndpointStatuses = cloneEndpointStatuses(endpointStatuses)
	record.UpdatedAt = normalizeUpdatedAt(now)
	return true
}

// RemoveByInstanceID 按 instance_id 删除本地 service。
func (catalog *Catalog) RemoveByInstanceID(instanceID string) bool {
	normalizedInstanceID := strings.TrimSpace(instanceID)
	if normalizedInstanceID == "" {
		return false
	}
	catalog.mu.Lock()
	defer catalog.mu.Unlock()
	record, exists := catalog.byInstanceID[normalizedInstanceID]
	if !exists || record == nil {
		return false
	}
	catalog.removeRecordIndexesLocked(record.Registration)
	delete(catalog.byInstanceID, normalizedInstanceID)
	return true
}

// List 返回本地 service 记录快照。
func (catalog *Catalog) List() []Record {
	catalog.mu.RLock()
	defer catalog.mu.RUnlock()
	result := make([]Record, 0, len(catalog.byInstanceID))
	for _, record := range catalog.byInstanceID {
		result = append(result, cloneRecord(*record))
	}
	sort.Slice(result, func(left int, right int) bool {
		leftKey := buildScopeServiceNameKey(result[left].Registration.ServiceName, result[left].Registration.Scope)
		rightKey := buildScopeServiceNameKey(result[right].Registration.ServiceName, result[right].Registration.Scope)
		if leftKey == rightKey {
			return result[left].Registration.InstanceID < result[right].Registration.InstanceID
		}
		return leftKey < rightKey
	})
	return result
}

// lookupRecordLocked 在已加锁上下文中按 logical_service_id / instance_id 查询记录。
func (catalog *Catalog) lookupRecordLocked(logicalServiceID string, instanceID string) *Record {
	if instanceID != "" {
		if record, exists := catalog.byInstanceID[instanceID]; exists {
			return record
		}
	}
	if logicalServiceID != "" {
		if instanceIDs, exists := catalog.instanceIDsByLogicalService[logicalServiceID]; exists {
			for candidateInstanceID := range instanceIDs {
				if record, ok := catalog.byInstanceID[candidateInstanceID]; ok {
					return record
				}
			}
		}
	}
	return nil
}

func (catalog *Catalog) addRecordIndexesLocked(registration adapter.LocalRegistration) {
	scopeServiceNameKey := buildScopeServiceNameKey(registration.ServiceName, registration.Scope)
	normalizedInstanceID := strings.TrimSpace(registration.InstanceID)
	if scopeServiceNameKey != "" && normalizedInstanceID != "" {
		catalog.instanceIDByScopeServiceName[scopeServiceNameKey] = normalizedInstanceID
	}
	normalizedLogicalServiceID := strings.TrimSpace(registration.LogicalServiceID)
	if normalizedLogicalServiceID != "" && normalizedInstanceID != "" {
		if _, exists := catalog.instanceIDsByLogicalService[normalizedLogicalServiceID]; !exists {
			catalog.instanceIDsByLogicalService[normalizedLogicalServiceID] = make(map[string]struct{})
		}
		catalog.instanceIDsByLogicalService[normalizedLogicalServiceID][normalizedInstanceID] = struct{}{}
	}
}

func (catalog *Catalog) removeRecordIndexesLocked(registration adapter.LocalRegistration) {
	scopeServiceNameKey := buildScopeServiceNameKey(registration.ServiceName, registration.Scope)
	normalizedInstanceID := strings.TrimSpace(registration.InstanceID)
	if scopeServiceNameKey != "" && normalizedInstanceID != "" {
		if mappedInstanceID, exists := catalog.instanceIDByScopeServiceName[scopeServiceNameKey]; exists && mappedInstanceID == normalizedInstanceID {
			delete(catalog.instanceIDByScopeServiceName, scopeServiceNameKey)
		}
	}
	normalizedLogicalServiceID := strings.TrimSpace(registration.LogicalServiceID)
	if normalizedLogicalServiceID != "" && normalizedInstanceID != "" {
		if instanceIDs, exists := catalog.instanceIDsByLogicalService[normalizedLogicalServiceID]; exists {
			delete(instanceIDs, normalizedInstanceID)
			if len(instanceIDs) == 0 {
				delete(catalog.instanceIDsByLogicalService, normalizedLogicalServiceID)
			}
		}
	}
}

func buildScopeServiceNameKey(serviceName string, scope pb.Scope) string {
	normalizedServiceName := strings.TrimSpace(serviceName)
	normalizedNamespace := strings.TrimSpace(scope.Namespace)
	normalizedEnvironment := strings.TrimSpace(scope.Environment)
	if normalizedServiceName == "" || normalizedNamespace == "" || normalizedEnvironment == "" {
		return ""
	}
	return normalizedNamespace + "|" + normalizedEnvironment + "|" + normalizedServiceName
}

// normalizeRegistration 归一化并深拷贝注册对象。
func normalizeRegistration(registration adapter.LocalRegistration) adapter.LocalRegistration {
	return adapter.LocalRegistration{
		LogicalServiceID: strings.TrimSpace(registration.LogicalServiceID),
		InstanceID:       strings.TrimSpace(registration.InstanceID),
		Scope: pb.Scope{
			Namespace:   strings.TrimSpace(registration.Scope.Namespace),
			Environment: strings.TrimSpace(registration.Scope.Environment),
		},
		ServiceName:     strings.TrimSpace(registration.ServiceName),
		ServiceType:     strings.TrimSpace(registration.ServiceType),
		Endpoints:       cloneEndpoints(registration.Endpoints),
		Exposure:        registration.Exposure,
		HealthCheck:     registration.HealthCheck,
		DiscoveryPolicy: registration.DiscoveryPolicy,
		Labels:          cloneStringMap(registration.Labels),
		Metadata:        cloneStringMap(registration.Metadata),
	}
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
			LogicalServiceID: record.Registration.LogicalServiceID,
			InstanceID:       record.Registration.InstanceID,
			Scope: pb.Scope{
				Namespace:   record.Registration.Scope.Namespace,
				Environment: record.Registration.Scope.Environment,
			},
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

func buildGeneratedInstanceID(now time.Time, serviceName string, serviceType string) string {
	nameSegment := normalizeServiceIDSegment(serviceName, "service")
	protocolSegment := normalizeServiceIDSegment(serviceType, "tcp")
	return fmt.Sprintf("%s-%s-%s", nameSegment, protocolSegment, tsbase62.EncodeUint64(uint64(now.UnixNano())))
}

func normalizeServiceIDSegment(value string, fallback string) string {
	normalizedFallback := strings.ToLower(strings.TrimSpace(fallback))
	if normalizedFallback == "" {
		normalizedFallback = "x"
	}
	raw := strings.ToLower(strings.TrimSpace(value))
	if raw == "" {
		return normalizedFallback
	}
	builder := strings.Builder{}
	builder.Grow(len(raw))
	lastHyphen := false
	for _, currentRune := range raw {
		isAlphaNumeric := (currentRune >= 'a' && currentRune <= 'z') || (currentRune >= '0' && currentRune <= '9')
		if isAlphaNumeric {
			builder.WriteRune(currentRune)
			lastHyphen = false
			continue
		}
		if !lastHyphen && builder.Len() > 0 {
			builder.WriteByte('-')
			lastHyphen = true
		}
	}
	segment := strings.Trim(builder.String(), "-")
	if segment == "" {
		return normalizedFallback
	}
	const maxSegmentLength = 48
	if len(segment) > maxSegmentLength {
		segment = strings.Trim(segment[:maxSegmentLength], "-")
		if segment == "" {
			return normalizedFallback
		}
	}
	return segment
}

func newULIDString(now time.Time) string {
	normalizedNow := normalizeUpdatedAt(now)
	timestampMillis := uint64(normalizedNow.UnixMilli())
	var raw [16]byte
	raw[0] = byte(timestampMillis >> 40)
	raw[1] = byte(timestampMillis >> 32)
	raw[2] = byte(timestampMillis >> 24)
	raw[3] = byte(timestampMillis >> 16)
	raw[4] = byte(timestampMillis >> 8)
	raw[5] = byte(timestampMillis)
	if _, err := rand.Read(raw[6:]); err != nil {
		// 熵源异常时回退到时间戳片段，保证格式可用。
		fallbackEntropy := uint64(time.Now().UTC().UnixNano())
		binary.BigEndian.PutUint16(raw[6:8], uint16(fallbackEntropy>>48))
		binary.BigEndian.PutUint64(raw[8:16], fallbackEntropy)
	}
	return encodeULID(raw)
}

func encodeULID(raw [16]byte) string {
	var value big.Int
	value.SetBytes(raw[:])
	base := big.NewInt(32)
	var remainder big.Int
	encoded := make([]byte, 26)
	for index := len(encoded) - 1; index >= 0; index-- {
		remainder.Mod(&value, base)
		encoded[index] = ulidEncoding[remainder.Int64()]
		value.Div(&value, base)
	}
	return string(encoded)
}
