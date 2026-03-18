package control

import (
	"context"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type tunnelReportHandlerTestTunnel struct {
	tunnelID string
	closed   bool
}

func (tunnel *tunnelReportHandlerTestTunnel) ID() string {
	return tunnel.tunnelID
}

func (tunnel *tunnelReportHandlerTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	_ = ctx
	return pb.StreamPayload{}, nil
}

func (tunnel *tunnelReportHandlerTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	_ = ctx
	_ = payload
	return nil
}

func (tunnel *tunnelReportHandlerTestTunnel) Close() error {
	tunnel.closed = true
	return nil
}

// TestTunnelReportHandlerHandleReport 验证 tunnel 池上报可触发补池请求。
func TestTunnelReportHandlerHandleReport(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       9,
		State:       registry.SessionActive,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-1", &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-idle"}); err != nil {
		t.Fatalf("upsert idle tunnel failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-1", "session-1", &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-active"}); err != nil {
		t.Fatalf("upsert active tunnel failed: %v", err)
	}
	acquiredRuntime, ok := tunnelRegistry.AcquireIdle(now, "connector-1")
	if !ok {
		t.Fatalf("expected acquire idle tunnel success")
	}
	if err := tunnelRegistry.MarkActive(now, acquiredRuntime.TunnelID, "traffic-1"); err != nil {
		t.Fatalf("mark active failed: %v", err)
	}
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
		TunnelRegistry:  tunnelRegistry,
		RefillController: NewRefillController(RefillControllerOptions{
			Now: func() time.Time { return time.Unix(1700003000, 0).UTC() },
		}),
	})

	refillRequest, shouldSend := handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-1",
		SessionEpoch: 9,
	}, pb.TunnelPoolReport{
		IdleCount:       1,
		InUseCount:      1,
		TargetIdleCount: 8,
		Trigger:         "event:pool_low",
	})
	if !shouldSend {
		t.Fatalf("expected refill request generated")
	}
	if refillRequest.RequestedIdleDelta <= 0 {
		t.Fatalf("unexpected refill delta: %d", refillRequest.RequestedIdleDelta)
	}
	if refillRequest.Metadata["bridge_idle_count"] != "1" || refillRequest.Metadata["bridge_in_use_count"] != "1" {
		t.Fatalf("unexpected bridge pool metadata: %+v", refillRequest.Metadata)
	}
	if refillRequest.Metadata["bridge_idle_recycled_count"] != "0" {
		t.Fatalf("unexpected recycled metadata: %+v", refillRequest.Metadata)
	}
}

// TestTunnelReportHandlerRejectStaleEpoch 验证旧会话代际上报不会触发补池。
func TestTunnelReportHandlerRejectStaleEpoch(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-2",
		ConnectorID: "connector-1",
		Epoch:       10,
		State:       registry.SessionActive,
	})
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
	})
	_, shouldSend := handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-2",
		SessionEpoch: 9,
	}, pb.TunnelPoolReport{
		IdleCount:       0,
		TargetIdleCount: 6,
		Trigger:         "event:pool_low",
	})
	if shouldSend {
		t.Fatalf("stale epoch should not trigger refill request")
	}
}

// TestTunnelReportHandlerRejectNonActiveSession 验证 draining 会话上报不会触发补池。
func TestTunnelReportHandlerRejectNonActiveSession(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-draining",
		ConnectorID: "connector-1",
		Epoch:       10,
		State:       registry.SessionDraining,
	})
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
	})
	_, shouldSend := handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-draining",
		SessionEpoch: 10,
	}, pb.TunnelPoolReport{
		IdleCount:       0,
		TargetIdleCount: 6,
		Trigger:         "event:pool_low",
	})
	if shouldSend {
		t.Fatalf("non-active session should not trigger refill request")
	}
}

// TestTunnelReportHandlerWritesReportStore 验证有效上报会写入 tunnel 池快照存储。
func TestTunnelReportHandlerWritesReportStore(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-3",
		ConnectorID: "connector-3",
		Epoch:       7,
		State:       registry.SessionActive,
	})
	reportStore := NewTunnelPoolReportStore()
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
		ReportStore:     reportStore,
	})
	handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-3",
		SessionEpoch: 7,
		ConnectorID:  "connector-3",
	}, pb.TunnelPoolReport{
		IdleCount:       4,
		InUseCount:      2,
		TargetIdleCount: 8,
		Trigger:         "periodic",
		TimestampUnix:   time.Now().UTC().Unix(),
	})
	items := reportStore.List()
	if len(items) != 1 {
		t.Fatalf("unexpected report store size: got=%d want=1", len(items))
	}
	if items[0].ConnectorID != "connector-3" || items[0].SessionID != "session-3" {
		t.Fatalf("unexpected report identity: %+v", items[0])
	}
	if items[0].IdleCount != 4 || items[0].InUseCount != 2 {
		t.Fatalf("unexpected report counts: %+v", items[0])
	}
}

