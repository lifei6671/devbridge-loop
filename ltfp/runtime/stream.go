package runtime

import (
	"errors"
	"fmt"
	"io"
	"sync"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const (
	// defaultMaxDataFrameSizeBytes 为首版 grpc_h2 保守默认值，避免单帧过大触发消息大小限制。
	defaultMaxDataFrameSizeBytes = 1 * 1024 * 1024
)

// TrafficDataStreamConfig 描述 TrafficDataStream 行为配置。
type TrafficDataStreamConfig struct {
	MaxDataFrameSize int
}

// NormalizeAndValidate 归一化并校验配置。
func (config TrafficDataStreamConfig) NormalizeAndValidate() (TrafficDataStreamConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MaxDataFrameSize <= 0 {
		// 未显式配置时采用保守默认值。
		normalizedConfig.MaxDataFrameSize = defaultMaxDataFrameSizeBytes
	}
	return normalizedConfig, nil
}

// DefaultTrafficDataStreamConfig 返回默认配置。
func DefaultTrafficDataStreamConfig() TrafficDataStreamConfig {
	return TrafficDataStreamConfig{
		MaxDataFrameSize: defaultMaxDataFrameSizeBytes,
	}
}

// ErrTrafficReset 表示对端发送了 Reset 帧。
var ErrTrafficReset = errors.New("traffic reset")

// TrafficResetError 描述对端 Reset 的显式错误信息。
type TrafficResetError struct {
	Code    string
	Message string
}

// Error 返回 Reset 错误字符串。
func (err *TrafficResetError) Error() string {
	if err == nil {
		return ErrTrafficReset.Error()
	}
	if err.Code == "" && err.Message == "" {
		return ErrTrafficReset.Error()
	}
	return fmt.Sprintf("%s: code=%s message=%s", ErrTrafficReset.Error(), err.Code, err.Message)
}

// Is 支持 errors.Is(err, ErrTrafficReset)。
func (err *TrafficResetError) Is(target error) bool {
	return target == ErrTrafficReset
}

// TrafficDataStream 是 runtime framed adapter。
//
// 语义约束：
// 1. Write 按 max_data_frame_size 拆分 Data 帧并保持顺序。
// 2. Read 仅消费 Data/Close/Reset 帧，Data 大于读缓冲时保留内部缓存。
// 3. 支持一侧读、一侧写并发。
type TrafficDataStream struct {
	tunnel   transport.Tunnel
	protocol TrafficProtocol

	maxDataFrameSize int

	stateMutex        sync.RWMutex
	readMutex         sync.Mutex
	writeMutex        sync.Mutex
	pendingRead       []byte
	reachedEOF        bool
	terminalReadError error
}

var _ io.Reader = (*TrafficDataStream)(nil)
var _ io.Writer = (*TrafficDataStream)(nil)

// NewTrafficDataStream 使用默认配置创建 framed adapter。
func NewTrafficDataStream(tunnel transport.Tunnel, protocol TrafficProtocol) (*TrafficDataStream, error) {
	return NewTrafficDataStreamWithConfig(tunnel, protocol, DefaultTrafficDataStreamConfig())
}

// NewTrafficDataStreamWithConfig 使用指定配置创建 framed adapter。
func NewTrafficDataStreamWithConfig(
	tunnel transport.Tunnel,
	protocol TrafficProtocol,
	config TrafficDataStreamConfig,
) (*TrafficDataStream, error) {
	if tunnel == nil {
		return nil, fmt.Errorf("new traffic data stream: %w: nil tunnel", transport.ErrInvalidArgument)
	}
	if protocol == nil {
		return nil, fmt.Errorf("new traffic data stream: %w: nil protocol", transport.ErrInvalidArgument)
	}
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, fmt.Errorf("new traffic data stream: %w", err)
	}
	return &TrafficDataStream{
		tunnel:           tunnel,
		protocol:         protocol,
		maxDataFrameSize: normalizedConfig.MaxDataFrameSize,
		pendingRead:      make([]byte, 0),
	}, nil
}

// Write 将输入字节流拆分为 Data 帧写入 tunnel。
func (stream *TrafficDataStream) Write(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	stream.writeMutex.Lock()
	defer stream.writeMutex.Unlock()

	writtenSize := 0
	for writtenSize < len(payload) {
		if terminalErr := stream.terminalError(); terminalErr != nil {
			return writtenSize, fmt.Errorf("traffic data stream write: %w", terminalErr)
		}
		chunkSize := stream.maxDataFrameSize
		remainingSize := len(payload) - writtenSize
		if remainingSize < chunkSize {
			chunkSize = remainingSize
		}
		chunk := payload[writtenSize : writtenSize+chunkSize]
		if err := stream.protocol.WriteFrame(stream.tunnel, TrafficFrame{
			Type: TrafficFrameData,
			Data: chunk,
		}); err != nil {
			return writtenSize, fmt.Errorf("traffic data stream write: %w", err)
		}
		writtenSize += chunkSize
	}
	return writtenSize, nil
}

// Read 从 tunnel 读取下一段字节流数据。
func (stream *TrafficDataStream) Read(payload []byte) (int, error) {
	if len(payload) == 0 {
		return 0, nil
	}
	stream.readMutex.Lock()
	defer stream.readMutex.Unlock()

	if len(stream.pendingRead) > 0 {
		readSize := copy(payload, stream.pendingRead)
		stream.pendingRead = stream.pendingRead[readSize:]
		return readSize, nil
	}
	stream.stateMutex.RLock()
	reachedEOF := stream.reachedEOF
	terminalReadError := stream.terminalReadError
	stream.stateMutex.RUnlock()
	if reachedEOF {
		return 0, io.EOF
	}
	if terminalReadError != nil {
		return 0, terminalReadError
	}

	for {
		frame, err := stream.protocol.ReadFrame(stream.tunnel)
		if err != nil {
			return 0, fmt.Errorf("traffic data stream read: %w", err)
		}
		switch frame.Type {
		case TrafficFrameData:
			if len(frame.Data) == 0 {
				// 空 Data 帧不产生有效载荷，继续读取下一帧。
				continue
			}
			readSize := copy(payload, frame.Data)
			if readSize < len(frame.Data) {
				stream.pendingRead = append(stream.pendingRead[:0], frame.Data[readSize:]...)
			}
			return readSize, nil
		case TrafficFrameClose:
			stream.stateMutex.Lock()
			stream.reachedEOF = true
			stream.stateMutex.Unlock()
			return 0, io.EOF
		case TrafficFrameReset:
			resetCode := ""
			resetMessage := ""
			if frame.Reset != nil {
				resetCode = frame.Reset.Code
				resetMessage = frame.Reset.Message
			}
			resetError := &TrafficResetError{
				Code:    resetCode,
				Message: resetMessage,
			}
			stream.stateMutex.Lock()
			stream.terminalReadError = resetError
			stream.stateMutex.Unlock()
			return 0, resetError
		default:
			return 0, fmt.Errorf(
				"traffic data stream read: %w: unexpected frame type=%s",
				transport.ErrStateTransition,
				frame.Type,
			)
		}
	}
}

func (stream *TrafficDataStream) terminalError() error {
	stream.stateMutex.RLock()
	defer stream.stateMutex.RUnlock()
	if stream.terminalReadError != nil {
		return stream.terminalReadError
	}
	if stream.reachedEOF {
		return io.EOF
	}
	return nil
}
