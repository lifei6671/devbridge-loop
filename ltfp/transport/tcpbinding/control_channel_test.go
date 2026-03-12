package tcpbinding

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTCPControlChannelWriteReadAndClose 验证控制通道的读写和关闭语义。
func TestTCPControlChannelWriteReadAndClose(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientChannel, err := NewTCPControlChannel(clientConn, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create client control channel failed: %v", err)
	}
	serverChannel, err := NewTCPControlChannel(serverConn, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create server control channel failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- clientChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    31,
			Payload: []byte("control-payload"),
		})
	}()

	frame, err := serverChannel.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read control frame failed: %v", err)
	}
	if frame.Type != 31 || string(frame.Payload) != "control-payload" {
		testingObject.Fatalf("unexpected control frame: %+v", frame)
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("write control frame failed: %v", writeErr)
	}

	if err := clientChannel.Close(context.Background()); err != nil {
		testingObject.Fatalf("close client control channel failed: %v", err)
	}
	if err := clientChannel.Close(context.Background()); err != nil {
		testingObject.Fatalf("close client control channel second call should be idempotent, got %v", err)
	}
	_, err = serverChannel.ReadControlFrame(context.Background())
	if err == nil {
		testingObject.Fatalf("expected closed read error")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
}

// TestTCPControlChannelWriteReadLargeFrameWithFragmentation 验证大控制帧会自动分块并在对端重组。
func TestTCPControlChannelWriteReadLargeFrameWithFragmentation(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	config := DefaultTransportConfig()
	config.MaxControlFramePayloadSize = 80

	clientChannel, err := NewTCPControlChannel(clientConn, config)
	if err != nil {
		testingObject.Fatalf("create client control channel failed: %v", err)
	}
	serverChannel, err := NewTCPControlChannel(serverConn, config)
	if err != nil {
		testingObject.Fatalf("create server control channel failed: %v", err)
	}

	largePayload := bytes.Repeat([]byte("x"), 200)
	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- clientChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    41,
			Payload: largePayload,
		})
	}()

	frame, err := serverChannel.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read fragmented control frame failed: %v", err)
	}
	if frame.Type != 41 {
		testingObject.Fatalf("unexpected frame type after reassembly: got=%d want=%d", frame.Type, 41)
	}
	if !bytes.Equal(frame.Payload, largePayload) {
		testingObject.Fatalf("unexpected reassembled payload length: got=%d want=%d", len(frame.Payload), len(largePayload))
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("write fragmented control frame failed: %v", writeErr)
	}
}

// TestTCPControlChannelPrioritizedWriteBypassesLowPriorityFragments 验证高优控制帧可在低优大消息分块之间插队。
func TestTCPControlChannelPrioritizedWriteBypassesLowPriorityFragments(testingObject *testing.T) {
	conn := newMockConn()
	conn.writeReadyChannel = make(chan struct{}, 8)
	conn.writeReleaseChannel = make(chan struct{}, 8)

	config := DefaultTransportConfig()
	config.MaxControlFramePayloadSize = 40
	controlChannel, err := NewTCPControlChannel(conn, config)
	if err != nil {
		testingObject.Fatalf("create control channel failed: %v", err)
	}

	lowWriteResultChannel := make(chan error, 1)
	go func() {
		lowWriteResultChannel <- controlChannel.WritePrioritizedControlFrame(context.Background(), transport.PrioritizedControlFrame{
			Priority: transport.ControlMessagePriorityLow,
			Frame: transport.ControlFrame{
				Type:    51,
				Payload: bytes.Repeat([]byte("l"), 64),
			},
		})
	}()

	select {
	case <-conn.writeReadyChannel:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected first low-priority fragment write")
	}
	recordedWrites := conn.Writes()
	if len(recordedWrites) != 1 {
		testingObject.Fatalf("expected first raw frame recorded, got %d", len(recordedWrites))
	}
	if binary.BigEndian.Uint16(recordedWrites[0][0:2]) != transport.ControlFrameTypeFragment {
		testingObject.Fatalf("expected first raw frame to be fragment, got type=%d", binary.BigEndian.Uint16(recordedWrites[0][0:2]))
	}

	highWriteResultChannel := make(chan error, 1)
	go func() {
		highWriteResultChannel <- controlChannel.WritePrioritizedControlFrame(context.Background(), transport.PrioritizedControlFrame{
			Priority: transport.ControlMessagePriorityHigh,
			Frame: transport.ControlFrame{
				Type:    7,
				Payload: []byte("heartbeat"),
			},
		})
	}()

	deadline := time.Now().Add(time.Second)
	for {
		controlChannel.queueMutex.Lock()
		highPriorityQueued := len(controlChannel.writeQueue.highPriorityWrites)
		controlChannel.queueMutex.Unlock()
		if highPriorityQueued == 1 {
			break
		}
		if time.Now().After(deadline) {
			testingObject.Fatalf("expected high-priority frame to enter queue before releasing low fragment")
		}
		time.Sleep(time.Millisecond)
	}

	conn.writeReleaseChannel <- struct{}{}
	select {
	case <-conn.writeReadyChannel:
	case <-time.After(time.Second):
		testingObject.Fatalf("expected high-priority write after first low fragment")
	}
	recordedWrites = conn.Writes()
	if len(recordedWrites) < 2 {
		testingObject.Fatalf("expected second raw frame recorded, got %d", len(recordedWrites))
	}
	if binary.BigEndian.Uint16(recordedWrites[1][0:2]) != 7 {
		testingObject.Fatalf("expected second raw frame to be high priority heartbeat, got type=%d", binary.BigEndian.Uint16(recordedWrites[1][0:2]))
	}

	for remainingWrites := 0; remainingWrites < 3; remainingWrites++ {
		conn.writeReleaseChannel <- struct{}{}
		select {
		case <-conn.writeReadyChannel:
		case <-time.After(time.Second):
			testingObject.Fatalf("expected remaining low-priority fragment write %d", remainingWrites)
		}
	}
	conn.writeReleaseChannel <- struct{}{}

	if highWriteErr := <-highWriteResultChannel; highWriteErr != nil {
		testingObject.Fatalf("high-priority write failed: %v", highWriteErr)
	}
	if lowWriteErr := <-lowWriteResultChannel; lowWriteErr != nil {
		testingObject.Fatalf("low-priority fragmented write failed: %v", lowWriteErr)
	}
}

