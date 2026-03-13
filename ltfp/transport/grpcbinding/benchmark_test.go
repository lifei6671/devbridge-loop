package grpcbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync/atomic"
	"testing"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"golang.org/x/time/rate"
)

type benchmarkTunnelEnvelopeStream struct {
	readPayload []byte
}

func (stream *benchmarkTunnelEnvelopeStream) Send(*transportgen.TunnelEnvelope) error {
	return nil
}

func (stream *benchmarkTunnelEnvelopeStream) Recv() (*transportgen.TunnelEnvelope, error) {
	if len(stream.readPayload) == 0 {
		return nil, io.EOF
	}
	return &transportgen.TunnelEnvelope{
		Payload: append([]byte(nil), stream.readPayload...),
	}, nil
}

func (stream *benchmarkTunnelEnvelopeStream) Context() context.Context {
	return context.Background()
}

func (stream *benchmarkTunnelEnvelopeStream) CloseSend() error {
	return nil
}

type benchmarkGRPCTunnelProducer struct {
	nextTunnelID uint64
}

func (producer *benchmarkGRPCTunnelProducer) OpenTunnel(ctx context.Context) (transport.Tunnel, error) {
	tunnelID := atomic.AddUint64(&producer.nextTunnelID, 1)
	tunnelStream, err := newGRPCH2TunnelStream(&benchmarkTunnelEnvelopeStream{
		readPayload: []byte("bench"),
	})
	if err != nil {
		return nil, err
	}
	return NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: fmt.Sprintf("bench-grpc-%d", tunnelID),
	})
}

func newBenchmarkGRPCH2Tunnel(testingB *testing.B, readPayload []byte) *GRPCH2Tunnel {
	testingB.Helper()
	tunnelStream, err := newGRPCH2TunnelStream(&benchmarkTunnelEnvelopeStream{
		readPayload: readPayload,
	})
	if err != nil {
		testingB.Fatalf("create benchmark tunnel stream failed: %v", err)
	}
	tunnel, err := NewGRPCH2Tunnel(tunnelStream, transport.TunnelMeta{
		TunnelID: "benchmark-grpc-tunnel",
	})
	if err != nil {
		testingB.Fatalf("create benchmark tunnel failed: %v", err)
	}
	testingB.Cleanup(func() {
		_ = tunnel.Close()
	})
	return tunnel
}

// BenchmarkGRPCH2TunnelSmallPayload 基准：小包流量。
func BenchmarkGRPCH2TunnelSmallPayload(testingB *testing.B) {
	payload := make([]byte, 256)

	testingB.Run("write", func(testingB *testing.B) {
		tunnel := newBenchmarkGRPCH2Tunnel(testingB, payload)
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := tunnel.Write(payload); err != nil {
				testingB.Fatalf("write failed: %v", err)
			}
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		tunnel := newBenchmarkGRPCH2Tunnel(testingB, payload)
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

// BenchmarkGRPCH2TunnelLargePayload 基准：大包流量。
func BenchmarkGRPCH2TunnelLargePayload(testingB *testing.B) {
	payload := make([]byte, 256*1024)

	testingB.Run("write", func(testingB *testing.B) {
		tunnel := newBenchmarkGRPCH2Tunnel(testingB, payload)
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := tunnel.Write(payload); err != nil {
				testingB.Fatalf("write failed: %v", err)
			}
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		tunnel := newBenchmarkGRPCH2Tunnel(testingB, payload)
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

// BenchmarkGRPCH2TunnelIdleDeadline 基准：空闲维持场景下的 deadline 检查。
func BenchmarkGRPCH2TunnelIdleDeadline(testingB *testing.B) {
	tunnel := newBenchmarkGRPCH2Tunnel(testingB, []byte("ignored"))
	readBuffer := make([]byte, 16)
	testingB.ResetTimer()
	for index := 0; index < testingB.N; index++ {
		if err := tunnel.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
			testingB.Fatalf("set read deadline failed: %v", err)
		}
		_, err := tunnel.Read(readBuffer)
		if !errors.Is(err, transport.ErrTimeout) {
			testingB.Fatalf("expected timeout, got %v", err)
		}
	}
}

// BenchmarkGRPCH2BurstRefill 基准：突发 refill（控制器 + idle pool）。
func BenchmarkGRPCH2BurstRefill(testingB *testing.B) {
	producer := &benchmarkGRPCTunnelProducer{}
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
		drainAndCloseTunnelPool(testingB, pool, 64)
	}
}

func drainAndCloseTunnelPool(testingB *testing.B, pool transport.TunnelPool, expectedCount int) {
	testingB.Helper()
	for index := 0; index < expectedCount; index++ {
		tunnel, err := pool.Acquire(context.Background())
		if err != nil {
			testingB.Fatalf("acquire refill tunnel failed: %v", err)
		}
		_ = tunnel.Close()
		_ = pool.Remove(tunnel.ID())
	}
}
