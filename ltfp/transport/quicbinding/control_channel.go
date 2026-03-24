package quicbinding

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	quic "github.com/quic-go/quic-go"
)

const controlFrameHeaderSize = 6

// QUICControlChannel 把一条专用 QUIC 双向 stream 适配成控制面通道。
type QUICControlChannel struct {
	stream *quic.Stream
	config TransportConfig

	stateMutex sync.Mutex
	readMutex  sync.Mutex
	writeMutex sync.Mutex

	fragmenter  *transport.ControlFrameFragmenter
	reassembler *transport.ControlFrameReassembler
	doneChannel chan struct{}
	doneOnce    sync.Once
	lastError   error
	closed      bool
}

var _ transport.ControlChannel = (*QUICControlChannel)(nil)
var _ transport.PrioritizedControlChannel = (*QUICControlChannel)(nil)

func newQUICControlChannel(stream *quic.Stream, config TransportConfig) (*QUICControlChannel, error) {
	if stream == nil {
		return nil, fmt.Errorf("new quic control channel: %w: nil stream", transport.ErrInvalidArgument)
	}
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	fragmentationConfig := transport.DefaultControlFragmentationConfig()
	fragmentationConfig.MaxPayloadSize = normalizedConfig.MaxControlFramePayloadSize
	fragmenter, err := transport.NewControlFrameFragmenter(fragmentationConfig)
	if err != nil {
		return nil, err
	}
	reassembler, err := transport.NewControlFrameReassembler(fragmentationConfig)
	if err != nil {
		return nil, err
	}
	channel := &QUICControlChannel{
		stream:      stream,
		config:      normalizedConfig,
		fragmenter:  fragmenter,
		reassembler: reassembler,
		doneChannel: make(chan struct{}),
	}
	go channel.watchStreamContext()
	return channel, nil
}

// WriteControlFrame 写入一条控制帧。
func (channel *QUICControlChannel) WriteControlFrame(ctx context.Context, frame transport.ControlFrame) error {
	return channel.WritePrioritizedControlFrame(ctx, transport.PrioritizedControlFrame{
		Priority: transport.ControlMessagePriorityNormal,
		Frame:    frame,
	})
}

// WritePrioritizedControlFrame 首版先保证语义正确，优先级信息仅用于保留统一接口。
func (channel *QUICControlChannel) WritePrioritizedControlFrame(
	ctx context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	if channel == nil || channel.stream == nil {
		return fmt.Errorf("quic control channel write: %w", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("quic control channel write: %w", ctx.Err())
	default:
	}
	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return fmt.Errorf("quic control channel write: %w", err)
	}
	channel.stateMutex.Unlock()

	framesToWrite, err := channel.fragmenter.Fragment(frame.Frame)
	if err != nil {
		return fmt.Errorf("quic control channel write: %w", err)
	}

	channel.writeMutex.Lock()
	defer channel.writeMutex.Unlock()
	restoreDeadline, err := channel.prepareWriteContext(ctx)
	if err != nil {
		return fmt.Errorf("quic control channel write: %w", err)
	}
	defer restoreDeadline()

	for _, controlFrame := range framesToWrite {
		if err := writeControlFrameToStream(channel.stream, channel.config.MaxControlFramePayloadSize, controlFrame); err != nil {
			normalizedErr := normalizeQUICOperationError("quic control channel write", err)
			if errors.Is(normalizedErr, transport.ErrTimeout) {
				return fmt.Errorf("quic control channel write: %w", transport.ErrTimeout)
			}
			channel.shutdownWithError(normalizedErr)
			return fmt.Errorf("quic control channel write: %w", normalizedErr)
		}
	}
	return nil
}

// ReadControlFrame 读取一条完整控制帧。
func (channel *QUICControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	if channel == nil || channel.stream == nil {
		return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", transport.ErrInvalidArgument)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", ctx.Err())
	default:
	}
	channel.stateMutex.Lock()
	if channel.closed {
		err := channel.closedErrorLocked()
		channel.stateMutex.Unlock()
		return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", err)
	}
	channel.stateMutex.Unlock()

	channel.readMutex.Lock()
	defer channel.readMutex.Unlock()
	restoreDeadline, err := channel.prepareReadContext(ctx)
	if err != nil {
		return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", err)
	}
	defer restoreDeadline()

	for {
		frame, err := readControlFrameFromStream(channel.stream, channel.config.MaxControlFramePayloadSize)
		if err != nil {
			normalizedErr := normalizeQUICOperationError("quic control channel read", err)
			if errors.Is(normalizedErr, transport.ErrTimeout) {
				return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", transport.ErrTimeout)
			}
			channel.shutdownWithError(normalizedErr)
			return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", normalizedErr)
		}
		reassembledFrame, ready, err := channel.reassembler.Reassemble(frame)
		if err != nil {
			channel.shutdownWithError(err)
			return transport.ControlFrame{}, fmt.Errorf("quic control channel read: %w", err)
		}
		if !ready {
			continue
		}
		return reassembledFrame, nil
	}
}

