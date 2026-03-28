package adminview

import (
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestBuildTunnelSummaryFromRuntimesUsesLatestTunnelUpdate
// 验证过滤聚合的 updated_at_ms 来源于 runtime 最新更新时间，而不是当前请求时间。
func TestBuildTunnelSummaryFromRuntimesUsesLatestTunnelUpdate(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_000_000, 0).UTC()
	oldestUpdate := now.Add(-30 * time.Minute)
	latestUpdate := now.Add(-5 * time.Minute)

	summary := BuildTunnelSummaryFromRuntimes(now, []registry.TunnelRuntime{
		{
			TunnelID:  "tunnel-a",
			State:     registry.TunnelStateIdle,
			UpdatedAt: oldestUpdate,
		},
		{
			TunnelID:  "tunnel-b",
			State:     registry.TunnelStateActive,
			UpdatedAt: latestUpdate,
		},
	})

	if summary.UpdatedAtMS != uint64(latestUpdate.UnixMilli()) {
		testingObject.Fatalf(
			"unexpected updated_at_ms: got=%d want=%d",
			summary.UpdatedAtMS,
			uint64(latestUpdate.UnixMilli()),
		)
	}
}

// TestBuildServiceItemsIncludesConnectorAndAccessHint
// 验证服务明细可关联 connector/session，并输出 route+sni 访问提示。
func TestBuildServiceItemsIncludesConnectorAndAccessHint(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_100_000, 0).UTC()
	sessionUpdatedAt := now.Add(-2 * time.Minute)
	items := BuildServiceItems(
		now,
		[]pb.LogicalService{
			{
				LogicalServiceID:     "ls-order",
				ServiceName:          "order-service",
				Scope:                pb.Scope{Namespace: "dev", Environment: "demo"},
				Status:               pb.ServiceStatusActive,
				ActiveInstanceCount:  1,
				HealthyInstanceCount: 1,
			},
		},
		[]pb.ServiceInstance{
			{
				InstanceID:       "inst-order-1",
				LogicalServiceID: "ls-order",
				ConnectorID:      "agent-a",
				SessionID:        "session-a",
				InstanceStatus:   pb.ServiceStatusActive,
				HealthStatus:     pb.HealthStatusHealthy,
				Endpoints: []pb.ServiceEndpoint{
					{
						EndpointID: "ep-order-1",
						Protocol:   "https",
						Host:       "127.0.0.1",
						Port:       18080,
						ServerName: "order.demo.example.com",
					},
				},
			},
		},
		[]registry.SessionRuntime{
			{
				SessionID:   "session-a",
				ConnectorID: "agent-a",
				Epoch:       3,
				State:       registry.SessionActive,
				UpdatedAt:   sessionUpdatedAt,
			},
		},
	)

	if len(items) != 1 {
		testingObject.Fatalf("unexpected service item size: got=%d want=1", len(items))
	}
	item := items[0]
	if item.ConnectorID != "agent-a" || item.SessionID != "session-a" {
		testingObject.Fatalf("unexpected service-session mapping: %+v", item)
	}
	if item.EndpointAddress != "127.0.0.1:18080" {
		testingObject.Fatalf("unexpected endpoint address: %+v", item.EndpointAddress)
	}
	if item.SNIName != "order.demo.example.com" {
		testingObject.Fatalf("unexpected sni name: %+v", item.SNIName)
	}
	if item.RouteTarget != "connector_service.selector={serviceName:order-service,scope:dev/demo}" {
		testingObject.Fatalf("unexpected route target: %+v", item.RouteTarget)
	}
	if item.Scope.Namespace != "dev" || item.Scope.Environment != "demo" {
		testingObject.Fatalf("unexpected scope: %+v", item.Scope)
	}
	if item.UpdatedAtMS != uint64(sessionUpdatedAt.UnixMilli()) {
		testingObject.Fatalf(
			"unexpected updated_at_ms: got=%d want=%d",
			item.UpdatedAtMS,
			uint64(sessionUpdatedAt.UnixMilli()),
		)
	}
}

