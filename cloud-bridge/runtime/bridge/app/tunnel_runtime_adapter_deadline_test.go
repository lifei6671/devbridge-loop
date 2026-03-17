package app

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type bridgeDeadlinePolicyTestTunnel struct {
	bindingType transport.BindingType
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Read(payload []byte) (int, error) {
	_ = payload
	return 0, io.EOF
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Close() error {
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) ID() string {
	return "bridge-deadline-policy-test"
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Meta() transport.TunnelMeta {
	return transport.TunnelMeta{}
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) State() transport.TunnelState {
	return transport.TunnelStateIdle
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: tunnel.bindingType}
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) CloseWrite() error {
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Reset(cause error) error {
	_ = cause
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) SetDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) SetReadDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) SetWriteDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Flush() error {
	return nil
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) ReuseCount() int {
	return 0
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Recyclable() bool {
	return true
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Done() <-chan struct{} {
	doneChannel := make(chan struct{})
	close(doneChannel)
	return doneChannel
}

func (tunnel *bridgeDeadlinePolicyTestTunnel) Err() error {
	return nil
}

func TestShouldUseBridgeTunnelPollingDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	if !shouldUseBridgeTunnelPollingDeadline(nil) {
		testingObject.Fatalf("expected nil tunnel to default polling enabled")
	}
	if shouldUseBridgeTunnelPollingDeadline(&bridgeDeadlinePolicyTestTunnel{
		bindingType: transport.BindingTypeGRPCH2,
	}) {
		testingObject.Fatalf("expected grpc_h2 tunnel to disable polling deadline")
	}
	if !shouldUseBridgeTunnelPollingDeadline(&bridgeDeadlinePolicyTestTunnel{
		bindingType: transport.BindingTypeTCPFramed,
	}) {
		testingObject.Fatalf("expected tcp_framed tunnel to keep polling deadline")
	}
}

func TestNextBridgeTunnelReadDeadlineForGRPCWithoutContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	deadline := nextBridgeTunnelReadDeadline(context.Background(), 100*time.Millisecond, false)
	if !deadline.IsZero() {
		testingObject.Fatalf("expected zero deadline for grpc_h2 without context deadline, got=%s", deadline)
	}
}

func TestNextBridgeTunnelReadDeadlineForGRPCWithContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	expectedDeadline := time.Now().UTC().Add(2 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), expectedDeadline)
	defer cancel()

	deadline := nextBridgeTunnelReadDeadline(ctx, 100*time.Millisecond, false)
	if !deadline.Equal(expectedDeadline) {
		testingObject.Fatalf("unexpected grpc_h2 read deadline: got=%s want=%s", deadline, expectedDeadline)
	}
}

func TestNextBridgeTunnelWriteDeadlineForGRPCWithoutContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	deadline := nextBridgeTunnelWriteDeadline(context.Background(), 100*time.Millisecond, false)
	if !deadline.IsZero() {
		testingObject.Fatalf("expected zero deadline for grpc_h2 without context deadline, got=%s", deadline)
	}
}

func TestNextBridgeTunnelReadDeadlineForPollingBinding(testingObject *testing.T) {
	testingObject.Parallel()

	beforeCall := time.Now().UTC()
	deadline := nextBridgeTunnelReadDeadline(context.Background(), 120*time.Millisecond, true)
	if deadline.IsZero() {
		testingObject.Fatalf("expected non-zero deadline for polling binding")
	}
	if !deadline.After(beforeCall) {
		testingObject.Fatalf("expected polling deadline after current time, got=%s", deadline)
	}
}
