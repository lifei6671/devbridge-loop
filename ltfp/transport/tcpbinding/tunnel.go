package tcpbinding

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
)

// TCPTunnel 将 framed TCP 连接适配为 transport.Tunnel。
type TCPTunnel struct {
	conn   net.Conn
	config TransportConfig
	meta   transport.TunnelMeta

	stateMutex sync.RWMutex
	state      transport.TunnelState
	lastError  error

	readMutex   sync.Mutex
	readState   framedReadState
	pendingRead []byte

	writeMutex sync.Mutex

	doneChannel chan struct{}
	doneOnce    sync.Once
	closeOnce   sync.Once
}

var _ transport.Tunnel = (*TCPTunnel)(nil)
var _ transport.TunnelHealthProber = (*TCPTunnel)(nil)

// NewTCPTunnel 使用指定元数据创建 tunnel 适配器。
func NewTCPTunnel(conn net.Conn, meta transport.TunnelMeta, config TransportConfig) (*TCPTunnel, error) {
	if conn == nil {
		return nil, fmt.Errorf("new tcp tunnel: %w: nil conn", transport.ErrInvalidArgument)
	}
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	normalizedTunnelID := strings.TrimSpace(meta.TunnelID)
	if normalizedTunnelID == "" {
		return nil, fmt.Errorf("new tcp tunnel: %w: empty tunnel id", transport.ErrInvalidArgument)
	}
	normalizedMeta := meta
	normalizedMeta.TunnelID = normalizedTunnelID
	if normalizedMeta.CreatedAt.IsZero() {
		// 未提供创建时间时自动补当前 UTC 时间。
		normalizedMeta.CreatedAt = time.Now().UTC()
	}
	if len(meta.Labels) > 0 {
		normalizedMeta.Labels = make(map[string]string, len(meta.Labels))
		for key, value := range meta.Labels {
			normalizedMeta.Labels[key] = value
		}
	}
	return &TCPTunnel{
		conn:        conn,
		config:      normalizedConfig,
		meta:        normalizedMeta,
		state:       transport.TunnelStateIdle,
		readState:   newFramedReadState(tunnelFrameHeaderSize),
		pendingRead: make([]byte, 0),
		doneChannel: make(chan struct{}),
	}, nil
}

// ID 返回 tunnel id。
func (tunnel *TCPTunnel) ID() string {
	if tunnel == nil {
		return ""
	}
	return tunnel.meta.TunnelID
}

// Meta 返回 tunnel 元数据快照。
func (tunnel *TCPTunnel) Meta() transport.TunnelMeta {
	if tunnel == nil {
		return transport.TunnelMeta{}
	}
	meta := tunnel.meta
	if len(tunnel.meta.Labels) > 0 {
		meta.Labels = make(map[string]string, len(tunnel.meta.Labels))
		for key, value := range tunnel.meta.Labels {
			meta.Labels[key] = value
		}
	}
	return meta
}

// State 返回 tunnel 当前状态。
func (tunnel *TCPTunnel) State() transport.TunnelState {
	if tunnel == nil {
		return transport.TunnelStateBroken
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.state
}

// BindingInfo 返回 binding 元信息。
func (tunnel *TCPTunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: transport.BindingTypeTCPFramed}
}

