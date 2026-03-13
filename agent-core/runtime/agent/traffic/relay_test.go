package traffic

import (
	"bytes"
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type relayTestTunnelReadResult struct {
	payload pb.StreamPayload
	err     error
}

type relayTestTunnel struct {
	readQueue chan relayTestTunnelReadResult

	writeMutex sync.Mutex
	writes     []pb.StreamPayload
}

func newRelayTestTunnel() *relayTestTunnel {
	return &relayTestTunnel{
		readQueue: make(chan relayTestTunnelReadResult, 32),
	}
}

func (tunnel *relayTestTunnel) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	select {
	case <-ctx.Done():
		return pb.StreamPayload{}, ctx.Err()
	case result := <-tunnel.readQueue:
		return result.payload, result.err
	}
}

func (tunnel *relayTestTunnel) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	tunnel.writes = append(tunnel.writes, payload)
	return nil
}

func (tunnel *relayTestTunnel) EnqueueReadPayload(payload pb.StreamPayload) {
	tunnel.readQueue <- relayTestTunnelReadResult{payload: payload}
}

func (tunnel *relayTestTunnel) Writes() []pb.StreamPayload {
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	copied := make([]pb.StreamPayload, 0, len(tunnel.writes))
	copied = append(copied, tunnel.writes...)
	return copied
}

type relayTestUpstream struct {
	readQueue chan []byte
	closeCh   chan struct{}
	closeOnce sync.Once

	writeGate <-chan struct{}

	writeMutex sync.Mutex
	writes     bytes.Buffer

	readMutex sync.Mutex
	pending   []byte
}

func newRelayTestUpstream(writeGate <-chan struct{}) *relayTestUpstream {
	return &relayTestUpstream{
		readQueue: make(chan []byte, 32),
		closeCh:   make(chan struct{}),
		writeGate: writeGate,
	}
}

func (upstream *relayTestUpstream) Read(payload []byte) (int, error) {
	upstream.readMutex.Lock()
	if len(upstream.pending) > 0 {
		readSize := copy(payload, upstream.pending)
		upstream.pending = upstream.pending[readSize:]
		upstream.readMutex.Unlock()
		return readSize, nil
	}
	upstream.readMutex.Unlock()
	select {
	case <-upstream.closeCh:
		return 0, io.EOF
	case chunk := <-upstream.readQueue:
		if len(chunk) == 0 {
			return 0, nil
		}
		upstream.readMutex.Lock()
		upstream.pending = append(upstream.pending[:0], chunk...)
		readSize := copy(payload, upstream.pending)
		upstream.pending = upstream.pending[readSize:]
		upstream.readMutex.Unlock()
		return readSize, nil
	}
}

func (upstream *relayTestUpstream) Write(payload []byte) (int, error) {
	if upstream.writeGate != nil {
		select {
		case <-upstream.closeCh:
			return 0, io.EOF
		case <-upstream.writeGate:
		}
	}
	select {
	case <-upstream.closeCh:
		return 0, io.EOF
	default:
	}
	upstream.writeMutex.Lock()
	defer upstream.writeMutex.Unlock()
	return upstream.writes.Write(payload)
}

func (upstream *relayTestUpstream) Close() error {
	upstream.closeOnce.Do(func() {
		close(upstream.closeCh)
	})
	return nil
}

func (upstream *relayTestUpstream) PushReadData(payload []byte) {
	upstream.readQueue <- append([]byte(nil), payload...)
}

func (upstream *relayTestUpstream) WrittenBytes() []byte {
	upstream.writeMutex.Lock()
	defer upstream.writeMutex.Unlock()
	return append([]byte(nil), upstream.writes.Bytes()...)
}

