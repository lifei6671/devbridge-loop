package health

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestAggregateServiceHealth 验证 endpoint 到 service 的健康聚合规则。
func TestAggregateServiceHealth(t *testing.T) {
	t.Parallel()

	status := AggregateServiceHealth([]pb.EndpointHealthStatus{
		{HealthStatus: pb.HealthStatusUnhealthy},
		{HealthStatus: pb.HealthStatusHealthy},
	})
	// 任一 endpoint 健康时聚合结果应为 HEALTHY。
	if status != pb.HealthStatusHealthy {
		t.Fatalf("unexpected aggregated status: %s", status)
	}
}