// Read 按 io.Reader 语义读取下一段 payload。
func (tunnel *TCPTunnel) Read(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("tcp tunnel read: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	tunnel.readMutex.Lock()
	defer tunnel.readMutex.Unlock()

	for {
		if len(tunnel.pendingRead) > 0 {
			readSize := copy(payload, tunnel.pendingRead)
			tunnel.pendingRead = tunnel.pendingRead[readSize:]
			return readSize, nil
		}

		switch tunnel.State() {
		case transport.TunnelStateClosed:
			return 0, io.EOF
		case transport.TunnelStateBroken:
			return 0, tunnel.errorOrDefaultLocked("tcp tunnel read")
		}

		nextPayload, err := readTunnelPayloadFromConn(tunnel.conn, &tunnel.readState, tunnel.config.MaxTunnelFramePayloadSize)
		if err != nil {
			if errors.Is(err, transport.ErrTimeout) {
				// deadline 超时不破坏 tunnel 生命周期，允许调用方稍后重试。
				return 0, fmt.Errorf("tcp tunnel read: %w", transport.ErrTimeout)
			}
			if errors.Is(err, transport.ErrClosed) {
				tunnel.markClosed(transport.ErrClosed)
				return 0, io.EOF
			}
			brokenErr := fmt.Errorf("tcp tunnel read: %w: %v", transport.ErrTunnelBroken, err)
			tunnel.markBroken(brokenErr)
			return 0, brokenErr
		}
		if len(nextPayload) == 0 {
			// 空 payload 不直接暴露给上层，继续读取下一条帧。
			continue
		}
		readSize := copy(payload, nextPayload)
		if readSize < len(nextPayload) {
			tunnel.pendingRead = append(tunnel.pendingRead[:0], nextPayload[readSize:]...)
		}
		return readSize, nil
	}
}

// Write 按 io.Writer 语义写入一条 payload。
func (tunnel *TCPTunnel) Write(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("tcp tunnel write: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	switch tunnel.State() {
	case transport.TunnelStateClosed:
		return 0, fmt.Errorf("tcp tunnel write: %w", transport.ErrClosed)
	case transport.TunnelStateBroken:
		return 0, tunnel.errorOrDefaultLocked("tcp tunnel write")
	}

	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()

	partialWrite, err := writeTunnelPayloadToConn(tunnel.conn, tunnel.config.MaxTunnelFramePayloadSize, payload)
	if err != nil {
		if errors.Is(err, transport.ErrInvalidArgument) && !partialWrite {
			// 本地参数校验失败时底层连接未被污染，不应把健康 tunnel 打成 broken。
			return 0, fmt.Errorf("tcp tunnel write: %w", err)
		}
		if errors.Is(err, transport.ErrTimeout) && !partialWrite {
			// 整帧尚未落到连接上时，超时仍可视为可重试。
			return 0, fmt.Errorf("tcp tunnel write: %w", transport.ErrTimeout)
		}
		if errors.Is(err, transport.ErrClosed) {
			tunnel.markClosed(transport.ErrClosed)
			return 0, fmt.Errorf("tcp tunnel write: %w", transport.ErrClosed)
		}
		// 一旦发生半帧写入失败，就无法再保证对端 framing 一致性，必须判定为 broken。
		brokenErr := fmt.Errorf("tcp tunnel write: %w: %v", transport.ErrTunnelBroken, err)
		tunnel.markBroken(brokenErr)
		return 0, brokenErr
	}
	return len(payload), nil
}

// Probe 以非阻塞方式探测空闲 tunnel 是否已被对端关闭。
func (tunnel *TCPTunnel) Probe(ctx context.Context) error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel probe: %w", transport.ErrInvalidArgument)
	}
	switch tunnel.State() {
	case transport.TunnelStateClosed:
		return fmt.Errorf("tcp tunnel probe: %w", transport.ErrClosed)
	case transport.TunnelStateBroken:
		return tunnel.errorOrDefaultLocked("tcp tunnel probe")
	}

	tunnel.readMutex.Lock()
	defer tunnel.readMutex.Unlock()

	switch tunnel.State() {
	case transport.TunnelStateClosed:
		return fmt.Errorf("tcp tunnel probe: %w", transport.ErrClosed)
	case transport.TunnelStateBroken:
		return tunnel.errorOrDefaultLocked("tcp tunnel probe")
	}
	if len(tunnel.pendingRead) > 0 {
		// 已有预读数据时无需再触发底层探测。
		return nil
	}

	probeDeadline := time.Now()
	if ctxDeadline, ok := probeContextDeadline(ctx); ok && ctxDeadline.Before(probeDeadline) {
		probeDeadline = ctxDeadline
	}
	restoreReadDeadline, err := setConnReadDeadlineTemporarily(tunnel.conn, probeDeadline)
	if err != nil {
		if errors.Is(err, io.ErrClosedPipe) || errors.Is(err, net.ErrClosed) {
			tunnel.markClosed(transport.ErrClosed)
			return fmt.Errorf("tcp tunnel probe: %w", transport.ErrClosed)
		}
		return fmt.Errorf("tcp tunnel probe: %w", err)
	}
	defer restoreReadDeadline()

	for {
		nextPayload, err := readTunnelPayloadFromConn(tunnel.conn, &tunnel.readState, tunnel.config.MaxTunnelFramePayloadSize)
		if err != nil {
			if errors.Is(err, transport.ErrTimeout) {
				// 未读到新数据或关闭事件时保持 tunnel 可用。
				return nil
			}
			if errors.Is(err, transport.ErrClosed) {
				tunnel.markClosed(transport.ErrClosed)
				return fmt.Errorf("tcp tunnel probe: %w", transport.ErrClosed)
			}
			brokenErr := fmt.Errorf("tcp tunnel probe: %w: %v", transport.ErrTunnelBroken, err)
			tunnel.markBroken(brokenErr)
			return brokenErr
		}
		if len(nextPayload) == 0 {
			// 空帧不对外暴露，继续尝试读取下一帧。
			continue
		}
		tunnel.pendingRead = append(tunnel.pendingRead[:0], nextPayload...)
		return nil
	}
}

