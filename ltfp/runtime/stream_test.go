package runtime

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// blockingMockTunnel 提供可阻塞读写的最小 tunnel 测试桩。
type blockingMockTunnel struct {
	id string

	state       transport.TunnelState
	reader      *io.PipeReader
	writer      *io.PipeWriter
	doneChannel chan struct{}

	mutex     sync.Mutex
	lastError error
	closeOnce sync.Once
}

func newBlockingMockTunnel(tunnelID string) *blockingMockTunnel {
	reader, writer := io.Pipe()
	return &blockingMockTunnel{
		id:          tunnelID,
		state:       transport.TunnelStateIdle,
		reader:      reader,
		writer:      writer,
		doneChannel: make(chan struct{}),
	}
}

func (tunnel *blockingMockTunnel) Read(payload []byte) (int, error) {
	return tunnel.reader.Read(payload)
}

func (tunnel *blockingMockTunnel) Write(payload []byte) (int, error) {
	return tunnel.writer.Write(payload)
}

func (tunnel *blockingMockTunnel) Close() error {
	tunnel.closeOnce.Do(func() {
		tunnel.mutex.Lock()
		tunnel.lastError = transport.ErrClosed
		tunnel.mutex.Unlock()
		_ = tunnel.writer.Close()
		_ = tunnel.reader.Close()
		close(tunnel.doneChannel)
	})
	return nil
}

func (tunnel *blockingMockTunnel) ID() string {
	return tunnel.id
}

func (tunnel *blockingMockTunnel) Meta() transport.TunnelMeta {
	return transport.TunnelMeta{TunnelID: tunnel.id}
}

func (tunnel *blockingMockTunnel) State() transport.TunnelState {
	return tunnel.state
}

func (tunnel *blockingMockTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: transport.BindingTypeGRPCH2}
}

func (tunnel *blockingMockTunnel) CloseWrite() error {
	return tunnel.writer.Close()
}

func (tunnel *blockingMockTunnel) Reset(cause error) error {
	tunnel.closeOnce.Do(func() {
		tunnel.mutex.Lock()
		tunnel.lastError = cause
		tunnel.mutex.Unlock()
		_ = tunnel.writer.CloseWithError(cause)
		_ = tunnel.reader.CloseWithError(cause)
		close(tunnel.doneChannel)
	})
	return nil
}

func (tunnel *blockingMockTunnel) SetDeadline(deadline time.Time) error {
	return nil
}

func (tunnel *blockingMockTunnel) SetReadDeadline(deadline time.Time) error {
	return nil
}

func (tunnel *blockingMockTunnel) SetWriteDeadline(deadline time.Time) error {
	return nil
}

func (tunnel *blockingMockTunnel) Done() <-chan struct{} {
	return tunnel.doneChannel
}

func (tunnel *blockingMockTunnel) Err() error {
	tunnel.mutex.Lock()
	defer tunnel.mutex.Unlock()
	return tunnel.lastError
}

// TestTrafficDataStreamWriteFragmentsByMaxDataFrameSize 验证 Write 会按配置拆分 Data 帧并保持顺序。
func TestTrafficDataStreamWriteFragmentsByMaxDataFrameSize(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-write-fragment")
	stream, err := NewTrafficDataStreamWithConfig(tunnel, protocol, TrafficDataStreamConfig{
		MaxDataFrameSize: 4,
	})
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}

	payload := []byte("abcdefghij")
	writtenSize, err := stream.Write(payload)
	if err != nil {
		testingObject.Fatalf("write payload failed: %v", err)
	}
	if writtenSize != len(payload) {
		testingObject.Fatalf("expected written size %d, got %d", len(payload), writtenSize)
	}

	expectedChunks := [][]byte{
		[]byte("abcd"),
		[]byte("efgh"),
		[]byte("ij"),
	}
	for chunkIndex, expectedChunk := range expectedChunks {
		frame, err := protocol.ReadFrame(tunnel)
		if err != nil {
			testingObject.Fatalf("read chunk frame %d failed: %v", chunkIndex, err)
		}
		if frame.Type != TrafficFrameData {
			testingObject.Fatalf("expected data frame at chunk %d, got %s", chunkIndex, frame.Type)
		}
		if !bytes.Equal(frame.Data, expectedChunk) {
			testingObject.Fatalf(
				"chunk %d mismatch: expected=%v got=%v",
				chunkIndex,
				expectedChunk,
				frame.Data,
			)
		}
	}
}

