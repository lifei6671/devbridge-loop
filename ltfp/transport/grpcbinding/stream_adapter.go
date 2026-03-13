package grpcbinding

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type tunnelStream interface {
	Send(*transportgen.TunnelEnvelope) error
	Recv() (*transportgen.TunnelEnvelope, error)
	Context() context.Context
}

type tunnelStreamCloseSender interface {
	CloseSend() error
}

type tunnelStreamAdapterOptions struct {
	enableReadPayloadFastPath bool
}

func defaultTunnelStreamAdapterOptions() tunnelStreamAdapterOptions {
	return tunnelStreamAdapterOptions{
		enableReadPayloadFastPath: true,
	}
}

// GRPCH2TunnelStream 封装 TunnelEnvelope 的双向字节读写。
//
// 该类型用于 binding 内部适配，向 runtime 提供纯 bytes 载荷收发能力。
type GRPCH2TunnelStream struct {
	stream tunnelStream

	readPayloadFastPath bool

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

func newGRPCH2TunnelStream(stream tunnelStream) (*GRPCH2TunnelStream, error) {
	return newGRPCH2TunnelStreamWithCancel(stream, nil)
}

func newGRPCH2TunnelStreamWithCancel(
	stream tunnelStream,
	cancel context.CancelFunc,
) (*GRPCH2TunnelStream, error) {
	return newGRPCH2TunnelStreamWithOptions(stream, cancel, defaultTunnelStreamAdapterOptions())
}

func newGRPCH2TunnelStreamWithOptions(
	stream tunnelStream,
	cancel context.CancelFunc,
	options tunnelStreamAdapterOptions,
) (*GRPCH2TunnelStream, error) {
	if stream == nil {
		return nil, fmt.Errorf("new grpc tunnel stream: %w: nil stream", transport.ErrInvalidArgument)
	}
	return &GRPCH2TunnelStream{
		stream:              stream,
		readPayloadFastPath: options.enableReadPayloadFastPath,
		doneChannel:         make(chan struct{}),
		cancel:              cancel,
	}, nil
}

// WritePayload 写入一条数据面 payload。
func (stream *GRPCH2TunnelStream) WritePayload(ctx context.Context, payload []byte) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("grpc tunnel stream write: %w", ctx.Err())
	default:
	}
	stream.stateMutex.Lock()
	if stream.closed {
		err := stream.closedErrorLocked()
		stream.stateMutex.Unlock()
		return fmt.Errorf("grpc tunnel stream write: %w", err)
	}
	stream.stateMutex.Unlock()

	stream.writeMutex.Lock()
	defer stream.writeMutex.Unlock()
	stream.stateMutex.Lock()
	if stream.closed {
		err := stream.closedErrorLocked()
		stream.stateMutex.Unlock()
		return fmt.Errorf("grpc tunnel stream write: %w", err)
	}
	stream.stateMutex.Unlock()

	stopWatch := stream.watchCallContext(ctx)
	defer stopWatch()
	if err := stream.stream.Send(&transportgen.TunnelEnvelope{
		Payload: append([]byte(nil), payload...),
	}); err != nil {
		stream.stateMutex.Lock()
		defer stream.stateMutex.Unlock()
		if stream.closed {
			return fmt.Errorf("grpc tunnel stream write: %w", stream.closedErrorLocked())
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			stream.markClosedLocked(ctxErr)
			return fmt.Errorf("grpc tunnel stream write: %w", ctxErr)
		}
		if errors.Is(err, io.EOF) {
			stream.markClosedLocked(transport.ErrClosed)
			return fmt.Errorf("grpc tunnel stream write: %w", transport.ErrClosed)
		}
		stream.markClosedLocked(err)
		return fmt.Errorf("grpc tunnel stream write: %w", err)
	}
	return nil
}

// ReadPayload 读取下一条数据面 payload。
func (stream *GRPCH2TunnelStream) ReadPayload(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("grpc tunnel stream read: %w", ctx.Err())
	default:
	}
	stream.stateMutex.Lock()
	if stream.closed {
		err := stream.closedErrorLocked()
		stream.stateMutex.Unlock()
		return nil, fmt.Errorf("grpc tunnel stream read: %w", err)
	}
	stream.stateMutex.Unlock()

	stream.readMutex.Lock()
	defer stream.readMutex.Unlock()
	stream.stateMutex.Lock()
	if stream.closed {
		err := stream.closedErrorLocked()
		stream.stateMutex.Unlock()
		return nil, fmt.Errorf("grpc tunnel stream read: %w", err)
	}
	stream.stateMutex.Unlock()

	stopWatch := stream.watchCallContext(ctx)
	defer stopWatch()
	envelope, err := stream.stream.Recv()
	if err != nil {
		stream.stateMutex.Lock()
		defer stream.stateMutex.Unlock()
		if stream.closed {
			return nil, fmt.Errorf("grpc tunnel stream read: %w", stream.closedErrorLocked())
		}
		if ctxErr := ctx.Err(); ctxErr != nil {
			stream.markClosedLocked(ctxErr)
			return nil, fmt.Errorf("grpc tunnel stream read: %w", ctxErr)
		}
		if errors.Is(err, io.EOF) {
			stream.markClosedLocked(transport.ErrClosed)
			return nil, fmt.Errorf("grpc tunnel stream read: %w", transport.ErrClosed)
		}
		stream.markClosedLocked(err)
		return nil, fmt.Errorf("grpc tunnel stream read: %w", err)
	}
	if envelope == nil {
		return nil, fmt.Errorf("grpc tunnel stream read: %w: nil envelope", transport.ErrInvalidArgument)
	}
	if stream.readPayloadFastPath {
		return envelope.Payload, nil
	}
	return append([]byte(nil), envelope.Payload...), nil
}

