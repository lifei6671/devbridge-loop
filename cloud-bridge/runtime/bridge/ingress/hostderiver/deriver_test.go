package hostderiver

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestDeriveBuildsExpectedHost 验证 Host 派生遵循默认模板并归一化 label。
func TestDeriveBuildsExpectedHost(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	deriver := New("Example.COM", metrics)
	host, err := deriver.Derive("Order_Service", pb.Scope{
		Namespace:   "Dev Team",
		Environment: "Alice",
	})
	if err != nil {
		testingObject.Fatalf("derive host failed: %v", err)
	}
	if host != "order-service.alice.dev-team.example.com" {
		testingObject.Fatalf("unexpected derived host: got=%s want=%s", host, "order-service.alice.dev-team.example.com")
	}
	if metrics.BridgeHostDeriveTotal(true) != 1 {
		testingObject.Fatalf("unexpected success metric: got=%d want=1", metrics.BridgeHostDeriveTotal(true))
	}
}

// TestDeriveRejectsEmptyBaseDomain 验证缺失 base_domain 时会拒绝派生并记录失败指标。
func TestDeriveRejectsEmptyBaseDomain(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	deriver := New("", metrics)
	_, err := deriver.Derive("order-service", pb.Scope{
		Namespace:   "dev",
		Environment: "alice",
	})
	if err == nil {
		testingObject.Fatalf("expected derive host error")
	}
	if metrics.BridgeHostDeriveTotal(false) != 1 {
		testingObject.Fatalf("unexpected failure metric: got=%d want=1", metrics.BridgeHostDeriveTotal(false))
	}
}
