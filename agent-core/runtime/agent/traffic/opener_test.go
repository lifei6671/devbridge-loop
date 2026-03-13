package traffic

import (
	"context"
	"errors"
	"io"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// openerTestSelector 记录 endpoint 选择输入并返回预设结果。
type openerTestSelector struct {
	mutex sync.Mutex

	endpoint Endpoint
	err      error

	lastServiceID string
	lastHint      map[string]string
}

// SelectEndpoint 返回预设 endpoint。
func (selector *openerTestSelector) SelectEndpoint(ctx context.Context, serviceID string, hint map[string]string) (Endpoint, error) {
	selector.mutex.Lock()
	defer selector.mutex.Unlock()
	selector.lastServiceID = serviceID
	selector.lastHint = copyStringMap(hint)
	if selector.err != nil {
		return Endpoint{}, selector.err
	}
	return selector.endpoint, nil
}

// openerTestDialer 模拟 upstream 拨号行为。
type openerTestDialer struct {
	dialFunc func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error)
}

// Dial 执行预设拨号逻辑。
func (dialer *openerTestDialer) Dial(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
	return dialer.dialFunc(ctx, endpoint)
}

// openerTestRelay 模拟 relay 结果。
type openerTestRelay struct {
	relayFunc func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error
}

// Relay 执行预设 relay 逻辑。
func (relay *openerTestRelay) Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
	return relay.relayFunc(ctx, tunnel, upstream, trafficID)
}

// openerTestConn 是测试用 upstream 连接。
type openerTestConn struct {
	closeCount atomic.Int32
}

// Read 测试中不消费上游数据。
func (connection *openerTestConn) Read(payload []byte) (int, error) {
	return 0, io.EOF
}

// Write 测试中不向上游写数据。
func (connection *openerTestConn) Write(payload []byte) (int, error) {
	return len(payload), nil
}

// Close 记录关闭次数。
func (connection *openerTestConn) Close() error {
	connection.closeCount.Add(1)
	return nil
}

// openerTestTunnelIO 记录向 tunnel 写出的 payload。
type openerTestTunnelIO struct {
	mutex sync.Mutex

	writes                   []pb.StreamPayload
	failWriteWhenContextDone bool
}

// ReadPayload 测试中不依赖 tunnel 读路径。
func (ioAdapter *openerTestTunnelIO) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	return pb.StreamPayload{}, io.EOF
}

// WritePayload 记录写出的 payload。
func (ioAdapter *openerTestTunnelIO) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	if ioAdapter.failWriteWhenContextDone && ctx != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
	}
	ioAdapter.mutex.Lock()
	defer ioAdapter.mutex.Unlock()
	ioAdapter.writes = append(ioAdapter.writes, payload)
	return nil
}

// Writes 返回已写 payload 副本。
func (ioAdapter *openerTestTunnelIO) Writes() []pb.StreamPayload {
	ioAdapter.mutex.Lock()
	defer ioAdapter.mutex.Unlock()
	copied := make([]pb.StreamPayload, 0, len(ioAdapter.writes))
	copied = append(copied, ioAdapter.writes...)
	return copied
}

// TestNilOpenerHandleReleasesLease 验证 nil opener 也会释放 handoff reader lease。
func TestNilOpenerHandleReleasesLease(testingObject *testing.T) {
	testingObject.Parallel()
	acceptor := NewAcceptor()
	handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-nil-opener", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-nil-opener",
				ServiceID: "svc-nil-opener",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	var opener *Opener
	_, err = opener.Handle(context.Background(), handoff, &openerTestTunnelIO{})
	if !errors.Is(err, ErrTrafficRuntimeDependencyMissing) {
		testingObject.Fatalf("expected dependency missing error, got: %v", err)
	}
	if acceptor.IsReaderOwned("tunnel-nil-opener") {
		testingObject.Fatalf("expected reader lease released on nil opener path")
	}
}

