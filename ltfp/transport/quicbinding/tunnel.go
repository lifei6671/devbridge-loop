package quicbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	quic "github.com/quic-go/quic-go"
)

// QUICTunnel 把一条长期 QUIC 双向 stream 适配为 transport.Tunnel。
type QUICTunnel struct {
	stream *quic.Stream
	meta   transport.TunnelMeta

	stateMutex sync.RWMutex
	state      transport.TunnelState
	lastError  error
	reuseCount int

	readMutex  sync.Mutex
	writeMutex sync.Mutex

	deadlineMutex sync.RWMutex
	readDeadline  time.Time
	writeDeadline time.Time

	doneChannel chan struct{}
	doneOnce    sync.Once
}

var _ transport.Tunnel = (*QUICTunnel)(nil)
var _ transport.TunnelHealthProber = (*QUICTunnel)(nil)

// NewQUICTunnel 使用指定元数据创建 tunnel 适配器。
func NewQUICTunnel(stream *quic.Stream, meta transport.TunnelMeta) (*QUICTunnel, error) {
	if stream == nil {
		return nil, fmt.Errorf("new quic tunnel: %w: nil stream", transport.ErrInvalidArgument)
	}
	normalizedTunnelID := strings.TrimSpace(meta.TunnelID)
	if normalizedTunnelID == "" {
		return nil, fmt.Errorf("new quic tunnel: %w: empty tunnel id", transport.ErrInvalidArgument)
	}
	normalizedMeta := meta
	normalizedMeta.TunnelID = normalizedTunnelID
	if normalizedMeta.CreatedAt.IsZero() {
		normalizedMeta.CreatedAt = time.Now().UTC()
	}
	if len(meta.Labels) > 0 {
		normalizedMeta.Labels = make(map[string]string, len(meta.Labels))
		for key, value := range meta.Labels {
			normalizedMeta.Labels[key] = value
		}
	}
	tunnel := &QUICTunnel{
		stream:      stream,
		meta:        normalizedMeta,
		state:       transport.TunnelStateIdle,
		doneChannel: make(chan struct{}),
	}
	go tunnel.watchStreamContext()
	return tunnel, nil
}

// ID 返回 tunnel id。
func (tunnel *QUICTunnel) ID() string {
	if tunnel == nil {
		return ""
	}
	return tunnel.meta.TunnelID
}

// Meta 返回 tunnel 元数据快照。
func (tunnel *QUICTunnel) Meta() transport.TunnelMeta {
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
func (tunnel *QUICTunnel) State() transport.TunnelState {
	if tunnel == nil {
		return transport.TunnelStateBroken
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.state
}

// BindingInfo 返回 binding 元信息。
func (tunnel *QUICTunnel) BindingInfo() transport.BindingInfo {
	return transport.NewBindingInfo(transport.BindingTypeQUICNative)
}

// Probe 在 quic_native 首版中依赖连接级 keepalive，不额外暴露 tunnel 探活。
func (tunnel *QUICTunnel) Probe(ctx context.Context) error {
	_ = ctx
	if tunnel == nil {
		return fmt.Errorf("quic tunnel probe: %w", transport.ErrInvalidArgument)
	}
	return fmt.Errorf("quic tunnel probe: %w", transport.ErrUnsupported)
}

// Read 从底层 QUIC stream 读取数据。
func (tunnel *QUICTunnel) Read(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("quic tunnel read: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	tunnel.readMutex.Lock()
	defer tunnel.readMutex.Unlock()
	if err := tunnel.stream.SetReadDeadline(tunnel.currentReadDeadline()); err != nil {
		return 0, fmt.Errorf("quic tunnel read: %w", err)
	}
	readSize, err := tunnel.stream.Read(payload)
	if err == nil {
		return readSize, nil
	}
	if errors.Is(normalizeQUICOperationError("quic tunnel read", err), transport.ErrTimeout) {
		return readSize, fmt.Errorf("quic tunnel read: %w", transport.ErrTimeout)
	}
	if errors.Is(err, io.EOF) || errors.Is(normalizeQUICOperationError("quic tunnel read", err), transport.ErrClosed) {
		tunnel.markClosed(transport.ErrClosed)
		if readSize > 0 {
			return readSize, nil
		}
		return 0, io.EOF
	}
	brokenErr := fmt.Errorf("quic tunnel read: %w: %v", transport.ErrTunnelBroken, err)
	tunnel.markBroken(brokenErr)
	return readSize, brokenErr
}

// Write 向底层 QUIC stream 写入数据。
func (tunnel *QUICTunnel) Write(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("quic tunnel write: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	switch tunnel.State() {
	case transport.TunnelStateClosed:
		return 0, fmt.Errorf("quic tunnel write: %w", transport.ErrClosed)
	case transport.TunnelStateBroken:
		return 0, tunnel.errorOrDefaultLocked("quic tunnel write")
	}
	tunnel.writeMutex.Lock()
	defer tunnel.writeMutex.Unlock()
	if err := tunnel.stream.SetWriteDeadline(tunnel.currentWriteDeadline()); err != nil {
		return 0, fmt.Errorf("quic tunnel write: %w", err)
	}
	writtenSize, err := tunnel.stream.Write(payload)
	if err == nil {
		return writtenSize, nil
	}
	if errors.Is(normalizeQUICOperationError("quic tunnel write", err), transport.ErrTimeout) {
		return writtenSize, fmt.Errorf("quic tunnel write: %w", transport.ErrTimeout)
	}
	if errors.Is(normalizeQUICOperationError("quic tunnel write", err), transport.ErrClosed) {
		tunnel.markClosed(transport.ErrClosed)
		return writtenSize, fmt.Errorf("quic tunnel write: %w", transport.ErrClosed)
	}
	brokenErr := fmt.Errorf("quic tunnel write: %w: %v", transport.ErrTunnelBroken, err)
	tunnel.markBroken(brokenErr)
	return writtenSize, brokenErr
}

// Close 关闭整条 tunnel。
func (tunnel *QUICTunnel) Close() error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel close: %w", transport.ErrInvalidArgument)
	}
	if tunnel.State().IsTerminal() {
		return nil
	}
	tunnel.stream.CancelRead(localStreamResetCode)
	if err := tunnel.stream.Close(); err != nil && !errors.Is(normalizeQUICOperationError("quic tunnel close", err), transport.ErrClosed) {
		brokenErr := fmt.Errorf("quic tunnel close: %w: %v", transport.ErrTunnelBroken, err)
		tunnel.markBroken(brokenErr)
		return brokenErr
	}
	tunnel.markClosed(transport.ErrClosed)
	return nil
}

// CloseWrite 对应 QUIC stream 的半关闭写方向。
func (tunnel *QUICTunnel) CloseWrite() error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel close write: %w", transport.ErrInvalidArgument)
	}
	if err := tunnel.stream.Close(); err != nil {
		if errors.Is(normalizeQUICOperationError("quic tunnel close write", err), transport.ErrClosed) {
			return fmt.Errorf("quic tunnel close write: %w", transport.ErrClosed)
		}
		return fmt.Errorf("quic tunnel close write: %w", err)
	}
	return nil
}

