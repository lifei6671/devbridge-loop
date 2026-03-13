package session

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
)

type sessionTestTunnel struct {
	id string
}

func (tunnelRuntime *sessionTestTunnel) ID() string {
	return tunnelRuntime.id
}

func (tunnelRuntime *sessionTestTunnel) Close() error {
	return nil
}

type sessionTestTunnelOpener struct {
	nextID atomic.Int64
}

func (opener *sessionTestTunnelOpener) Open(ctx context.Context) (tunnel.RuntimeTunnel, error) {
	_ = ctx
	tunnelID := opener.nextID.Add(1)
	return &sessionTestTunnel{id: fmt.Sprintf("tunnel-%d", tunnelID)}, nil
}

type sessionTestStateHandler struct {
	mutex  sync.Mutex
	states []string
}

func (handler *sessionTestStateHandler) HandleSessionState(state string) (int, error) {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	handler.states = append(handler.states, state)
	return 0, nil
}

func (handler *sessionTestStateHandler) States() []string {
	handler.mutex.Lock()
	defer handler.mutex.Unlock()
	copied := make([]string, len(handler.states))
	copy(copied, handler.states)
	return copied
}

type sessionTestBlockingStateHandler struct {
	entered chan string
	release chan struct{}
}

func newSessionTestBlockingStateHandler() *sessionTestBlockingStateHandler {
	return &sessionTestBlockingStateHandler{
		entered: make(chan string, 1),
		release: make(chan struct{}),
	}
}

func (handler *sessionTestBlockingStateHandler) HandleSessionState(state string) (int, error) {
	select {
	case handler.entered <- state:
	default:
	}
	<-handler.release
	return 0, nil
}

func TestManagerPropagatesDrainingAndResumeToTunnelPool(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelManager := newSessionTestTunnelManager(testingObject)
	sessionManager := NewManagerWithOptions(Options{
		StateHandler: tunnelManager,
	})

	if _, err := tunnelManager.ReconcileNow(context.Background(), "startup"); err != nil {
		testingObject.Fatalf("startup reconcile failed: %v", err)
	}
	if tunnelManager.Snapshot().IdleCount != 2 {
		testingObject.Fatalf("unexpected startup idle count: %d", tunnelManager.Snapshot().IdleCount)
	}

	sessionManager.MarkDraining()
	if tunnelManager.Snapshot().IdleCount != 0 {
		testingObject.Fatalf("expected idle recycled after draining, got=%d", tunnelManager.Snapshot().IdleCount)
	}
	if tunnelManager.RequestRefill(2, "manual") {
		testingObject.Fatalf("expected refill ignored while draining")
	}

	if err := sessionManager.Start(context.Background()); err != nil {
		testingObject.Fatalf("session start failed: %v", err)
	}
	result, err := tunnelManager.ReconcileNow(context.Background(), "after_active")
	if err != nil {
		testingObject.Fatalf("reconcile after active failed: %v", err)
	}
	if result.After.IdleCount != 2 {
		testingObject.Fatalf("expected idle rebuilt after active, got=%d", result.After.IdleCount)
	}
}

func TestManagerPropagatesStaleToTunnelPool(testingObject *testing.T) {
	testingObject.Parallel()
	tunnelManager := newSessionTestTunnelManager(testingObject)
	sessionManager := NewManagerWithOptions(Options{
		StateHandler: tunnelManager,
	})

	if _, err := tunnelManager.ReconcileNow(context.Background(), "startup"); err != nil {
		testingObject.Fatalf("startup reconcile failed: %v", err)
	}
	sessionManager.MarkStale()

	if tunnelManager.Snapshot().IdleCount != 0 {
		testingObject.Fatalf("expected idle recycled after stale, got=%d", tunnelManager.Snapshot().IdleCount)
	}
	if tunnelManager.RequestRefill(2, "manual") {
		testingObject.Fatalf("expected refill ignored while stale")
	}
}

func TestManagerSkipsStaleStateHandlerNotification(testingObject *testing.T) {
	testingObject.Parallel()
	stateHandler := &sessionTestStateHandler{}
	sessionManager := NewManagerWithOptions(Options{
		StateHandler: stateHandler,
	})

	sessionManager.mu.Lock()
	drainingChanged, drainingVersion := sessionManager.setStateLocked(StateDraining, time.Now())
	sessionManager.mu.Unlock()

	sessionManager.mu.Lock()
	activeChanged, activeVersion := sessionManager.setStateLocked(StateActive, time.Now())
	sessionManager.mu.Unlock()

	sessionManager.notifyStateHandler(StateActive, activeChanged, activeVersion)
	sessionManager.notifyStateHandler(StateDraining, drainingChanged, drainingVersion)

	states := stateHandler.States()
	if len(states) != 1 {
		testingObject.Fatalf("expected only latest state notification, got=%v", states)
	}
	if states[0] != string(StateActive) {
		testingObject.Fatalf("unexpected notified state: %s", states[0])
	}
}

func TestManagerBlocksStateAdvanceWhileHandlerRunning(testingObject *testing.T) {
	testingObject.Parallel()
	blockingHandler := newSessionTestBlockingStateHandler()
	sessionManager := NewManagerWithOptions(Options{
		StateHandler: blockingHandler,
	})

	drainingDone := make(chan struct{})
	go func() {
		sessionManager.MarkDraining()
		close(drainingDone)
	}()

	select {
	case notifiedState := <-blockingHandler.entered:
		if notifiedState != string(StateDraining) {
			testingObject.Fatalf("unexpected first notified state: %s", notifiedState)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("timed out waiting for draining callback start")
	}

	startDone := make(chan error, 1)
	go func() {
		startDone <- sessionManager.Start(context.Background())
	}()

	lockAcquired := make(chan struct{}, 1)
	go func() {
		sessionManager.mu.Lock()
		sessionManager.mu.Unlock()
		lockAcquired <- struct{}{}
	}()

	select {
	case <-lockAcquired:
		testingObject.Fatalf("expected manager state lock blocked while callback in-flight")
	case <-time.After(30 * time.Millisecond):
	}
	select {
	case startErr := <-startDone:
		testingObject.Fatalf("expected start blocked by in-flight callback, err=%v", startErr)
	default:
	}

	close(blockingHandler.release)

	select {
	case <-drainingDone:
	case <-time.After(time.Second):
		testingObject.Fatalf("timed out waiting for draining callback completion")
	}
	select {
	case <-lockAcquired:
	case <-time.After(time.Second):
		testingObject.Fatalf("timed out waiting for lock probe completion")
	}
	select {
	case startErr := <-startDone:
		if startErr != nil {
			testingObject.Fatalf("start failed: %v", startErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("timed out waiting for start completion")
	}
	if sessionManager.State() != StateActive {
		testingObject.Fatalf("expected final state active, got=%s", sessionManager.State())
	}
}

func newSessionTestTunnelManager(testingObject *testing.T) *tunnel.Manager {
	testingObject.Helper()
	tunnelManager, err := tunnel.NewManager(tunnel.ManagerOptions{
		Config: tunnel.ManagerConfig{
			MinIdle:           2,
			MaxIdle:           4,
			IdleTTL:           0,
			MaxInflightOpens:  2,
			TunnelOpenRate:    1000,
			TunnelOpenBurst:   1000,
			ReconcileInterval: time.Second,
		},
		Opener: &sessionTestTunnelOpener{},
	})
	if err != nil {
		testingObject.Fatalf("new tunnel manager failed: %v", err)
	}
	return tunnelManager
}