// TestBuildBridgeOverviewIncludesActiveListeners 验证 overview 会附带当前启用的监听地址、端口和用途。
func TestBuildBridgeOverviewIncludesActiveListeners(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_200_000, 0).UTC()
	overview := BuildBridgeOverview(
		now,
		[]registry.SessionRuntime{
			{SessionID: "session-a", ConnectorID: "agent-a", State: registry.SessionActive},
		},
		[]pb.LogicalService{{LogicalServiceID: "ls-a"}},
		[]pb.Route{{RouteID: "route-a"}},
		registry.TunnelSnapshot{IdleCount: 2, ActiveCount: 1, BrokenCount: 0},
		map[string]any{
			"ingress": map[string]any{
				"http_addr": ":38080",
				"grpc_addr": "127.0.0.1:38081",
			},
			"admin": map[string]any{
				"enabled":     true,
				"listen_addr": ":39080",
			},
			"control_plane": map[string]any{
				"listen_addr":         ":39081",
				"grpc_h2_listen_addr": ":39082",
				"quic_listen_addr":    ":39083",
				"tls_mode":            "required",
			},
		},
	)

	if len(overview.Listeners) != 6 {
		testingObject.Fatalf("unexpected listener size: got=%d want=6", len(overview.Listeners))
	}
	if overview.Listeners[0].ListenerID != "ingress_http" || overview.Listeners[0].Port != "38080" {
		testingObject.Fatalf("unexpected ingress http listener: %+v", overview.Listeners[0])
	}
	if overview.Listeners[1].ListenerID != "ingress_grpc" || overview.Listeners[1].Port != "38081" {
		testingObject.Fatalf("unexpected ingress grpc listener: %+v", overview.Listeners[1])
	}
	if overview.Listeners[4].ListenerID != "control_plane_quic" || overview.Listeners[4].Port != "39083" {
		testingObject.Fatalf("unexpected quic listener: %+v", overview.Listeners[4])
	}
	if overview.Listeners[5].ListenerID != "admin_ui_api" || overview.Listeners[5].Port != "39080" {
		testingObject.Fatalf("unexpected admin listener: %+v", overview.Listeners[4])
	}
}

func TestBuildSessionAndTunnelItemsIncludeBinding(testingObject *testing.T) {
	testingObject.Parallel()

	sessions := BuildSessionItems([]registry.SessionRuntime{
		{
			SessionID:   "session-quic-1",
			ConnectorID: "agent-quic",
			Epoch:       7,
			Binding:     "quic_native",
			State:       registry.SessionActive,
			UpdatedAt:   time.Unix(1_900_250_000, 0).UTC(),
		},
	})
	if len(sessions) != 1 {
		testingObject.Fatalf("unexpected session item count: got=%d want=1", len(sessions))
	}
	if sessions[0].Binding != "quic_native" {
		testingObject.Fatalf("unexpected session binding: %+v", sessions[0])
	}

	tunnels := BuildTunnelItems([]registry.TunnelRuntime{
		{
			TunnelID:    "tun-quic-1",
			ConnectorID: "agent-quic",
			SessionID:   "session-quic-1",
			Binding:     "quic_native",
			State:       registry.TunnelStateIdle,
			UpdatedAt:   time.Unix(1_900_250_010, 0).UTC(),
		},
	})
	if len(tunnels) != 1 {
		testingObject.Fatalf("unexpected tunnel item count: got=%d want=1", len(tunnels))
	}
	if tunnels[0].Binding != "quic_native" {
		testingObject.Fatalf("unexpected tunnel binding: %+v", tunnels[0])
	}
}