// Reset 中断 tunnel 并把当前 stream 标记为 broken。
func (tunnel *QUICTunnel) Reset(cause error) error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel reset: %w", transport.ErrInvalidArgument)
	}
	if cause == nil {
		cause = transport.ErrTunnelBroken
	}
	tunnel.stream.CancelRead(localStreamResetCode)
	tunnel.stream.CancelWrite(localStreamResetCode)
	brokenErr := fmt.Errorf("quic tunnel reset: %w: %v", transport.ErrTunnelBroken, cause)
	tunnel.markBroken(brokenErr)
	return nil
}

// SetDeadline 同时设置读写 deadline。
func (tunnel *QUICTunnel) SetDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel set deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.readDeadline = deadline
	tunnel.writeDeadline = deadline
	return tunnel.stream.SetDeadline(deadline)
}

// SetReadDeadline 设置读 deadline。
func (tunnel *QUICTunnel) SetReadDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel set read deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.readDeadline = deadline
	return tunnel.stream.SetReadDeadline(deadline)
}

// SetWriteDeadline 设置写 deadline。
func (tunnel *QUICTunnel) SetWriteDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel set write deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.writeDeadline = deadline
	return tunnel.stream.SetWriteDeadline(deadline)
}

// Flush 在 QUIC stream 语义下只需确认本地没有额外缓存。
func (tunnel *QUICTunnel) Flush() error {
	if tunnel == nil {
		return fmt.Errorf("quic tunnel flush: %w", transport.ErrInvalidArgument)
	}
	return nil
}

// ReuseCount 返回当前 tunnel 已完成回收轮次。
func (tunnel *QUICTunnel) ReuseCount() int {
	if tunnel == nil {
		return 0
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.reuseCount
}

// Recyclable 报告 tunnel 是否满足回收前提。
func (tunnel *QUICTunnel) Recyclable() bool {
	if tunnel == nil {
		return false
	}
	if tunnel.State().IsTerminal() {
		return false
	}
	return tunnel.Err() == nil
}

// Done 返回 tunnel 结束信号。
func (tunnel *QUICTunnel) Done() <-chan struct{} {
	if tunnel == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return tunnel.doneChannel
}

// Err 返回最近错误。
func (tunnel *QUICTunnel) Err() error {
	if tunnel == nil {
		return transport.ErrInvalidArgument
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.lastError
}

func (tunnel *QUICTunnel) currentReadDeadline() time.Time {
	tunnel.deadlineMutex.RLock()
	defer tunnel.deadlineMutex.RUnlock()
	return tunnel.readDeadline
}

func (tunnel *QUICTunnel) currentWriteDeadline() time.Time {
	tunnel.deadlineMutex.RLock()
	defer tunnel.deadlineMutex.RUnlock()
	return tunnel.writeDeadline
}

func (tunnel *QUICTunnel) markClosed(err error) {
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	if tunnel.state.IsTerminal() {
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

func (tunnel *QUICTunnel) markBroken(err error) {
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

func (tunnel *QUICTunnel) errorOrDefaultLocked(operation string) error {
	if tunnel == nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(operation), transport.ErrInvalidArgument)
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	if tunnel.lastError == nil {
		return fmt.Errorf("%s: %w", strings.TrimSpace(operation), transport.ErrTunnelBroken)
	}
	return tunnel.lastError
}

func (tunnel *QUICTunnel) watchStreamContext() {
	if tunnel == nil || tunnel.stream == nil {
		return
	}
	<-tunnel.stream.Context().Done()
	if tunnel.State().IsTerminal() {
		return
	}
	tunnel.markClosed(normalizeQUICOperationError("watch quic tunnel stream", tunnel.stream.Context().Err()))
}