// TestTunnelReportHandlerReconcileExcessIdle 验证 Agent 上报会触发 Bridge 端 idle 收敛。
func TestTunnelReportHandlerReconcileExcessIdle(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-4",
		ConnectorID: "connector-4",
		Epoch:       12,
		State:       registry.SessionActive,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnelA := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-a"}
	tunnelB := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-b"}
	tunnelC := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-c"}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-4", "session-4", tunnelA); err != nil {
		t.Fatalf("upsert tunnel-a failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now.Add(time.Millisecond), "connector-4", "session-4", tunnelB); err != nil {
		t.Fatalf("upsert tunnel-b failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now.Add(2*time.Millisecond), "connector-4", "session-4", tunnelC); err != nil {
		t.Fatalf("upsert tunnel-c failed: %v", err)
	}
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
		TunnelRegistry:  tunnelRegistry,
		RefillController: NewRefillController(RefillControllerOptions{
			Now: func() time.Time { return time.Unix(1700003000, 0).UTC() },
		}),
	})

	_, shouldSend := handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-4",
		SessionEpoch: 12,
	}, pb.TunnelPoolReport{
		IdleCount:       1,
		InUseCount:      0,
		TargetIdleCount: 0,
		Trigger:         "periodic",
	})
	if shouldSend {
		t.Fatalf("expected no refill request when target_idle_count is zero")
	}

	snapshot := tunnelRegistry.Snapshot()
	if snapshot.IdleCount != 1 || snapshot.TotalCount != 1 {
		t.Fatalf("unexpected registry snapshot after reconcile: %+v", snapshot)
	}
	closedCount := 0
	for _, tunnel := range []*tunnelReportHandlerTestTunnel{tunnelA, tunnelB, tunnelC} {
		if tunnel.closed {
			closedCount++
		}
	}
	if closedCount != 2 {
		t.Fatalf("expected 2 recycled tunnels closed, got=%d", closedCount)
	}
}

// TestTunnelReportHandlerSkipReconcileOnEventTrigger 验证 event 报告不会触发 Bridge 端 idle 强制对账回收。
func TestTunnelReportHandlerSkipReconcileOnEventTrigger(t *testing.T) {
	t.Parallel()

	now := time.Now().UTC()
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(now, registry.SessionRuntime{
		SessionID:   "session-5",
		ConnectorID: "connector-5",
		Epoch:       13,
		State:       registry.SessionActive,
	})
	tunnelRegistry := registry.NewTunnelRegistry()
	tunnelA := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-a"}
	tunnelB := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-b"}
	tunnelC := &tunnelReportHandlerTestTunnel{tunnelID: "tunnel-c"}
	if _, err := tunnelRegistry.UpsertIdle(now, "connector-5", "session-5", tunnelA); err != nil {
		t.Fatalf("upsert tunnel-a failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now.Add(time.Millisecond), "connector-5", "session-5", tunnelB); err != nil {
		t.Fatalf("upsert tunnel-b failed: %v", err)
	}
	if _, err := tunnelRegistry.UpsertIdle(now.Add(2*time.Millisecond), "connector-5", "session-5", tunnelC); err != nil {
		t.Fatalf("upsert tunnel-c failed: %v", err)
	}
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
		TunnelRegistry:  tunnelRegistry,
	})

	_, shouldSend := handler.HandleReport(pb.ControlEnvelope{
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-5",
		SessionEpoch: 13,
	}, pb.TunnelPoolReport{
		IdleCount:       1,
		InUseCount:      0,
		TargetIdleCount: 0,
		Trigger:         "event:tunnel_closed",
	})
	if shouldSend {
		t.Fatalf("expected no refill request when target_idle_count is zero")
	}
	snapshot := tunnelRegistry.Snapshot()
	if snapshot.IdleCount != 3 || snapshot.TotalCount != 3 {
		t.Fatalf("unexpected registry snapshot after event trigger: %+v", snapshot)
	}
	for _, tunnel := range []*tunnelReportHandlerTestTunnel{tunnelA, tunnelB, tunnelC} {
		if tunnel.closed {
			t.Fatalf("expected no tunnel recycled on event trigger")
		}
	}
}
