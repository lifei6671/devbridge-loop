package routing

import (
	"strings"
	"sync"
	"time"
)

const (
	// defaultTrafficAffinityTTL 定义 traffic 粘性记录默认存活时间。
	defaultTrafficAffinityTTL = 2 * time.Minute
	// defaultTrafficAffinityCapacity 定义 traffic 粘性表默认容量，防止无限增长。
	defaultTrafficAffinityCapacity = 16384
)

// trafficAffinityEntry 表示单条 traffic 的实例粘性记录。
type trafficAffinityEntry struct {
	serviceInstanceID string
	expiresAt         time.Time
}

// trafficAffinityStore 维护 traffic_id 到 service_instance_id 的短期粘性映射。
type trafficAffinityStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	entries  map[string]trafficAffinityEntry
}

// newTrafficAffinityStore 创建粘性映射表，参数无效时回退默认值。
func newTrafficAffinityStore(ttl time.Duration, capacity int) *trafficAffinityStore {
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		normalizedTTL = defaultTrafficAffinityTTL
	}
	normalizedCapacity := capacity
	if normalizedCapacity <= 0 {
		normalizedCapacity = defaultTrafficAffinityCapacity
	}
	return &trafficAffinityStore{
		ttl:      normalizedTTL,
		capacity: normalizedCapacity,
		entries:  make(map[string]trafficAffinityEntry, normalizedCapacity),
	}
}

// Load 查询 traffic 粘性映射，命中过期记录时会自动清理。
func (store *trafficAffinityStore) Load(trafficID string, now time.Time) (string, bool) {
	if store == nil {
		return "", false
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return "", false
	}
	normalizedNow := normalizeAffinityTime(now)
	store.mu.Lock()
	defer store.mu.Unlock()
	record, exists := store.entries[normalizedTrafficID]
	if !exists {
		return "", false
	}
	if !record.expiresAt.After(normalizedNow) {
		delete(store.entries, normalizedTrafficID)
		return "", false
	}
	return record.serviceInstanceID, true
}

// Store 写入 traffic 粘性映射，并在容量紧张时执行过期与最旧记录清理。
func (store *trafficAffinityStore) Store(trafficID string, serviceInstanceID string, now time.Time) {
	if store == nil {
		return
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if normalizedTrafficID == "" || normalizedServiceInstanceID == "" {
		return
	}
	normalizedNow := normalizeAffinityTime(now)
	store.mu.Lock()
	defer store.mu.Unlock()
	if _, exists := store.entries[normalizedTrafficID]; !exists && len(store.entries) >= store.capacity {
		store.cleanupExpiredLocked(normalizedNow)
		if len(store.entries) >= store.capacity {
			store.evictOldestLocked()
		}
	}
	store.entries[normalizedTrafficID] = trafficAffinityEntry{
		serviceInstanceID: normalizedServiceInstanceID,
		expiresAt:         normalizedNow.Add(store.ttl),
	}
}

// cleanupExpiredLocked 清理已过期粘性记录。
func (store *trafficAffinityStore) cleanupExpiredLocked(now time.Time) {
	for trafficID, record := range store.entries {
		if !record.expiresAt.After(now) {
			delete(store.entries, trafficID)
		}
	}
}

// evictOldestLocked 驱逐最早过期的一条记录，为新写入腾出容量。
func (store *trafficAffinityStore) evictOldestLocked() {
	var oldestTrafficID string
	var oldestExpiresAt time.Time
	for trafficID, record := range store.entries {
		if oldestTrafficID == "" || record.expiresAt.Before(oldestExpiresAt) {
			oldestTrafficID = trafficID
			oldestExpiresAt = record.expiresAt
		}
	}
	if oldestTrafficID != "" {
		delete(store.entries, oldestTrafficID)
	}
}

// normalizeAffinityTime 归一化粘性存储时间戳。
func normalizeAffinityTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
