package grpcbinding

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"google.golang.org/grpc/metadata"
)

type fakeGeneratedTunnelBidiServerStream struct {
	base *fakeTunnelEnvelopeStream
}

func (stream *fakeGeneratedTunnelBidiServerStream) Send(frame *transportgen.TunnelEnvelope) error {
	return stream.base.Send(frame)
}

func (stream *fakeGeneratedTunnelBidiServerStream) Recv() (*transportgen.TunnelEnvelope, error) {
	return stream.base.Recv()
}

func (stream *fakeGeneratedTunnelBidiServerStream) SetHeader(metadata.MD) error {
	return nil
}

func (stream *fakeGeneratedTunnelBidiServerStream) SendHeader(metadata.MD) error {
	return nil
}

func (stream *fakeGeneratedTunnelBidiServerStream) SetTrailer(metadata.MD) {}

func (stream *fakeGeneratedTunnelBidiServerStream) Context() context.Context {
	return stream.base.Context()
}

func (stream *fakeGeneratedTunnelBidiServerStream) SendMsg(any) error {
	return nil
}

func (stream *fakeGeneratedTunnelBidiServerStream) RecvMsg(any) error {
	return nil
}

// TestTunnelAcceptorHandleAndAccept 验证 Server 接收 TunnelStream 后可被 AcceptTunnel 消费。
func TestTunnelAcceptorHandleAndAccept(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{
		IdentityConfig: TunnelIdentityConfig{
			SessionID:      "server-session",
			SessionEpoch:   11,
			TunnelIDPrefix: "server-tunnel",
		},
		QueueSize: 1,
	})
	serverStream := &fakeGeneratedTunnelBidiServerStream{
		base: &fakeTunnelEnvelopeStream{ctx: streamContext},
	}

	handleResultChannel := make(chan error, 1)
	go func() {
		handleResultChannel <- acceptor.HandleTunnelStream(serverStream)
	}()

	acceptContext, cancelAccept := context.WithTimeout(context.Background(), time.Second)
	defer cancelAccept()
	tunnel, err := acceptor.AcceptTunnel(acceptContext)
	if err != nil {
		testingObject.Fatalf("accept tunnel failed: %v", err)
	}
	meta := tunnel.Meta()
	if meta.SessionID != "server-session" || meta.SessionEpoch != 11 {
		testingObject.Fatalf("unexpected accepted tunnel meta: %+v", meta)
	}
	if !strings.HasPrefix(meta.TunnelID, "server-tunnel-") {
		testingObject.Fatalf("unexpected accepted tunnel id: %s", meta.TunnelID)
	}

	writtenSize, err := tunnel.Write([]byte("from-server"))
	if err != nil {
		testingObject.Fatalf("write payload to accepted tunnel failed: %v", err)
	}
	if writtenSize != len("from-server") {
		testingObject.Fatalf("unexpected write size: %d", writtenSize)
	}
	if len(serverStream.base.sendFrames) != 1 || string(serverStream.base.sendFrames[0].Payload) != "from-server" {
		testingObject.Fatalf("unexpected sent frames: %+v", serverStream.base.sendFrames)
	}

	if err := tunnel.Close(); err != nil {
		testingObject.Fatalf("close accepted tunnel failed: %v", err)
	}
	select {
	case handleErr := <-handleResultChannel:
		if handleErr != nil {
			testingObject.Fatalf("expected handle stream to exit cleanly, got %v", handleErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("handle stream did not exit after tunnel close")
	}
}

// TestTunnelAcceptorAcceptTunnelToPool 验证接收 tunnel 后可直接写入 idle pool。
func TestTunnelAcceptorAcceptTunnelToPool(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{})
	serverStream := &fakeGeneratedTunnelBidiServerStream{
		base: &fakeTunnelEnvelopeStream{ctx: streamContext},
	}
	handleResultChannel := make(chan error, 1)
	go func() {
		handleResultChannel <- acceptor.HandleTunnelStream(serverStream)
	}()

	pool := transport.NewInMemoryTunnelPool()
	acceptContext, cancelAccept := context.WithTimeout(context.Background(), time.Second)
	defer cancelAccept()
	tunnel, err := acceptor.AcceptTunnelToPool(acceptContext, pool)
	if err != nil {
		testingObject.Fatalf("accept tunnel to pool failed: %v", err)
	}
	if tunnel == nil {
		testingObject.Fatalf("expected non-nil tunnel")
	}
	if pool.IdleCount() != 1 {
		testingObject.Fatalf("expected idle pool count 1, got %d", pool.IdleCount())
	}

	cancelStream()
	select {
	case handleErr := <-handleResultChannel:
		if !errors.Is(handleErr, context.Canceled) {
			testingObject.Fatalf("expected context canceled from handler, got %v", handleErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("handle stream did not exit after stream cancellation")
	}
}

// TestTunnelAcceptorAcceptTunnelHonorsContext 验证 AcceptTunnel 受调用方 context 控制。
func TestTunnelAcceptorAcceptTunnelHonorsContext(testingObject *testing.T) {
	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{})
	acceptContext, cancelAccept := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancelAccept()
	_, err := acceptor.AcceptTunnel(acceptContext)
	if err == nil {
		testingObject.Fatalf("expected accept timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

// TestTunnelAcceptorAcceptTunnelAfterCloseWithPending 验证关闭后即使有 pending tunnel 也返回 ErrClosed。
func TestTunnelAcceptorAcceptTunnelAfterCloseWithPending(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{QueueSize: 1})
	serverStream := &fakeGeneratedTunnelBidiServerStream{
		base: &fakeTunnelEnvelopeStream{ctx: streamContext},
	}
	handleResultChannel := make(chan error, 1)
	go func() {
		handleResultChannel <- acceptor.HandleTunnelStream(serverStream)
	}()

	waitDeadline := time.Now().Add(time.Second)
	for len(acceptor.pendingTunnels) == 0 {
		if time.Now().After(waitDeadline) {
			testingObject.Fatalf("expected pending tunnel before close")
		}
		time.Sleep(5 * time.Millisecond)
	}

	acceptor.Close(nil)

	acceptContext, cancelAccept := context.WithTimeout(context.Background(), time.Second)
	defer cancelAccept()
	tunnel, err := acceptor.AcceptTunnel(acceptContext)
	if tunnel != nil {
		testingObject.Fatalf("expected nil tunnel after close, got %+v", tunnel)
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed after close, got %v", err)
	}

	select {
	case handleErr := <-handleResultChannel:
		if !errors.Is(handleErr, transport.ErrClosed) {
			testingObject.Fatalf("expected ErrClosed from handler after close, got %v", handleErr)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("handle stream did not exit after close")
	}
}

// TestTunnelAcceptorHandleStreamAfterClose 验证关闭后不再接受新 tunnel。
func TestTunnelAcceptorHandleStreamAfterClose(testingObject *testing.T) {
	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{})
	acceptor.Close(nil)
	serverStream := &fakeGeneratedTunnelBidiServerStream{
		base: &fakeTunnelEnvelopeStream{ctx: context.Background()},
	}
	err := acceptor.HandleTunnelStream(serverStream)
	if err == nil {
		testingObject.Fatalf("expected closed error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
}

// TestTunnelAcceptorAcceptTunnelSkipsTerminalPending 验证 AcceptTunnel 会跳过终态 pending tunnel。
func TestTunnelAcceptorAcceptTunnelSkipsTerminalPending(testingObject *testing.T) {
	acceptor := NewTunnelAcceptor(TunnelAcceptorConfig{QueueSize: 2})

	staleStream, err := newGRPCH2TunnelStream(&fakeTunnelEnvelopeStream{})
	if err != nil {
		testingObject.Fatalf("create stale grpc tunnel stream failed: %v", err)
	}
	staleTunnel, err := NewGRPCH2Tunnel(staleStream, transport.TunnelMeta{TunnelID: "stale-tunnel"})
	if err != nil {
		testingObject.Fatalf("create stale grpc tunnel failed: %v", err)
	}
	if err := staleTunnel.Reset(errors.New("stale tunnel")); err != nil {
		testingObject.Fatalf("reset stale tunnel failed: %v", err)
	}

	healthyStream, err := newGRPCH2TunnelStream(&fakeTunnelEnvelopeStream{})
	if err != nil {
		testingObject.Fatalf("create healthy grpc tunnel stream failed: %v", err)
	}
	healthyTunnel, err := NewGRPCH2Tunnel(healthyStream, transport.TunnelMeta{TunnelID: "healthy-tunnel"})
	if err != nil {
		testingObject.Fatalf("create healthy grpc tunnel failed: %v", err)
	}

	acceptor.pendingTunnels <- staleTunnel
	acceptor.pendingTunnels <- healthyTunnel

	acceptContext, cancelAccept := context.WithTimeout(context.Background(), time.Second)
	defer cancelAccept()
	acceptedTunnel, err := acceptor.AcceptTunnel(acceptContext)
	if err != nil {
		testingObject.Fatalf("accept tunnel failed: %v", err)
	}
	if acceptedTunnel == nil {
		testingObject.Fatalf("expected non-nil accepted tunnel")
	}
	if acceptedTunnel.ID() != "healthy-tunnel" {
		testingObject.Fatalf("expected healthy tunnel, got %s", acceptedTunnel.ID())
	}
}
