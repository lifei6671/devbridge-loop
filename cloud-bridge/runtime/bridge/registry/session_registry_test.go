package registry

import (
	"testing"
	"time"
)

// TestSessionRegistryRemoveKeepNewestConnectorIndex 验证删除旧 session 不会清空新映射。
func TestSessionRegistryRemoveKeepNewestConnectorIndex(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewSessionRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, SessionRuntime{
		SessionID:   "session-old",
		ConnectorID: "connector-1",
		Epoch:       1,
		State:       SessionActive,
	})
	registry.Upsert(now.Add(time.Second), SessionRuntime{
		SessionID:   "session-new",
		ConnectorID: "connector-1",
		Epoch:       2,
		State:       SessionActive,
	})

	registry.Remove("session-old")

	runtime, exists := registry.GetByConnector("connector-1")
	if !exists {
		testingObject.Fatalf("expected connector index still exists")
	}
	if runtime.SessionID != "session-new" {
		testingObject.Fatalf("unexpected session mapping: got=%s want=session-new", runtime.SessionID)
	}
}