// TestOpenerHandleInvalidTrafficOpenErrorCode 验证 invalid open 会返回对应错误码。
func TestOpenerHandleInvalidTrafficOpenErrorCode(testingObject *testing.T) {
	testingObject.Parallel()
	opener, err := NewOpener(OpenerOptions{
		Selector: &openerTestSelector{
			err: errors.New("selector should not be called for invalid open"),
		},
		Dialer: &openerTestDialer{
			dialFunc: func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
				return nil, errors.New("dialer should not be called for invalid open")
			},
		},
		Relay: &openerTestRelay{
			relayFunc: func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
				return nil
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}

	testCases := []struct {
		name          string
		trafficOpen   pb.TrafficOpen
		wantErrorCode string
	}{
		{
			name: "empty_traffic_id",
			trafficOpen: pb.TrafficOpen{
				TrafficID: "",
				ServiceID: "svc-1",
			},
			wantErrorCode: ltfperrors.CodeMissingRequiredField,
		},
		{
			name: "empty_service_id",
			trafficOpen: pb.TrafficOpen{
				TrafficID: "traffic-1",
				ServiceID: "   ",
			},
			wantErrorCode: ltfperrors.CodeTrafficInvalidServiceID,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		testingObject.Run(testCase.name, func(testingObject *testing.T) {
			testingObject.Parallel()
			acceptor := NewAcceptor()
			handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-"+testCase.name, &acceptorTestReader{
				payload: pb.StreamPayload{
					OpenReq: &testCase.trafficOpen,
				},
			})
			if err != nil {
				testingObject.Fatalf("wait traffic open failed: %v", err)
			}
			tunnelIO := &openerTestTunnelIO{}
			_, err = opener.Handle(context.Background(), handoff, tunnelIO)
			if err == nil {
				testingObject.Fatalf("expected invalid open returns error")
			}

			writes := tunnelIO.Writes()
			if len(writes) != 1 {
				testingObject.Fatalf("unexpected tunnel write count: %d", len(writes))
			}
			if writes[0].OpenAck == nil {
				testingObject.Fatalf("expected open_ack written for invalid open")
			}
			if writes[0].OpenAck.Success {
				testingObject.Fatalf("expected open_ack reject for invalid open")
			}
			if writes[0].OpenAck.ErrorCode != testCase.wantErrorCode {
				testingObject.Fatalf(
					"unexpected open_ack error code: got=%s want=%s",
					writes[0].OpenAck.ErrorCode,
					testCase.wantErrorCode,
				)
			}
			if acceptor.IsReaderOwned("tunnel-" + testCase.name) {
				testingObject.Fatalf("expected reader lease released for invalid open")
			}
		})
	}
}

