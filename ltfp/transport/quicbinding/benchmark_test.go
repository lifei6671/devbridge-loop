package quicbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"golang.org/x/time/rate"
)

const benchmarkQUICTunnelTimeout = 5 * time.Second

type benchmarkQUICRig struct {
	listener      *Listener
	clientConn    *Conn
	serverConn    *Conn
	clientControl transport.ControlChannel
	serverControl transport.ControlChannel
	producer      *TunnelProducer
	acceptor      *TunnelAcceptor
}

func newBenchmarkQUICRig(testingObject testing.TB) *benchmarkQUICRig {
	return newBenchmarkQUICRigWithConfig(testingObject, TransportConfig{})
}

func newBenchmarkQUICRigWithConfig(testingObject testing.TB, config TransportConfig) *benchmarkQUICRig {
	testingObject.Helper()

	binding, err := NewTransportWithConfig(config)
	if err != nil {
		testingObject.Fatalf("new quic benchmark transport failed: %v", err)
	}
	serverTLSConfig, clientTLSConfig := newTestTLSConfigs(testingObject)
	listener, err := binding.ListenAddr("127.0.0.1:0", serverTLSConfig)
	if err != nil {
		testingObject.Fatalf("listen quic benchmark transport failed: %v", err)
	}

	acceptContext, cancelAccept := context.WithTimeout(context.Background(), benchmarkQUICTunnelTimeout)
	defer cancelAccept()
	serverConnResult := make(chan acceptConnResult, 1)
	go func() {
		serverConn, acceptErr := listener.Accept(acceptContext)
		serverConnResult <- acceptConnResult{conn: serverConn, err: acceptErr}
	}()

	clientContext, cancelClient := context.WithTimeout(context.Background(), benchmarkQUICTunnelTimeout)
	defer cancelClient()
	clientConn, err := binding.Dial(clientContext, listener.Addr().String(), clientTLSConfig)
	if err != nil {
		_ = listener.Close()
		testingObject.Fatalf("dial quic benchmark transport failed: %v", err)
	}

	serverAccepted := <-serverConnResult
	if serverAccepted.err != nil {
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("accept quic benchmark transport failed: %v", serverAccepted.err)
	}
	serverConn := serverAccepted.conn

	controlContext, cancelControl := context.WithTimeout(context.Background(), benchmarkQUICTunnelTimeout)
	defer cancelControl()
	serverControlResult := make(chan acceptControlResult, 1)
	go func() {
		controlChannel, acceptErr := serverConn.AcceptControlChannel(controlContext)
		serverControlResult <- acceptControlResult{control: controlChannel, err: acceptErr}
	}()

	clientControl, err := clientConn.OpenControlChannel(controlContext)
	if err != nil {
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("open quic benchmark control channel failed: %v", err)
	}
	if err := clientControl.WriteControlFrame(controlContext, testControlFrame()); err != nil {
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("write quic benchmark bootstrap control frame failed: %v", err)
	}

	serverControlAccepted := <-serverControlResult
	if serverControlAccepted.err != nil {
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("accept quic benchmark control channel failed: %v", serverControlAccepted.err)
	}
	serverControl := serverControlAccepted.control
	if _, err := serverControl.ReadControlFrame(controlContext); err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("read quic benchmark bootstrap control frame failed: %v", err)
	}

	producer, err := NewTunnelProducer(clientConn, TunnelIdentityConfig{
		SessionID:      "bench-session",
		SessionEpoch:   1,
		TunnelIDPrefix: "bench-quic",
	})
	if err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("new quic benchmark tunnel producer failed: %v", err)
	}
	acceptor, err := NewTunnelAcceptor(serverConn, TunnelAcceptorConfig{})
	if err != nil {
		_ = serverControl.Close(context.Background())
		_ = clientControl.Close(context.Background())
		_ = serverConn.Close(nil)
		_ = clientConn.Close(nil)
		_ = listener.Close()
		testingObject.Fatalf("new quic benchmark tunnel acceptor failed: %v", err)
	}

	return &benchmarkQUICRig{
		listener:      listener,
		clientConn:    clientConn,
		serverConn:    serverConn,
		clientControl: clientControl,
		serverControl: serverControl,
		producer:      producer,
		acceptor:      acceptor,
	}
}

func (rig *benchmarkQUICRig) Close() {
	if rig == nil {
		return
	}
	if rig.acceptor != nil {
		rig.acceptor.Close(nil)
	}
	if rig.serverControl != nil {
		_ = rig.serverControl.Close(context.Background())
	}
	if rig.clientControl != nil {
		_ = rig.clientControl.Close(context.Background())
	}
	if rig.serverConn != nil {
		_ = rig.serverConn.Close(nil)
	}
	if rig.clientConn != nil {
		_ = rig.clientConn.Close(nil)
	}
	if rig.listener != nil {
		_ = rig.listener.Close()
	}
}

