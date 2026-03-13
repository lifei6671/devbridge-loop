package grpcbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// GRPCH2Tunnel 将 GRPCH2TunnelStream 适配为 transport.Tunnel。
type GRPCH2Tunnel struct {
	stream *GRPCH2TunnelStream
	meta   transport.TunnelMeta

	stateMutex sync.RWMutex
	state      transport.TunnelState
	lastError  error

	readMutex         sync.Mutex
	pendingRead       []byte
	pendingReadOffset int
	pendingReadPooled bool

	deadlineMutex sync.RWMutex
	readDeadline  time.Time
	writeDeadline time.Time
}

var _ transport.Tunnel = (*GRPCH2Tunnel)(nil)

// NewGRPCH2Tunnel 使用指定元数据创建 tunnel 适配器。
func NewGRPCH2Tunnel(stream *GRPCH2TunnelStream, meta transport.TunnelMeta) (*GRPCH2Tunnel, error) {
	if stream == nil {
		return nil, fmt.Errorf("new grpc tunnel: %w: nil stream", transport.ErrInvalidArgument)
	}
	normalizedTunnelID := strings.TrimSpace(meta.TunnelID)
	if normalizedTunnelID == "" {
		return nil, fmt.Errorf("new grpc tunnel: %w: empty tunnel id", transport.ErrInvalidArgument)
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
	tunnel := &GRPCH2Tunnel{
		stream: stream,
		meta:   normalizedMeta,
		state:  transport.TunnelStateIdle,
	}
	go tunnel.watchStreamDone()
	return tunnel, nil
}

// ID 返回 tunnel id。
func (tunnel *GRPCH2Tunnel) ID() string {
	if tunnel == nil {
		return ""
	}
	return tunnel.meta.TunnelID
}

// Meta 返回 tunnel 元数据快照。
func (tunnel *GRPCH2Tunnel) Meta() transport.TunnelMeta {
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
func (tunnel *GRPCH2Tunnel) State() transport.TunnelState {
	if tunnel == nil {
		return transport.TunnelStateBroken
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	return tunnel.state
}

// BindingInfo 返回绑定类型信息。
func (tunnel *GRPCH2Tunnel) BindingInfo() transport.BindingInfo {
	return transport.BindingInfo{Type: transport.BindingTypeGRPCH2}
}

// Read 按 io.Reader 语义读取 payload。
func (tunnel *GRPCH2Tunnel) Read(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("grpc tunnel read: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	tunnel.readMutex.Lock()
	defer tunnel.readMutex.Unlock()

	for {
		if readSize, hasPending := tunnel.copyFromPendingRead(payload); hasPending {
			return readSize, nil
		}
		switch tunnel.State() {
		case transport.TunnelStateClosed:
			return 0, io.EOF
		case transport.TunnelStateBroken:
			return 0, tunnel.errorOrDefaultLocked("grpc tunnel read")
		}

		if isDeadlineExceeded(tunnel.currentReadDeadline()) {
			return 0, fmt.Errorf("grpc tunnel read: %w", transport.ErrTimeout)
		}
		readContext, cancelRead := tunnel.operationContextWithDeadline(tunnel.currentReadDeadline())
		nextPayload, err := tunnel.stream.ReadPayload(readContext)
		cancelRead()
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return 0, fmt.Errorf("grpc tunnel read: %w", transport.ErrTimeout)
			}
			if errors.Is(err, transport.ErrClosed) {
				tunnel.markClosed(transport.ErrClosed)
				return 0, io.EOF
			}
			brokenErr := fmt.Errorf("grpc tunnel read: %w: %v", transport.ErrTunnelBroken, err)
			tunnel.markBroken(brokenErr)
			return 0, brokenErr
		}
		if len(nextPayload) == 0 {
			// 空 payload 不返回给上层，继续读取下一条消息。
			continue
		}
		readSize := copy(payload, nextPayload)
		if readSize < len(nextPayload) {
			tunnel.storePendingRead(nextPayload[readSize:])
		}
		return readSize, nil
	}
}

// Write 按 io.Writer 语义写入 payload。
func (tunnel *GRPCH2Tunnel) Write(payload []byte) (int, error) {
	if tunnel == nil {
		return 0, fmt.Errorf("grpc tunnel write: %w", transport.ErrInvalidArgument)
	}
	if len(payload) == 0 {
		return 0, nil
	}
	switch tunnel.State() {
	case transport.TunnelStateClosed:
		return 0, fmt.Errorf("grpc tunnel write: %w", transport.ErrClosed)
	case transport.TunnelStateBroken:
		return 0, tunnel.errorOrDefaultLocked("grpc tunnel write")
	}

	if isDeadlineExceeded(tunnel.currentWriteDeadline()) {
		return 0, fmt.Errorf("grpc tunnel write: %w", transport.ErrTimeout)
	}
	writeContext, cancelWrite := tunnel.operationContextWithDeadline(tunnel.currentWriteDeadline())
	err := tunnel.stream.WritePayload(writeContext, payload)
	cancelWrite()
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return 0, fmt.Errorf("grpc tunnel write: %w", transport.ErrTimeout)
		}
		if errors.Is(err, transport.ErrClosed) {
			tunnel.markClosed(transport.ErrClosed)
			return 0, fmt.Errorf("grpc tunnel write: %w", transport.ErrClosed)
		}
		brokenErr := fmt.Errorf("grpc tunnel write: %w: %v", transport.ErrTunnelBroken, err)
		tunnel.markBroken(brokenErr)
		return 0, brokenErr
	}
	return len(payload), nil
}

