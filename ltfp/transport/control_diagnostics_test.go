package transport

import (
	"errors"
	"testing"
)

// TestBuildControlDiagnosticFields 验证统一控制面诊断字段提取逻辑。
func TestBuildControlDiagnosticFields(testingObject *testing.T) {
	fields := BuildControlDiagnosticFields(
		SessionMeta{
			SessionID:    "session-1",
			SessionEpoch: 7,
			LastError:    "stale meta error",
		},
		BindingInfo{Type: BindingTypeGRPCH2},
		errors.New("live control error"),
		true,
	)

	if fields.SessionID != "session-1" {
		testingObject.Fatalf("unexpected session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 7 {
		testingObject.Fatalf("unexpected session epoch: %d", fields.SessionEpoch)
	}
	if fields.Binding != BindingTypeGRPCH2.String() {
		testingObject.Fatalf("unexpected binding: %s", fields.Binding)
	}
	if fields.LastError != "live control error" {
		testingObject.Fatalf("unexpected last error: %s", fields.LastError)
	}
	if !fields.Retryable {
		testingObject.Fatalf("expected retryable=true")
	}
}

// TestBuildControlDiagnosticFieldsForSession 验证可直接从 session 采集统一控制面日志字段。
func TestBuildControlDiagnosticFieldsForSession(testingObject *testing.T) {
	session := NewInMemorySession(
		SessionMeta{
			SessionID:    "session-2",
			SessionEpoch: 9,
		},
		BindingInfo{Type: BindingTypeTCPFramed},
		SessionCapabilities{},
	)
	if err := session.MarkFailed(errors.New("heartbeat timeout"), false, false); err != nil {
		testingObject.Fatalf("mark session failed: %v", err)
	}

	fields := BuildControlDiagnosticFieldsForSession(session, false)
	if fields.SessionID != "session-2" {
		testingObject.Fatalf("unexpected session id: %s", fields.SessionID)
	}
	if fields.SessionEpoch != 9 {
		testingObject.Fatalf("unexpected session epoch: %d", fields.SessionEpoch)
	}
	if fields.Binding != BindingTypeTCPFramed.String() {
		testingObject.Fatalf("unexpected binding: %s", fields.Binding)
	}
	if fields.LastError != "heartbeat timeout" {
		testingObject.Fatalf("unexpected last error: %s", fields.LastError)
	}
	logFields := fields.Fields()
	if logFields["retryable"] != false {
		testingObject.Fatalf("unexpected retryable field: %v", logFields["retryable"])
	}
}