func (rig *benchmarkQUICRig) OpenTunnelPair(testingObject testing.TB) (transport.Tunnel, transport.Tunnel) {
	testingObject.Helper()
	if rig == nil || rig.producer == nil || rig.acceptor == nil {
		testingObject.Fatalf("quic benchmark rig not initialized")
	}
	tunnelContext, cancelTunnel := context.WithTimeout(context.Background(), benchmarkQUICTunnelTimeout)
	defer cancelTunnel()
	serverTunnelResult := make(chan acceptTunnelResult, 1)
	go func() {
		tunnel, acceptErr := rig.acceptor.AcceptTunnel(tunnelContext)
		serverTunnelResult <- acceptTunnelResult{tunnel: tunnel, err: acceptErr}
	}()

	clientTunnel, err := rig.producer.OpenTunnel(tunnelContext)
	if err != nil {
		testingObject.Fatalf("open quic benchmark tunnel failed: %v", err)
	}
	serverAccepted := <-serverTunnelResult
	if serverAccepted.err != nil {
		_ = clientTunnel.Close()
		testingObject.Fatalf("accept quic benchmark tunnel failed: %v", serverAccepted.err)
	}
	return clientTunnel, serverAccepted.tunnel
}

type benchmarkQUICTunnelProducer struct {
	nextTunnelID uint64
}

func (producer *benchmarkQUICTunnelProducer) OpenTunnel(context.Context) (transport.Tunnel, error) {
	producer.nextTunnelID++
	return &benchmarkQUICPoolTunnel{
		id: fmt.Sprintf("bench-quic-%d", producer.nextTunnelID),
	}, nil
}

type benchmarkQUICPoolTunnel struct {
	id string
}

func (tunnel *benchmarkQUICPoolTunnel) Read(payload []byte) (int, error) {
	_ = payload
	return 0, io.EOF
}

func (tunnel *benchmarkQUICPoolTunnel) Write(payload []byte) (int, error) {
	return len(payload), nil
}

func (tunnel *benchmarkQUICPoolTunnel) Close() error {
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) ID() string {
	if tunnel == nil {
		return ""
	}
	return tunnel.id
}

func (tunnel *benchmarkQUICPoolTunnel) Meta() transport.TunnelMeta {
	return transport.TunnelMeta{TunnelID: tunnel.ID()}
}

func (tunnel *benchmarkQUICPoolTunnel) State() transport.TunnelState {
	return transport.TunnelStateIdle
}

func (tunnel *benchmarkQUICPoolTunnel) BindingInfo() transport.BindingInfo {
	return transport.NewBindingInfo(transport.BindingTypeQUICNative)
}

func (tunnel *benchmarkQUICPoolTunnel) CloseWrite() error {
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) Reset(cause error) error {
	_ = cause
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) SetDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) SetReadDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) SetWriteDeadline(deadline time.Time) error {
	_ = deadline
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) Flush() error {
	return nil
}

func (tunnel *benchmarkQUICPoolTunnel) ReuseCount() int {
	return 0
}

func (tunnel *benchmarkQUICPoolTunnel) Recyclable() bool {
	return true
}

func (tunnel *benchmarkQUICPoolTunnel) Done() <-chan struct{} {
	doneChannel := make(chan struct{})
	return doneChannel
}

func (tunnel *benchmarkQUICPoolTunnel) Err() error {
	return nil
}

// BenchmarkQUICTunnelSmallPayload 基准：小包流量。
func BenchmarkQUICTunnelSmallPayload(testingB *testing.B) {
	payload := make([]byte, 256)

	testingB.Run("write", func(testingB *testing.B) {
		rig := newBenchmarkQUICRig(testingB)
		defer rig.Close()
		clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)
		defer func() {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
		}()

		readDone := make(chan error, 1)
		go func() {
			readBuffer := make([]byte, len(payload))
			for index := 0; index < testingB.N; index++ {
				if _, err := io.ReadFull(serverTunnel, readBuffer); err != nil {
					readDone <- err
					return
				}
			}
			readDone <- nil
		}()

		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := clientTunnel.Write(payload); err != nil {
				testingB.Fatalf("quic write failed: %v", err)
			}
		}
		testingB.StopTimer()
		if err := <-readDone; err != nil {
			testingB.Fatalf("quic write drain failed: %v", err)
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		rig := newBenchmarkQUICRig(testingB)
		defer rig.Close()
		clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)
		defer func() {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
		}()

		writeDone := make(chan error, 1)
		go func() {
			for index := 0; index < testingB.N; index++ {
				if _, err := serverTunnel.Write(payload); err != nil {
					writeDone <- err
					return
				}
			}
			writeDone <- nil
		}()

		readBuffer := make([]byte, len(payload))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := io.ReadFull(clientTunnel, readBuffer); err != nil {
				testingB.Fatalf("quic read failed: %v", err)
			}
		}
		testingB.StopTimer()
		if err := <-writeDone; err != nil {
			testingB.Fatalf("quic read producer failed: %v", err)
		}
	})
}

