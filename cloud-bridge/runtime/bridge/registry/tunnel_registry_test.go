package registry

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type tunnelRegistryTestTunnel struct {
	tunnelID string
	closed   bool
}

func (tunnel *tunnelRegistryTestTunnel) ID() string {
	return tunnel.tunnelID
}

func (tunnel *tunnelRegistryTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	_ = ctx
	return pb.StreamPayload{}, errors.New("read not implemented in test tunnel")
}

func (tunnel *tunnelRegistryTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	_ = ctx
	_ = payload
	return nil
}

func (tunnel *tunnelRegistryTestTunnel) Close() error {
	tunnel.closed = true
	return nil
}

// TestTunnelRegistryStateFlow 验证 tunnel 状态流转 idle -> reserved -> active -> closed -> remove。
func TestTunnelRegistryStateFlow(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewTunnelRegistry()
	now := time.Now().UTC()
	_, err := registry.UpsertIdle(now, "connector-1", "session-1", &tunnelRegistryTestTunnel{
		tunnelID: "tunnel-1",
	})
	if err != nil {
		testingObject.Fatalf("upsert idle failed: %v", err)
	}

	acquired, ok := registry.AcquireIdle(now.Add(time.Millisecond), "connector-1")
	if !ok {
		testingObject.Fatalf("expected acquire idle success")
	}
	if acquired.State != TunnelStateReserved {
		testingObject.Fatalf("unexpected acquired state: %s", acquired.State)
	}
	if err := registry.MarkActive(now.Add(2*time.Millisecond), "tunnel-1", "traffic-1"); err != nil {
		testingObject.Fatalf("mark active failed: %v", err)
	}
	if err := registry.MarkClosed(now.Add(3*time.Millisecond), "tunnel-1"); err != nil {
		testingObject.Fatalf("mark closed failed: %v", err)
	}
	if _, err := registry.RemoveTerminal("tunnel-1"); err != nil {
		testingObject.Fatalf("remove terminal failed: %v", err)
	}
	if _, exists := registry.Get("tunnel-1"); exists {
		testingObject.Fatalf("expected tunnel removed from registry")
	}
}

// TestTunnelRegistryAcquireFIFO 验证同一 connector 下 idle 分配遵循 FIFO。
func TestTunnelRegistryAcquireFIFO(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewTunnelRegistry()
	now := time.Now().UTC()
	_, err := registry.UpsertIdle(now, "connector-1", "session-1", &tunnelRegistryTestTunnel{
		tunnelID: "tunnel-1",
	})
	if err != nil {
		testingObject.Fatalf("upsert tunnel-1 failed: %v", err)
	}
	_, err = registry.UpsertIdle(now.Add(time.Millisecond), "connector-1", "session-1", &tunnelRegistryTestTunnel{
		tunnelID: "tunnel-2",
	})
	if err != nil {
		testingObject.Fatalf("upsert tunnel-2 failed: %v", err)
	}

	first, ok := registry.AcquireIdle(now.Add(2*time.Millisecond), "connector-1")
	if !ok {
		testingObject.Fatalf("expected first acquire success")
	}
	second, ok := registry.AcquireIdle(now.Add(3*time.Millisecond), "connector-1")
	if !ok {
		testingObject.Fatalf("expected second acquire success")
	}
	if first.TunnelID != "tunnel-1" || second.TunnelID != "tunnel-2" {
		testingObject.Fatalf(
			"unexpected fifo order: first=%s second=%s",
			first.TunnelID,
			second.TunnelID,
		)
	}
}

// TestTunnelRegistryPurgeBySession 验证按 session 摘除会关闭并移除对应 tunnel。
func TestTunnelRegistryPurgeBySession(testingObject *testing.T) {
	testingObject.Parallel()
	registry := NewTunnelRegistry()
	now := time.Now().UTC()
	oldSessionTunnel := &tunnelRegistryTestTunnel{tunnelID: "tunnel-old"}
	newSessionTunnel := &tunnelRegistryTestTunnel{tunnelID: "tunnel-new"}
	if _, err := registry.UpsertIdle(now, "connector-1", "session-old", oldSessionTunnel); err != nil {
		testingObject.Fatalf("upsert old session tunnel failed: %v", err)
	}
	if _, err := registry.UpsertIdle(now.Add(time.Millisecond), "connector-1", "session-new", newSessionTunnel); err != nil {
		testingObject.Fatalf("upsert new session tunnel failed: %v", err)
	}

	purged := registry.PurgeBySession(now.Add(2*time.Millisecond), "session-old", "session takeover")
	if len(purged) != 1 {
		testingObject.Fatalf("unexpected purged tunnel count: got=%d want=1", len(purged))
	}
	if purged[0].TunnelID != "tunnel-old" || purged[0].State != TunnelStateBroken {
		testingObject.Fatalf("unexpected purged tunnel snapshot: %+v", purged[0])
	}
	if _, exists := registry.Get("tunnel-old"); exists {
		testingObject.Fatalf("expected old session tunnel removed from registry")
	}
	if _, exists := registry.Get("tunnel-new"); !exists {
		testingObject.Fatalf("expected new session tunnel remains")
	}
	if !oldSessionTunnel.closed {
		testingObject.Fatalf("expected old session tunnel closed")
	}
	if newSessionTunnel.closed {
		testingObject.Fatalf("expected new session tunnel not closed")
	}
}

