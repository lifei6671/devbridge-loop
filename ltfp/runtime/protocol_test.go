package runtime

import (
	"bytes"
	"context"
	"errors"
	"io"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// mockTunnel 是 runtime 测试使用的最小 tunnel 实现。
type mockTunnel struct {
	id          string
	state       transport.TunnelState
	buffer      bytes.Buffer
	mutex       sync.Mutex
	doneChannel chan struct{}
	lastError   error
}

// Read 实现 io.Reader。
func (tunnel *mockTunnel) Read(payload []byte) (int, error) {
	tunnel.mutex.Lock()
	defer tunnel.mutex.Unlock()
	return tunnel.buffer.Read(payload)
}

// Write 实现 io.Writer。
func (tunnel *mockTunnel) Write(payload []byte) (int, error) {
	tunnel.mutex.Lock()
	defer tunnel.mutex.Unlock()
	return tunnel.buffer.Write(payload)
}

// Close 实现 io.Closer。
func (tunnel *mockTunnel) Close() error {
	tunnel.lastError = transport.ErrClosed
	return nil
}

// ID 返回 tunnel ID。
func (tunnel *mockTunnel) ID() string {
	return tunnel.id
}

// Meta 返回 tunnel 元数据。
func (tunnel *mockTunnel) Meta() transport.TunnelMeta {
	return transport.TunnelMeta{TunnelID: tunnel.id}
}

// State 返回 tunnel 状态。
func (tunnel *mockTunnel) State() transport.TunnelState {
	return tunnel.state
}

// BindingInfo 返回 binding 信息。
func (tunnel *mockTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: transport.BindingTypeGRPCH2}
}

// CloseWrite 实现半关闭写侧。
func (tunnel *mockTunnel) CloseWrite() error {
	return nil
}

// Reset 实现重置语义。
func (tunnel *mockTunnel) Reset(cause error) error {
	tunnel.lastError = cause
	return nil
}

// SetDeadline 实现 deadline 设置。
func (tunnel *mockTunnel) SetDeadline(deadline time.Time) error {
	return nil
}

// SetReadDeadline 实现读 deadline 设置。
func (tunnel *mockTunnel) SetReadDeadline(deadline time.Time) error {
	return nil
}

// SetWriteDeadline 实现写 deadline 设置。
func (tunnel *mockTunnel) SetWriteDeadline(deadline time.Time) error {
	return nil
}

// Done 返回生命周期结束通知。
func (tunnel *mockTunnel) Done() <-chan struct{} {
	return tunnel.doneChannel
}

// Err 返回最近错误。
func (tunnel *mockTunnel) Err() error {
	return tunnel.lastError
}

// newMockTunnel 创建测试用 idle tunnel。
func newMockTunnel(tunnelID string) *mockTunnel {
	return &mockTunnel{
		id:          tunnelID,
		state:       transport.TunnelStateIdle,
		doneChannel: make(chan struct{}),
	}
}

// overlapDetectTunnel 用于检测并发 Write 是否发生重叠。
type overlapDetectTunnel struct {
	*mockTunnel
	inflightWrites int32
	overlapCount   int32
}

// Write 记录并发写重叠次数。
func (tunnel *overlapDetectTunnel) Write(payload []byte) (int, error) {
	// 先记录并发写计数，再通过 sleep 放大竞态窗口。
	inflight := atomic.AddInt32(&tunnel.inflightWrites, 1)
	if inflight > 1 {
		atomic.AddInt32(&tunnel.overlapCount, 1)
	}
	time.Sleep(2 * time.Millisecond)
	writtenSize, err := tunnel.mockTunnel.Write(payload)
	atomic.AddInt32(&tunnel.inflightWrites, -1)
	return writtenSize, err
}

// OverlapCount 返回写重叠次数。
func (tunnel *overlapDetectTunnel) OverlapCount() int32 {
	return atomic.LoadInt32(&tunnel.overlapCount)
}

