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
