package adapter

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestBuildServiceKey 验证 service_key 生成格式。
func TestBuildServiceKey(t *testing.T) {
	t.Parallel()

	serviceKey := BuildServiceKey("order-service", " HTTP ")
	if serviceKey != "order-service/http" {
		t.Fatalf("unexpected service key: %s", serviceKey)
	}
}

// TestToPublishService 验证本地注册到 PublishService 的转换。
func TestToPublishService(t *testing.T) {
	t.Parallel()

	publish := ToPublishService(LocalRegistration{
		ServiceID:   "svc-001",
		Namespace:   "dev",
		Environment: "alice",
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
	})
	if publish.ServiceKey != "order-service/http" {
		t.Fatalf("unexpected service key: %s", publish.ServiceKey)
	}
	if publish.ServiceID != "svc-001" || publish.ServiceType != "http" {
		t.Fatalf("unexpected publish payload: %+v", publish)
	}
}

// TestToUnpublishService 验证本地下线到 UnpublishService 的转换。
func TestToUnpublishService(t *testing.T) {
	t.Parallel()

	unpublish := ToUnpublishService(LocalRegistration{
		ServiceID:   "svc-001",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
	}, "service removed")
	if unpublish.ServiceKey != "order-service/http" || unpublish.Reason != "service removed" {
		t.Fatalf("unexpected unpublish payload: %+v", unpublish)
	}
}

// TestToHealthReport 验证 endpoint 健康聚合与上报转换。
func TestToHealthReport(t *testing.T) {
	t.Parallel()

	report := ToHealthReport(
		"svc-001",
		"order-service/http",
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
