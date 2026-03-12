package tcpbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TCPControlChannel 将 framed TCP 连接适配为 transport.ControlChannel。
type TCPControlChannel struct {
	conn   net.Conn
	config TransportConfig

	stateMutex sync.Mutex
	readMutex  sync.Mutex

	readState framedReadState

	queueMutex           sync.Mutex
	writeQueue           *controlWriteQueue
	queueReadyChannel    chan struct{}
	activeWriteOperation *controlWriteOperation
	controlFragmenter    *transport.ControlFrameFragmenter
	controlReassembler   *transport.ControlFrameReassembler
	connCloseOnce        sync.Once

	doneChannel chan struct{}
	doneOnce    sync.Once
	lastError   error
	closed      bool
}

var _ transport.ControlChannel = (*TCPControlChannel)(nil)
var _ transport.PrioritizedControlChannel = (*TCPControlChannel)(nil)

// NewTCPControlChannel 使用指定连接创建控制通道。
func NewTCPControlChannel(conn net.Conn, config TransportConfig) (*TCPControlChannel, error) {
	if conn == nil {
		return nil, fmt.Errorf("new tcp control channel: %w: nil conn", transport.ErrInvalidArgument)
	}
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	controlFragmenter, controlReassembler, err := newTCPControlFragmentationPipeline(normalizedConfig)
	if err != nil {
		return nil, err
	}
	channel := &TCPControlChannel{
		conn:               conn,
		config:             normalizedConfig,
		readState:          newFramedReadState(controlFrameHeaderSize),
		writeQueue:         newControlWriteQueue(),
		queueReadyChannel:  make(chan struct{}, 1),
		controlFragmenter:  controlFragmenter,
		controlReassembler: controlReassembler,
		doneChannel:        make(chan struct{}),
	}
	go channel.runWriteLoop()
	return channel, nil
}

// WriteControlFrame 写入一条控制面帧。
func (channel *TCPControlChannel) WriteControlFrame(ctx context.Context, frame transport.ControlFrame) error {
	return channel.WritePrioritizedControlFrame(ctx, transport.PrioritizedControlFrame{
		Priority: transport.ControlMessagePriorityNormal,
		Frame:    frame,
	})
}

// WritePrioritizedControlFrame 写入一条带优先级的控制面帧。
func (channel *TCPControlChannel) WritePrioritizedControlFrame(
	ctx context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	if ctx == nil {
		// 控制面统一接受 nil ctx，内部回退到背景上下文。
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("tcp control channel write: %w", ctx.Err())
	default:
	}

	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return fmt.Errorf("tcp control channel write: %w", err)
	}
	channel.stateMutex.Unlock()

	framesToWrite, err := channel.prepareFramesForWrite(frame.Frame)
	if err != nil {
		return controlWriteReturnError(err)
	}
	writeOperation := newControlWriteOperation(ctx, len(framesToWrite))
	writeOperation.Watch(channel)

	channel.queueMutex.Lock()
	for _, controlFrame := range framesToWrite {
		channel.writeQueue.Enqueue(queuedControlWrite{
			priority:  frame.Priority,
			frame:     controlFrame,
			operation: writeOperation,
		})
	}
	channel.queueMutex.Unlock()
	channel.notifyWriteLoop()

	select {
	case <-writeOperation.Done():
		if writeErr := writeOperation.Err(); writeErr != nil {
			return writeErr
		}
		return nil
	case <-channel.Done():
		channel.stateMutex.Lock()
		closedErr := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		if writeErr := writeOperation.Err(); writeErr != nil {
			return writeErr
		}
		return controlWriteReturnError(closedErr)
	}
}

