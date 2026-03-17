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
		[]pb.Service{
			{
				ServiceID:    "svc-order",
				ServiceKey:   "dev/demo/order-service",
				Namespace:    "dev",
				Environment:  "demo",
				ConnectorID:  "agent-a",
				ServiceName:  "order-service",
				ServiceType:  "https",
				Status:       pb.ServiceStatusActive,
				HealthStatus: pb.HealthStatusHealthy,
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
	if item.RouteTarget != "connector_service.service_key=dev/demo/order-service" {
		testingObject.Fatalf("unexpected route target: %+v", item.RouteTarget)
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
		[]pb.Service{{ServiceID: "svc-a"}},
		[]pb.Route{{RouteID: "route-a"}},
		registry.TunnelSnapshot{IdleCount: 2, ActiveCount: 1, BrokenCount: 0},
		map[string]any{
			"ingress": map[string]any{
				"http_addr": ":38080",
				"grpc_addr": "127.0.0.1:38081",
			},
			"admin": map[string]any{
				"enabled":     true,
				"listen_addr": ":39081",
			},
			"control_plane": map[string]any{
				"listen_addr":         ":39080",
				"grpc_h2_listen_addr": ":39082",
			},
		},
	)

	if len(overview.Listeners) != 5 {
		testingObject.Fatalf("unexpected listener size: got=%d want=5", len(overview.Listeners))
	}
	if overview.Listeners[0].ListenerID != "ingress_http" || overview.Listeners[0].Port != "38080" {
		testingObject.Fatalf("unexpected ingress http listener: %+v", overview.Listeners[0])
	}
	if overview.Listeners[1].ListenerID != "ingress_grpc" || overview.Listeners[1].Port != "38081" {
		testingObject.Fatalf("unexpected ingress grpc listener: %+v", overview.Listeners[1])
	}
	if overview.Listeners[4].ListenerID != "admin_ui_api" || overview.Listeners[4].Port != "39081" {
		testingObject.Fatalf("unexpected admin listener: %+v", overview.Listeners[4])
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