// TestTrafficMetaValidate 验证 TrafficMeta 的最小字段约束。
func TestTrafficMetaValidate(testingObject *testing.T) {
	testCases := []struct {
		name       string
		meta       TrafficMeta
		expectErr  bool
		errMatcher error
	}{
		{
			name: "valid meta",
			meta: TrafficMeta{
				TrafficID: "traffic-1",
				ServiceID: "svc-1",
			},
		},
		{
			name: "empty traffic id",
			meta: TrafficMeta{
				ServiceID: "svc-1",
			},
			expectErr:  true,
			errMatcher: transport.ErrInvalidArgument,
		},
		{
			name: "empty service id",
			meta: TrafficMeta{
				TrafficID: "traffic-1",
			},
			expectErr:  true,
			errMatcher: transport.ErrInvalidArgument,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			err := testCase.meta.Validate()
			if testCase.expectErr {
				if err == nil {
					testingObject.Fatalf("expected error, got nil")
				}
				if testCase.errMatcher != nil && !errors.Is(err, testCase.errMatcher) {
					testingObject.Fatalf("expected error %v, got %v", testCase.errMatcher, err)
				}
				return
			}
			if err != nil {
				testingObject.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

// TestTrafficFrameValidate 验证帧类型与载荷一致性约束。
func TestTrafficFrameValidate(testingObject *testing.T) {
	testCases := []struct {
		name      string
		frame     TrafficFrame
		expectErr bool
	}{
		{
			name: "valid open",
			frame: TrafficFrame{
				Type: TrafficFrameOpen,
				Open: &TrafficMeta{TrafficID: "traffic-1", ServiceID: "svc-1"},
			},
		},
		{
			name: "valid data",
			frame: TrafficFrame{
				Type: TrafficFrameData,
				Data: []byte("payload"),
			},
		},
		{
			name: "open missing payload",
			frame: TrafficFrame{
				Type: TrafficFrameOpen,
			},
			expectErr: true,
		},
		{
			name: "open carries data",
			frame: TrafficFrame{
				Type: TrafficFrameOpen,
				Open: &TrafficMeta{TrafficID: "traffic-1", ServiceID: "svc-1"},
				Data: []byte("unexpected"),
			},
			expectErr: true,
		},
		{
			name: "unknown frame type",
			frame: TrafficFrame{
				Type: TrafficFrameType("unknown"),
			},
			expectErr: true,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			err := testCase.frame.Validate()
			if testCase.expectErr && err == nil {
				testingObject.Fatalf("expected error, got nil")
			}
			if !testCase.expectErr && err != nil {
				testingObject.Fatalf("expected nil error, got %v", err)
			}
		})
	}
}

// TestLengthPrefixedTrafficProtocolRoundTrip 验证统一 ReadFrame/WriteFrame 入口可读写最小帧集合。
func TestLengthPrefixedTrafficProtocolRoundTrip(testingObject *testing.T) {
	testCases := []struct {
		name  string
		frame TrafficFrame
	}{
		{
			name: "open",
			frame: TrafficFrame{
				Type: TrafficFrameOpen,
				Open: &TrafficMeta{
					TrafficID:    "traffic-open",
					SessionID:    "session-1",
					SessionEpoch: 9,
					ServiceID:    "svc-open",
					Labels: map[string]string{
						"trace": "trace-open",
					},
				},
			},
		},
		{
			name: "open_ack",
			frame: TrafficFrame{
				Type:    TrafficFrameOpenAck,
				OpenAck: &TrafficOpenAck{Accepted: true, Code: "OK", Message: "accepted"},
			},
		},
		{
			name: "data",
			frame: TrafficFrame{
				Type: TrafficFrameData,
				Data: []byte("hello-runtime"),
			},
		},
		{
			name: "close",
			frame: TrafficFrame{
				Type:  TrafficFrameClose,
				Close: &TrafficClose{Code: "EOF", Message: "done"},
			},
		},
		{
			name: "reset",
			frame: TrafficFrame{
				Type:  TrafficFrameReset,
				Reset: &TrafficReset{Code: "BROKEN", Message: "network broken"},
			},
		},
	}

	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("round-trip-tunnel")
	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			if err := protocol.WriteFrame(tunnel, testCase.frame); err != nil {
				testingObject.Fatalf("write frame failed: %v", err)
			}
			decodedFrame, err := protocol.ReadFrame(tunnel)
			if err != nil {
				testingObject.Fatalf("read frame failed: %v", err)
			}
			if !reflect.DeepEqual(decodedFrame, testCase.frame) {
				testingObject.Fatalf("decoded frame mismatch: got=%+v expected=%+v", decodedFrame, testCase.frame)
			}
		})
	}
}

