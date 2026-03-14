package control

import (
	"context"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type testEndpointHealthProbe struct {
	resultByEndpointID map[string]pb.HealthStatus
}

func (probe *testEndpointHealthProbe) Probe(
	_ context.Context,
	_ adapter.LocalRegistration,
	endpoint pb.ServiceEndpoint,
) (pb.HealthStatus, string) {
	if probe == nil {
		return pb.HealthStatusUnknown, "probe is nil"
	}
	if status, exists := probe.resultByEndpointID[endpoint.EndpointID]; exists {
		return status, "stubbed"
	}
	return pb.HealthStatusUnknown, "not_found"
}

// TestHealthReporterBuildServiceReport 验证 endpoint 状态会聚合为 service 健康状态。
func TestHealthReporterBuildServiceReport(t *testing.T) {
	t.Parallel()

	reporter := NewHealthReporter(HealthReporterOptions{
		Probe: &testEndpointHealthProbe{
			resultByEndpointID: map[string]pb.HealthStatus{
				"ep-1": pb.HealthStatusHealthy,
				"ep-2": pb.HealthStatusUnhealthy,
			},
		},
		Now: func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	report := reporter.BuildServiceReport(context.Background(), adapter.LocalRegistration{
		ServiceID:   "svc-3001",
		ServiceKey:  "dev/demo/order-service",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		Endpoints: []pb.ServiceEndpoint{
			{EndpointID: "ep-1", Protocol: "http", Host: "127.0.0.1", Port: 18080},
			{EndpointID: "ep-2", Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	if report.ServiceID != "svc-3001" {
		t.Fatalf("unexpected service_id: %s", report.ServiceID)
	}
	if report.ServiceHealthStatus != pb.HealthStatusHealthy {
		t.Fatalf("unexpected aggregated health: got=%s want=%s", report.ServiceHealthStatus, pb.HealthStatusHealthy)
	}
	if len(report.EndpointStatuses) != 2 {
		t.Fatalf("unexpected endpoint status count: got=%d want=2", len(report.EndpointStatuses))
	}
}

// TestHealthReporterBuildReports 验证批量构建会输出等量报告。
func TestHealthReporterBuildReports(t *testing.T) {
	t.Parallel()

	reporter := NewHealthReporter(HealthReporterOptions{
		Probe: &testEndpointHealthProbe{
			resultByEndpointID: map[string]pb.HealthStatus{
				"ep-1": pb.HealthStatusHealthy,
				"ep-2": pb.HealthStatusUnhealthy,
			},
		},
		Now: func() time.Time { return time.Unix(1700000010, 0).UTC() },
	})
	reports := reporter.BuildReports(context.Background(), []adapter.LocalRegistration{
		{
			ServiceID:   "svc-1",
			ServiceKey:  "dev/demo/s1",
			Namespace:   "dev",
			Environment: "demo",
			ServiceName: "s1",
			Endpoints: []pb.ServiceEndpoint{
				{EndpointID: "ep-1", Protocol: "tcp", Host: "127.0.0.1", Port: 18080},
			},
		},
		{
			ServiceID:   "svc-2",
			ServiceKey:  "dev/demo/s2",
			Namespace:   "dev",
			Environment: "demo",
			ServiceName: "s2",
			Endpoints: []pb.ServiceEndpoint{
				{EndpointID: "ep-2", Protocol: "tcp", Host: "127.0.0.1", Port: 18081},
			},
		},
	})
	if len(reports) != 2 {
		t.Fatalf("unexpected report count: got=%d want=2", len(reports))
	}
	if reports[0].ServiceID != "svc-1" {
		t.Fatalf("unexpected first report service_id: %s", reports[0].ServiceID)
	}
	if reports[1].ServiceID != "svc-2" {
		t.Fatalf("unexpected second report service_id: %s", reports[1].ServiceID)
	}
}