// Close 关闭 tunnel。
func (tunnel *GRPCH2Tunnel) Close() error {
	if tunnel == nil {
		return fmt.Errorf("grpc tunnel close: %w", transport.ErrInvalidArgument)
	}
	if tunnel.State().IsTerminal() {
		return nil
	}
	if err := tunnel.stream.Close(context.Background()); err != nil && !errors.Is(err, transport.ErrClosed) {
		brokenErr := fmt.Errorf("grpc tunnel close: %w: %v", transport.ErrTunnelBroken, err)
		tunnel.markBroken(brokenErr)
		return brokenErr
	}
	tunnel.markClosed(transport.ErrClosed)
	return nil
}

// CloseWrite 在 grpc_h2 首版未暴露半关闭能力。
func (tunnel *GRPCH2Tunnel) CloseWrite() error {
	return fmt.Errorf("grpc tunnel close write: %w", transport.ErrUnsupported)
}

// Reset 中断 tunnel 并标记为 broken。
func (tunnel *GRPCH2Tunnel) Reset(cause error) error {
	if tunnel == nil {
		return fmt.Errorf("grpc tunnel reset: %w", transport.ErrInvalidArgument)
	}
	if cause == nil {
		cause = transport.ErrTunnelBroken
	}
	_ = tunnel.stream.Close(context.Background())
	brokenErr := fmt.Errorf("grpc tunnel reset: %w: %v", transport.ErrTunnelBroken, cause)
	tunnel.markBroken(brokenErr)
	return nil
}

// SetDeadline 同时设置读写超时。
func (tunnel *GRPCH2Tunnel) SetDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("grpc tunnel set deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.readDeadline = deadline
	tunnel.writeDeadline = deadline
	return nil
}

// SetReadDeadline 设置读超时。
func (tunnel *GRPCH2Tunnel) SetReadDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("grpc tunnel set read deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.readDeadline = deadline
	return nil
}

// SetWriteDeadline 设置写超时。
func (tunnel *GRPCH2Tunnel) SetWriteDeadline(deadline time.Time) error {
	if tunnel == nil {
		return fmt.Errorf("grpc tunnel set write deadline: %w", transport.ErrInvalidArgument)
	}
	tunnel.deadlineMutex.Lock()
	defer tunnel.deadlineMutex.Unlock()
	tunnel.writeDeadline = deadline
	return nil
}

// Done 返回 tunnel 结束信号。
func (tunnel *GRPCH2Tunnel) Done() <-chan struct{} {
	if tunnel == nil {
		doneChannel := make(chan struct{})
		close(doneChannel)
		return doneChannel
	}
	return tunnel.stream.Done()
}