// TestOpenerHandleSuccessWithActualEndpointMetadata 验证 open 成功路径会回传实际 endpoint 元信息并发送 close。
func TestOpenerHandleSuccessWithActualEndpointMetadata(testingObject *testing.T) {
	testingObject.Parallel()
	selector := &openerTestSelector{
		endpoint: Endpoint{
			ID:   "endpoint-2",
			Addr: "10.0.0.2:8080",
		},
	}
	upstreamConnection := &openerTestConn{}
	opener, err := NewOpener(OpenerOptions{
		Selector: selector,
		Dialer: &openerTestDialer{
			dialFunc: func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
				return upstreamConnection, nil
			},
		},
		Relay: &openerTestRelay{
			relayFunc: func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
				return nil
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}
	acceptor := NewAcceptor()
	handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-1", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-1",
				ServiceID: "svc-1",
				EndpointSelectionHint: map[string]string{
					"endpoint_id": "endpoint-1",
				},
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	tunnelIO := &openerTestTunnelIO{}
	result, err := opener.Handle(context.Background(), handoff, tunnelIO)
	if err != nil {
		testingObject.Fatalf("handle open failed: %v", err)
	}

	if result.Endpoint.ID != "endpoint-2" {
		testingObject.Fatalf("unexpected endpoint id: %s", result.Endpoint.ID)
	}
	if selector.lastServiceID != "svc-1" {
		testingObject.Fatalf("unexpected selected service: %s", selector.lastServiceID)
	}
	if selector.lastHint["endpoint_id"] != "endpoint-1" {
		testingObject.Fatalf("expected hint propagated to selector")
	}

	writes := tunnelIO.Writes()
	if len(writes) != 2 {
		testingObject.Fatalf("unexpected tunnel write count: %d", len(writes))
	}
	if writes[0].OpenAck == nil || !writes[0].OpenAck.Success {
		testingObject.Fatalf("expected first payload open_ack success")
	}
	if writes[0].OpenAck.Metadata[OpenAckMetadataActualEndpointID] != "endpoint-2" {
		testingObject.Fatalf("unexpected actual endpoint id metadata: %v", writes[0].OpenAck.Metadata)
	}
	if writes[0].OpenAck.Metadata[OpenAckMetadataActualEndpointAddr] != "10.0.0.2:8080" {
		testingObject.Fatalf("unexpected actual endpoint addr metadata: %v", writes[0].OpenAck.Metadata)
	}
	if writes[1].Close == nil || writes[1].Close.TrafficID != "traffic-1" {
		testingObject.Fatalf("expected close payload after relay success")
	}
	if upstreamConnection.closeCount.Load() != 1 {
		testingObject.Fatalf("expected upstream closed once, got %d", upstreamConnection.closeCount.Load())
	}
	if acceptor.IsReaderOwned("tunnel-1") {
		testingObject.Fatalf("expected reader ownership released by opener")
	}
}

// TestOpenerHandleDialCanceled 验证取消信号会中止拨号并返回失败 ack。
func TestOpenerHandleDialCanceled(testingObject *testing.T) {
	testingObject.Parallel()
	opener, err := NewOpener(OpenerOptions{
		Selector: &openerTestSelector{
			endpoint: Endpoint{
				ID:   "endpoint-3",
				Addr: "10.0.0.3:8080",
			},
		},
		Dialer: &openerTestDialer{
			dialFunc: func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
				<-ctx.Done()
				return nil, ctx.Err()
			},
		},
		Relay: &openerTestRelay{
			relayFunc: func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
				return nil
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}
	acceptor := NewAcceptor()
	handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-2", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-2",
				ServiceID: "svc-2",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	tunnelIO := &openerTestTunnelIO{
		failWriteWhenContextDone: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = opener.Handle(ctx, handoff, tunnelIO)
	if err == nil {
		testingObject.Fatalf("expected canceled dial returns error")
	}

	writes := tunnelIO.Writes()
	if len(writes) != 1 {
		testingObject.Fatalf("unexpected tunnel write count: %d", len(writes))
	}
	if writes[0].OpenAck == nil || writes[0].OpenAck.Success {
		testingObject.Fatalf("expected open ack reject on canceled dial")
	}
	if writes[0].OpenAck.ErrorCode != ltfperrors.CodeTrafficOpenRejected {
		testingObject.Fatalf("unexpected open ack error code: %s", writes[0].OpenAck.ErrorCode)
	}
	if acceptor.IsReaderOwned("tunnel-2") {
		testingObject.Fatalf("expected reader ownership released after canceled dial")
	}
}

// TestOpenerHandleCancelSignalDuringRelay 验证 relay 阶段收到取消信号会发送 reset 并清理 upstream。
func TestOpenerHandleCancelSignalDuringRelay(testingObject *testing.T) {
	testingObject.Parallel()
	upstreamConnection := &openerTestConn{}
	opener, err := NewOpener(OpenerOptions{
		Selector: &openerTestSelector{
			endpoint: Endpoint{
				ID:   "endpoint-4",
				Addr: "10.0.0.4:8080",
			},
		},
		Dialer: &openerTestDialer{
			dialFunc: func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
				return upstreamConnection, nil
			},
		},
		Relay: &openerTestRelay{
			relayFunc: func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
				<-ctx.Done()
				return ctx.Err()
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}
	acceptor := NewAcceptor()
	handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-3", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-3",
				ServiceID: "svc-3",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	tunnelIO := &openerTestTunnelIO{
		failWriteWhenContextDone: true,
	}
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	_, err = opener.Handle(ctx, handoff, tunnelIO)
	if err == nil {
		testingObject.Fatalf("expected relay cancellation returns error")
	}

	writes := tunnelIO.Writes()
	if len(writes) != 2 {
		testingObject.Fatalf("unexpected tunnel write count: %d", len(writes))
	}
	if writes[0].OpenAck == nil || !writes[0].OpenAck.Success {
		testingObject.Fatalf("expected open ack success before relay")
	}
	if writes[1].Reset == nil {
		testingObject.Fatalf("expected reset payload on relay cancellation")
	}
	if writes[1].Reset.ErrorCode != ResetCodeCanceled {
		testingObject.Fatalf("unexpected reset code: %s", writes[1].Reset.ErrorCode)
	}
	if upstreamConnection.closeCount.Load() != 1 {
		testingObject.Fatalf("expected upstream closed once, got %d", upstreamConnection.closeCount.Load())
	}
	if acceptor.IsReaderOwned("tunnel-3") {
		testingObject.Fatalf("expected reader ownership released after relay cancellation")
	}
}

// TestOpenerHandleRelayFailure 验证 relay 失败会返回 reset。
func TestOpenerHandleRelayFailure(testingObject *testing.T) {
	testingObject.Parallel()
	upstreamConnection := &openerTestConn{}
	opener, err := NewOpener(OpenerOptions{
		Selector: &openerTestSelector{
			endpoint: Endpoint{
				ID:   "endpoint-5",
				Addr: "10.0.0.5:8080",
			},
		},
		Dialer: &openerTestDialer{
			dialFunc: func(ctx context.Context, endpoint Endpoint) (io.ReadWriteCloser, error) {
				return upstreamConnection, nil
			},
		},
		Relay: &openerTestRelay{
			relayFunc: func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
				return errors.New("relay broken")
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("new opener failed: %v", err)
	}
	acceptor := NewAcceptor()
	handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-4", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-4",
				ServiceID: "svc-4",
			},
		},
	})
	if err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	tunnelIO := &openerTestTunnelIO{}
	_, err = opener.Handle(context.Background(), handoff, tunnelIO)
	if err == nil {
		testingObject.Fatalf("expected relay failure returns error")
	}

	writes := tunnelIO.Writes()
	if len(writes) != 2 {
		testingObject.Fatalf("unexpected tunnel write count: %d", len(writes))
	}
	if writes[1].Reset == nil || writes[1].Reset.ErrorCode != ResetCodeRelayFailed {
		testingObject.Fatalf("expected relay failed reset payload")
	}
	if upstreamConnection.closeCount.Load() != 1 {
		testingObject.Fatalf("expected upstream closed once, got %d", upstreamConnection.closeCount.Load())
	}
}
