package app

import (
	"strings"
	"sync"
	"time"
)

const (
	// defaultTrafficOwnershipTTL 定义 traffic 归属记录默认保留窗口，支持排障时短期反查。
	defaultTrafficOwnershipTTL = 30 * time.Minute
	// defaultTrafficOwnershipCapacity 定义 traffic 归属索引默认容量，避免无界增长。
	defaultTrafficOwnershipCapacity = 32768
)

// trafficOwnershipRecord 描述一条 traffic 的服务归属快照。
type trafficOwnershipRecord struct {
	TrafficID         string
	RouteID           string
	TargetKind        string
	IngressMode       string
	ServiceID         string
	ServiceKey        string
	ServiceInstanceID string
	ConnectorID       string
	SessionID         string
	UpdatedAt         time.Time
}

// trafficOwnershipEntry 表示索引中的版本化记录，用于处理同 traffic_id 的覆盖写入。
type trafficOwnershipEntry struct {
	record  trafficOwnershipRecord
	version uint64
}

// trafficOwnershipQueueItem 表示 FIFO 驱逐队列中的一条引用。
type trafficOwnershipQueueItem struct {
	trafficID string
	version   uint64
}

// trafficOwnershipStore 维护 traffic_id 到服务归属的短期反查索引。
type trafficOwnershipStore struct {
	mu       sync.Mutex
	ttl      time.Duration
	capacity int
	now      func() time.Time
	sequence uint64
	entries  map[string]trafficOwnershipEntry
	queue    []trafficOwnershipQueueItem
}

// newTrafficOwnershipStore 创建 traffic 归属索引，参数无效时回退默认值。
func newTrafficOwnershipStore(
	ttl time.Duration,
	capacity int,
	now func() time.Time,
) *trafficOwnershipStore {
	normalizedTTL := ttl
	if normalizedTTL <= 0 {
		normalizedTTL = defaultTrafficOwnershipTTL
	}
	normalizedCapacity := capacity
	if normalizedCapacity <= 0 {
		normalizedCapacity = defaultTrafficOwnershipCapacity
	}
	nowFunc := now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	return &trafficOwnershipStore{
		ttl:      normalizedTTL,
		capacity: normalizedCapacity,
		now:      nowFunc,
		entries:  make(map[string]trafficOwnershipEntry, normalizedCapacity),
		queue:    make([]trafficOwnershipQueueItem, 0, normalizedCapacity),
	}
}

// Observe 写入或覆盖一条 traffic 归属记录。
func (store *trafficOwnershipStore) Observe(record trafficOwnershipRecord) {
	if store == nil {
		return
	}
	normalizedRecord := normalizeTrafficOwnershipRecord(record)
	if normalizedRecord.TrafficID == "" {
		return
	}
	now := normalizeTrafficOwnershipStoreTime(store.now())
	normalizedRecord.UpdatedAt = now

	store.mu.Lock()
	defer store.mu.Unlock()

	// 先清理过期记录，再写入新版本，避免容量被过期数据占满。
	store.cleanupExpiredLocked(now)
	store.sequence++
	currentVersion := store.sequence
	store.entries[normalizedRecord.TrafficID] = trafficOwnershipEntry{
		record:  normalizedRecord,
		version: currentVersion,
	}
	store.queue = append(store.queue, trafficOwnershipQueueItem{
		trafficID: normalizedRecord.TrafficID,
		version:   currentVersion,
	})
	store.evictOverflowLocked()
	store.compactQueueIfNeededLocked()
}

// Load 查询指定 traffic_id 的服务归属记录，命中过期数据时会自动清理。
func (store *trafficOwnershipStore) Load(trafficID string) (trafficOwnershipRecord, bool) {
	if store == nil {
		return trafficOwnershipRecord{}, false
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return trafficOwnershipRecord{}, false
	}
	now := normalizeTrafficOwnershipStoreTime(store.now())
	store.mu.Lock()
	defer store.mu.Unlock()
	entry, exists := store.entries[normalizedTrafficID]
	if !exists {
		return trafficOwnershipRecord{}, false
	}
	if store.isExpiredLocked(entry.record, now) {
		delete(store.entries, normalizedTrafficID)
		return trafficOwnershipRecord{}, false
	}
	return entry.record, true
}

// cleanupExpiredLocked 清理过期记录。
func (store *trafficOwnershipStore) cleanupExpiredLocked(now time.Time) {
	for trafficID, entry := range store.entries {
		if store.isExpiredLocked(entry.record, now) {
			delete(store.entries, trafficID)
		}
	}
}

// evictOverflowLocked 在超过容量上限时按 FIFO 驱逐最老有效记录。
func (store *trafficOwnershipStore) evictOverflowLocked() {
	for len(store.entries) > store.capacity {
		if len(store.queue) == 0 {
			return
		}
		head := store.queue[0]
		store.queue = store.queue[1:]
		entry, exists := store.entries[head.trafficID]
		if !exists {
			continue
		}
		if entry.version != head.version {
			// 队首是旧版本引用时跳过，保留当前最新记录。
			continue
		}
		delete(store.entries, head.trafficID)
	}
}

// compactQueueIfNeededLocked 在队列膨胀时压缩有效引用，避免长时间积累旧版本节点。
func (store *trafficOwnershipStore) compactQueueIfNeededLocked() {
	if len(store.queue) <= store.capacity*2 {
		return
	}
	compacted := make([]trafficOwnershipQueueItem, 0, len(store.entries))
	for _, item := range store.queue {
		entry, exists := store.entries[item.trafficID]
		if !exists || entry.version != item.version {
			continue
		}
		compacted = append(compacted, item)
	}
	store.queue = compacted
}

// isExpiredLocked 判断记录是否过期。
func (store *trafficOwnershipStore) isExpiredLocked(record trafficOwnershipRecord, now time.Time) bool {
	if record.UpdatedAt.IsZero() {
		return false
	}
	return !record.UpdatedAt.Add(store.ttl).After(now)
}

// normalizeTrafficOwnershipRecord 归一化 traffic 归属记录字段，避免索引混入空白值。
func normalizeTrafficOwnershipRecord(record trafficOwnershipRecord) trafficOwnershipRecord {
	record.TrafficID = strings.TrimSpace(record.TrafficID)
	record.RouteID = strings.TrimSpace(record.RouteID)
	record.TargetKind = strings.TrimSpace(record.TargetKind)
	record.IngressMode = strings.TrimSpace(record.IngressMode)
	record.ServiceID = strings.TrimSpace(record.ServiceID)
	record.ServiceKey = strings.TrimSpace(record.ServiceKey)
	record.ServiceInstanceID = strings.TrimSpace(record.ServiceInstanceID)
	record.ConnectorID = strings.TrimSpace(record.ConnectorID)
	record.SessionID = strings.TrimSpace(record.SessionID)
	return record
}

// normalizeTrafficOwnershipStoreTime 归一化索引内部时间戳，统一使用 UTC。
func normalizeTrafficOwnershipStoreTime(now time.Time) time.Time {
	if now.IsZero() {
		return time.Now().UTC()
	}
	return now.UTC()
}