// TestStreamRelayBidirectionalAndClose 验证默认 relay 支持双向转发并在 Close 帧后正常结束。
func TestStreamRelayBidirectionalAndClose(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newRelayTestTunnel()
	upstream := newRelayTestUpstream(nil)
	relay := NewStreamRelay(StreamRelayOptions{
		BufferFrames:        4,
		MaxDataFrameBytes:   8,
		BackpressureTimeout: 100 * time.Millisecond,
	})

	relayResultChannel := make(chan error, 1)
	go func() {
		relayResultChannel <- relay.Relay(context.Background(), tunnel, upstream, "traffic-1")
	}()

	tunnel.EnqueueReadPayload(pb.StreamPayload{
		Data: []byte("from-tunnel"),
	})
	waitUntil(testingObject, time.Second, func() bool {
		return string(upstream.WrittenBytes()) == "from-tunnel"
	})

	upstream.PushReadData([]byte("from-upstream"))
	waitUntil(testingObject, time.Second, func() bool {
		return string(flattenTunnelData(tunnel.Writes())) == "from-upstream"
	})

	tunnel.EnqueueReadPayload(pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: "traffic-1",
			Reason:    "done",
		},
	})
	_ = upstream.Close()

	select {
	case err := <-relayResultChannel:
		if err != nil {
			testingObject.Fatalf("expected relay completes without error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("timed out waiting relay completion")
	}
}

// TestStreamRelayBackpressureTimeout 验证慢下游导致有界缓冲超时后返回反压错误。
func TestStreamRelayBackpressureTimeout(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newRelayTestTunnel()
	writeGate := make(chan struct{})
	upstream := newRelayTestUpstream(writeGate)
	relay := NewStreamRelay(StreamRelayOptions{
		BufferFrames:        1,
		MaxDataFrameBytes:   8,
		BackpressureTimeout: 30 * time.Millisecond,
	})

	tunnel.EnqueueReadPayload(pb.StreamPayload{Data: []byte("a")})
	tunnel.EnqueueReadPayload(pb.StreamPayload{Data: []byte("b")})
	tunnel.EnqueueReadPayload(pb.StreamPayload{Data: []byte("c")})

	relayResultChannel := make(chan error, 1)
	go func() {
		relayResultChannel <- relay.Relay(context.Background(), tunnel, upstream, "traffic-2")
	}()

	time.Sleep(120 * time.Millisecond)
	_ = upstream.Close()
	close(writeGate)

	select {
	case err := <-relayResultChannel:
		if !errors.Is(err, ErrRelayBackpressureTimeout) {
			testingObject.Fatalf("expected backpressure timeout, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("timed out waiting relay backpressure timeout")
	}
}

// TestStreamRelayResetByPeer 验证收到匹配 traffic 的 Reset 帧会终止 relay。
func TestStreamRelayResetByPeer(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newRelayTestTunnel()
	upstream := newRelayTestUpstream(nil)
	relay := NewStreamRelay(StreamRelayOptions{
		BufferFrames:        4,
		MaxDataFrameBytes:   8,
		BackpressureTimeout: 100 * time.Millisecond,
	})

	tunnel.EnqueueReadPayload(pb.StreamPayload{
		Reset: &pb.TrafficReset{
			TrafficID:    "traffic-3",
			ErrorCode:    "UPSTREAM_RESET",
			ErrorMessage: "peer reset",
		},
	})

	relayResultChannel := make(chan error, 1)
	go func() {
		relayResultChannel <- relay.Relay(context.Background(), tunnel, upstream, "traffic-3")
	}()

	select {
	case err := <-relayResultChannel:
		if !errors.Is(err, ErrRelayResetByPeer) {
			testingObject.Fatalf("expected reset by peer error, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("timed out waiting relay reset error")
	}
}

// TestStreamRelayUpstreamEOFTerminates 验证上游先 EOF 时 relay 能及时结束。
func TestStreamRelayUpstreamEOFTerminates(testingObject *testing.T) {
	testingObject.Parallel()
	tunnel := newRelayTestTunnel()
	upstream := newRelayTestUpstream(nil)
	relay := NewStreamRelay(StreamRelayOptions{
		BufferFrames:        4,
		MaxDataFrameBytes:   8,
		BackpressureTimeout: 100 * time.Millisecond,
	})
	_ = upstream.Close()

	relayResultChannel := make(chan error, 1)
	go func() {
		relayResultChannel <- relay.Relay(context.Background(), tunnel, upstream, "traffic-eof")
	}()

	select {
	case err := <-relayResultChannel:
		if err != nil {
			testingObject.Fatalf("expected relay completes without error on EOF, got: %v", err)
		}
	case <-time.After(2 * time.Second):
		testingObject.Fatalf("timed out waiting relay completion on EOF")
	}
}

func waitUntil(testingObject *testing.T, timeout time.Duration, condition func() bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	testingObject.Fatalf("condition not satisfied within %s", timeout)
}

func flattenTunnelData(writes []pb.StreamPayload) []byte {
	result := make([]byte, 0, 32)
	for _, payload := range writes {
		if len(payload.Data) == 0 {
			continue
		}
		result = append(result, payload.Data...)
	}
	return result
}
