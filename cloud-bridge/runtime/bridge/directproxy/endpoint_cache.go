package directproxy

import (
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	defaultEndpointCacheTTL    = 30 * time.Second
	defaultEndpointStaleIfErro = 30 * time.Second
)

type endpointCacheRecord struct {
	endpoints  []ExternalEndpoint
	freshUntil time.Time
	staleUntil time.Time
	updatedAt  time.Time
}

// CacheLookup 描述 endpoint cache 查询结果。
type CacheLookup struct {
	Endpoints []ExternalEndpoint
	Hit       bool
	Stale     bool
}

// EndpointCacheOptions 定义 endpoint cache 参数。
type EndpointCacheOptions struct {
	Now func() time.Time
}

// EndpointCache caches external endpoint lists.
type EndpointCache struct {
	mutex   sync.RWMutex
	records map[string]endpointCacheRecord
	now     func() time.Time
}

// NewEndpointCache 创建 endpoint cache。
func NewEndpointCache(options EndpointCacheOptions) *EndpointCache {
	nowFunc := options.Now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	return &EndpointCache{
		records: make(map[string]endpointCacheRecord),
		now:     nowFunc,
	}
}

// BuildCacheKey 构建 external_service 的缓存 key。
func BuildCacheKey(target pb.ExternalServiceTarget) string {
	selectorKeys := make([]string, 0, len(target.Selector))
	for key := range target.Selector {
		selectorKeys = append(selectorKeys, key)
	}
	sort.Strings(selectorKeys)
	selectorParts := make([]string, 0, len(selectorKeys))
	for _, key := range selectorKeys {
		selectorParts = append(selectorParts, fmt.Sprintf("%s=%s", key, target.Selector[key]))
	}
	return strings.Join([]string{
		strings.TrimSpace(target.Provider),
		strings.TrimSpace(target.Namespace),
		strings.TrimSpace(target.Environment),
		strings.TrimSpace(target.ServiceName),
		strings.TrimSpace(target.Group),
		strings.Join(selectorParts, "&"),
	}, "|")
}

// Get 查询 endpoint cache；当超出 stale 窗口时返回 miss。
func (cache *EndpointCache) Get(target pb.ExternalServiceTarget) CacheLookup {
	if cache == nil {
		return CacheLookup{}
	}
	cacheKey := BuildCacheKey(target)
	nowTime := cache.now()
	cache.mutex.RLock()
	record, exists := cache.records[cacheKey]
	cache.mutex.RUnlock()
	if !exists {
		return CacheLookup{}
	}
	if !record.staleUntil.IsZero() && nowTime.After(record.staleUntil) {
		rechecked, recheckHit := cache.recheckOrEvictExpired(cacheKey, nowTime)
		if !recheckHit {
			return CacheLookup{}
		}
		record = rechecked
	}
	stale := false
	if !record.freshUntil.IsZero() && nowTime.After(record.freshUntil) {
		stale = true
	}
	return CacheLookup{
		Endpoints: cloneEndpoints(record.endpoints),
		Hit:       true,
		Stale:     stale,
	}
}

// Put 写入 endpoint cache。
func (cache *EndpointCache) Put(target pb.ExternalServiceTarget, endpoints []ExternalEndpoint) {
	if cache == nil {
		return
	}
	cacheKey := BuildCacheKey(target)
	nowTime := cache.now()
	freshTTL := normalizeCacheTTL(target.CacheTTLSeconds)
	staleTTL := normalizeStaleTTL(target.StaleIfErrorSec)
	cache.mutex.Lock()
	cache.records[cacheKey] = endpointCacheRecord{
		endpoints:  cloneEndpoints(endpoints),
		freshUntil: nowTime.Add(freshTTL),
		staleUntil: nowTime.Add(freshTTL + staleTTL),
		updatedAt:  nowTime,
	}
	cache.mutex.Unlock()
}

func (cache *EndpointCache) recheckOrEvictExpired(cacheKey string, nowTime time.Time) (endpointCacheRecord, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	current, exists := cache.records[cacheKey]
	if !exists {
		return endpointCacheRecord{}, false
	}
	if !current.staleUntil.IsZero() && nowTime.After(current.staleUntil) {
		delete(cache.records, cacheKey)
		return endpointCacheRecord{}, false
	}
	return current, true
}

func normalizeCacheTTL(seconds uint32) time.Duration {
	if seconds == 0 {
		return defaultEndpointCacheTTL
	}
	return time.Duration(seconds) * time.Second
}

func normalizeStaleTTL(seconds uint32) time.Duration {
	if seconds == 0 {
		return defaultEndpointStaleIfErro
	}
	return time.Duration(seconds) * time.Second
}

func cloneEndpoints(source []ExternalEndpoint) []ExternalEndpoint {
	if len(source) == 0 {
		return nil
	}
	copied := make([]ExternalEndpoint, 0, len(source))
	for _, endpoint := range source {
		copied = append(copied, ExternalEndpoint{
			EndpointID: endpoint.EndpointID,
			Address:    endpoint.Address,
			Metadata:   copyStringMap(endpoint.Metadata),
		})
	}
	return copied
}
