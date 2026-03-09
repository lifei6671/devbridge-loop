package tunnel

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

func TestSyncManager_AutoReconnectWhenBridgeStartsLater(t *testing.T) {
	t.Parallel()

	bridgeAddr := allocateLoopbackAddr(t)
	cfg := config.Config{
		HTTPAddr: "127.0.0.1:19090",
		RDName:   "rd-test",
		EnvName:  "dev-test",
		Registration: config.RegistrationConfig{
			DefaultTTLSeconds: 30,
			ScanInterval:      time.Second,
		},
		Tunnel: config.TunnelConfig{
			BridgeAddress:     "http://" + bridgeAddr,
			BackflowBaseURL:   "http://127.0.0.1:19090",
			HeartbeatInterval: 200 * time.Millisecond,
			ReconnectBackoff:  []time.Duration{80 * time.Millisecond, 120 * time.Millisecond, 200 * time.Millisecond},
			RequestTimeout:    120 * time.Millisecond,
		},
		EnvResolve: config.EnvResolveConfig{
			Order: []string{"requestHeader", "payload", "runtimeDefault", "baseFallback"},
		},
	}

	state := store.NewMemoryStore()
	manager := NewSyncManager(cfg, state)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		manager.Run(ctx)
		close(done)
	}()

	// 先等待一小段时间，确保 agent 在 bridge 不可达时进入自动重连循环。
	time.Sleep(300 * time.Millisecond)
	if err := waitReconnectPending(state, cfg.Tunnel.BridgeAddress, 2*time.Second); err != nil {
		t.Fatalf("expected reconnect countdown state when bridge is unavailable: %v", err)
	}

	server, reqCount := startMockBridgeServer(t, bridgeAddr)
	defer shutdownServer(t, server)

	if err := waitTunnelConnected(state, cfg.Tunnel.BridgeAddress, 4*time.Second); err != nil {
		t.Fatalf("expected tunnel to reconnect after bridge starts later: %v", err)
	}

	// 握手 + 全量同步至少会触发 3 次事件请求；这个断言用于验证确实发生了重连建链。
	if got := atomic.LoadInt64(reqCount); got < 3 {
		t.Fatalf("expected at least 3 tunnel events after reconnect, got %d", got)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sync manager did not stop after context canceled")
	}
}

func TestSyncManager_BackoffForAttemptUsesExponentialWithCap(t *testing.T) {
	t.Parallel()

	manager := &SyncManager{
		cfg: config.Config{
			Tunnel: config.TunnelConfig{
				ReconnectBackoff: []time.Duration{
					500 * time.Millisecond,
					5 * time.Second,
				},
			},
		},
	}

	// 指数退避序列：0.5s,1s,2s,4s,5s(cap),5s(cap)
	expected := []time.Duration{
		500 * time.Millisecond,
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		5 * time.Second,
		5 * time.Second,
	}
	for attempt, want := range expected {
		got := manager.backoffForAttempt(attempt)
		if got != want {
			t.Fatalf("backoff mismatch at attempt=%d: got=%s want=%s", attempt, got, want)
		}
	}
}

func allocateLoopbackAddr(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen loopback failed: %v", err)
	}
	addr := listener.Addr().String()
	_ = listener.Close()
	return addr
}

func startMockBridgeServer(t *testing.T, addr string) (*http.Server, *int64) {
	t.Helper()

	var reqCount int64
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/tunnel/events", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&reqCount, 1)

		var message domain.TunnelMessage
		if err := json.NewDecoder(r.Body).Decode(&message); err != nil {
			http.Error(w, fmt.Sprintf("decode tunnel message failed: %v", err), http.StatusBadRequest)
			return
		}

		reply := domain.TunnelReply{
			Type:            domain.MessageACK,
			Status:          "accepted",
			EventID:         message.EventID,
			SessionEpoch:    message.SessionEpoch,
			ResourceVersion: message.ResourceVersion,
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(reply)
	})

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 2 * time.Second,
	}
	go func() {
		_ = server.ListenAndServe()
	}()
	return server, &reqCount
}

func shutdownServer(t *testing.T, server *http.Server) {
	t.Helper()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = server.Shutdown(shutdownCtx)
}

func waitTunnelConnected(state *store.MemoryStore, bridgeAddr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if state.TunnelState(bridgeAddr).Connected {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("tunnel still disconnected after %s", timeout)
}

func waitReconnectPending(state *store.MemoryStore, bridgeAddr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		current := state.TunnelState(bridgeAddr)
		if current.Reconnecting && current.ReconnectAttempt > 0 && current.NextReconnectAt != nil {
			return nil
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("reconnect state is not ready after %s", timeout)
}
