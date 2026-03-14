package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestTunnelReportHandlerHandleReport 验证 tunnel 池上报可触发补池请求。
func TestTunnelReportHandlerHandleReport(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       9,
		State:       registry.SessionActive,
	})
	handler := NewTunnelReportHandler(TunnelReportHandlerOptions{
		SessionRegistry: sessionRegistry,
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
		TargetIdleCount: 8,
		Trigger:         "event:pool_low",
	})
	if !shouldSend {
		t.Fatalf("expected refill request generated")
	}
	if refillRequest.RequestedIdleDelta <= 0 {
		t.Fatalf("unexpected refill delta: %d", refillRequest.RequestedIdleDelta)
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
