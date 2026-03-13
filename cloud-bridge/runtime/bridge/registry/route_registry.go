package registry

import (
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RouteSnapshot 描述路由注册表中的一条记录。
type RouteSnapshot struct {
	Route     pb.Route
	UpdatedAt time.Time
}

// RouteRegistry 存储路由快照。
type RouteRegistry struct {
	mu      sync.RWMutex
	byRoute map[string]*RouteSnapshot
}

// NewRouteRegistry 创建路由注册表。
func NewRouteRegistry() *RouteRegistry {
	return &RouteRegistry{
		byRoute: make(map[string]*RouteSnapshot),
	}
}

// Upsert 写入或更新路由快照。
func (registry *RouteRegistry) Upsert(now time.Time, route pb.Route) {
	normalizedRouteID := strings.TrimSpace(route.RouteID)
	if normalizedRouteID == "" {
		// routeId 为空时无法建立索引，直接忽略。
		return
	}
	route.RouteID = normalizedRouteID

	registry.mu.Lock()
	defer registry.mu.Unlock()
	// 同 routeId 覆盖旧值。
	registry.byRoute[normalizedRouteID] = &RouteSnapshot{
		Route:     route,
		UpdatedAt: now,
	}
}

// Remove 删除指定 routeId 的快照。
func (registry *RouteRegistry) Remove(routeID string) bool {
	normalizedRouteID := strings.TrimSpace(routeID)
	if normalizedRouteID == "" {
		// 空 routeId 不执行删除。
		return false
	}
	registry.mu.Lock()
	defer registry.mu.Unlock()
	if _, exists := registry.byRoute[normalizedRouteID]; !exists {
		return false
	}
	delete(registry.byRoute, normalizedRouteID)
	return true
}

// Get 返回指定 routeId 的快照。
func (registry *RouteRegistry) Get(routeID string) (pb.Route, bool) {
	normalizedRouteID := strings.TrimSpace(routeID)
	if normalizedRouteID == "" {
		// 空 routeId 返回未命中。
		return pb.Route{}, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := registry.byRoute[normalizedRouteID]
	if !exists {
		return pb.Route{}, false
	}
	return record.Route, true
}

// CurrentVersion 返回指定 route 的当前版本。
func (registry *RouteRegistry) CurrentVersion(routeID string) uint64 {
	normalizedRouteID := strings.TrimSpace(routeID)
	if normalizedRouteID == "" {
		// 空 routeId 视为不存在。
		return 0
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	record, exists := registry.byRoute[normalizedRouteID]
	if !exists {
		return 0
	}
	return record.Route.ResourceVersion
}

// ReplaceAll 用 full-sync 快照覆盖全部路由视图。
func (registry *RouteRegistry) ReplaceAll(now time.Time, routes []pb.Route) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	// full-sync 先清空旧路由，保证与权威快照一致。
	registry.byRoute = make(map[string]*RouteSnapshot, len(routes))
	for _, route := range routes {
		normalizedRouteID := strings.TrimSpace(route.RouteID)
		if normalizedRouteID == "" {
			// 无法索引的脏路由跳过。
			continue
		}
		route.RouteID = normalizedRouteID
		registry.byRoute[normalizedRouteID] = &RouteSnapshot{
			Route:     route,
			UpdatedAt: now,
		}
	}
}

// List 返回当前全部路由快照。
func (registry *RouteRegistry) List() []pb.Route {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	result := make([]pb.Route, 0, len(registry.byRoute))
	for _, record := range registry.byRoute {
		// 返回副本，避免调用方修改内部状态。
		result = append(result, record.Route)
	}
	return result
}