// TestTCPControlChannelRejectReservedFrameTypeWithoutClosing 验证本地参数校验失败不会关闭控制通道。
func TestTCPControlChannelRejectReservedFrameTypeWithoutClosing(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	config := DefaultTransportConfig()
	config.MaxControlFramePayloadSize = 80
	clientChannel, err := NewTCPControlChannel(clientConn, config)
	if err != nil {
		testingObject.Fatalf("create client control channel failed: %v", err)
	}
	serverChannel, err := NewTCPControlChannel(serverConn, config)
	if err != nil {
		testingObject.Fatalf("create server control channel failed: %v", err)
	}

	err = clientChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
		Type:    transport.ControlFrameTypeFragment,
		Payload: []byte("forbidden"),
	})
	if err == nil {
		testingObject.Fatalf("expected reserved frame type rejection")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if clientChannel.Err() != nil {
		testingObject.Fatalf("expected control channel err to remain nil after local validation failure, got %v", clientChannel.Err())
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		writeResultChannel <- clientChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    23,
			Payload: []byte("still-open"),
		})
	}()
	frame, err := serverChannel.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read control frame after local validation failure failed: %v", err)
	}
	if frame.Type != 23 || string(frame.Payload) != "still-open" {
		testingObject.Fatalf("unexpected control frame after local validation failure: %+v", frame)
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("write after local validation failure failed: %v", writeErr)
	}
}

// TestTCPControlChannelProtocolErrorClosesUnderlyingConn 验证协议错误会真正关闭底层连接。
func TestTCPControlChannelProtocolErrorClosesUnderlyingConn(testingObject *testing.T) {
	conn := newMockConn()
	header := make([]byte, controlFrameHeaderSize)
	binary.BigEndian.PutUint16(header[0:2], 9)
	binary.BigEndian.PutUint32(header[2:6], 128)
	conn.EnqueueReadChunk(header)

	config := DefaultTransportConfig()
	config.MaxControlFramePayloadSize = 32
	controlChannel, err := NewTCPControlChannel(conn, config)
	if err != nil {
		testingObject.Fatalf("create control channel failed: %v", err)
	}

	_, err = controlChannel.ReadControlFrame(context.Background())
	if err == nil {
		testingObject.Fatalf("expected protocol error")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if conn.CloseCount() != 1 {
		testingObject.Fatalf("expected underlying conn closed once, got %d", conn.CloseCount())
	}
}

// TestTCPControlChannelReadHonorsContextCancellation 验证阻塞读取会响应 ctx 超时并关闭通道。
func TestTCPControlChannelReadHonorsContextCancellation(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	controlChannel, err := NewTCPControlChannel(clientConn, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create control channel failed: %v", err)
	}

	readContext, cancelRead := context.WithTimeout(context.Background(), 30*time.Millisecond)
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
		testingObject.Fatalf("expected control channel done after timeout")
	}
}

// TestTCPControlChannelWriteHonorsContextCancellation 验证阻塞写入会返回调用方的 ctx 超时。
func TestTCPControlChannelWriteHonorsContextCancellation(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	controlChannel, err := NewTCPControlChannel(clientConn, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create control channel failed: %v", err)
	}

	writeContext, cancelWrite := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelWrite()
	err = controlChannel.WriteControlFrame(writeContext, transport.ControlFrame{
		Type:    11,
		Payload: []byte("blocked-write"),
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
		testingObject.Fatalf("expected control channel done after timeout")
	}
}