// BenchmarkQUICTunnelLargePayload 基准：大包流量。
func BenchmarkQUICTunnelLargePayload(testingB *testing.B) {
	payload := make([]byte, 256*1024)

	testingB.Run("write", func(testingB *testing.B) {
		rig := newBenchmarkQUICRig(testingB)
		defer rig.Close()
		clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)
		defer func() {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
		}()

		readDone := make(chan error, 1)
		go func() {
			readBuffer := make([]byte, len(payload))
			for index := 0; index < testingB.N; index++ {
				if _, err := io.ReadFull(serverTunnel, readBuffer); err != nil {
					readDone <- err
					return
				}
			}
			readDone <- nil
		}()

		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := clientTunnel.Write(payload); err != nil {
				testingB.Fatalf("quic large write failed: %v", err)
			}
		}
		testingB.StopTimer()
		if err := <-readDone; err != nil {
			testingB.Fatalf("quic large write drain failed: %v", err)
		}
	})

	testingB.Run("read", func(testingB *testing.B) {
		rig := newBenchmarkQUICRig(testingB)
		defer rig.Close()
		clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)
		defer func() {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
		}()

		writeDone := make(chan error, 1)
		go func() {
			for index := 0; index < testingB.N; index++ {
				if _, err := serverTunnel.Write(payload); err != nil {
					writeDone <- err
					return
				}
			}
			writeDone <- nil
		}()

		readBuffer := make([]byte, len(payload))
		testingB.SetBytes(int64(len(payload)))
		testingB.ResetTimer()
		for index := 0; index < testingB.N; index++ {
			if _, err := io.ReadFull(clientTunnel, readBuffer); err != nil {
				testingB.Fatalf("quic large read failed: %v", err)
			}
		}
		testingB.StopTimer()
		if err := <-writeDone; err != nil {
			testingB.Fatalf("quic large read producer failed: %v", err)
		}
	})
}

// BenchmarkQUICTunnelIdleDeadline 基准：空闲维持场景下的 deadline 检查。
func BenchmarkQUICTunnelIdleDeadline(testingB *testing.B) {
	rig := newBenchmarkQUICRig(testingB)
	defer rig.Close()
	clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)
	defer func() {
		_ = clientTunnel.Close()
		_ = serverTunnel.Close()
	}()

	readBuffer := make([]byte, 16)
	testingB.ResetTimer()
	for index := 0; index < testingB.N; index++ {
		if err := clientTunnel.SetReadDeadline(time.Now().Add(-time.Millisecond)); err != nil {
			testingB.Fatalf("set quic read deadline failed: %v", err)
		}
		_, err := clientTunnel.Read(readBuffer)
		if err == nil || err.Error() == "" {
			testingB.Fatalf("expected quic deadline timeout, got nil")
		}
		if !containsTimeout(err) {
			testingB.Fatalf("expected timeout error, got %v", err)
		}
	}
}

// BenchmarkQUICBurstRefill 基准：突发 refill（控制器 + idle pool）。
func BenchmarkQUICBurstRefill(testingB *testing.B) {
	producer := &benchmarkQUICTunnelProducer{}
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
			testingB.Fatalf("create quic refill controller failed: %v", err)
		}
		result, err := controller.RefillToTarget(context.Background(), 64)
		if err != nil {
			testingB.Fatalf("quic refill to target failed: %v", err)
		}
		if result.OpenedCount != 64 {
			testingB.Fatalf("unexpected quic opened count: %d", result.OpenedCount)
		}
		for drainIndex := 0; drainIndex < 64; drainIndex++ {
			tunnel, err := pool.Acquire(context.Background())
			if err != nil {
				testingB.Fatalf("acquire quic refill tunnel failed: %v", err)
			}
			_ = tunnel.Close()
			_ = pool.Remove(tunnel.ID())
		}
	}
}

// BenchmarkQUICStreamLimitSaturation 基准：控制流 + 单条数据流占满 stream 配额时，额外开流会快速超时返回。
func BenchmarkQUICStreamLimitSaturation(testingB *testing.B) {
	config := TransportConfig{
		MaxIncomingStreams: 2,
		StreamOpenTimeout:  40 * time.Millisecond,
	}

	testingB.ResetTimer()
	for index := 0; index < testingB.N; index++ {
		rig := newBenchmarkQUICRigWithConfig(testingB, config)
		clientTunnel, serverTunnel := rig.OpenTunnelPair(testingB)

		openContext, cancelOpen := context.WithTimeout(context.Background(), 120*time.Millisecond)
		_, err := rig.producer.OpenTunnel(openContext)
		cancelOpen()
		if err == nil {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
			rig.Close()
			testingB.Fatalf("expected quic stream limit saturation error")
		}
		if !containsTimeout(err) {
			_ = clientTunnel.Close()
			_ = serverTunnel.Close()
			rig.Close()
			testingB.Fatalf("expected quic stream saturation timeout, got %v", err)
		}

		_ = clientTunnel.Close()
		_ = serverTunnel.Close()
		rig.Close()
	}
}

func containsTimeout(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, transport.ErrTimeout) || strings.Contains(err.Error(), transport.ErrTimeout.Error())
}
