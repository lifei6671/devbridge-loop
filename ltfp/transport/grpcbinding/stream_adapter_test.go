package grpcbinding

import (
	"context"
	"errors"
	"io"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type fakeTunnelEnvelopeStream struct {
	sendFrames []*transportgen.TunnelEnvelope
	recvFrames []*transportgen.TunnelEnvelope

	ctx       context.Context
	sendBlock <-chan struct{}
	sendReady chan<- struct{}
	recvBlock <-chan struct{}

	sendError  error
	recvError  error
	closeError error
	closeCount int
}

func (stream *fakeTunnelEnvelopeStream) Send(frame *transportgen.TunnelEnvelope) error {
	if stream.sendReady != nil {
		select {
		case stream.sendReady <- struct{}{}:
		default:
		}
	}
	if stream.sendBlock != nil {
		select {
		case <-stream.sendBlock:
		case <-stream.Context().Done():
			return stream.Context().Err()
		}
	}
	if stream.sendError != nil {
		return stream.sendError
	}
	stream.sendFrames = append(stream.sendFrames, &transportgen.TunnelEnvelope{
		Payload: append([]byte(nil), frame.Payload...),
	})
	return nil
}

func (stream *fakeTunnelEnvelopeStream) Recv() (*transportgen.TunnelEnvelope, error) {
	if stream.recvBlock != nil {
		select {
		case <-stream.recvBlock:
		case <-stream.Context().Done():
			return nil, stream.Context().Err()
		}
	}
	if len(stream.recvFrames) > 0 {
		frame := stream.recvFrames[0]
		stream.recvFrames = stream.recvFrames[1:]
		return &transportgen.TunnelEnvelope{
			Payload: append([]byte(nil), frame.Payload...),
		}, nil
	}
	if stream.recvError != nil {
		return nil, stream.recvError
	}
	return nil, io.EOF
}

func (stream *fakeTunnelEnvelopeStream) CloseSend() error {
	stream.closeCount++
	return stream.closeError
}

func (stream *fakeTunnelEnvelopeStream) Context() context.Context {
	if stream.ctx != nil {
		return stream.ctx
	}
	return context.Background()
}

type fakeTunnelEnvelopeStreamWithoutCloseSend struct {
	base *fakeTunnelEnvelopeStream
}

func (stream *fakeTunnelEnvelopeStreamWithoutCloseSend) Send(frame *transportgen.TunnelEnvelope) error {
	return stream.base.Send(frame)
}

func (stream *fakeTunnelEnvelopeStreamWithoutCloseSend) Recv() (*transportgen.TunnelEnvelope, error) {
	return stream.base.Recv()
}

func (stream *fakeTunnelEnvelopeStreamWithoutCloseSend) Context() context.Context {
	return stream.base.Context()
}

// TestGRPCH2TunnelStreamWriteReadAndClose 验证 TunnelEnvelope 的读写映射与关闭语义。
func TestGRPCH2TunnelStreamWriteReadAndClose(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{
		recvFrames: []*transportgen.TunnelEnvelope{
			{Payload: []byte("incoming-bytes")},
		},
	}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}

	outgoingPayload := []byte("outgoing-bytes")
	if err := tunnelStream.WritePayload(context.Background(), outgoingPayload); err != nil {
		testingObject.Fatalf("write payload failed: %v", err)
	}
	outgoingPayload[0] = 'X'
	if len(fakeStream.sendFrames) != 1 || string(fakeStream.sendFrames[0].Payload) != "outgoing-bytes" {
		testingObject.Fatalf("unexpected written payload: %+v", fakeStream.sendFrames)
	}

	incomingPayload, err := tunnelStream.ReadPayload(context.Background())
	if err != nil {
		testingObject.Fatalf("read payload failed: %v", err)
	}
	if string(incomingPayload) != "incoming-bytes" {
		testingObject.Fatalf("unexpected incoming payload: %q", string(incomingPayload))
	}

	if err := tunnelStream.Close(context.Background()); err != nil {
		testingObject.Fatalf("close tunnel stream failed: %v", err)
	}
	if err := tunnelStream.Close(context.Background()); err != nil {
		testingObject.Fatalf("close tunnel stream second call should be idempotent, got %v", err)
	}
	if fakeStream.closeCount != 1 {
		testingObject.Fatalf("expected close send once, got %d", fakeStream.closeCount)
	}
	select {
	case <-tunnelStream.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close")
	}
	if !errors.Is(tunnelStream.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected stream err ErrClosed, got %v", tunnelStream.Err())
	}
}

