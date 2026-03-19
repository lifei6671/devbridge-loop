package adapter

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestToPublishService 验证本地注册到 PublishService 的转换。
func TestToPublishService(t *testing.T) {
	t.Parallel()

	publish := ToPublishService(LocalRegistration{
		InstanceID:  "si-001",
		Scope:       pb.Scope{Namespace: "dev", Environment: "alice"},
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Host: "127.0.0.1", Port: 8080, Protocol: "http"},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			Host:        "api.dev.example.com",
		},
		DiscoveryPolicy: pb.DiscoveryPolicy{
			Enabled: true,
		},
		RouteHint: pb.RouteHint{
			MatchHeaders: []pb.HeaderMatcher{
				{Name: "x-tenant", Exact: "alice"},
			},
			Priority: 9,
		},
	})
	if publish.InstanceID != "si-001" || publish.ServiceType != "http" {
		t.Fatalf("unexpected publish payload: %+v", publish)
	}
	if publish.Scope.Namespace != "dev" || publish.Scope.Environment != "alice" {
		t.Fatalf("unexpected publish scope: %+v", publish.Scope)
	}
	if len(publish.RouteHint.MatchHeaders) != 1 || publish.RouteHint.Priority != 9 {
		t.Fatalf("unexpected route hint: %+v", publish.RouteHint)
	}
}

// TestToUnpublishService 验证本地下线到 UnpublishService 的转换。
func TestToUnpublishService(t *testing.T) {
	t.Parallel()

	unpublish := ToUnpublishService(LocalRegistration{
		LogicalServiceID: "ls-001",
		InstanceID:       "si-001",
		Scope:            pb.Scope{Namespace: "dev", Environment: "alice"},
		ServiceName:      "order-service",
		ServiceType:      "http",
	}, "service removed")
	if unpublish.InstanceID != "si-001" || unpublish.Reason != "service removed" {
		t.Fatalf("unexpected unpublish payload: %+v", unpublish)
	}
}

// TestToHealthReport 验证 endpoint 健康聚合与上报转换。
func TestToHealthReport(t *testing.T) {
	t.Parallel()

	report := ToHealthReport(
		"si-001",
		"ls-001",
		[]pb.EndpointHealthStatus{
			{EndpointID: "ep-1", HealthStatus: pb.HealthStatusHealthy},
			{EndpointID: "ep-2", HealthStatus: pb.HealthStatusUnknown},
		},
		time.Unix(1700000000, 0),
		"probe ok",
		map[string]string{"source": "agent"},
	)
	if report.ServiceHealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected aggregated status: %s", report.ServiceHealthStatus)
	}
	if report.CheckTimeUnix != 1700000000 {
		t.Fatalf("unexpected checkTimeUnix: %d", report.CheckTimeUnix)
	}
	if report.Metadata["source"] != "agent" {
		t.Fatalf("unexpected metadata: %+v", report.Metadata)
	}
}
