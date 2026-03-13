package transport

import (
	"errors"
	"testing"
)

// TestBuildSessionDiagnosticFieldsForSession 验证可从 Session 直接提取统一日志字段。
func TestBuildSessionDiagnosticFieldsForSession(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{
			SessionID:    "session-observe",
			SessionEpoch: 12,
		},
		BindingInfo{Type: BindingTypeGRPCH2},
		SessionCapabilities{},
	)
	if err := session.MarkFailed(errors.New("auth rejected"), false, false); err != nil {
		testingObject.Fatalf("mark session failed: %v", err)
	}

	fields := BuildSessionDiagnosticFieldsForSession(session)
	if fields.SessionID != "session-observe" {
		testingObject.Fatalf("unexpected session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 12 {
		testingObject.Fatalf("unexpected session epoch: %d", fields.SessionEpoch)
	}
	if fields.Binding != BindingTypeGRPCH2.String() {
		testingObject.Fatalf("unexpected binding: %s", fields.Binding)
	}
	if fields.State != SessionStateFailed {
		testingObject.Fatalf("unexpected session state: %s", fields.State)
	}
	if fields.ProtocolState != ProtocolStateClosed {
		testingObject.Fatalf("unexpected protocol state: %s", fields.ProtocolState)
	}
	if fields.LastError != "auth rejected" {
		testingObject.Fatalf("unexpected last error: %s", fields.LastError)
	}
	logFields := fields.Fields()
	if logFields["session_id"] != "session-observe" {
		testingObject.Fatalf("unexpected log field session_id: %v", logFields["session_id"])
	}
}

// TestBuildTunnelDiagnosticFieldsForTunnel 验证 tunnel 维度日志字段输出。
func TestBuildTunnelDiagnosticFieldsForTunnel(testingObject *testing.T) {
	tunnel := newIdleMockTunnel("tunnel-observe")
	tunnel.meta = TunnelMeta{
		TunnelID:     "tunnel-observe",
		SessionID:    "session-tunnel",
		SessionEpoch: 5,
	}
	tunnel.bindingInfo = BindingInfo{Type: BindingTypeTCPFramed}
	tunnel.state = TunnelStateBroken
	tunnel.lastError = errors.New("link broken")

	fields := BuildTunnelDiagnosticFieldsForTunnel(tunnel)
	if fields.SessionID != "session-tunnel" {
		testingObject.Fatalf("unexpected session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 5 {
		testingObject.Fatalf("unexpected session epoch: %d", fields.SessionEpoch)
	}
	if fields.Binding != BindingTypeTCPFramed.String() {
		testingObject.Fatalf("unexpected binding: %s", fields.Binding)
	}
	if fields.TunnelID != "tunnel-observe" {
		testingObject.Fatalf("unexpected tunnel id: %s", fields.TunnelID)
	}
	if fields.TunnelState != TunnelStateBroken {
		testingObject.Fatalf("unexpected tunnel state: %s", fields.TunnelState)
	}
	if fields.LastError != "link broken" {
		testingObject.Fatalf("unexpected last error: %s", fields.LastError)
	}
}

// TestBuildPoolDiagnosticFields 验证 pool report 与 refill 诊断字段输出。
func TestBuildPoolDiagnosticFields(testingObject *testing.T) {
	reportFields := BuildPoolDiagnosticFields(
		TunnelPoolReport{
			SessionID:       "session-pool",
			SessionEpoch:    8,
			IdleCount:       3,
			InUseCount:      4,
			TargetIdleCount: 6,
		},
		BindingInfo{Type: BindingTypeGRPCH2},
		errors.New("pool degraded"),
	)
	if reportFields.LastError != "pool degraded" {
		testingObject.Fatalf("unexpected pool report last error: %s", reportFields.LastError)
	}
	reportLogFields := reportFields.Fields()
	if reportLogFields["idle_count"] != 3 {
		testingObject.Fatalf("unexpected idle count field: %v", reportLogFields["idle_count"])
	}

	refillFields := BuildPoolRefillDiagnosticFields(
		TunnelRefillRequest{
			SessionID:          "session-pool",
			SessionEpoch:       8,
			RequestID:          "request-1",
			RequestedIdleDelta: 2,
			Reason:             TunnelRefillReasonLowWatermark,
		},
		RefillResult{
			EffectiveTargetIdle: 5,
			OpenedCount:         2,
			FailedCount:         1,
			AfterIdleCount:      4,
		},
		BindingInfo{Type: BindingTypeGRPCH2},
		errors.New("open timeout"),
	)
	if refillFields.RequestID != "request-1" {
		testingObject.Fatalf("unexpected request id: %s", refillFields.RequestID)
	}
	if refillFields.Reason != TunnelRefillReasonLowWatermark {
		testingObject.Fatalf("unexpected refill reason: %s", refillFields.Reason)
	}
	if refillFields.LastError != "open timeout" {
		testingObject.Fatalf("unexpected refill last error: %s", refillFields.LastError)
	}
	refillLogFields := refillFields.Fields()
	if refillLogFields["effective_target_idle"] != 5 {
		testingObject.Fatalf("unexpected effective target field: %v", refillLogFields["effective_target_idle"])
	}
}