// TestTunnelRegistryRecycleIdleBySession 验证可按 session 仅回收 idle tunnel。
func TestTunnelRegistryRecycleIdleBySession(testingObject *testing.T) {
	testingObject.Parallel()

	registry := NewTunnelRegistry()
	now := time.Now().UTC()
	idleTunnelA1 := &tunnelRegistryTestTunnel{tunnelID: "tunnel-a1"}
	idleTunnelA2 := &tunnelRegistryTestTunnel{tunnelID: "tunnel-a2"}
	idleTunnelA3 := &tunnelRegistryTestTunnel{tunnelID: "tunnel-a3"}
	otherSessionTunnel := &tunnelRegistryTestTunnel{tunnelID: "tunnel-b1"}
	if _, err := registry.UpsertIdle(now, "connector-1", "session-a", idleTunnelA1); err != nil {
		testingObject.Fatalf("upsert tunnel-a1 failed: %v", err)
	}
	if _, err := registry.UpsertIdle(now.Add(time.Millisecond), "connector-1", "session-a", idleTunnelA2); err != nil {
		testingObject.Fatalf("upsert tunnel-a2 failed: %v", err)
	}
	if _, err := registry.UpsertIdle(now.Add(2*time.Millisecond), "connector-1", "session-a", idleTunnelA3); err != nil {
		testingObject.Fatalf("upsert tunnel-a3 failed: %v", err)
	}
	if _, err := registry.UpsertIdle(now.Add(3*time.Millisecond), "connector-1", "session-b", otherSessionTunnel); err != nil {
		testingObject.Fatalf("upsert tunnel-b1 failed: %v", err)
	}
	reservedRuntime, ok := registry.AcquireIdle(now.Add(4*time.Millisecond), "connector-1")
	if !ok {
		testingObject.Fatalf("expected acquire idle success")
	}
	if reservedRuntime.TunnelID != "tunnel-a1" || reservedRuntime.State != TunnelStateReserved {
		testingObject.Fatalf("unexpected reserved runtime: %+v", reservedRuntime)
	}

	recycled := registry.RecycleIdleBySession(now.Add(5*time.Millisecond), "session-a", 5, "agent_pool_reconcile")
	if len(recycled) != 2 {
		testingObject.Fatalf("unexpected recycled tunnel count: got=%d want=2", len(recycled))
	}
	for _, runtime := range recycled {
		if runtime.SessionID != "session-a" || runtime.State != TunnelStateBroken {
			testingObject.Fatalf("unexpected recycled runtime: %+v", runtime)
		}
		if runtime.TunnelID != "tunnel-a2" && runtime.TunnelID != "tunnel-a3" {
			testingObject.Fatalf("unexpected recycled tunnel id: %s", runtime.TunnelID)
		}
	}

	if _, exists := registry.Get("tunnel-a2"); exists {
		testingObject.Fatalf("expected tunnel-a2 removed")
	}
	if _, exists := registry.Get("tunnel-a3"); exists {
		testingObject.Fatalf("expected tunnel-a3 removed")
	}
	if _, exists := registry.Get("tunnel-a1"); !exists {
		testingObject.Fatalf("expected reserved tunnel-a1 remains")
	}
	if _, exists := registry.Get("tunnel-b1"); !exists {
		testingObject.Fatalf("expected other session tunnel remains")
	}
	if !idleTunnelA2.closed || !idleTunnelA3.closed {
		testingObject.Fatalf("expected recycled idle tunnels closed")
	}
	if idleTunnelA1.closed {
		testingObject.Fatalf("expected reserved tunnel not closed by idle recycle")
	}
	if otherSessionTunnel.closed {
		testingObject.Fatalf("expected other session tunnel not closed by idle recycle")
	}
}
