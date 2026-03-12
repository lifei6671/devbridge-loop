package grpcbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"sync"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type controlChannelStream interface {
	Send(*transportgen.ControlFrameEnvelope) error
	Recv() (*transportgen.ControlFrameEnvelope, error)
	CloseSend() error
	Context() context.Context
}

// GRPCH2ControlChannel 是 grpc stream 到 transport.ControlChannel 的适配器。
type GRPCH2ControlChannel struct {
	stream controlChannelStream

	stateMutex  sync.Mutex
	readMutex   sync.Mutex
	writeMutex  sync.Mutex
	doneChannel chan struct{}
	doneOnce    sync.Once
	cancel      context.CancelFunc
	cancelOnce  sync.Once
	lastError   error
	closed      bool
}

const closeWriteLockRetryInterval = 2 * time.Millisecond

var _ transport.ControlChannel = (*GRPCH2ControlChannel)(nil)
var _ transport.PrioritizedControlChannel = (*GRPCH2ControlChannel)(nil)

func newGRPCH2ControlChannel(stream controlChannelStream) (*GRPCH2ControlChannel, error) {
	return newGRPCH2ControlChannelWithCancel(stream, nil)
}

func newGRPCH2ControlChannelWithCancel(
	stream controlChannelStream,
	cancel context.CancelFunc,
) (*GRPCH2ControlChannel, error) {
	if stream == nil {
		return nil, fmt.Errorf("new grpc control channel: %w: nil stream", transport.ErrInvalidArgument)
	}
	return &GRPCH2ControlChannel{
		stream:      stream,
		doneChannel: make(chan struct{}),
		cancel:      cancel,
	}, nil
}

// WriteControlFrame 写入一条控制帧。
func (channel *GRPCH2ControlChannel) WriteControlFrame(ctx context.Context, frame transport.ControlFrame) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("grpc control channel write: %w", ctx.Err())
	default:
	}

	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return fmt.Errorf("grpc control channel write: %w", err)
	}
	channel.stateMutex.Unlock()

	channel.writeMutex.Lock()
	defer channel.writeMutex.Unlock()
	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return fmt.Errorf("grpc control channel write: %w", err)
	}
	channel.stateMutex.Unlock()

	stopWatch := channel.watchCallContext(ctx)
	defer stopWatch()
	if err := channel.stream.Send(&transportgen.ControlFrameEnvelope{
		FrameType: uint32(frame.Type),
		Payload:   append([]byte(nil), frame.Payload...),
	}); err != nil {
		channel.stateMutex.Lock()
		defer channel.stateMutex.Unlock()
		if channel.closed {
			return fmt.Errorf("grpc control channel write: %w", channel.closedErrorLocked())
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			channel.markClosedLocked(ctxErr)
			return fmt.Errorf("grpc control channel write: %w", ctxErr)
		}
		if errors.Is(err, io.EOF) {
			channel.markClosedLocked(transport.ErrClosed)
			return fmt.Errorf("grpc control channel write: %w", transport.ErrClosed)
		}
		channel.markClosedLocked(err)
		return fmt.Errorf("grpc control channel write: %w", err)
	}
	return nil
}

// WritePrioritizedControlFrame 为 grpc_h2 暴露统一优先级发送入口。
func (channel *GRPCH2ControlChannel) WritePrioritizedControlFrame(
	ctx context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	return channel.WriteControlFrame(ctx, frame.Frame)
}