// TestTrafficDataStreamReadKeepsInternalBuffer 验证 Read 能在单帧大于调用方缓冲时保留剩余字节。
func TestTrafficDataStreamReadKeepsInternalBuffer(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-read-buffer")
	stream, err := NewTrafficDataStreamWithConfig(tunnel, protocol, TrafficDataStreamConfig{
		MaxDataFrameSize: 8,
	})
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type: TrafficFrameData,
		Data: []byte("abcdef"),
	}); err != nil {
		testingObject.Fatalf("write data frame failed: %v", err)
	}

	readBuffer := make([]byte, 2)
	firstReadSize, err := stream.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("first read failed: %v", err)
	}
	if firstReadSize != 2 || string(readBuffer[:firstReadSize]) != "ab" {
		testingObject.Fatalf("unexpected first read: size=%d payload=%q", firstReadSize, string(readBuffer[:firstReadSize]))
	}

	secondReadSize, err := stream.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("second read failed: %v", err)
	}
	if secondReadSize != 2 || string(readBuffer[:secondReadSize]) != "cd" {
		testingObject.Fatalf("unexpected second read: size=%d payload=%q", secondReadSize, string(readBuffer[:secondReadSize]))
	}

	thirdReadSize, err := stream.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("third read failed: %v", err)
	}
	if thirdReadSize != 2 || string(readBuffer[:thirdReadSize]) != "ef" {
		testingObject.Fatalf("unexpected third read: size=%d payload=%q", thirdReadSize, string(readBuffer[:thirdReadSize]))
	}
}

// TestTrafficDataStreamReadCloseReturnsEOF 验证读取 Close 帧后返回 io.EOF。
func TestTrafficDataStreamReadCloseReturnsEOF(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-read-close")
	stream, err := NewTrafficDataStream(tunnel, protocol)
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type:  TrafficFrameClose,
		Close: &TrafficClose{Code: "EOF", Message: "done"},
	}); err != nil {
		testingObject.Fatalf("write close frame failed: %v", err)
	}

	readBuffer := make([]byte, 8)
	readSize, err := stream.Read(readBuffer)
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 on close, got %d", readSize)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF, got %v", err)
	}

	readSize, err = stream.Read(readBuffer)
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 after EOF, got %d", readSize)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF after close, got %v", err)
	}
}

// TestTrafficDataStreamReadResetReturnsExplicitError 验证读取 Reset 帧会返回显式 reset 错误。
func TestTrafficDataStreamReadResetReturnsExplicitError(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-read-reset")
	stream, err := NewTrafficDataStream(tunnel, protocol)
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type:  TrafficFrameReset,
		Reset: &TrafficReset{Code: "UPSTREAM_BROKEN", Message: "upstream reset"},
	}); err != nil {
		testingObject.Fatalf("write reset frame failed: %v", err)
	}

	readSize, err := stream.Read(make([]byte, 8))
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 on reset, got %d", readSize)
	}
	if !errors.Is(err, ErrTrafficReset) {
		testingObject.Fatalf("expected ErrTrafficReset, got %v", err)
	}
	resetErr := &TrafficResetError{}
	if !errors.As(err, &resetErr) {
		testingObject.Fatalf("expected TrafficResetError, got %T", err)
	}
	if resetErr.Code != "UPSTREAM_BROKEN" || resetErr.Message != "upstream reset" {
		testingObject.Fatalf("unexpected reset details: %+v", resetErr)
	}

	readSize, err = stream.Read(make([]byte, 8))
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 after reset, got %d", readSize)
	}
	if !errors.Is(err, ErrTrafficReset) {
		testingObject.Fatalf("expected ErrTrafficReset after reset, got %v", err)
	}
}

// TestTrafficDataStreamWriteAfterCloseReturnsEOF 验证读到 Close 后后续 Write 会返回 EOF。
func TestTrafficDataStreamWriteAfterCloseReturnsEOF(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-write-after-close")
	stream, err := NewTrafficDataStream(tunnel, protocol)
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type:  TrafficFrameClose,
		Close: &TrafficClose{Code: "EOF", Message: "done"},
	}); err != nil {
		testingObject.Fatalf("write close frame failed: %v", err)
	}

	readSize, err := stream.Read(make([]byte, 8))
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 on close, got %d", readSize)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF, got %v", err)
	}

	writtenSize, err := stream.Write([]byte("should-not-write"))
	if writtenSize != 0 {
		testingObject.Fatalf("expected write size 0 after close, got %d", writtenSize)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected io.EOF after close, got %v", err)
	}
}

