package quicbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	quic "github.com/quic-go/quic-go"
)

const (
	defaultIncomingStreamQueueSize = 128
	localApplicationCloseCode      = quic.ApplicationErrorCode(0x4442)
	localStreamResetCode           = quic.StreamErrorCode(0x4442)
)

// Listener 包装 quic-go listener，并把接入连接映射为 quicbinding.Conn。
type Listener struct {
	binding  *Transport
	listener *quic.Listener
}

// Accept 接受一条 QUIC 连接。
func (listener *Listener) Accept(ctx context.Context) (*Conn, error) {
	if listener == nil || listener.listener == nil {
		return nil, fmt.Errorf("accept quic conn: %w: nil listener", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	quicConn, err := listener.listener.Accept(ctx)
	if err != nil {
		return nil, fmt.Errorf("accept quic conn: %w", err)
	}
	return newServerConn(listener.binding, quicConn), nil
}

// Addr 返回监听地址。
func (listener *Listener) Addr() net.Addr {
	if listener == nil || listener.listener == nil {
		return nil
	}
	return listener.listener.Addr()
}

// Close 停止监听。
func (listener *Listener) Close() error {
	if listener == nil || listener.listener == nil {
		return nil
	}
	return listener.listener.Close()
}

// Conn 表示一条 Agent/Bridge 共享的 QUIC 连接。
type Conn struct {
	binding *Transport
	conn    *quic.Conn
	role    SessionRole

	stateMutex      sync.Mutex
	controlAssigned bool
	lastError       error
	incomingControl chan *quic.Stream
	incomingTunnels chan *quic.Stream
	doneChannel     chan struct{}
	doneOnce        sync.Once
	closeOnce       sync.Once
}

func newClientConn(binding *Transport, quicConn *quic.Conn) *Conn {
	conn := &Conn{
		binding:     binding,
		conn:        quicConn,
		role:        SessionRoleAgent,
		doneChannel: make(chan struct{}),
	}
	go conn.watchConnectionContext()
	return conn
}

func newServerConn(binding *Transport, quicConn *quic.Conn) *Conn {
	conn := &Conn{
		binding:         binding,
		conn:            quicConn,
		role:            SessionRoleServer,
		incomingControl: make(chan *quic.Stream, 1),
		incomingTunnels: make(chan *quic.Stream, defaultIncomingStreamQueueSize),
		doneChannel:     make(chan struct{}),
	}
	go conn.watchConnectionContext()
	go conn.runAcceptLoop()
	return conn
}

// OpenControlChannel 在客户端侧打开控制流。
func (conn *Conn) OpenControlChannel(ctx context.Context) (transport.ControlChannel, error) {
	if conn == nil || conn.conn == nil {
		return nil, fmt.Errorf("open quic control channel: %w: nil conn", transport.ErrInvalidArgument)
	}
	if conn.role != SessionRoleAgent {
		return nil, fmt.Errorf("open quic control channel: %w", transport.ErrUnsupported)
	}
	stream, err := conn.openStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open quic control channel: %w", err)
	}
	controlChannel, err := newQUICControlChannel(stream, conn.binding.Config())
	if err != nil {
		conn.resetStream(stream)
		return nil, fmt.Errorf("open quic control channel: %w", err)
	}
	return controlChannel, nil
}

// AcceptControlChannel 在服务端等待首条入站 stream 作为控制流。
func (conn *Conn) AcceptControlChannel(ctx context.Context) (transport.ControlChannel, error) {
	if conn == nil || conn.conn == nil {
		return nil, fmt.Errorf("accept quic control channel: %w: nil conn", transport.ErrInvalidArgument)
	}
	if conn.role != SessionRoleServer {
		return nil, fmt.Errorf("accept quic control channel: %w", transport.ErrUnsupported)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("accept quic control channel: %w", ctx.Err())
		case <-conn.Done():
			return nil, fmt.Errorf("accept quic control channel: %w", conn.Err())
		case stream := <-conn.incomingControl:
			if stream == nil {
				continue
			}
			controlChannel, err := newQUICControlChannel(stream, conn.binding.Config())
			if err != nil {
				conn.resetStream(stream)
				return nil, fmt.Errorf("accept quic control channel: %w", err)
			}
			return controlChannel, nil
		}
	}
}

func (conn *Conn) openTunnelStream(ctx context.Context) (*quic.Stream, error) {
	if conn == nil || conn.conn == nil {
		return nil, fmt.Errorf("open quic tunnel stream: %w: nil conn", transport.ErrInvalidArgument)
	}
	if conn.role != SessionRoleAgent {
		return nil, fmt.Errorf("open quic tunnel stream: %w", transport.ErrUnsupported)
	}
	stream, err := conn.openStream(ctx)
	if err != nil {
		return nil, fmt.Errorf("open quic tunnel stream: %w", err)
	}
	return stream, nil
}

func (conn *Conn) acceptTunnelStream(ctx context.Context) (*quic.Stream, error) {
	if conn == nil || conn.conn == nil {
		return nil, fmt.Errorf("accept quic tunnel stream: %w: nil conn", transport.ErrInvalidArgument)
	}
	if conn.role != SessionRoleServer {
		return nil, fmt.Errorf("accept quic tunnel stream: %w", transport.ErrUnsupported)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("accept quic tunnel stream: %w", ctx.Err())
		case <-conn.Done():
			return nil, fmt.Errorf("accept quic tunnel stream: %w", conn.Err())
		case stream := <-conn.incomingTunnels:
			if stream == nil {
				continue
			}
			return stream, nil
		}
	}
}