// Close 关闭 tunnel。
func (tunnel *TCPTunnel) Close() error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel close: %w", transport.ErrInvalidArgument)
	}
	if tunnel.State() == transport.TunnelStateClosed {
		return nil
	}
	if err := tunnel.closeConn(); err != nil && !errors.Is(err, net.ErrClosed) {
		brokenErr := fmt.Errorf("tcp tunnel close: %w: %v", transport.ErrTunnelBroken, err)
		tunnel.markBroken(brokenErr)
		return brokenErr
	}
	if tunnel.State() == transport.TunnelStateBroken {
		return nil
	}
	tunnel.markClosed(transport.ErrClosed)
	return nil
}

// CloseWrite 在支持 half-close 的 TCPConn 上关闭写方向。
func (tunnel *TCPTunnel) CloseWrite() error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel close write: %w", transport.ErrInvalidArgument)
	}
	closeWriter, ok := tunnel.conn.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("tcp tunnel close write: %w", transport.ErrUnsupported)
	}
	if err := closeWriter.CloseWrite(); err != nil {
		if errors.Is(err, net.ErrClosed) {
			return fmt.Errorf("tcp tunnel close write: %w", transport.ErrClosed)
		}
		return fmt.Errorf("tcp tunnel close write: %w", err)
	}
	return nil
}

// Reset 中断 tunnel 并尽量映射到 TCP RST。
func (tunnel *TCPTunnel) Reset(cause error) error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel reset: %w", transport.ErrInvalidArgument)
	}
	if cause == nil {
		cause = transport.ErrTunnelBroken
	}
	if lingerSetter, ok := tunnel.conn.(interface{ SetLinger(int) error }); ok {
		// 支持 linger=0 时优先发 RST，尽快让对端感知异常终止。
		_ = lingerSetter.SetLinger(0)
	}
	_ = tunnel.closeConn()
	brokenErr := fmt.Errorf("tcp tunnel reset: %w: %v", transport.ErrTunnelBroken, cause)
	tunnel.markBroken(brokenErr)
	return nil
}

// SetDeadline 同时设置读写 deadline。
func (tunnel *TCPTunnel) SetDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel set deadline: %w", transport.ErrInvalidArgument)
	}
	if err := tunnel.conn.SetDeadline(deadline); err != nil {
		return fmt.Errorf("tcp tunnel set deadline: %w", err)
	}
	return nil
}

// SetReadDeadline 设置读 deadline。
func (tunnel *TCPTunnel) SetReadDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel set read deadline: %w", transport.ErrInvalidArgument)
	}
	if err := tunnel.conn.SetReadDeadline(deadline); err != nil {
		return fmt.Errorf("tcp tunnel set read deadline: %w", err)
	}
	return nil
}

// SetWriteDeadline 设置写 deadline。
func (tunnel *TCPTunnel) SetWriteDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("tcp tunnel set write deadline: %w", transport.ErrInvalidArgument)
	}
	if err := tunnel.conn.SetWriteDeadline(deadline); err != nil {
		return fmt.Errorf("tcp tunnel set write deadline: %w", err)
	}
	return nil
}

// Done 返回 tunnel 终止信号。
func (tunnel *TCPTunnel) Done() <-chan struct{} {
	if tunnel == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return tunnel.doneChannel
}

// Err 返回最近错误。
func (tunnel *TCPTunnel) Err() error {
	if tunnel == nil {
		return transport.ErrInvalidArgument
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.lastError
}

// closeConn 确保底层连接只被关闭一次。
func (tunnel *TCPTunnel) closeConn() error {
	var closeErr error
	tunnel.closeOnce.Do(func() {
		closeErr = tunnel.conn.Close()
	})
	return closeErr
}

// markClosed 将 tunnel 标记为正常关闭。
func (tunnel *TCPTunnel) markClosed(err error) {
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	if tunnel.state == transport.TunnelStateBroken {
		return
	}
	tunnel.state = transport.TunnelStateClosed
	if err == nil {
		tunnel.lastError = transport.ErrClosed
	} else {
		tunnel.lastError = err
	}
	tunnel.doneOnce.Do(func() {
		close(tunnel.doneChannel)
	})
}

// markBroken 将 tunnel 标记为损坏。
func (tunnel *TCPTunnel) markBroken(err error) {
	_ = tunnel.closeConn()
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	tunnel.state = transport.TunnelStateBroken
	if err == nil {
		tunnel.lastError = transport.ErrTunnelBroken
	} else {
		tunnel.lastError = err
	}
	tunnel.doneOnce.Do(func() {
		close(tunnel.doneChannel)
	})
}

// errorOrDefaultLocked 返回当前 tunnel 的最近错误或默认 broken 错误。
func (tunnel *TCPTunnel) errorOrDefaultLocked(prefix string) error {
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	if tunnel.lastError != nil {
		return tunnel.lastError
	}
	return fmt.Errorf("%s: %w", prefix, transport.ErrTunnelBroken)
}

func probeContextDeadline(ctx context.Context) (time.Time, bool) {
	if ctx == nil {
		return time.Time{}, false
	}
	deadline, ok := ctx.Deadline()
	return deadline, ok
}
