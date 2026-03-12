package tcpbinding

import (
	"context"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTCPTunnelReadWriteRoundTrip 验证 framed TCP tunnel 的读写闭环。
func TestTCPTunnelReadWriteRoundTrip(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientTunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "client-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create client tunnel failed: %v", err)
	}
	serverTunnel, err := NewTCPTunnel(serverConn, transport.TunnelMeta{TunnelID: "server-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create server tunnel failed: %v", err)
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		_, writeErr := clientTunnel.Write([]byte("hello-from-client"))
		writeResultChannel <- writeErr
	}()

	readBuffer := make([]byte, 64)
	readSize, err := serverTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("read tunnel payload failed: %v", err)
	}
	if string(readBuffer[:readSize]) != "hello-from-client" {
		testingObject.Fatalf("unexpected tunnel payload: %q", string(readBuffer[:readSize]))
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("write tunnel payload failed: %v", writeErr)
	}
}

// TestTCPTunnelReadDeadlineDoesNotBreakTunnel 验证读 deadline 超时不会破坏 tunnel 生命周期。
func TestTCPTunnelReadDeadlineDoesNotBreakTunnel(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	clientTunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "deadline-client"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create client tunnel failed: %v", err)
	}
	serverTunnel, err := NewTCPTunnel(serverConn, transport.TunnelMeta{TunnelID: "deadline-server"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create server tunnel failed: %v", err)
	}

	if err := clientTunnel.SetReadDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
		testingObject.Fatalf("set read deadline failed: %v", err)
	}
	_, err = clientTunnel.Read(make([]byte, 16))
	if err == nil {
		testingObject.Fatalf("expected read timeout")
	}
	if !errors.Is(err, transport.ErrTimeout) {
		testingObject.Fatalf("expected ErrTimeout, got %v", err)
	}
	if clientTunnel.State().IsTerminal() {
		testingObject.Fatalf("expected tunnel to stay non-terminal after read timeout")
	}
	if err := clientTunnel.SetReadDeadline(time.Time{}); err != nil {
		testingObject.Fatalf("clear read deadline failed: %v", err)
	}

	go func() {
		_, _ = serverTunnel.Write([]byte("after-timeout"))
	}()
	readBuffer := make([]byte, 32)
	readSize, err := clientTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("read after timeout failed: %v", err)
	}
	if string(readBuffer[:readSize]) != "after-timeout" {
		testingObject.Fatalf("unexpected payload after timeout: %q", string(readBuffer[:readSize]))
	}
}

// TestTCPTunnelCloseWriteUnsupportedOnPipe 验证非 TCPConn 场景 CloseWrite 返回 ErrUnsupported。
func TestTCPTunnelCloseWriteUnsupportedOnPipe(testingObject *testing.T) {
	clientConn, _ := net.Pipe()
	defer clientConn.Close()

	tunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "pipe-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create pipe tunnel failed: %v", err)
	}
	err = tunnel.CloseWrite()
	if err == nil {
		testingObject.Fatalf("expected close write unsupported")
	}
	if !errors.Is(err, transport.ErrUnsupported) {
		testingObject.Fatalf("expected ErrUnsupported, got %v", err)
	}
}

// TestTCPTunnelCloseWriteOnTCP 验证真实 TCPConn 可映射 CloseWrite 语义。
func TestTCPTunnelCloseWriteOnTCP(testingObject *testing.T) {
	clientConn, serverConn, cleanup := mustCreateTCPConnPair(testingObject)
	defer cleanup()

	clientTunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "client-close-write"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create client tunnel failed: %v", err)
	}
	serverTunnel, err := NewTCPTunnel(serverConn, transport.TunnelMeta{TunnelID: "server-close-write"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create server tunnel failed: %v", err)
	}

	if err := clientTunnel.CloseWrite(); err != nil {
		testingObject.Fatalf("close write failed: %v", err)
	}
	_, err = serverTunnel.Read(make([]byte, 16))
	if !errors.Is(err, io.EOF) {
		testingObject.Fatalf("expected EOF after close write, got %v", err)
	}
}

// TestTCPTunnelResetMarksBroken 验证 Reset 会把 tunnel 标记为 broken。
func TestTCPTunnelResetMarksBroken(testingObject *testing.T) {
	clientConn, _ := net.Pipe()
	defer clientConn.Close()

	tunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "reset-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create tunnel failed: %v", err)
	}
	if err := tunnel.Reset(errors.New("boom")); err != nil {
		testingObject.Fatalf("reset tunnel failed: %v", err)
	}
	if tunnel.State() != transport.TunnelStateBroken {
		testingObject.Fatalf("expected broken state, got %s", tunnel.State())
	}
	if !errors.Is(tunnel.Err(), transport.ErrTunnelBroken) {
		testingObject.Fatalf("expected ErrTunnelBroken, got %v", tunnel.Err())
	}
	select {
	case <-tunnel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected tunnel done after reset")
	}
}

