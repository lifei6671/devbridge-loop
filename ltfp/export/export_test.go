package export

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestCheckEligibilitySuccess 验证 export 准入条件全部满足时返回成功。
func TestCheckEligibilitySuccess(t *testing.T) {
	t.Parallel()

	err := CheckEligibility(EligibilityInput{
		Connector: pb.Connector{
			ConnectorID: "conn-001",
			Status:      "online",
		},
		Session: pb.Session{
			SessionID:   "sess-001",
			ConnectorID: "conn-001",
			State:       pb.SessionStateActive,
		},
		Service: pb.Service{
			ServiceID:    "svc-001",
			ConnectorID:  "conn-001",
			Status:       pb.ServiceStatusActive,
			HealthStatus: pb.HealthStatusHealthy,
			Exposure: pb.ServiceExposure{
				AllowExport: true,
			},
			DiscoveryPolicy: pb.DiscoveryPolicy{
				Enabled: true,
			},
		},
		HasReachableIngress: true,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestCheckEligibilityRejectUnhealthy 验证 unhealthy service 不允许 export。
func TestCheckEligibilityRejectUnhealthy(t *testing.T) {
	t.Parallel()

	err := CheckEligibility(EligibilityInput{
		Connector: pb.Connector{Status: "online"},
		Session: pb.Session{
			SessionID:   "sess-001",
			ConnectorID: "conn-001",
			State:       pb.SessionStateActive,
		},
		Service: pb.Service{
			ConnectorID:  "conn-001",
			Status:       pb.ServiceStatusActive,
			HealthStatus: pb.HealthStatusUnhealthy,
			Exposure: pb.ServiceExposure{
				AllowExport: true,
			},
			DiscoveryPolicy: pb.DiscoveryPolicy{
				Enabled: true,
			},
		},
		HasReachableIngress: true,
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeExportNotEligible) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildEndpointL7Shared 验证 l7_shared 导出地址规则。
func TestBuildEndpointL7Shared(t *testing.T) {
	t.Parallel()

	result, err := BuildEndpoint(pb.Service{
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			Host:        "api.dev.example.com",
		},
	}, EndpointBuildOptions{
		SharedPort: 443,
	})
	if err != nil {
		t.Fatalf("build endpoint failed: %v", err)
	}
	if result.Address != "api.dev.example.com:443" {
		t.Fatalf("unexpected endpoint: %s", result.Address)
	}
}

// TestBuildEndpointTLSSNIShared 验证 tls_sni_shared 导出地址与 sni metadata。
func TestBuildEndpointTLSSNIShared(t *testing.T) {
	t.Parallel()

	result, err := BuildEndpoint(pb.Service{
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeTLSSNIShared,
			SNIName:     "order.dev.example.com",
		},
	}, EndpointBuildOptions{
		GatewayHost: "tls.dev.example.com",
		SharedPort:  443,
	})
	if err != nil {
		t.Fatalf("build endpoint failed: %v", err)
	}
	if result.Address != "tls.dev.example.com:443" || result.Metadata["sni"] != "order.dev.example.com" {
		t.Fatalf("unexpected endpoint result: %+v", result)
	}
}

// TestBuildEndpointL4DedicatedPort 验证 l4_dedicated_port 导出地址规则。
func TestBuildEndpointL4DedicatedPort(t *testing.T) {
	t.Parallel()

	result, err := BuildEndpoint(pb.Service{
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL4DedicatedPort,
			ListenPort:  18081,
		},
	}, EndpointBuildOptions{
		GatewayHost: "gateway.internal",
	})
	if err != nil {
		t.Fatalf("build endpoint failed: %v", err)
	}
	if result.Address != "gateway.internal:18081" {
		t.Fatalf("unexpected endpoint: %s", result.Address)
	}
}

// TestBuildEndpointRejectUpstreamAddr 验证导出地址命中 upstream 时会被拒绝。
func TestBuildEndpointRejectUpstreamAddr(t *testing.T) {
	t.Parallel()

	_, err := BuildEndpoint(pb.Service{
		Endpoints: []pb.ServiceEndpoint{
			{Host: "api.dev.example.com", Port: 443},
		},
		Exposure: pb.ServiceExposure{
			IngressMode: pb.IngressModeL7Shared,
			Host:        "api.dev.example.com",
			ListenPort:  443,
		},
	}, EndpointBuildOptions{})
	if !ltfperrors.IsCode(err, ltfperrors.CodeExportNotEligible) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestBuildDesiredProjections 验证按 provider 列表生成 projection。
func TestBuildDesiredProjections(t *testing.T) {
	t.Parallel()

	projections := BuildDesiredProjections(pb.Service{
		ServiceID:   "svc-001",
		Namespace:   "dev",
		Environment: "alice",
		DiscoveryPolicy: pb.DiscoveryPolicy{
			Enabled:   true,
			Providers: []string{"consul", "nacos"},
			Namespace: "ns-export",
			Tags: map[string]string{
				"tier": "core",
			},
		},
	}, EndpointBuildResult{
		Address: "api.dev.example.com:443",
		Metadata: map[string]string{
			"sni": "order.dev.example.com",
		},
	})
	if len(projections) != 2 {
		t.Fatalf("unexpected projection count: %d", len(projections))
	}
	// providers 会排序，确保 reconcile 结果稳定。
	if projections[0].Provider != "consul" || projections[1].Provider != "nacos" {
		t.Fatalf("unexpected projection order: %+v", projections)
	}
}

// TestBuildReconcilePlan 验证 projection 的 create/update/delete 计算。
func TestBuildReconcilePlan(t *testing.T) {
	t.Parallel()

	current := []pb.DiscoveryProjection{
		{
			ProjectionID: "p-1",
			Provider:     "nacos",
			ExportedAddr: "old:443",
		},
		{
			ProjectionID: "p-2",
			Provider:     "consul",
			ExportedAddr: "keep:443",
		},
	}
	desired := []pb.DiscoveryProjection{
		{
			ProjectionID: "p-1",
			Provider:     "nacos",
			ExportedAddr: "new:443",
		},
		{
			ProjectionID: "p-3",
			Provider:     "etcd",
			ExportedAddr: "add:443",
		},
	}
	plan := BuildReconcilePlan(current, desired)
	if len(plan.Create) != 1 || plan.Create[0].ProjectionID != "p-3" {
		t.Fatalf("unexpected create plan: %+v", plan.Create)
	}
	if len(plan.Update) != 1 || plan.Update[0].ProjectionID != "p-1" {
		t.Fatalf("unexpected update plan: %+v", plan.Update)
	}
	if len(plan.Delete) != 1 || plan.Delete[0].ProjectionID != "p-2" {
		t.Fatalf("unexpected delete plan: %+v", plan.Delete)
	}
}
