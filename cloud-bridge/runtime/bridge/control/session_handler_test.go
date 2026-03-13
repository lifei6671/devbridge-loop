package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestSessionHandlerBuildReconnectSyncPlan 验证重连后 full-sync/增量策略判定。
func TestSessionHandlerBuildReconnectSyncPlan(t *testing.T) {
	t.Parallel()

	handler := NewSessionHandler(SessionHandlerOptions{})

	initial := handler.BuildReconnectSyncPlan("session-1", 1)
	if !initial.NeedFullSync || initial.Reason != "initial_sync" {
		t.Fatalf("unexpected initial plan: %+v", initial)
	}

	handler.MarkReconnectBaseline("session-1", 1, 9)
	delta := handler.BuildReconnectSyncPlan("session-1", 1)
	if delta.NeedFullSync || delta.Reason != "delta_sync" || delta.SinceResourceVersion != 9 {
		t.Fatalf("unexpected delta plan: %+v", delta)
	}

	epochAdvanced := handler.BuildReconnectSyncPlan("session-1", 2)
	if !epochAdvanced.NeedFullSync || epochAdvanced.Reason != "session_epoch_advanced" {
		t.Fatalf("unexpected epoch advanced plan: %+v", epochAdvanced)
	}
}

// TestSessionHandlerApplyFullSyncSnapshot 验证 full-sync 快照覆盖与版本对账。
func TestSessionHandlerApplyFullSyncSnapshot(t *testing.T) {
	t.Parallel()

	guard := consistency.NewResourceEventGuard(32)
	serviceRegistry := registry.NewServiceRegistry()
	routeRegistry := registry.NewRouteRegistry()
	handler := NewSessionHandler(SessionHandlerOptions{
		ServiceRegistry: serviceRegistry,
		RouteRegistry:   routeRegistry,
		Guard:           guard,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})

	snapshot := pb.FullSyncSnapshot{
		RequestID:       "full-sync-1",
		SessionEpoch:    3,
		SnapshotVersion: 11,
		Services: []pb.Service{
			{
				ServiceID:       "svc-1",
				ServiceKey:      "dev/alice/order-service",
				ResourceVersion: 10,
			},
		},
		Routes: []pb.Route{
			{
				RouteID:         "route-1",
				ResourceVersion: 11,
			},
		},
		Completed: true,
	}

	handler.ApplyFullSyncSnapshot(snapshot)

	if services := serviceRegistry.List(); len(services) != 1 {
		t.Fatalf("unexpected service size: got=%d want=1", len(services))
	}
	if routes := routeRegistry.List(); len(routes) != 1 {
		t.Fatalf("unexpected route size: got=%d want=1", len(routes))
	}
	versions := guard.SnapshotVersions()
	if versions["service:svc-1"] != 10 {
		t.Fatalf("unexpected service version: got=%d want=10", versions["service:svc-1"])
	}
	if versions["route:route-1"] != 11 {
		t.Fatalf("unexpected route version: got=%d want=11", versions["route:route-1"])
	}

	_, baselineEpoch, baselineVersion := handler.CurrentBaseline()
	if baselineEpoch != 3 || baselineVersion != 11 {
		t.Fatalf("unexpected baseline: epoch=%d version=%d", baselineEpoch, baselineVersion)
	}
}