// Err 返回最近错误。
func (tunnel *GRPCH2Tunnel) Err() error {
	if tunnel == nil {
		return transport.ErrInvalidArgument
	}
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	if tunnel.lastError != nil {
		return tunnel.lastError
	}
	return tunnel.stream.Err()
}

func (tunnel *GRPCH2Tunnel) watchStreamDone() {
	<-tunnel.stream.Done()
	streamErr := tunnel.stream.Err()
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	if tunnel.state.IsTerminal() {
		if tunnel.lastError == nil {
			tunnel.lastError = streamErr
		}
		return
	}
	if streamErr == nil || errors.Is(streamErr, transport.ErrClosed) {
		tunnel.state = transport.TunnelStateClosed
		tunnel.lastError = transport.ErrClosed
		return
	}
	tunnel.state = transport.TunnelStateBroken
	tunnel.lastError = fmt.Errorf("grpc tunnel stream done: %w: %v", transport.ErrTunnelBroken, streamErr)
}

func (tunnel *GRPCH2Tunnel) markClosed(err error) {
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	if tunnel.state == transport.TunnelStateBroken {
		return
	}
	tunnel.state = transport.TunnelStateClosed
	if err == nil {
		tunnel.lastError = transport.ErrClosed
		return
	}
	tunnel.lastError = err
}

func (tunnel *GRPCH2Tunnel) markBroken(err error) {
	tunnel.stateMutex.Lock()
	defer tunnel.stateMutex.Unlock()
	tunnel.state = transport.TunnelStateBroken
	if err == nil {
		tunnel.lastError = transport.ErrTunnelBroken
		return
	}
	tunnel.lastError = err
}

func (tunnel *GRPCH2Tunnel) errorOrDefaultLocked(prefix string) error {
	tunnel.stateMutex.RLock()
	defer tunnel.stateMutex.RUnlock()
	if tunnel.lastError != nil {
		return tunnel.lastError
	}
	return fmt.Errorf("%s: %w", prefix, transport.ErrTunnelBroken)
}

func (tunnel *GRPCH2Tunnel) currentReadDeadline() time.Time {
	tunnel.deadlineMutex.RLock()
	defer tunnel.deadlineMutex.RUnlock()
	return tunnel.readDeadline
}

func (tunnel *GRPCH2Tunnel) currentWriteDeadline() time.Time {
	tunnel.deadlineMutex.RLock()
	defer tunnel.deadlineMutex.RUnlock()
	return tunnel.writeDeadline
}

func (tunnel *GRPCH2Tunnel) copyFromPendingRead(payload []byte) (int, bool) {
	if len(tunnel.pendingRead) == 0 {
		return 0, false
	}
	readSize := copy(payload, tunnel.pendingRead[tunnel.pendingReadOffset:])
	tunnel.pendingReadOffset += readSize
	if tunnel.pendingReadOffset >= len(tunnel.pendingRead) {
		tunnel.releasePendingRead()
	}
	return readSize, true
}

func (tunnel *GRPCH2Tunnel) storePendingRead(payload []byte) {
	tunnel.releasePendingRead()
	buffer, pooled := acquireGRPCPayloadBuffer(len(payload))
	copy(buffer, payload)
	tunnel.pendingRead = buffer
	tunnel.pendingReadOffset = 0
	tunnel.pendingReadPooled = pooled
}

func (tunnel *GRPCH2Tunnel) releasePendingRead() {
	if tunnel.pendingReadPooled {
		releaseGRPCPayloadBuffer(tunnel.pendingRead)
	}
	tunnel.pendingRead = nil
	tunnel.pendingReadOffset = 0
	tunnel.pendingReadPooled = false
}

func isDeadlineExceeded(deadline time.Time) bool {
	if deadline.IsZero() {
		return false
	}
	return !time.Now().Before(deadline)
}

func (tunnel *GRPCH2Tunnel) operationContextWithDeadline(deadline time.Time) (context.Context, context.CancelFunc) {
	if deadline.IsZero() {
		return context.Background(), func() {}
	}
	return context.WithDeadline(context.Background(), deadline)
}
