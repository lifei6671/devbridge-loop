package grpcbinding

import (
	"context"
	"errors"
	"io"
	"math"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type fakeControlChannelStream struct {
	sendFrames []*transportgen.ControlFrameEnvelope
	recvFrames []*transportgen.ControlFrameEnvelope

	ctx       context.Context
	sendBlock <-chan struct{}
	sendReady chan<- struct{}
	recvBlock <-chan struct{}

	sendError  error
	recvError  error
	closeError error
	closeCount int
}

func (stream *fakeControlChannelStream) Send(frame *transportgen.ControlFrameEnvelope) error {
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
	stream.sendFrames = append(stream.sendFrames, &transportgen.ControlFrameEnvelope{
		FrameType: frame.FrameType,
		Payload:   append([]byte(nil), frame.Payload...),
	})
	return nil
}

func (stream *fakeControlChannelStream) Recv() (*transportgen.ControlFrameEnvelope, error) {
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
		return &transportgen.ControlFrameEnvelope{
			FrameType: frame.FrameType,
			Payload:   append([]byte(nil), frame.Payload...),
		}, nil
	}
	if stream.recvError != nil {
		return nil, stream.recvError
	}
	return nil, io.EOF
}

func (stream *fakeControlChannelStream) CloseSend() error {
	stream.closeCount++
	return stream.closeError
}

func (stream *fakeControlChannelStream) Context() context.Context {
	if stream.ctx != nil {
		return stream.ctx
	}
	return context.Background()
}

// TestGRPCH2ControlChannelWriteReadAndClose 验证控制流读写映射与关闭语义。
func TestGRPCH2ControlChannelWriteReadAndClose(testingObject *testing.T) {
	fakeStream := &fakeControlChannelStream{
		recvFrames: []*transportgen.ControlFrameEnvelope{
			{FrameType: 23, Payload: []byte("incoming")},
		},
	}
	controlChannel, err := newGRPCH2ControlChannel(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}

	outgoingPayload := []byte("outgoing")
	if err := controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
		Type:    7,
		Payload: outgoingPayload,
	}); err != nil {
		testingObject.Fatalf("write control frame failed: %v", err)
	}
	outgoingPayload[0] = 'X'
	if len(fakeStream.sendFrames) != 1 {
		testingObject.Fatalf("expected one sent frame, got %d", len(fakeStream.sendFrames))
	}
	if fakeStream.sendFrames[0].FrameType != 7 || string(fakeStream.sendFrames[0].Payload) != "outgoing" {
		testingObject.Fatalf("unexpected sent frame: %+v", fakeStream.sendFrames[0])
	}

	incomingFrame, err := controlChannel.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read control frame failed: %v", err)
	}
	if incomingFrame.Type != 23 || string(incomingFrame.Payload) != "incoming" {
		testingObject.Fatalf("unexpected incoming frame: %+v", incomingFrame)
	}

	if err := controlChannel.Close(context.Background()); err != nil {
		testingObject.Fatalf("close control channel failed: %v", err)
	}
	if err := controlChannel.Close(context.Background()); err != nil {
		testingObject.Fatalf("close control channel second call should be idempotent, got %v", err)
	}
	if fakeStream.closeCount != 1 {
		testingObject.Fatalf("expected close send once, got %d", fakeStream.closeCount)
	}
	select {
	case <-controlChannel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close")
	}
	if !errors.Is(controlChannel.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected control channel err to be ErrClosed, got %v", controlChannel.Err())
	}
}

