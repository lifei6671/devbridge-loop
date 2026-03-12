package grpcbinding

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestGRPCH2TunnelReadWrite 验证 tunnel 的 io 读写适配与缓冲行为。
func TestGRPCH2TunnelReadWrite(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{
		recvFrames: []*transportgen.TunnelEnvelope{
			{Payload: []byte("abcdef")},
		},
	}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID:     "tunnel-1",
		SessionID:    "session-1",
		SessionEpoch: 3,
	})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}

	readBuffer := make([]byte, 2)
	firstReadSize, err := tunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("first read failed: %v", err)
	}
	if firstReadSize != 2 || string(readBuffer[:firstReadSize]) != "ab" {
		testingObject.Fatalf("unexpected first read: size=%d payload=%q", firstReadSize, string(readBuffer[:firstReadSize]))
	}
	secondReadSize, err := tunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("second read failed: %v", err)
	}
	if secondReadSize != 2 || string(readBuffer[:secondReadSize]) != "cd" {
		testingObject.Fatalf("unexpected second read: size=%d payload=%q", secondReadSize, string(readBuffer[:secondReadSize]))
	}
	thirdReadSize, err := tunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("third read failed: %v", err)
	}
	if thirdReadSize != 2 || string(readBuffer[:thirdReadSize]) != "ef" {
		testingObject.Fatalf("unexpected third read: size=%d payload=%q", thirdReadSize, string(readBuffer[:thirdReadSize]))
	}

	writtenSize, err := tunnel.Write([]byte("outgoing"))
	if err != nil {
		testingObject.Fatalf("write payload failed: %v", err)
	}
	if writtenSize != len("outgoing") {
		testingObject.Fatalf("unexpected write size: %d", writtenSize)
	}
	if len(fakeStream.sendFrames) != 1 || string(fakeStream.sendFrames[0].Payload) != "outgoing" {
		testingObject.Fatalf("unexpected sent frames: %+v", fakeStream.sendFrames)
	}

	fourthReadSize, err := tunnel.Read(readBuffer)
	if fourthReadSize != 0 {
		testingObject.Fatalf("expected EOF read size 0, got %d", fourthReadSize)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF, got %v", err)
	}
}

// TestGRPCH2TunnelReadDeadline 验证读 deadline 超时不会破坏 tunnel 生命周期。
func TestGRPCH2TunnelReadDeadline(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{
		recvFrames: []*transportgen.TunnelEnvelope{
			{Payload: []byte("still-readable")},
		},
	}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "tunnel-deadline",
	})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}
	if err := tunnel.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		testingObject.Fatalf("set read deadline failed: %v", err)
	}
	_, err = tunnel.Read(make([]byte, 8))
	if !errors.Is(err, transport.ErrTimeout) {
		testingObject.Fatalf("expected ErrTimeout, got %v", err)
	}
	if state := tunnel.State(); state != transport.TunnelStateIdle {
		testingObject.Fatalf("expected idle tunnel after read timeout, got %s", state)
	}

	if err := tunnel.SetReadDeadline(time.Time{}); err != nil {
		testingObject.Fatalf("clear read deadline failed: %v", err)
	}
	readBuffer := make([]byte, 32)
	readSize, err := tunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("expected read success after timeout, got %v", err)
	}
	if string(readBuffer[:readSize]) != "still-readable" {
		testingObject.Fatalf("unexpected payload after timeout: %q", string(readBuffer[:readSize]))
	}
}

// TestGRPCH2TunnelWriteDeadline 验证写 deadline 超时不会破坏 tunnel 生命周期。
func TestGRPCH2TunnelWriteDeadline(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "tunnel-write-deadline",
	})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}

	if err := tunnel.SetWriteDeadline(time.Now().Add(-time.Millisecond)); err != nil {
		testingObject.Fatalf("set write deadline failed: %v", err)
	}
	writtenSize, err := tunnel.Write([]byte("timed-out-write"))
	if !errors.Is(err, transport.ErrTimeout) {
		testingObject.Fatalf("expected ErrTimeout, got %v", err)
	}
	if writtenSize != 0 {
		testingObject.Fatalf("expected zero write size on timeout, got %d", writtenSize)
	}
	if state := tunnel.State(); state != transport.TunnelStateIdle {
		testingObject.Fatalf("expected idle tunnel after write timeout, got %s", state)
	}

	if err := tunnel.SetWriteDeadline(time.Time{}); err != nil {
		testingObject.Fatalf("clear write deadline failed: %v", err)
	}
	writtenSize, err = tunnel.Write([]byte("write-after-timeout"))
	if err != nil {
		testingObject.Fatalf("expected write success after timeout, got %v", err)
	}
	if writtenSize != len("write-after-timeout") {
		testingObject.Fatalf("unexpected write size after timeout: %d", writtenSize)
	}
	if len(fakeStream.sendFrames) != 1 || string(fakeStream.sendFrames[0].Payload) != "write-after-timeout" {
		testingObject.Fatalf("unexpected sent frames after timeout: %+v", fakeStream.sendFrames)
	}
}