// ReadControlFrame 读取一条控制面帧。
func (channel *TCPControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	if ctx == nil {
		// 调用方未提供 ctx 时，内部继续使用背景上下文阻塞等待。
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", ctx.Err())
	default:
	}

	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", err)
	}
	channel.stateMutex.Unlock()

	channel.readMutex.Lock()
	defer channel.readMutex.Unlock()

	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", err)
	}
	channel.stateMutex.Unlock()

	stopWatch := channel.watchContextCancellation(ctx)
	defer stopWatch()

	for {
		frame, err := readControlFrameFromConn(channel.conn, &channel.readState, channel.config.MaxControlFramePayloadSize)
		if err != nil {
			if channel.isClosed() {
				channel.stateMutex.Lock()
				closedErr := channel.closedErrorLocked()
				channel.stateMutex.Unlock()
				return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", closedErr)
			}
			if ctxErr := ctx.Err(); ctxErr != nil {
				channel.shutdownWithError(ctxErr)
				return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", ctxErr)
			}
			if errors.Is(err, transport.ErrTimeout) {
				// 控制面读超时视为读操作失败并关闭通道，保持与 grpcbinding 语义对齐。
				channel.shutdownWithError(transport.ErrTimeout)
				return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", transport.ErrTimeout)
			}
			if errors.Is(err, transport.ErrClosed) {
				channel.shutdownWithError(transport.ErrClosed)
				return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", transport.ErrClosed)
			}
			if errors.Is(err, io.ErrUnexpectedEOF) {
				protocolErr := fmt.Errorf("tcp control channel read: %w: unexpected eof", transport.ErrClosed)
				channel.shutdownWithError(protocolErr)
				return transport.ControlFrame{}, protocolErr
			}
			channel.shutdownWithError(err)
			return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", err)
		}

		reassembledFrame, ready, err := channel.reassembleFrame(frame)
		if err != nil {
			channel.shutdownWithError(err)
			return transport.ControlFrame{}, fmt.Errorf("tcp control channel read: %w", err)
		}
		if !ready {
			continue
		}
		return reassembledFrame, nil
	}
}

// Close 关闭控制通道。
func (channel *TCPControlChannel) Close(ctx context.Context) error {
	if ctx == nil {
		// 关闭路径同样容忍 nil ctx。
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("tcp control channel close: %w", ctx.Err())
	default:
	}

	closeErr := channel.closeConn()
	channel.stateMutex.Lock()
	alreadyClosed := channel.closed
	if !channel.closed {
		if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
			channel.markClosedLocked(closeErr)
		} else {
			channel.markClosedLocked(transport.ErrClosed)
		}
	}
	closedErr := channel.closedErrorLocked()
	channel.stateMutex.Unlock()
	if !alreadyClosed {
		channel.failPendingWriteOperations(controlWriteReturnError(closedErr))
	}
	if closeErr != nil && !errors.Is(closeErr, net.ErrClosed) {
		return fmt.Errorf("tcp control channel close: %w", closeErr)
	}
	return nil
}

// Done 返回控制通道关闭信号。
func (channel *TCPControlChannel) Done() <-chan struct{} {
	if channel == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return channel.doneChannel
}

// Err 返回最近错误。
func (channel *TCPControlChannel) Err() error {
	if channel == nil {
		return transport.ErrInvalidArgument
	}
	channel.stateMutex.Lock()
	defer channel.stateMutex.Unlock()
	return channel.lastError
}

// watchContextCancellation 在没有显式 Close 的情况下，用 ctx 取消打断阻塞 I/O。
func (channel *TCPControlChannel) watchContextCancellation(ctx context.Context) func() {
	if ctx == nil || ctx.Done() == nil || ctx.Err() != nil {
		return func() {}
	}
	doneChannel := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			// 控制面 I/O 被 ctx 取消时，直接关闭底层连接以中断阻塞读写。
			channel.shutdownWithError(ctx.Err())
		case <-doneChannel:
		}
	}()
	return func() {
		close(doneChannel)
	}
}

// shutdownWithError 以指定错误收敛控制通道，并回收底层连接与待发送队列。
func (channel *TCPControlChannel) shutdownWithError(err error) {
	if channel == nil {
		return
	}
	if err == nil {
		err = transport.ErrClosed
	}
	_ = channel.closeConn()
	channel.stateMutex.Lock()
	alreadyClosed := channel.closed
	if !channel.closed {
		channel.markClosedLocked(err)
	}
	closedErr := channel.closedErrorLocked()
	channel.stateMutex.Unlock()
	if alreadyClosed {
		return
	}
	channel.failPendingWriteOperations(controlWriteReturnError(closedErr))
}