// TestGRPCH2ControlChannelReadEOFReturnsClosed 验证 EOF 会收敛为 closed 错误。
func TestGRPCH2ControlChannelReadEOFReturnsClosed(testingObject *testing.T) {
	fakeStream := &fakeControlChannelStream{
		recvError: io.EOF,
	}
	controlChannel, err := newGRPCH2ControlChannel(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}
	_, err = controlChannel.ReadControlFrame(context.Background())
	if err == nil {
		testingObject.Fatalf("expected read error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
	if !errors.Is(controlChannel.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected channel err ErrClosed, got %v", controlChannel.Err())
	}
}

// TestGRPCH2ControlChannelWriteEOFReturnsClosed 验证写路径 EOF 会收敛为 closed 错误。
func TestGRPCH2ControlChannelWriteEOFReturnsClosed(testingObject *testing.T) {
	fakeStream := &fakeControlChannelStream{
		sendError: io.EOF,
	}
	controlChannel, err := newGRPCH2ControlChannel(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}
	err = controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
		Type:    9,
		Payload: []byte("outgoing"),
	})
	if err == nil {
		testingObject.Fatalf("expected write error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
	if !errors.Is(controlChannel.Err(), transport.ErrClosed) {
		testingObject.Fatalf("expected channel err ErrClosed, got %v", controlChannel.Err())
	}
}

// TestGRPCH2ControlChannelReadRejectsLargeFrameType 验证超出 uint16 的 frame_type 会被拒绝。
func TestGRPCH2ControlChannelReadRejectsLargeFrameType(testingObject *testing.T) {
	fakeStream := &fakeControlChannelStream{
		recvFrames: []*transportgen.ControlFrameEnvelope{
			{FrameType: math.MaxUint16 + 1, Payload: []byte("bad")},
		},
	}
	controlChannel, err := newGRPCH2ControlChannel(fakeStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}
	_, err = controlChannel.ReadControlFrame(context.Background())
	if err == nil {
		testingObject.Fatalf("expected invalid argument error")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestGRPCH2ControlChannelReadHonorsContextCancellation 验证 Read 在阻塞期间遵循调用方 context。
func TestGRPCH2ControlChannelReadHonorsContextCancellation(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	fakeStream := &fakeControlChannelStream{
		ctx:       streamContext,
		recvBlock: make(chan struct{}),
	}
	controlChannel, err := newGRPCH2ControlChannelWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}

	readContext, cancelRead := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelRead()
	_, err = controlChannel.ReadControlFrame(readContext)
	if err == nil {
		testingObject.Fatalf("expected read timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
	select {
	case <-controlChannel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after read cancellation")
	}
}

// TestGRPCH2ControlChannelWriteHonorsContextCancellation 验证 Write 在阻塞期间遵循调用方 context。
func TestGRPCH2ControlChannelWriteHonorsContextCancellation(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	fakeStream := &fakeControlChannelStream{
		ctx:       streamContext,
		sendBlock: make(chan struct{}),
	}
	controlChannel, err := newGRPCH2ControlChannelWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}

	writeContext, cancelWrite := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancelWrite()
	err = controlChannel.WriteControlFrame(writeContext, transport.ControlFrame{
		Type:    9,
		Payload: []byte("blocked"),
	})
	if err == nil {
		testingObject.Fatalf("expected write timeout")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		testingObject.Fatalf("expected context deadline exceeded, got %v", err)
	}
	select {
	case <-controlChannel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after write cancellation")
	}
}

// TestGRPCH2ControlChannelCloseUnblocksBlockedWrite 验证写阻塞时 Close 不会卡死并能触发收敛。
func TestGRPCH2ControlChannelCloseUnblocksBlockedWrite(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	sendBlock := make(chan struct{})
	sendReady := make(chan struct{}, 1)
	fakeStream := &fakeControlChannelStream{
		ctx:       streamContext,
		sendBlock: sendBlock,
		sendReady: sendReady,
	}
	controlChannel, err := newGRPCH2ControlChannelWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    17,
			Payload: []byte("blocked-write"),
		})
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
		closeResultChannel <- controlChannel.Close(closeContext)
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
	case <-controlChannel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected done channel to close after close")
	}
}

// TestGRPCH2ControlChannelReadCanProceedWhenWriteBlocked 验证写阻塞不会阻断读路径。
func TestGRPCH2ControlChannelReadCanProceedWhenWriteBlocked(testingObject *testing.T) {
	streamContext, cancelStream := context.WithCancel(context.Background())
	defer cancelStream()
	sendBlock := make(chan struct{})
	sendReady := make(chan struct{}, 1)
	fakeStream := &fakeControlChannelStream{
		ctx:       streamContext,
		sendBlock: sendBlock,
		sendReady: sendReady,
		recvFrames: []*transportgen.ControlFrameEnvelope{
			{FrameType: 29, Payload: []byte("incoming-while-write-blocked")},
		},
	}
	controlChannel, err := newGRPCH2ControlChannelWithCancel(fakeStream, cancelStream)
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    13,
			Payload: []byte("blocked-write"),
		})
	}()
	select {
	case <-sendReady:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected write path to enter blocked send")
	}

	type readResult struct {
		frame transport.ControlFrame
		err   error
	}
	readResultChannel := make(chan readResult, 1)
	go func() {
		frame, readErr := controlChannel.ReadControlFrame(context.Background())
		readResultChannel <- readResult{frame: frame, err: readErr}
	}()

	select {
	case result := <-readResultChannel:
		if result.err != nil {
			testingObject.Fatalf("expected read to proceed while write blocked, got %v", result.err)
		}
		if result.frame.Type != 29 || string(result.frame.Payload) != "incoming-while-write-blocked" {
			testingObject.Fatalf("unexpected incoming frame: %+v", result.frame)
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

// TestGRPCH2ControlChannelReadAfterCloseReturnsClosed 验证关闭后后续读取会直接返回 closed。
func TestGRPCH2ControlChannelReadAfterCloseReturnsClosed(testingObject *testing.T) {
	controlChannel, err := newGRPCH2ControlChannel(&fakeControlChannelStream{})
	if err != nil {
		testingObject.Fatalf("create grpc control channel failed: %v", err)
	}
	if err := controlChannel.Close(context.Background()); err != nil {
		testingObject.Fatalf("close control channel failed: %v", err)
	}
	_, err = controlChannel.ReadControlFrame(context.Background())
	if err == nil {
		testingObject.Fatalf("expected closed error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
}
