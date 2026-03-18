package control

import (
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RefreshServiceAvailabilityMetricsByServiceIDs 按 service_id 列表刷新服务可用实例指标快照。
func RefreshServiceAvailabilityMetricsByServiceIDs(
	metrics *obs.Metrics,
	serviceRegistry *registry.ServiceRegistry,
	serviceIDs []string,
) {
	if len(serviceIDs) == 0 {
		return
	}
	refreshedServiceIDs := make(map[string]struct{}, len(serviceIDs))
	for _, serviceID := range serviceIDs {
		normalizedServiceID := strings.TrimSpace(serviceID)
		if normalizedServiceID == "" {
			continue
		}
		if _, exists := refreshedServiceIDs[normalizedServiceID]; exists {
			continue
		}
		// 同一 service_id 仅刷新一次，避免重复遍历注册表实例。
		RefreshServiceAvailabilityMetrics(metrics, serviceRegistry, normalizedServiceID)
		refreshedServiceIDs[normalizedServiceID] = struct{}{}
	}
}

// RefreshServiceAvailabilityMetrics 刷新单个服务池的可用实例指标快照。
func RefreshServiceAvailabilityMetrics(
	metrics *obs.Metrics,
	serviceRegistry *registry.ServiceRegistry,
	serviceID string,
) {
	if metrics == nil || serviceRegistry == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	serviceInstances := serviceRegistry.ListInstancesByServiceID(normalizedServiceID)
	availableServiceInstanceIDs := make([]string, 0, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		if !isServiceInstanceAvailableForRouting(serviceInstance.Service) {
			continue
		}
		normalizedServiceInstanceID := strings.TrimSpace(serviceInstance.ServiceInstanceID)
		if normalizedServiceInstanceID == "" {
			continue
		}
		availableServiceInstanceIDs = append(availableServiceInstanceIDs, normalizedServiceInstanceID)
	}
	// 以当前可用实例快照覆盖指标，确保服务池可用数与路由过滤口径一致。
	metrics.SetBridgeServiceAvailableInstances(normalizedServiceID, availableServiceInstanceIDs)
}

// isServiceInstanceAvailableForRouting 判断实例是否满足 ACTIVE+HEALTHY 的路由可用口径。
func isServiceInstanceAvailableForRouting(service pb.Service) bool {
	return service.Status == pb.ServiceStatusActive && service.HealthStatus == pb.HealthStatusHealthy
}