// TestLengthPrefixedTrafficProtocolConcurrentWritesSerialized 验证同 tunnel 多写方会被串行化。
func TestLengthPrefixedTrafficProtocolConcurrentWritesSerialized(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := &overlapDetectTunnel{
		mockTunnel: newMockTunnel("serial-write-tunnel"),
	}
	const writerCount = 10
	var writerGroup sync.WaitGroup
	for writerIndex := 0; writerIndex < writerCount; writerIndex++ {
		writerGroup.Add(1)
		go func(index int) {
			defer writerGroup.Done()
			frame := TrafficFrame{
				Type: TrafficFrameData,
				Data: []byte{byte(index), byte(index + 1), byte(index + 2)},
			}
			if err := protocol.WriteFrame(tunnel, frame); err != nil {
				testingObject.Errorf("write frame failed: %v", err)
			}
		}(writerIndex)
	}
	writerGroup.Wait()
	if overlapCount := tunnel.OverlapCount(); overlapCount != 0 {
		testingObject.Fatalf("expected no overlapping writes, got %d", overlapCount)
	}
}

// TestLengthPrefixedTrafficProtocolReadFrameFromClosedTunnel 验证 EOF 会收敛为 tunnel closed 错误。
func TestLengthPrefixedTrafficProtocolReadFrameFromClosedTunnel(testingObject *testing.T) {
	protocol := NewLengthPrefixedTrafficProtocol()
	tunnel := newMockTunnel("closed-tunnel")
	_, err := protocol.ReadFrame(tunnel)
	if err == nil {
		testingObject.Fatalf("expected read frame error")
	}
	if !errors.Is(err, transport.ErrTunnelClosed) {
		testingObject.Fatalf("expected ErrTunnelClosed, got %v", err)
	}
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected wrapped io.EOF, got %v", err)
	}
}

// TestTrafficMetaDoesNotContainServiceKey 验证 runtime 元数据不引入 service_key 字段。
func TestTrafficMetaDoesNotContainServiceKey(testingObject *testing.T) {
	trafficMetaType := reflect.TypeOf(TrafficMeta{})
	if _, exists := trafficMetaType.FieldByName("ServiceKey"); exists {
		testingObject.Fatalf("TrafficMeta should not contain ServiceKey field")
	}
}

// TestLengthPrefixedTrafficProtocolNoContextInIO 确认协议读写入口未引入 context 参数。
func TestLengthPrefixedTrafficProtocolNoContextInIO(testingObject *testing.T) {
	protocolType := reflect.TypeOf(&LengthPrefixedTrafficProtocol{})
	writeFrameMethod, exists := protocolType.MethodByName("WriteFrame")
	if !exists {
		testingObject.Fatalf("WriteFrame method not found")
	}
	readFrameMethod, exists := protocolType.MethodByName("ReadFrame")
	if !exists {
		testingObject.Fatalf("ReadFrame method not found")
	}

	contextType := reflect.TypeOf((*context.Context)(nil)).Elem()
	for argumentIndex := 1; argumentIndex < writeFrameMethod.Type.NumIn(); argumentIndex++ {
		if writeFrameMethod.Type.In(argumentIndex) == contextType {
			testingObject.Fatalf("WriteFrame should not contain context.Context parameter")
		}
	}
	for argumentIndex := 1; argumentIndex < readFrameMethod.Type.NumIn(); argumentIndex++ {
		if readFrameMethod.Type.In(argumentIndex) == contextType {
			testingObject.Fatalf("ReadFrame should not contain context.Context parameter")
		}
	}
}