// TestBuildTrafficSummaryIncludesAuthAndTLSMetrics 验证 traffic 汇总会暴露认证与 TLS 拒绝指标。
func TestBuildTrafficSummaryIncludesAuthAndTLSMetrics(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_300_000, 0).UTC()
	metrics := obs.NewMetrics()
	metrics.IncBridgeAuthSuccessTotal()
	metrics.IncBridgeAuthSupersedeTotal()
	metrics.IncBridgeAuthRateLimitTotal()
	metrics.ObserveBridgeAuthFailure("auth_invalid_token")
	metrics.IncBridgeTLSRejectPlaintextOnRequiredTotal()
	metrics.IncBridgeTLSRejectTLSOnPlaintextTotal()
	metrics.ObserveBridgeTunnelRecycleFailure(ltfperrors.CodeTunnelRecycleCloseAckRequired)
	metrics.IncBridgeQUICConnectionAcceptTotal()
	metrics.AddBridgeQUICConnectionActive(2)
	metrics.IncBridgeQUICConnectionAuthenticatedTotal()
	metrics.IncBridgeQUICTunnelRegisteredTotal()

	summary := BuildTrafficSummary(now, metrics)
	if summary.AuthSuccessTotal != 1 || summary.AuthFailureTotal != 1 {
		testingObject.Fatalf("unexpected auth summary totals: %+v", summary)
	}
	if summary.AuthRateLimitTotal != 1 || summary.AuthSupersedeTotal != 1 {
		testingObject.Fatalf("unexpected auth takeover totals: %+v", summary)
	}
	if summary.TLSRejectPlaintextOnRequiredTotal != 1 || summary.TLSRejectTLSOnPlaintextTotal != 1 {
		testingObject.Fatalf("unexpected tls reject totals: %+v", summary)
	}
	if summary.TunnelRecycleFailureTotal != 1 {
		testingObject.Fatalf("unexpected recycle failure totals: %+v", summary)
	}
	if summary.QUICConnectionAcceptTotal != 1 ||
		summary.QUICConnectionActive != 2 ||
		summary.QUICConnectionAuthenticatedTotal != 1 ||
		summary.QUICTunnelRegisteredTotal != 1 {
		testingObject.Fatalf("unexpected quic metric totals: %+v", summary)
	}
	if summary.AuthErrorCodeTotals["auth_invalid_token"] != 1 {
		testingObject.Fatalf("unexpected auth error code totals: %+v", summary.AuthErrorCodeTotals)
	}
	if summary.TunnelRecycleErrorCodeTotals[ltfperrors.CodeTunnelRecycleCloseAckRequired] != 1 {
		testingObject.Fatalf("unexpected recycle error code totals: %+v", summary.TunnelRecycleErrorCodeTotals)
	}
	if summary.UpdatedAtMS != uint64(now.UnixMilli()) {
		testingObject.Fatalf("unexpected updated_at_ms: got=%d want=%d", summary.UpdatedAtMS, uint64(now.UnixMilli()))
	}
}

// TestBuildDiagnoseSummaryFlagsAuthFailure 验证诊断摘要会在出现认证失败时输出安全排查提示。
func TestBuildDiagnoseSummaryFlagsAuthFailure(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_400_000, 0).UTC()
	metrics := obs.NewMetrics()
	metrics.ObserveBridgeAuthFailure("auth_invalid_token")

	diagnose := BuildDiagnoseSummary(now, nil, registry.TunnelSnapshot{}, metrics)
	if diagnose.Health != "degraded" {
		testingObject.Fatalf("unexpected diagnose health: got=%s want=%s", diagnose.Health, "degraded")
	}
	if len(diagnose.Issues) == 0 {
		testingObject.Fatalf("expected auth failure diagnose issue")
	}
}

// TestBuildDiagnoseSummaryFlagsTunnelRecycleFailure 验证诊断摘要会在 recycle 失败时输出针对性提示。
func TestBuildDiagnoseSummaryFlagsTunnelRecycleFailure(testingObject *testing.T) {
	testingObject.Parallel()

	now := time.Unix(1_900_500_000, 0).UTC()
	metrics := obs.NewMetrics()
	metrics.ObserveBridgeTunnelRecycleFailure(ltfperrors.CodeTunnelRecycleCloseAckRequired)

	diagnose := BuildDiagnoseSummary(now, nil, registry.TunnelSnapshot{}, metrics)
	if diagnose.Health != "degraded" {
		testingObject.Fatalf("unexpected diagnose health: got=%s want=%s", diagnose.Health, "degraded")
	}
	if len(diagnose.Issues) == 0 {
		testingObject.Fatalf("expected recycle failure diagnose issue")
	}
	if !strings.Contains(diagnose.Issues[0], "close_ack_required") && !strings.Contains(diagnose.Issues[0], "close_ack") {
		testingObject.Fatalf("expected recycle diagnose issue mentions close_ack, got=%+v", diagnose.Issues)
	}
}
