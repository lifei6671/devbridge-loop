package control

import (
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RefreshServiceAvailabilityMetricsByServiceIDs 按 logical_service_id 列表刷新服务可用实例指标快照。
func RefreshServiceAvailabilityMetricsByServiceIDs(
	metrics *obs.Metrics,
	serviceRegistry *registry.ServiceRegistry,
	logicalServiceIDs []string,
) {
	if len(logicalServiceIDs) == 0 {
		return
	}
	refreshedLogicalServiceIDs := make(map[string]struct{}, len(logicalServiceIDs))
	for _, logicalServiceID := range logicalServiceIDs {
		normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
		if normalizedLogicalServiceID == "" {
			continue
		}
		if _, exists := refreshedLogicalServiceIDs[normalizedLogicalServiceID]; exists {
			continue
		}
		// 同一 logical_service_id 仅刷新一次，避免重复遍历注册表实例。
		RefreshServiceAvailabilityMetrics(metrics, serviceRegistry, normalizedLogicalServiceID)
		refreshedLogicalServiceIDs[normalizedLogicalServiceID] = struct{}{}
	}
}

// RefreshServiceAvailabilityMetrics 刷新单个服务池的可用实例指标快照。
func RefreshServiceAvailabilityMetrics(
	metrics *obs.Metrics,
	serviceRegistry *registry.ServiceRegistry,
	logicalServiceID string,
) {
	if metrics == nil || serviceRegistry == nil {
		return
	}
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return
	}
	serviceInstances := serviceRegistry.ListInstancesByLogicalServiceID(normalizedLogicalServiceID)
	availableServiceInstanceIDs := make([]string, 0, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		if !isServiceInstanceAvailableForRouting(serviceInstance.Instance) {
			continue
		}
		normalizedServiceInstanceID := strings.TrimSpace(serviceInstance.Instance.InstanceID)
		if normalizedServiceInstanceID == "" {
			continue
		}
		availableServiceInstanceIDs = append(availableServiceInstanceIDs, normalizedServiceInstanceID)
	}
	// 以当前可用实例快照覆盖指标，确保服务池可用数与路由过滤口径一致。
	metrics.SetBridgeServiceAvailableInstances(normalizedLogicalServiceID, availableServiceInstanceIDs)
}

// isServiceInstanceAvailableForRouting 判断实例是否满足 ACTIVE+HEALTHY 的路由可用口径。
func isServiceInstanceAvailableForRouting(instance pb.ServiceInstance) bool {
	return instance.InstanceStatus == pb.ServiceStatusActive && instance.HealthStatus == pb.HealthStatusHealthy
}