// markClosedLocked 在持锁状态下标记控制通道关闭。
func (channel *TCPControlChannel) markClosedLocked(err error) {
	channel.closed = true
	if err == nil {
		channel.lastError = transport.ErrClosed
	} else {
		channel.lastError = err
	}
	channel.doneOnce.Do(func() {
		close(channel.doneChannel)
	})
}

// closedErrorLocked 返回关闭后的标准错误。
func (channel *TCPControlChannel) closedErrorLocked() error {
	if channel.lastError != nil {
		return channel.lastError
	}
	return transport.ErrClosed
}

func (channel *TCPControlChannel) prepareFramesForWrite(frame transport.ControlFrame) ([]transport.ControlFrame, error) {
	if channel == nil {
		return nil, transport.ErrInvalidArgument
	}
	if channel.controlFragmenter != nil {
		return channel.controlFragmenter.Fragment(frame)
	}
	if frame.Type == transport.ControlFrameTypeFragment {
		return nil, fmt.Errorf(
			"fragment control frame: %w: reserved frame_type=%d",
			transport.ErrInvalidArgument,
			frame.Type,
		)
	}
	if channel.config.MaxControlFramePayloadSize > 0 && len(frame.Payload) > channel.config.MaxControlFramePayloadSize {
		return nil, fmt.Errorf(
			"write tcp frame: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			len(frame.Payload),
			channel.config.MaxControlFramePayloadSize,
		)
	}
	return []transport.ControlFrame{{
		Type:    frame.Type,
		Payload: append([]byte(nil), frame.Payload...),
	}}, nil
}

func (channel *TCPControlChannel) reassembleFrame(
	frame transport.ControlFrame,
) (transport.ControlFrame, bool, error) {
	if channel.controlReassembler == nil {
		if frame.Type == transport.ControlFrameTypeFragment {
			return transport.ControlFrame{}, false, fmt.Errorf(
				"reassemble control frame: %w: fragmented frame unsupported by config",
				transport.ErrInvalidArgument,
			)
		}
		return transport.ControlFrame{
			Type:    frame.Type,
			Payload: append([]byte(nil), frame.Payload...),
		}, true, nil
	}
	return channel.controlReassembler.Reassemble(frame)
}

func (channel *TCPControlChannel) runWriteLoop() {
	if channel == nil {
		return
	}
	for {
		select {
		case <-channel.doneChannel:
			return
		case <-channel.queueReadyChannel:
		}
		for {
			write, ok := channel.dequeueWrite()
			if !ok {
				break
			}
			if write.operation == nil || write.operation.IsFinished() {
				channel.clearActiveWrite(write.operation)
				continue
			}
			if !write.operation.Begin() {
				channel.clearActiveWrite(write.operation)
				continue
			}

			restoreWriteDeadline, err := setConnWriteDeadlineTemporarily(
				channel.conn,
				writeContextDeadline(write.operation.ctx),
			)
			if err != nil {
				mappedErr := err
				if errors.Is(err, net.ErrClosed) || errors.Is(err, io.ErrClosedPipe) {
					mappedErr = transport.ErrClosed
				}
				write.operation.Finish(controlWriteReturnError(mappedErr))
				channel.shutdownWithError(mappedErr)
				return
			}

			partialWrite, err := writeControlFrameToConn(
				channel.conn,
				channel.config.MaxControlFramePayloadSize,
				write.frame,
			)
			restoreWriteDeadline()
			channel.clearActiveWrite(write.operation)
			write.operation.MarkIdle()

			if err != nil {
				if ctxErr := write.operation.ctx.Err(); ctxErr != nil {
					write.operation.Finish(controlWriteReturnError(ctxErr))
					channel.shutdownWithError(ctxErr)
					return
				}
				if errors.Is(err, transport.ErrInvalidArgument) {
					write.operation.Finish(controlWriteReturnError(err))
					continue
				}
				if errors.Is(err, transport.ErrTimeout) {
					timeoutCause := transport.ErrTimeout
					if deadline, hasDeadline := write.operation.ctx.Deadline(); hasDeadline && !time.Now().Before(deadline) {
						// 写超时由调用方 deadline 触发时，返回 context deadline 语义。
						timeoutCause = context.DeadlineExceeded
					}
					write.operation.Finish(controlWriteReturnError(timeoutCause))
					channel.shutdownWithError(timeoutCause)
					return
				}
				if errors.Is(err, transport.ErrClosed) {
					write.operation.Finish(controlWriteReturnError(transport.ErrClosed))
					channel.shutdownWithError(transport.ErrClosed)
					return
				}
				if partialWrite {
					write.operation.Finish(controlWriteReturnError(transport.ErrClosed))
					channel.shutdownWithError(transport.ErrClosed)
					return
				}
				write.operation.Finish(controlWriteReturnError(err))
				channel.shutdownWithError(err)
				return
			}
			write.operation.CompleteFragment()
		}
	}
}