// TestTrafficDataStreamWriteAfterResetReturnsResetError 验证读到 Reset 后后续 Write 返回 reset 错误。
func TestTrafficDataStreamWriteAfterResetReturnsResetError(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("stream-write-after-reset")
	stream, err := NewTrafficDataStream(tunnel, protocol)
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type:  TrafficFrameReset,
		Reset: &TrafficReset{Code: "UPSTREAM_BROKEN", Message: "upstream reset"},
	}); err != nil {
		testingObject.Fatalf("write reset frame failed: %v", err)
	}

	readSize, err := stream.Read(make([]byte, 8))
	if readSize != 0 {
		testingObject.Fatalf("expected read size 0 on reset, got %d", readSize)
	}
	if !errors.Is(err, ErrTrafficReset) {
		testingObject.Fatalf("expected ErrTrafficReset, got %v", err)
	}

	writtenSize, err := stream.Write([]byte("should-not-write"))
	if writtenSize != 0 {
		testingObject.Fatalf("expected write size 0 after reset, got %d", writtenSize)
	}
	if !errors.Is(err, ErrTrafficReset) {
		testingObject.Fatalf("expected ErrTrafficReset after reset, got %v", err)
	}
	resetErr := &TrafficResetError{}
	if !errors.As(err, &resetErr) {
		testingObject.Fatalf("expected TrafficResetError after reset, got %T", err)
	}
	if resetErr.Code != "UPSTREAM_BROKEN" || resetErr.Message != "upstream reset" {
		testingObject.Fatalf("unexpected reset details after write: %+v", resetErr)
	}
}

// TestTrafficDataStreamSupportsConcurrentReadWrite 验证一侧读、一侧写并发可用。
func TestTrafficDataStreamSupportsConcurrentReadWrite(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newBlockingMockTunnel("stream-concurrent")
	stream, err := NewTrafficDataStreamWithConfig(tunnel, protocol, TrafficDataStreamConfig{
		MaxDataFrameSize: 3,
	})
	if err != nil {
		testingObject.Fatalf("create traffic data stream failed: %v", err)
	}
	expectedPayload := []byte("concurrent-stream-payload")

	type readResult struct {
		payload []byte
		err     error
	}
	readResultChannel := make(chan readResult, 1)
	go func() {
		result := bytes.Buffer{}
		buffer := make([]byte, 5)
		for {
			readSize, err := stream.Read(buffer)
			if readSize > 0 {
				_, _ = result.Write(buffer[:readSize])
			}
			if errors.Is(err, io.EOF) {
				readResultChannel <- readResult{payload: result.Bytes()}
				return
			}
			if err != nil {
				readResultChannel <- readResult{err: err}
				return
			}
		}
	}()

	writtenSize, err := stream.Write(expectedPayload)
	if err != nil {
		testingObject.Fatalf("write payload failed: %v", err)
	}
	if writtenSize != len(expectedPayload) {
		testingObject.Fatalf("expected written size %d, got %d", len(expectedPayload), writtenSize)
	}
	if err := protocol.WriteFrame(tunnel, TrafficFrame{
		Type:  TrafficFrameClose,
		Close: &TrafficClose{Code: "EOF"},
	}); err != nil {
		testingObject.Fatalf("write close frame failed: %v", err)
	}

	select {
	case result := <-readResultChannel:
		if result.err != nil {
			testingObject.Fatalf("concurrent read failed: %v", result.err)
		}
		if !bytes.Equal(result.payload, expectedPayload) {
			testingObject.Fatalf("read payload mismatch: expected=%v got=%v", expectedPayload, result.payload)
		}
	case <-time.After(time.Second):
		testingObject.Fatalf("timed out waiting concurrent read")
	}
}

// TestDefaultTrafficDataStreamConfigGrpcSafe 验证 grpc_h2 默认分片大小为安全值。
func TestDefaultTrafficDataStreamConfigGrpcSafe(testingObject *testing.T) {
	config := DefaultTrafficDataStreamConfig()
	if config.MaxDataFrameSize <= 0 {
		testingObject.Fatalf("expected positive max_data_frame_size, got %d", config.MaxDataFrameSize)
	}
	if config.MaxDataFrameSize > 1*1024*1024 {
		testingObject.Fatalf(
			"expected grpc_h2 safe default max_data_frame_size <= 1MiB, got %d",
			config.MaxDataFrameSize,
		)
	}
}