// TestTCPTunnelProbeDetectsRemoteClose 验证 idle tunnel 可在复用前探测到对端关闭。
func TestTCPTunnelProbeDetectsRemoteClose(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	tunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "probe-tunnel"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create tunnel failed: %v", err)
	}

	if err := serverConn.Close(); err != nil {
		testingObject.Fatalf("close peer conn failed: %v", err)
	}
	err = tunnel.Probe(context.Background())
	if err == nil {
		testingObject.Fatalf("expected probe to detect closed peer")
	}
	if !errors.Is(err, transport.ErrClosed) {
		testingObject.Fatalf("expected ErrClosed, got %v", err)
	}
	if tunnel.State() != transport.TunnelStateClosed {
		testingObject.Fatalf("expected closed state, got %s", tunnel.State())
	}
	select {
	case <-tunnel.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected tunnel done after probe detects close")
	}
}

// TestTCPTunnelOversizedWriteDoesNotBreakTunnel 验证本地 oversized 写入不会污染健康 tunnel。
func TestTCPTunnelOversizedWriteDoesNotBreakTunnel(testingObject *testing.T) {
	clientConn, serverConn := net.Pipe()
	defer clientConn.Close()
	defer serverConn.Close()

	config := DefaultTransportConfig()
	config.MaxTunnelFramePayloadSize = 8

	clientTunnel, err := NewTCPTunnel(clientConn, transport.TunnelMeta{TunnelID: "oversized-client"}, config)
	if err != nil {
		testingObject.Fatalf("create client tunnel failed: %v", err)
	}
	serverTunnel, err := NewTCPTunnel(serverConn, transport.TunnelMeta{TunnelID: "oversized-server"}, config)
	if err != nil {
		testingObject.Fatalf("create server tunnel failed: %v", err)
	}

	_, err = clientTunnel.Write([]byte("payload-too-large"))
	if err == nil {
		testingObject.Fatalf("expected oversized write rejection")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if clientTunnel.State().IsTerminal() {
		testingObject.Fatalf("expected tunnel to remain usable after oversized write, got %s", clientTunnel.State())
	}

	writeResultChannel := make(chan error, 1)
	go func() {
		_, writeErr := clientTunnel.Write([]byte("small"))
		writeResultChannel <- writeErr
	}()
	readBuffer := make([]byte, 16)
	readSize, err := serverTunnel.Read(readBuffer)
	if err != nil {
		testingObject.Fatalf("read after oversized write failed: %v", err)
	}
	if string(readBuffer[:readSize]) != "small" {
		testingObject.Fatalf("unexpected payload after oversized write: %q", string(readBuffer[:readSize]))
	}
	if writeErr := <-writeResultChannel; writeErr != nil {
		testingObject.Fatalf("small write after oversized write failed: %v", writeErr)
	}
}

// TestTCPTunnelBrokenReadClosesUnderlyingConn 验证 broken 路径会主动回收底层连接。
func TestTCPTunnelBrokenReadClosesUnderlyingConn(testingObject *testing.T) {
	conn := newMockConn()
	conn.SetReadError(errors.New("boom"))

	tunnel, err := NewTCPTunnel(conn, transport.TunnelMeta{TunnelID: "broken-close"}, DefaultTransportConfig())
	if err != nil {
		testingObject.Fatalf("create tunnel failed: %v", err)
	}

	_, err = tunnel.Read(make([]byte, 16))
	if err == nil {
		testingObject.Fatalf("expected broken read error")
	}
	if !errors.Is(err, transport.ErrTunnelBroken) {
		testingObject.Fatalf("expected ErrTunnelBroken, got %v", err)
	}
	if tunnel.State() != transport.TunnelStateBroken {
		testingObject.Fatalf("expected broken state, got %s", tunnel.State())
	}
	if conn.CloseCount() != 1 {
		testingObject.Fatalf("expected underlying conn closed once on broken read, got %d", conn.CloseCount())
	}
	if err := tunnel.Close(); err != nil {
		testingObject.Fatalf("close after broken read failed: %v", err)
	}
	if conn.CloseCount() != 1 {
		testingObject.Fatalf("expected close after broken read not to leak extra close attempts, got %d", conn.CloseCount())
	}
}
