package connectorproxy

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestWriteTrafficCloseAndAwaitAckHandlesPeerClose 验证收到对端 close 时会回 ack 并结束握手。
func TestWriteTrafficCloseAndAwaitAckHandlesPeerClose(testingObject *testing.T) {
	testingObject.Parallel()

	testTunnel := newConnectorProxyTestTunnel("tunnel-close-peer")
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: "traffic-1",
			Reason:    "agent_close_first",
		},
	})

	err := WriteTrafficCloseAndAwaitAck(
		context.Background(),
		testTunnel,
		"traffic-1",
		"bridge_close_after_body",
		500*time.Millisecond,
	)
	if err != nil {
		testingObject.Fatalf("write traffic close and await ack failed: %v", err)
	}

	writes := testTunnel.Writes()
	if len(writes) != 2 {
		testingObject.Fatalf("expected close+close_ack writes, got=%d", len(writes))
	}
	if writes[0].Close == nil || strings.TrimSpace(writes[0].Close.TrafficID) != "traffic-1" {
		testingObject.Fatalf("expected first write to be close for traffic-1")
	}
	if writes[1].CloseAck == nil || strings.TrimSpace(writes[1].CloseAck.TrafficID) != "traffic-1" || !writes[1].CloseAck.Accepted {
		testingObject.Fatalf("expected second write to be accepted close_ack for traffic-1")
	}
}

// TestWriteTrafficCloseAndAwaitAckReturnsResetError 验证握手阶段收到 reset 会返回错误并终止。
func TestWriteTrafficCloseAndAwaitAckReturnsResetError(testingObject *testing.T) {
	testingObject.Parallel()

	testTunnel := newConnectorProxyTestTunnel("tunnel-close-reset")
	testTunnel.EnqueueReadPayload(pb.StreamPayload{
		Reset: &pb.TrafficReset{
			TrafficID:    "traffic-2",
			ErrorCode:    "UPSTREAM_FAIL",
			ErrorMessage: "upstream disconnected",
		},
	})

	err := WriteTrafficCloseAndAwaitAck(
		context.Background(),
		testTunnel,
		"traffic-2",
		"bridge_close_after_body",
		500*time.Millisecond,
	)
	if err == nil {
		testingObject.Fatalf("expected reset error, got nil")
	}
	if !strings.Contains(err.Error(), "relay reset") {
		testingObject.Fatalf("expected relay reset error, got=%v", err)
	}

	writes := testTunnel.Writes()
	if len(writes) != 1 || writes[0].Close == nil {
		testingObject.Fatalf("expected only close write before reset, got=%+v", writes)
	}
}
