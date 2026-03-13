package tcpbinding

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"golang.org/x/time/rate"
)

type benchmarkAddr string

func (address benchmarkAddr) Network() string {
	return "benchmark"
}

func (address benchmarkAddr) String() string {
	return string(address)
}

type benchmarkConn struct {
	mutex sync.Mutex

	frame         []byte
	readOffset    int
	timeoutOnRead bool
	closed        bool
}

func newBenchmarkConn(payload []byte, timeoutOnRead bool) *benchmarkConn {
	frame := make([]byte, tunnelFrameHeaderSize+len(payload))
	binary.BigEndian.PutUint32(frame[0:4], uint32(len(payload)))
	copy(frame[4:], payload)
	return &benchmarkConn{
		frame:         frame,
		timeoutOnRead: timeoutOnRead,
	}
}

func (conn *benchmarkConn) Read(payload []byte) (int, error) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	if conn.timeoutOnRead {
		return 0, os.ErrDeadlineExceeded
	}
	if len(conn.frame) == 0 {
		return 0, io.EOF
	}
	if conn.readOffset >= len(conn.frame) {
		conn.readOffset = 0
	}
	readSize := copy(payload, conn.frame[conn.readOffset:])
	conn.readOffset += readSize
	return readSize, nil
}

func (conn *benchmarkConn) Write(payload []byte) (int, error) {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	if conn.closed {
		return 0, net.ErrClosed
	}
	return len(payload), nil
}

func (conn *benchmarkConn) Close() error {
	conn.mutex.Lock()
	defer conn.mutex.Unlock()
	conn.closed = true
	return nil
}

func (conn *benchmarkConn) LocalAddr() net.Addr {
	return benchmarkAddr("local")
}

func (conn *benchmarkConn) RemoteAddr() net.Addr {
	return benchmarkAddr("remote")
}

func (conn *benchmarkConn) SetDeadline(deadline time.Time) error {
	return nil
}

func (conn *benchmarkConn) SetReadDeadline(deadline time.Time) error {
	return nil
}

func (conn *benchmarkConn) SetWriteDeadline(deadline time.Time) error {
	return nil
}

type benchmarkTCPTunnelProducer struct {
	nextTunnelID uint64
	config       TransportConfig
}

func (producer *benchmarkTCPTunnelProducer) OpenTunnel(ctx context.Context) (transport.Tunnel, error) {
	tunnelID := atomic.AddUint64(&producer.nextTunnelID, 1)
	return NewTCPTunnel(
		newBenchmarkConn([]byte("bench"), false),
		transport.TunnelMeta{TunnelID: fmt.Sprintf("bench-tcp-%d", tunnelID)},
		producer.config,
	)
}

func newBenchmarkTCPTunnel(testingB *testing.B, conn net.Conn) *TCPTunnel {
	testingB.Helper()
	tunnel, err := NewTCPTunnel(conn, transport.TunnelMeta{TunnelID: "benchmark-tcp-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingB.Fatalf("create tcp benchmark tunnel failed: %v", err)
	}
	testingB.Cleanup(func() {
		_ = tunnel.Close()
	})
	return tunnel
}

// BenchmarkTCPTunnelSmallPayload 基准：小包流量。
func BenchmarkTCPTunnelSmallPayload(testingB *testing.B) {
	payload := make([]byte, 256)

	testingB.Run("write", func(testingB *testing.B) {
		tunnel := newBenchmarkTCPTunnel(testingB, newBenchmarkConn(payload, false))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := tunnel.Write(payload); err != nil {
				testingB.Fatalf("write failed: %v", err)
			}
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		tunnel := newBenchmarkTCPTunnel(testingB, newBenchmarkConn(payload, false))
		readBuffer := make([]byte, len(payload))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			readSize, err := tunnel.Read(readBuffer)
			if err != nil {
				testingB.Fatalf("read failed: %v", err)
			}
			if readSize != len(payload) {
				testingB.Fatalf("unexpected read size: %d", readSize)
			}
		}
	})
}

// BenchmarkTCPTunnelLargePayload 基准：大包流量。
func BenchmarkTCPTunnelLargePayload(testingB *testing.B) {
	payload := make([]byte, 256*1024)

	testingB.Run("write", func(testingB *testing.B) {
		tunnel := newBenchmarkTCPTunnel(testingB, newBenchmarkConn(payload, false))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := tunnel.Write(payload); err != nil {
				testingB.Fatalf("write failed: %v", err)
			}
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		tunnel := newBenchmarkTCPTunnel(testingB, newBenchmarkConn(payload, false))
		readBuffer := make([]byte, len(payload))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			readSize, err := tunnel.Read(readBuffer)
			if err != nil {
				testingB.Fatalf("read failed: %v", err)
			}
			if readSize != len(payload) {
				testingB.Fatalf("unexpected read size: %d", readSize)
			}
		}
	})
}

// BenchmarkTCPTunnelIdleProbe 基准：空闲维持场景下的 probe 成本。
func BenchmarkTCPTunnelIdleProbe(testingB *testing.B) {
	tunnel := newBenchmarkTCPTunnel(testingB, newBenchmarkConn(nil, true))
	testingB.ResetTimer()
	for index := 0; index < testingB.N; index++ {
		err := tunnel.Probe(context.Background())
		if err != nil {
			testingB.Fatalf("expected probe nil on timeout, got %v", err)
		}
	}
}

// BenchmarkTCPBurstRefill 基准：突发 refill（控制器 + idle pool）。
func BenchmarkTCPBurstRefill(testingB *testing.B) {
	producer := &benchmarkTCPTunnelProducer{
		config: DefaultTransportConfig(),
	}
	config := transport.RefillControllerConfig{
		MinIdleTunnels:         0,
		MaxIdleTunnels:         128,
		MaxInFlightTunnelOpens: 8,
		TunnelOpenRateLimit:    rate.Inf,
		TunnelOpenBurst:        128,
		RequestDeduplicateTTL:  time.Minute,
	}
	testingB.ResetTimer()
	for index := 0; index < testingB.N; index++ {
		pool := transport.NewInMemoryTunnelPoolWithConfig(transport.TunnelPoolConfig{
			MinIdleTunnels: 0,
			MaxIdleTunnels: 128,
		})
		controller, err := transport.NewRefillController(pool, producer, config)
		if err != nil {
			testingB.Fatalf("create refill controller failed: %v", err)
		}
		result, err := controller.RefillToTarget(context.Background(), 64)
		if err != nil {
			testingB.Fatalf("refill to target failed: %v", err)
		}
		if result.OpenedCount != 64 {
			testingB.Fatalf("unexpected opened count: %d", result.OpenedCount)
		}
		for drainIndex := 0; drainIndex < 64; drainIndex++ {
			tunnel, err := pool.Acquire(context.Background())
			if err != nil {
				testingB.Fatalf("acquire refill tunnel failed: %v", err)
			}
			if closeErr := tunnel.Close(); closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
				testingB.Fatalf("close refill tunnel failed: %v", closeErr)
			}
			_ = pool.Remove(tunnel.ID())
		}
	}
}
