package app

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type deadlinePolicyTestTunnel struct {
	bindingType transport.BindingType
}

func (tunnel *deadlinePolicyTestTunnel) Read(payload []byte) (int, error) {
	_ = payload
	return 0, io.EOF
}

func (tunnel *deadlinePolicyTestTunnel) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (tunnel *deadlinePolicyTestTunnel) Close() error {
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) ID() string {
	return "deadline-policy-test"
}

func (tunnel *deadlinePolicyTestTunnel) Meta() transport.TunnelMeta {
	return transport.TunnelMeta{}
}

func (tunnel *deadlinePolicyTestTunnel) State() transport.TunnelState {
	return transport.TunnelStateIdle
}

func (tunnel *deadlinePolicyTestTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: tunnel.bindingType}
}

func (tunnel *deadlinePolicyTestTunnel) CloseWrite() error {
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) Reset(cause error) error {
	_ = cause
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) SetDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) SetReadDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) SetWriteDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) Flush() error {
	return nil
}

func (tunnel *deadlinePolicyTestTunnel) ReuseCount() int {
	return 0
}

func (tunnel *deadlinePolicyTestTunnel) Recyclable() bool {
	return true
}

func (tunnel *deadlinePolicyTestTunnel) Done() <-chan struct{} {
	doneChannel := make(chan struct{})
	close(doneChannel)
	return doneChannel
}

func (tunnel *deadlinePolicyTestTunnel) Err() error {
	return nil
}

func TestShouldUseTrafficTunnelPollingDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	if !shouldUseTrafficTunnelPollingDeadline(nil) {
		testingObject.Fatalf("expected nil tunnel to default polling enabled")
	}
	if shouldUseTrafficTunnelPollingDeadline(&deadlinePolicyTestTunnel{
		bindingType: transport.BindingTypeGRPCH2,
	}) {
		testingObject.Fatalf("expected grpc_h2 tunnel to disable polling deadline")
	}
	if !shouldUseTrafficTunnelPollingDeadline(&deadlinePolicyTestTunnel{
		bindingType: transport.BindingTypeTCPFramed,
	}) {
		testingObject.Fatalf("expected tcp_framed tunnel to keep polling deadline")
	}
}

func TestNextTrafficTunnelReadDeadlineForGRPCWithoutContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	deadline := nextTrafficTunnelReadDeadline(context.Background(), 100*time.Millisecond, false)
	if !deadline.IsZero() {
		testingObject.Fatalf("expected zero deadline for grpc_h2 without context deadline, got=%s", deadline)
	}
}

func TestNextTrafficTunnelReadDeadlineForGRPCWithContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	expectedDeadline := time.Now().UTC().Add(2 * time.Second)
	ctx, cancel := context.WithDeadline(context.Background(), expectedDeadline)
	defer cancel()

	deadline := nextTrafficTunnelReadDeadline(ctx, 100*time.Millisecond, false)
	if !deadline.Equal(expectedDeadline) {
		testingObject.Fatalf("unexpected grpc_h2 read deadline: got=%s want=%s", deadline, expectedDeadline)
	}
}

func TestNextTrafficTunnelWriteDeadlineForGRPCWithoutContextDeadline(testingObject *testing.T) {
	testingObject.Parallel()

	deadline := nextTrafficTunnelWriteDeadline(context.Background(), 100*time.Millisecond, false)
	if !deadline.IsZero() {
		testingObject.Fatalf("expected zero deadline for grpc_h2 without context deadline, got=%s", deadline)
	}
}

func TestNextTrafficTunnelReadDeadlineForPollingBinding(testingObject *testing.T) {
	testingObject.Parallel()

	beforeCall := time.Now().UTC()
	deadline := nextTrafficTunnelReadDeadline(context.Background(), 120*time.Millisecond, true)
	if deadline.IsZero() {
		testingObject.Fatalf("expected non-zero deadline for polling binding")
	}
	if !deadline.After(beforeCall) {
		testingObject.Fatalf("expected polling deadline after current time, got=%s", deadline)
	}
}

func TestShouldUseTrafficRecycleReadTimeout(testingObject *testing.T) {
	testingObject.Parallel()

	if !shouldUseTrafficRecycleReadTimeout(nil) {
		testingObject.Fatalf("expected nil tunnel io to default timeout enabled")
	}

	grpcAdapter := &runtimeTrafficTunnel{
		tunnel: &deadlinePolicyTestTunnel{bindingType: transport.BindingTypeGRPCH2},
	}
	if shouldUseTrafficRecycleReadTimeout(grpcAdapter) {
		testingObject.Fatalf("expected grpc_h2 runtime tunnel to disable recycle read timeout")
	}

	tcpAdapter := &runtimeTrafficTunnel{
		tunnel: &deadlinePolicyTestTunnel{bindingType: transport.BindingTypeTCPFramed},
	}
	if !shouldUseTrafficRecycleReadTimeout(tcpAdapter) {
		testingObject.Fatalf("expected tcp_framed runtime tunnel to keep recycle read timeout")
	}
}