// Close 关闭数据流发送端。
func (stream *GRPCH2TunnelStream) Close(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("grpc tunnel stream close: %w", ctx.Err())
	default:
	}
	stream.stateMutex.Lock()
	if stream.closed {
		stream.stateMutex.Unlock()
		return nil
	}
	stream.stateMutex.Unlock()

	_, supportsCloseSend := stream.stream.(tunnelStreamCloseSender)
	canInterruptBlockedWrite := stream.cancel != nil || supportsCloseSend
	writeLockHeld := false
	if stream.writeMutex.TryLock() {
		writeLockHeld = true
	} else {
		// 先取消底层 stream，打断可能卡住的 Send，再按 ctx 等待写锁。
		stream.cancelStream()
		if !canInterruptBlockedWrite {
			// 服务端 stream 既无 cancel 也无 CloseSend 时，写阻塞不可中断；
			// 这里直接收敛到 closed，避免 Close 永久等待写锁。
			stream.stateMutex.Lock()
			if stream.closed {
				stream.stateMutex.Unlock()
				return nil
			}
			stream.markClosedLocked(transport.ErrClosed)
			stream.stateMutex.Unlock()
			return nil
		}
		lockWaitTicker := time.NewTicker(closeWriteLockRetryInterval)
		defer lockWaitTicker.Stop()
		for !writeLockHeld {
			stream.stateMutex.Lock()
			if stream.closed {
				stream.stateMutex.Unlock()
				return nil
			}
			stream.stateMutex.Unlock()

			if stream.writeMutex.TryLock() {
				writeLockHeld = true
				break
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("grpc tunnel stream close: %w", ctx.Err())
			case <-lockWaitTicker.C:
			}
		}
	}
	defer func() {
		if writeLockHeld {
			stream.writeMutex.Unlock()
		}
	}()

	stream.stateMutex.Lock()
	if stream.closed {
		stream.stateMutex.Unlock()
		return nil
	}
	stream.stateMutex.Unlock()

	if closeSender, ok := stream.stream.(tunnelStreamCloseSender); ok {
		if err := closeSender.CloseSend(); err != nil {
			stream.cancelStream()
			stream.stateMutex.Lock()
			stream.markClosedLocked(err)
			stream.stateMutex.Unlock()
			return fmt.Errorf("grpc tunnel stream close: %w", err)
		}
	}
	stream.cancelStream()
	stream.stateMutex.Lock()
	stream.markClosedLocked(transport.ErrClosed)
	stream.stateMutex.Unlock()
	return nil
}

// Done 返回流终止通知。
func (stream *GRPCH2TunnelStream) Done() <-chan struct{} {
	if stream == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return stream.doneChannel
}

// Err 返回最近错误。
func (stream *GRPCH2TunnelStream) Err() error {
	if stream == nil {
		return transport.ErrInvalidArgument
	}
	stream.stateMutex.Lock()
	defer stream.stateMutex.Unlock()
	return stream.lastError
}

func (stream *GRPCH2TunnelStream) markClosedLocked(err error) {
	stream.closed = true
	stream.lastError = err
	stream.doneOnce.Do(func() {
		close(stream.doneChannel)
	})
}

func (stream *GRPCH2TunnelStream) watchCallContext(ctx context.Context) func() {
	done := make(chan struct{})
	if ctx == nil || ctx.Done() == nil {
		close(done)
		return func() {}
	}
	go func() {
		select {
		case <-ctx.Done():
			stream.cancelStream()
		case <-done:
		}
	}()
	return func() {
		close(done)
	}
}

func (stream *GRPCH2TunnelStream) cancelStream() {
	stream.cancelOnce.Do(func() {
		if stream.cancel != nil {
			stream.cancel()
		}
	})
}

func (stream *GRPCH2TunnelStream) closedErrorLocked() error {
	if stream.lastError != nil {
		return stream.lastError
	}
	return transport.ErrClosed
}