// TestGRPCH2TunnelStreamReadEOFReturnsClosed 验证 EOF 会收敛为 closed 错误。
func TestGRPCH2TunnelStreamReadEOFReturnsClosed(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{recvError: io.EOF}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	_, err = tunnelStream.ReadPayload(context.Background())
	if err == nil {
		testingObject.Fatalf("expected read error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
	if !errors.Is(tunnelStream.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected stream err ErrClosed, got %v", tunnelStream.Err())
	}
}

// TestGRPCH2TunnelStreamWriteEOFReturnsClosed 验证写路径 EOF 会收敛为 closed 错误。
func TestGRPCH2TunnelStreamWriteEOFReturnsClosed(testingObject *testing.T) {
	fakeStream := &fakeTunnelEnvelopeStream{
		sendError: io.EOF,
	}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}
	err = tunnelStream.WritePayload(context.Background(), []byte("outgoing"))
	if err == nil {
		testingObject.Fatalf("expected write error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
	if !errors.Is(tunnelStream.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected stream err ErrClosed, got %v", tunnelStream.Err())
	}
}

// TestGRPCH2TunnelStreamReadHonorsContextCancellation 验证 Read 在阻塞期间遵循调用方 context。
func TestGRPCH2TunnelStreamReadHonorsContextCancellation(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	fakeStream := &fakeTunnelEnvelopeStream{
		ctx:       streamContext,
		recvBlock: make(chan struct{}),
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}

	readContext, cancelRead := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRead()
	_, err = tunnelStream.ReadPayload(readContext)
	if err == nil {
		testingObject.Fatalf("expected read timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
	select {
	case <-tunnelStream.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after read cancellation")
	}
}

// TestGRPCH2TunnelStreamWriteHonorsContextCancellation 验证 Write 在阻塞期间遵循调用方 context。
func TestGRPCH2TunnelStreamWriteHonorsContextCancellation(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	fakeStream := &fakeTunnelEnvelopeStream{
		ctx:       streamContext,
		sendBlock: make(chan struct{}),
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}

	writeContext, cancelWrite := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelWrite()
	err = tunnelStream.WritePayload(writeContext, []byte("blocked"))
	if err == nil {
		testingObject.Fatalf("expected write timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
	select {
	case <-tunnelStream.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after write cancellation")
	}
}

// TestGRPCH2TunnelStreamCloseUnblocksBlockedWrite 验证写阻塞时 Close 不会卡死并能触发收敛。
func TestGRPCH2TunnelStreamCloseUnblocksBlockedWrite(testingObject *testing.T) {
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

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- tunnelStream.WritePayload(context.Background(), []byte("blocked-write"))
	}()
	select {
	case <-sendReady:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected write path to enter blocked send")
	}

	closeContext, cancelClose := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelClose()
	closeResultChannel := make(chan error, 1)
	go func() {
		closeResultChannel <- tunnelStream.Close(closeContext)
	}()

	select {
	case closeErr := <-closeResultChannel:
		if closeErr != nil {
			testingObject.Fatalf("expected close to finish without error, got %v", closeErr)
		}
	case <-time.After(time.Second):
		// 清理卡住分支，避免测试 goroutine 泄漏。
		cancelStream()
		<-closeResultChannel
		testingObject.Fatalf("close was blocked by write backpressure")
	}

	select {
	case writeErr := <-writeResultChannel:
		if writeErr == nil {
			testingObject.Fatalf("expected blocked write to fail after close")
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("write goroutine did not exit after close")
	}
	select {
	case <-tunnelStream.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after close")
	}
}

// TestGRPCH2TunnelStreamCloseWithoutInterruptibleWrite 验证无 cancel/CloseSend 时 Close 不会卡住。
func TestGRPCH2TunnelStreamCloseWithoutInterruptibleWrite(testingObject *testing.T) {
	sendBlock := make(chan struct{})
	sendReady := make(chan struct{}, 1)
	fakeStream := &fakeTunnelEnvelopeStreamWithoutCloseSend{
		base: &fakeTunnelEnvelopeStream{
			sendBlock: sendBlock,
			sendReady: sendReady,
		},
	}
	tunnelStream, err := newGRPCH2TunnelStream(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- tunnelStream.WritePayload(context.Background(), []byte("blocked-write"))
	}()
	select {
	case <-sendReady:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected write path to enter blocked send")
	}

	closeContext, cancelClose := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancelClose()
	if err := tunnelStream.Close(closeContext); err != nil {
		testingObject.Fatalf("expected close to finish without error, got %v", err)
	}
	select {
	case <-tunnelStream.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after close")
	}

	close(sendBlock)
	select {
	case <-writeResultChannel:
	case <-time.After(time.Second):
		testingObject.Fatalf("write goroutine did not exit after unblocking send")
	}
}

// TestGRPCH2TunnelStreamReadCanProceedWhenWriteBlocked 验证写阻塞不会阻断读路径。
func TestGRPCH2TunnelStreamReadCanProceedWhenWriteBlocked(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	sendBlock := make(chan struct{})
	sendReady := make(chan struct{}, 1)
	fakeStream := &fakeTunnelEnvelopeStream{
		ctx:       streamContext,
		sendBlock: sendBlock,
		sendReady: sendReady,
		recvFrames: []*transportgen.TunnelEnvelope{
			{Payload: []byte("incoming-while-write-blocked")},
		},
	}
	tunnelStream, err := newGRPCH2TunnelStreamWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc tunnel stream failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- tunnelStream.WritePayload(context.Background(), []byte("blocked-write"))
	}()
	select {
	case <-sendReady:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected write path to enter blocked send")
	}

	type readResult struct {
		payload []byte
		err     error
	}
	readResultChannel := make(chan readResult, 1)
	go func() {
		payload, readErr := tunnelStream.ReadPayload(context.Background())
		readResultChannel <- readResult{payload: payload, err: readErr}
	}()

	select {
	case result := <-readResultChannel:
		if result.err != nil {
			testingObject.Fatalf("expected read to proceed while write blocked, got %v", result.err)
		}
		if string(result.payload) != "incoming-while-write-blocked" {
			testingObject.Fatalf("unexpected incoming payload: %q", string(result.payload))
		}
	case <-time.After(200 * time.Millisecond):
		close(sendBlock)
		<-writeResultChannel
		testingObject.Fatalf("read was blocked by concurrent write backpressure")
	}

	close(sendBlock)
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("write should succeed after unblocking send, got %v", writeErr)
	}
}
