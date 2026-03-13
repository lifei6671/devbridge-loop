package directproxy

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

func TestEndpointCacheRecheckKeepsRefreshedRecord(testingObject *testing.T) {
	testingObject.Parallel()
	nowTime := time.Date(2026, time.March, 13, 8, 0, 0, 0, time.UTC)
	cache := NewEndpointCache(EndpointCacheOptions{
		Now: func() time.Time { return nowTime },
	})
	target := pb.ExternalServiceTarget{
		Provider:        "k8s",
		Namespace:       "dev",
		Environment:     "alice",
		ServiceName:     "pay",
		CacheTTLSeconds: 1,
		StaleIfErrorSec: 1,
	}
	cacheKey := BuildCacheKey(target)

	cache.Put(target, []ExternalEndpoint{
		{EndpointID: "ep-old", Address: "10.0.0.1:443"},
	})
	nowTime = nowTime.Add(3 * time.Second)
	cache.Put(target, []ExternalEndpoint{
		{EndpointID: "ep-new", Address: "10.0.0.2:443"},
	})

	record, hit := cache.recheckOrEvictExpired(cacheKey, nowTime)
	if !hit {
		testingObject.Fatalf("expected refreshed record still available")
	}
	if len(record.endpoints) != 1 || record.endpoints[0].EndpointID != "ep-new" {
		testingObject.Fatalf("unexpected refreshed endpoint record: %+v", record.endpoints)
	}
}

func TestEndpointCacheRecheckEvictsExpiredRecord(testingObject *testing.T) {
	testingObject.Parallel()
	nowTime := time.Date(2026, time.March, 13, 8, 0, 0, 0, time.UTC)
	cache := NewEndpointCache(EndpointCacheOptions{
		Now: func() time.Time { return nowTime },
	})
	target := pb.ExternalServiceTarget{
		Provider:        "k8s",
		Namespace:       "dev",
		Environment:     "alice",
		ServiceName:     "pay",
		CacheTTLSeconds: 1,
		StaleIfErrorSec: 1,
	}
	cacheKey := BuildCacheKey(target)

	cache.Put(target, []ExternalEndpoint{
		{EndpointID: "ep-1", Address: "10.0.0.1:443"},
	})
	nowTime = nowTime.Add(3 * time.Second)

	_, hit := cache.recheckOrEvictExpired(cacheKey, nowTime)
	if hit {
		testingObject.Fatalf("expected expired record to be evicted")
	}
	cache.mutex.RLock()
	_, exists := cache.records[cacheKey]
	cache.mutex.RUnlock()
	if exists {
		testingObject.Fatalf("expected expired record removed from cache map")
	}
}