// TestGRPCH2TunnelReadDeadlineInterruptsBlockedRead 验证读 deadline 可中断进行中的阻塞读取。
func TestGRPCH2TunnelReadDeadlineInterruptsBlockedRead(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	fakeStream := &fakeTunnelEnvelopeStream{
		ctx:       streamContext,
		recvBlock: make(chan struct{}),
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{TunnelID: "tunnel-blocked-read-deadline"})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}
	if err := tunnel.SetReadDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		testingObject.Fatalf("set read deadline failed: %v", err)
	}

	type readResult struct {
		readSize int
		err      error
	}
	readResultChannel := make(chan readResult, 1)
	go func() {
		readSize, readErr := tunnel.Read(make([]byte, 16))
		readResultChannel <- readResult{readSize: readSize, err: readErr}
	}()

	select {
	case result := <-readResultChannel:
		if result.readSize != 0 {
			testingObject.Fatalf("expected zero read size on timeout, got %d", result.readSize)
		}
		if !errors.Is(result.err, transport.ErrTimeout) {
			testingObject.Fatalf("expected ErrTimeout, got %v", result.err)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("blocked read did not exit on deadline")
	}
}

// TestGRPCH2TunnelWriteDeadlineInterruptsBlockedWrite 验证写 deadline 可中断进行中的阻塞写入。
func TestGRPCH2TunnelWriteDeadlineInterruptsBlockedWrite(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()

	sendBlock := make(chan struct{})
	sendReady := make(chan struct{}, 1)
	fakeStream := &fakeTunnelEnvelopeStream{
		ctx:       streamContext,
		sendBlock: sendBlock,
		sendReady: sendReady,
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{TunnelID: "tunnel-blocked-write-deadline"})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}
	if err := tunnel.SetWriteDeadline(time.Now().Add(80 * time.Millisecond)); err != nil {
		testingObject.Fatalf("set write deadline failed: %v", err)
	}

	type writeResult struct {
		writtenSize int
		err         error
	}
	writeResultChannel := make(chan writeResult, 1)
	go func() {
		writtenSize, writeErr := tunnel.Write([]byte("blocked-write"))
		writeResultChannel <- writeResult{writtenSize: writtenSize, err: writeErr}
	}()

	select {
	case <-sendReady:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected write path to enter blocked send")
	}

	select {
	case result := <-writeResultChannel:
		if result.writtenSize != 0 {
			testingObject.Fatalf("expected zero write size on timeout, got %d", result.writtenSize)
		}
		if !errors.Is(result.err, transport.ErrTimeout) {
			testingObject.Fatalf("expected ErrTimeout, got %v", result.err)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("blocked write did not exit on deadline")
	}
}

// TestGRPCH2TunnelCloseWriteUnsupported 验证首版未开放 CloseWrite 能力。
func TestGRPCH2TunnelCloseWriteUnsupported(testingObject *testing.T) {
	tunnelStream, err := newGRPCH2TunnelStream(&fakeTunnelEnvelopeStream{})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "tunnel-close-write",
	})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}
	err = tunnel.CloseWrite()
	if !errors.Is(err, transport.ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestGRPCH2TunnelResetMarksBroken 验证 Reset 会将 tunnel 标记为 broken。
func TestGRPCH2TunnelResetMarksBroken(testingObject *testing.T) {
	tunnelStream, err := newGRPCH2TunnelStream(&fakeTunnelEnvelopeStream{})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "tunnel-reset",
	})
	if err != nil {
		testingObject.Fatalf("create grpc tunnel failed: %v", err)
	}
	if err := tunnel.Reset(errors.New("upstream broken")); err != nil {
		testingObject.Fatalf("reset tunnel failed: %v", err)
	}
	if state := tunnel.State(); state != transport.TunnelStateBroken {
		testingObject.Fatalf("expected broken state, got %s", state)
	}
	if tunnel.Err() == nil || !strings.Contains(tunnel.Err().Error(), "upstream broken") {
		testingObject.Fatalf("expected reset cause in error, got %v", tunnel.Err())
	}
}