func (conn *Conn) openStream(ctx context.Context) (*quic.Stream, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	openContext := ctx
	if _, hasDeadline := openContext.Deadline(); !hasDeadline && conn.binding != nil {
		if openTimeout := conn.binding.Config().StreamOpenTimeout; openTimeout > 0 {
			timedContext, cancel := context.WithTimeout(openContext, openTimeout)
			defer cancel()
			openContext = timedContext
		}
	}
	stream, err := conn.conn.OpenStreamSync(openContext)
	if err != nil {
		return nil, normalizeQUICOperationError("open quic stream", err)
	}
	return stream, nil
}

func (conn *Conn) runAcceptLoop() {
	for {
		stream, err := conn.conn.AcceptStream(context.Background())
		if err != nil {
			conn.closeWithError(normalizeQUICOperationError("accept quic stream", err))
			return
		}
		if conn.enqueueIncomingStream(stream) {
			continue
		}
		conn.resetStream(stream)
	}
}

func (conn *Conn) enqueueIncomingStream(stream *quic.Stream) bool {
	if stream == nil {
		return false
	}
	conn.stateMutex.Lock()
	routeToControl := !conn.controlAssigned
	if routeToControl {
		// 服务端约定首条入站双向 stream 就是控制流，后续 stream 全部视为 tunnel。
		conn.controlAssigned = true
	}
	conn.stateMutex.Unlock()

	if routeToControl {
		select {
		case <-conn.Done():
			return false
		case conn.incomingControl <- stream:
			return true
		}
	}
	select {
	case <-conn.Done():
		return false
	case conn.incomingTunnels <- stream:
		return true
	}
}

// Close 关闭整条 QUIC 连接。
func (conn *Conn) Close(cause error) error {
	if conn == nil || conn.conn == nil {
		return nil
	}
	conn.closeWithError(cause)
	return nil
}

// Done 返回连接结束信号。
func (conn *Conn) Done() <-chan struct{} {
	if conn == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return conn.doneChannel
}

// Err 返回连接最近错误。
func (conn *Conn) Err() error {
	if conn == nil {
		return transport.ErrInvalidArgument
	}
	conn.stateMutex.Lock()
	defer conn.stateMutex.Unlock()
	if conn.lastError == nil {
		return transport.ErrClosed
	}
	return conn.lastError
}

// LocalAddr 返回本地 UDP 地址。
func (conn *Conn) LocalAddr() net.Addr {
	if conn == nil || conn.conn == nil {
		return nil
	}
	return conn.conn.LocalAddr()
}

// RemoteAddr 返回对端 UDP 地址。
func (conn *Conn) RemoteAddr() net.Addr {
	if conn == nil || conn.conn == nil {
		return nil
	}
	return conn.conn.RemoteAddr()
}

func (conn *Conn) closeWithError(err error) {
	if conn == nil || conn.conn == nil {
		return
	}
	if err == nil {
		err = transport.ErrClosed
	}
	conn.closeOnce.Do(func() {
		conn.stateMutex.Lock()
		conn.lastError = err
		conn.stateMutex.Unlock()
		description := strings.TrimSpace(err.Error())
		if description == "" {
			description = transport.ErrClosed.Error()
		}
		_ = conn.conn.CloseWithError(localApplicationCloseCode, description)
		conn.doneOnce.Do(func() {
			close(conn.doneChannel)
		})
	})
}

func (conn *Conn) watchConnectionContext() {
	if conn == nil || conn.conn == nil {
		return
	}
	<-conn.conn.Context().Done()
	conn.closeWithError(normalizeQUICOperationError("watch quic conn", conn.conn.Context().Err()))
}

func (conn *Conn) resetStream(stream *quic.Stream) {
	if stream == nil {
		return
	}
	stream.CancelRead(localStreamResetCode)
	stream.CancelWrite(localStreamResetCode)
}

func normalizeQUICOperationError(operation string, err error) error {
	if err == nil {
		return transport.ErrClosed
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, io.EOF) {
		return transport.ErrClosed
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return transport.ErrTimeout
	}
	var timeoutCapable interface{ Timeout() bool }
	if errors.As(err, &timeoutCapable) && timeoutCapable.Timeout() {
		return transport.ErrTimeout
	}
	return fmt.Errorf("%s: %w", strings.TrimSpace(operation), err)
}

func streamDeadlineFromContext(baseDeadline time.Time, ctx context.Context) time.Time {
	if ctx == nil {
		return baseDeadline
	}
	ctxDeadline, hasDeadline := ctx.Deadline()
	if !hasDeadline {
		return baseDeadline
	}
	if baseDeadline.IsZero() || ctxDeadline.Before(baseDeadline) {
		return ctxDeadline
	}
	return baseDeadline
}

func watchContextCancellation(ctx context.Context, setDeadline func(time.Time) error) func() {
	if ctx == nil || ctx.Done() == nil || ctx.Err() != nil {
		return func() {}
	}
	if _, hasDeadline := ctx.Deadline(); hasDeadline {
		return func() {}
	}
	stopChannel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			_ = setDeadline(time.Now().UTC())
		case <-stopChannel:
		}
	}()
	return func() {
		close(stopChannel)
	}
}
