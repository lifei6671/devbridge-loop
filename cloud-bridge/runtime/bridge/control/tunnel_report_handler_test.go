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
		IdleCount:       0,
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
