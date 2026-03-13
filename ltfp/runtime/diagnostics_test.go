package runtime

import (
	"errors"
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestBuildTrafficDiagnosticFields 验证 traffic 维度诊断字段输出。
func TestBuildTrafficDiagnosticFields(testingObject *testing.T) {
	tunnel := newMockTunnel("tunnel-traffic")
	tunnel.state = transport.TunnelStateActive
	tunnel.lastError = errors.New("transport timeout")

	fields := BuildTrafficDiagnosticFields(
		TrafficMeta{
			TrafficID:    "traffic-1",
			ServiceID:    "svc-1",
			SessionID:    "session-1",
			SessionEpoch: 9,
		},
		tunnel,
		TrafficFrameData,
		errors.New("open ack timeout"),
	)
	if fields.TrafficID != "traffic-1" {
		testingObject.Fatalf("unexpected traffic id: %s", fields.TrafficID)
	}
	if fields.ServiceID != "svc-1" {
		testingObject.Fatalf("unexpected service id: %s", fields.ServiceID)
	}
	if fields.SessionID != "session-1" {
		testingObject.Fatalf("unexpected session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 9 {
		testingObject.Fatalf("unexpected session epoch: %d", fields.SessionEpoch)
	}
	if fields.TunnelID != "tunnel-traffic" {
		testingObject.Fatalf("unexpected tunnel id: %s", fields.TunnelID)
	}
	if fields.Binding != transport.BindingTypeGRPCH2.String() {
		testingObject.Fatalf("unexpected binding: %s", fields.Binding)
	}
	if fields.FrameType != TrafficFrameData {
		testingObject.Fatalf("unexpected frame type: %s", fields.FrameType)
	}
	if fields.LastError != "open ack timeout" {
		testingObject.Fatalf("unexpected last error: %s", fields.LastError)
	}
	logFields := fields.Fields()
	if logFields["traffic_id"] != "traffic-1" {
		testingObject.Fatalf("unexpected log field traffic_id: %v", logFields["traffic_id"])
	}
}

// TestBuildTrafficDiagnosticFieldsFallbackFromTunnelMeta 验证缺失会话信息时可回退 tunnel meta。
func TestBuildTrafficDiagnosticFieldsFallbackFromTunnelMeta(testingObject *testing.T) {
	tunnel := newMockTunnel("tunnel-fallback")
	tunnel.id = "tunnel-fallback"
	fields := BuildTrafficDiagnosticFields(
		TrafficMeta{
			TrafficID: "traffic-2",
			ServiceID: "svc-2",
		},
		&mockTunnelWithMeta{
			mockTunnel: tunnel,
			meta: transport.TunnelMeta{
				TunnelID:     "tunnel-fallback",
				SessionID:    "session-fallback",
				SessionEpoch: 11,
			},
		},
		TrafficFrameOpen,
		nil,
	)
	if fields.SessionID != "session-fallback" {
		testingObject.Fatalf("unexpected fallback session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 11 {
		testingObject.Fatalf("unexpected fallback session epoch: %d", fields.SessionEpoch)
	}
}

type mockTunnelWithMeta struct {
	*mockTunnel
	meta transport.TunnelMeta
}

func (tunnel *mockTunnelWithMeta) Meta() transport.TunnelMeta {
	return tunnel.meta
}