// Close 主动关闭控制流。
func (channel *QUICControlChannel) Close(ctx context.Context) error {
	if channel == nil || channel.stream == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return fmt.Errorf("quic control channel close: %w", ctx.Err())
	default:
	}
	channel.shutdownWithError(transport.ErrClosed)
	return nil
}

// Done 返回关闭信号。
func (channel *QUICControlChannel) Done() <-chan struct{} {
	if channel == nil {
		closedChannel := make(chan struct{})
		close(closedChannel)
		return closedChannel
	}
	return channel.doneChannel
}

// Err 返回最近错误。
func (channel *QUICControlChannel) Err() error {
	if channel == nil {
		return transport.ErrInvalidArgument
	}
	channel.stateMutex.Lock()
	defer channel.stateMutex.Unlock()
	if channel.lastError == nil {
		return transport.ErrClosed
	}
	return channel.lastError
}

func (channel *QUICControlChannel) prepareReadContext(ctx context.Context) (func(), error) {
	deadline := streamDeadlineFromContext(time.Time{}, ctx)
	if err := channel.stream.SetReadDeadline(deadline); err != nil {
		return nil, err
	}
	stopWatch := watchContextCancellation(ctx, channel.stream.SetReadDeadline)
	return func() {
		stopWatch()
		_ = channel.stream.SetReadDeadline(time.Time{})
	}, nil
}

func (channel *QUICControlChannel) prepareWriteContext(ctx context.Context) (func(), error) {
	deadline := streamDeadlineFromContext(time.Time{}, ctx)
	if err := channel.stream.SetWriteDeadline(deadline); err != nil {
		return nil, err
	}
	stopWatch := watchContextCancellation(ctx, channel.stream.SetWriteDeadline)
	return func() {
		stopWatch()
		_ = channel.stream.SetWriteDeadline(time.Time{})
	}, nil
}

func (channel *QUICControlChannel) shutdownWithError(err error) {
	if channel == nil || channel.stream == nil {
		return
	}
	if err == nil {
		err = transport.ErrClosed
	}
	channel.stateMutex.Lock()
	alreadyClosed := channel.closed
	if !channel.closed {
		channel.markClosedLocked(err)
	}
	channel.stateMutex.Unlock()
	if alreadyClosed {
		return
	}
	channel.stream.CancelRead(localStreamResetCode)
	channel.stream.CancelWrite(localStreamResetCode)
	_ = channel.stream.Close()
}

func (channel *QUICControlChannel) markClosedLocked(err error) {
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

func (channel *QUICControlChannel) closedErrorLocked() error {
	if channel.lastError == nil {
		return transport.ErrClosed
	}
	return channel.lastError
}

func (channel *QUICControlChannel) watchStreamContext() {
	if channel == nil || channel.stream == nil {
		return
	}
	<-channel.stream.Context().Done()
	channel.shutdownWithError(normalizeQUICOperationError("watch quic control stream", channel.stream.Context().Err()))
}

func readControlFrameFromStream(stream *quic.Stream, maxPayloadSize int) (transport.ControlFrame, error) {
	if stream == nil {
		return transport.ControlFrame{}, fmt.Errorf("read quic control frame: %w: nil stream", transport.ErrInvalidArgument)
	}
	header := make([]byte, controlFrameHeaderSize)
	if _, err := io.ReadFull(stream, header); err != nil {
		return transport.ControlFrame{}, err
	}
	payloadSize := int(binary.BigEndian.Uint32(header[2:6]))
	if payloadSize < 0 || payloadSize > maxPayloadSize {
		return transport.ControlFrame{}, fmt.Errorf(
			"read quic control frame: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			payloadSize,
			maxPayloadSize,
		)
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(stream, payload); err != nil {
		return transport.ControlFrame{}, err
	}
	return transport.ControlFrame{
		Type:    binary.BigEndian.Uint16(header[0:2]),
		Payload: payload,
	}, nil
}

func writeControlFrameToStream(stream *quic.Stream, maxPayloadSize int, frame transport.ControlFrame) error {
	if stream == nil {
		return fmt.Errorf("write quic control frame: %w: nil stream", transport.ErrInvalidArgument)
	}
	if frame.Type == transport.ControlFrameTypeFragment && len(frame.Payload) == 0 {
		return fmt.Errorf("write quic control frame: %w: empty fragment payload", transport.ErrInvalidArgument)
	}
	if len(frame.Payload) > maxPayloadSize {
		return fmt.Errorf(
			"write quic control frame: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			len(frame.Payload),
			maxPayloadSize,
		)
	}
	header := make([]byte, controlFrameHeaderSize)
	binary.BigEndian.PutUint16(header[0:2], frame.Type)
	binary.BigEndian.PutUint32(header[2:6], uint32(len(frame.Payload)))
	if err := writeAllToStream(stream, header); err != nil {
		return err
	}
	if len(frame.Payload) == 0 {
		return nil
	}
	return writeAllToStream(stream, frame.Payload)
}

func writeAllToStream(stream *quic.Stream, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	writtenSize := 0
	for writtenSize < len(payload) {
		n, err := stream.Write(payload[writtenSize:])
		writtenSize += n
		if err != nil {
			return err
		}
	}
	return nil
}
