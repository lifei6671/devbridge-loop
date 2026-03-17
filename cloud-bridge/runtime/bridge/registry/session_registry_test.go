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

// TestSessionRegistryCommitAuthoritativeDrainsPrevious 验证权威提交会原子切换 connector 映射并降级旧会话。
func TestSessionRegistryCommitAuthoritativeDrainsPrevious(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewSessionRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, SessionRuntime{
		SessionID:     "session-old",
		ConnectorID:   "connector-1",
		Epoch:         1,
		State:         SessionActive,
		LastHeartbeat: now,
	})

	result, committed := registry.CommitAuthoritative(now.Add(time.Second), SessionRuntime{
		SessionID:   "session-new",
		ConnectorID: "connector-1",
		Epoch:       2,
		State:       SessionActive,
	})
	if !committed {
		testingObject.Fatalf("expected authoritative commit success")
	}
	if !result.PreviousExists || !result.PreviousStateChanged {
		testingObject.Fatalf("expected previous session drained during authoritative commit")
	}
	if result.PreviousSession.State != SessionDraining {
		testingObject.Fatalf("unexpected previous session state: got=%s want=%s", result.PreviousSession.State, SessionDraining)
	}
	currentSession, exists := registry.GetByConnector("connector-1")
	if !exists {
		testingObject.Fatalf("expected connector authoritative session exists")
	}
	if currentSession.SessionID != "session-new" || currentSession.Epoch != 2 {
		testingObject.Fatalf("unexpected connector authoritative session: %+v", currentSession)
	}
}

// TestSessionRegistryCommitAuthoritativeAllowsStaleEpochReset 验证旧权威已终态时允许低 epoch 会话接管。
func TestSessionRegistryCommitAuthoritativeAllowsStaleEpochReset(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewSessionRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, SessionRuntime{
		SessionID:     "session-stale",
		ConnectorID:   "connector-1",
		Epoch:         9,
		State:         SessionStale,
		LastHeartbeat: now.Add(-time.Minute),
		UpdatedAt:     now.Add(-time.Minute),
	})

	_, committed := registry.CommitAuthoritative(now.Add(time.Second), SessionRuntime{
		SessionID:   "session-reset",
		ConnectorID: "connector-1",
		Epoch:       1,
		State:       SessionActive,
	})
	if !committed {
		testingObject.Fatalf("expected stale epoch reset authoritative commit success")
	}
	currentSession, exists := registry.GetByConnector("connector-1")
	if !exists {
		testingObject.Fatalf("expected connector authoritative session exists")
	}
	if currentSession.SessionID != "session-reset" || currentSession.Epoch != 1 {
		testingObject.Fatalf("unexpected connector authoritative session after reset: %+v", currentSession)
	}
}

// TestSessionRegistryCommitAuthoritativeRejectsSameEpochActiveTakeover 验证存活权威未终态时不允许同 epoch 抢占。
func TestSessionRegistryCommitAuthoritativeRejectsSameEpochActiveTakeover(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewSessionRegistry()
	now := time.Now().UTC()
	registry.Upsert(now, SessionRuntime{
		SessionID:     "session-active",
		ConnectorID:   "connector-1",
		Epoch:         2,
		State:         SessionActive,
		LastHeartbeat: now,
	})

	_, committed := registry.CommitAuthoritative(now.Add(time.Second), SessionRuntime{
		SessionID:   "session-racing",
		ConnectorID: "connector-1",
		Epoch:       2,
		State:       SessionActive,
	})
	if committed {
		testingObject.Fatalf("expected same-epoch active authoritative commit rejected")
	}
	currentSession, exists := registry.GetByConnector("connector-1")
	if !exists {
		testingObject.Fatalf("expected original connector authoritative session kept")
	}
	if currentSession.SessionID != "session-active" || currentSession.Epoch != 2 {
		testingObject.Fatalf("unexpected connector authoritative session after rejection: %+v", currentSession)
	}
}