// ReadControlFrame 读取一条控制帧。
func (channel *GRPCH2ControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", ctx.Err())
	default:
	}
	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", err)
	}
	channel.stateMutex.Unlock()

	channel.readMutex.Lock()
	defer channel.readMutex.Unlock()
	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", err)
	}
	channel.stateMutex.Unlock()

	stopWatch := channel.watchCallContext(ctx)
	defer stopWatch()
	envelope, err := channel.stream.Recv()
	if err != nil {
		channel.stateMutex.Lock()
		defer channel.stateMutex.Unlock()
		if channel.closed {
			return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", channel.closedErrorLocked())
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			channel.markClosedLocked(ctxErr)
			return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", ctxErr)
		}
		if errors.Is(err, io.EOF) {
			channel.markClosedLocked(transport.ErrClosed)
			return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", transport.ErrClosed)
		}
		channel.markClosedLocked(err)
		return transport.ControlFrame{}, fmt.Errorf("grpc control channel read: %w", err)
	}
	if envelope == nil {
		return transport.ControlFrame{}, fmt.Errorf(
			"grpc control channel read: %w: nil envelope",
			transport.ErrInvalidArgument,
		)
	}
	if envelope.FrameType > math.MaxUint16 {
		return transport.ControlFrame{}, fmt.Errorf(
			"grpc control channel read: %w: frame_type=%d",
			transport.ErrInvalidArgument,
			envelope.FrameType,
		)
	}
	return transport.ControlFrame{
		Type:    uint16(envelope.FrameType),
		Payload: append([]byte(nil), envelope.Payload...),
	}, nil
}

// Close 关闭控制流发送端并终止后续读写。
func (channel *GRPCH2ControlChannel) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("grpc control channel close: %w", ctx.Err())
	default:
	}
	channel.stateMutex.Lock()
	if channel.closed {
		channel.stateMutex.Unlock()
		return nil
	}
	channel.stateMutex.Unlock()

	writeLockHeld := false
	if channel.writeMutex.TryLock() {
		writeLockHeld = true
	} else {
		// 先取消底层 stream，打断可能卡住的 Send，再按 ctx 等待写锁。
		channel.cancelStream()
		lockWaitTicker := time.NewTicker(closeWriteLockRetryInterval)
		defer lockWaitTicker.Stop()
		for !writeLockHeld {
			channel.stateMutex.Lock()
			if channel.closed {
				channel.stateMutex.Unlock()
				return nil
			}
			channel.stateMutex.Unlock()

			if channel.writeMutex.TryLock() {
				writeLockHeld = true
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("grpc control channel close: %w", ctx.Err())
			case <-lockWaitTicker.C:
			}
		}
	}
	defer func() {
		if writeLockHeld {
			channel.writeMutex.Unlock()
		}
	}()

	channel.stateMutex.Lock()
	if channel.closed {
		channel.stateMutex.Unlock()
		return nil
	}
	channel.stateMutex.Unlock()

	if err := channel.stream.CloseSend(); err != nil {
		channel.cancelStream()
		channel.stateMutex.Lock()
		channel.markClosedLocked(err)
		channel.stateMutex.Unlock()
		return fmt.Errorf("grpc control channel close: %w", err)
	}
	channel.cancelStream()
	channel.stateMutex.Lock()
	channel.markClosedLocked(transport.ErrClosed)
	channel.stateMutex.Unlock()
	return nil
}

// Done 返回控制流终止信号。
func (channel *GRPCH2ControlChannel) Done() <-chan struct{} {
	if channel == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return channel.doneChannel
}

// Err 返回最近错误。
func (channel *GRPCH2ControlChannel) Err() error {
	if channel == nil {
		return transport.ErrInvalidArgument
	}
	channel.stateMutex.Lock()
	defer channel.stateMutex.Unlock()
	return channel.lastError
}

func (channel *GRPCH2ControlChannel) markClosedLocked(err error) {
	channel.closed = true
	channel.lastError = err
	channel.doneOnce.Do(func() {
		close(channel.doneChannel)
	})
}

func (channel *GRPCH2ControlChannel) watchCallContext(ctx context.Context) func() {
	done := make(chan struct{})
	if ctx == nil || ctx.Done() == nil {
		close(done)
		return func() {}
	}
	go func() {
		select {
		case <-ctx.Done():
			channel.cancelStream()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func (channel *GRPCH2ControlChannel) cancelStream() {
	channel.cancelOnce.Do(func() {
		if channel.cancel != nil {
			channel.cancel()
		}
	})
}

func (channel *GRPCH2ControlChannel) closedErrorLocked() error {
	if channel.lastError != nil {
		return channel.lastError
	}
	return transport.ErrClosed
}
