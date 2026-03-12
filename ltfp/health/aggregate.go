package health

import "github.com/lifei6671/devbridge-loop/ltfp/pb"

// AggregateServiceHealth 将 endpoint 健康状态聚合为 service 健康状态。
func AggregateServiceHealth(endpointStatuses []pb.EndpointHealthStatus) pb.HealthStatus {
	if len(endpointStatuses) == 0 {
		// 无 endpoint 状态时返回 UNKNOWN，避免误判为健康。
		return pb.HealthStatusUnknown
	}

	hasHealthy := false
	hasUnknown := false
	for _, endpointStatus := range endpointStatuses {
		switch endpointStatus.HealthStatus {
		case pb.HealthStatusHealthy:
			hasHealthy = true
		case pb.HealthStatusUnknown:
			hasUnknown = true
		}
	}
	// 任一 endpoint 健康则服务可视为健康。
	if hasHealthy {
		return pb.HealthStatusHealthy
	}
	// 无健康但存在未知时，服务状态保持 UNKNOWN。
	if hasUnknown {
		return pb.HealthStatusUnknown
	}
	// 全量不健康时返回 UNHEALTHY。
	return pb.HealthStatusUnhealthy
}