func (channel *TCPControlChannel) dequeueWrite() (queuedControlWrite, bool) {
	channel.queueMutex.Lock()
	defer channel.queueMutex.Unlock()
	write, ok := channel.writeQueue.Dequeue()
	if ok {
		channel.activeWriteOperation = write.operation
	}
	return write, ok
}

func (channel *TCPControlChannel) clearActiveWrite(operation *controlWriteOperation) {
	channel.queueMutex.Lock()
	defer channel.queueMutex.Unlock()
	if channel.activeWriteOperation == operation {
		channel.activeWriteOperation = nil
	}
}

func (channel *TCPControlChannel) failPendingWriteOperations(err error) {
	channel.queueMutex.Lock()
	activeWriteOperation := channel.activeWriteOperation
	channel.activeWriteOperation = nil
	pendingWrites := channel.writeQueue.Drain()
	channel.queueMutex.Unlock()

	if activeWriteOperation != nil {
		activeWriteOperation.Finish(err)
	}
	for _, write := range pendingWrites {
		if write.operation == nil {
			continue
		}
		write.operation.Finish(err)
	}
}

func (channel *TCPControlChannel) notifyWriteLoop() {
	if channel == nil {
		return
	}
	select {
	case channel.queueReadyChannel <- struct{}{}:
	default:
	}
}

func (channel *TCPControlChannel) closeConn() error {
	if channel == nil {
		return transport.ErrInvalidArgument
	}
	var closeErr error
	channel.connCloseOnce.Do(func() {
		closeErr = channel.conn.Close()
	})
	return closeErr
}

func (channel *TCPControlChannel) isClosed() bool {
	if channel == nil {
		return true
	}
	channel.stateMutex.Lock()
	defer channel.stateMutex.Unlock()
	return channel.closed
}

func newTCPControlFragmentationPipeline(
	config TransportConfig,
) (*transport.ControlFrameFragmenter, *transport.ControlFrameReassembler, error) {
	const controlFragmentHeaderPayloadSize = 24

	maxFragmentPayloadSize := config.MaxControlFramePayloadSize
	if maxFragmentPayloadSize > transport.DefaultControlFragmentMaxPayloadSize {
		maxFragmentPayloadSize = transport.DefaultControlFragmentMaxPayloadSize
	}
	if maxFragmentPayloadSize <= controlFragmentHeaderPayloadSize {
		return nil, nil, nil
	}
	fragmentationConfig := transport.DefaultControlFragmentationConfig()
	fragmentationConfig.MaxPayloadSize = maxFragmentPayloadSize
	fragmenter, err := transport.NewControlFrameFragmenter(fragmentationConfig)
	if err != nil {
		return nil, nil, err
	}
	reassembler, err := transport.NewControlFrameReassembler(fragmentationConfig)
	if err != nil {
		return nil, nil, err
	}
	return fragmenter, reassembler, nil
}

func writeContextDeadline(ctx context.Context) time.Time {
	if ctx == nil {
		return time.Time{}
	}
	deadline, ok := ctx.Deadline()
	if !ok {
		return time.Time{}
	}
	return deadline
}
