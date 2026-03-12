package runtime

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const (
	trafficFrameHeaderBytes          = 5
	defaultMaxFramePayloadBytes      = 8 * 1024 * 1024
	trafficWireTypeOpen         byte = 1
	trafficWireTypeOpenAck      byte = 2
	trafficWireTypeData         byte = 3
	trafficWireTypeClose        byte = 4
	trafficWireTypeReset        byte = 5
)

// LengthPrefixedTrafficProtocolConfig 描述长度前缀协议配置。
type LengthPrefixedTrafficProtocolConfig struct {
	MaxFramePayloadBytes int
}

// NormalizeAndValidate 归一化并校验长度前缀协议配置。
func (config LengthPrefixedTrafficProtocolConfig) NormalizeAndValidate() (LengthPrefixedTrafficProtocolConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MaxFramePayloadBytes <= 0 {
		// 未配置或非法值时回落到保守默认，避免无限制内存申请。
		normalizedConfig.MaxFramePayloadBytes = defaultMaxFramePayloadBytes
	}
	return normalizedConfig, nil
}

// DefaultLengthPrefixedTrafficProtocolConfig 返回默认配置。
func DefaultLengthPrefixedTrafficProtocolConfig() LengthPrefixedTrafficProtocolConfig {
	return LengthPrefixedTrafficProtocolConfig{
		MaxFramePayloadBytes: defaultMaxFramePayloadBytes,
	}
}

// LengthPrefixedTrafficProtocol 实现基于“类型 + 长度 + 载荷”的最小 runtime 帧协议。
//
// 并发语义：
// 1. 允许单读单写并发。
// 2. 同一 tunnel 上多写会被串行化，避免帧字节交错。
// 3. 不保证同一 tunnel 多读安全。
type LengthPrefixedTrafficProtocol struct {
	config LengthPrefixedTrafficProtocolConfig

	writerLocks sync.Map
}

var _ TrafficProtocol = (*LengthPrefixedTrafficProtocol)(nil)

// NewLengthPrefixedTrafficProtocol 使用默认配置创建协议实例。
func NewLengthPrefixedTrafficProtocol() *LengthPrefixedTrafficProtocol {
	return &LengthPrefixedTrafficProtocol{
		config: DefaultLengthPrefixedTrafficProtocolConfig(),
	}
}

// NewLengthPrefixedTrafficProtocolWithConfig 使用指定配置创建协议实例。
func NewLengthPrefixedTrafficProtocolWithConfig(config LengthPrefixedTrafficProtocolConfig) (*LengthPrefixedTrafficProtocol, error) {
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &LengthPrefixedTrafficProtocol{
		config: normalizedConfig,
	}, nil
}

// WriteFrame 将一帧写入 tunnel。
func (protocol *LengthPrefixedTrafficProtocol) WriteFrame(tunnel transport.Tunnel, frame TrafficFrame) error {
	normalizedTunnelID, err := normalizeTunnelID(tunnel)
	if err != nil {
		return fmt.Errorf("write traffic frame: %w", err)
	}
	if err := frame.Validate(); err != nil {
		return fmt.Errorf("write traffic frame: %w", err)
	}
	wireType, err := encodeWireType(frame.Type)
	if err != nil {
		return fmt.Errorf("write traffic frame: %w", err)
	}
	payload, err := encodePayload(frame)
	if err != nil {
		return fmt.Errorf("write traffic frame: %w", err)
	}
	if len(payload) > protocol.config.MaxFramePayloadBytes {
		return fmt.Errorf(
			"write traffic frame: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			len(payload),
			protocol.config.MaxFramePayloadBytes,
		)
	}

	// 同一 tunnel 的多写方必须串行化，避免 header/payload 与其他帧交错。
	writerLock := protocol.writerLockForTunnel(normalizedTunnelID)
	writerLock.Lock()
	defer writerLock.Unlock()

	header := make([]byte, trafficFrameHeaderBytes)
	header[0] = wireType
	binary.BigEndian.PutUint32(header[1:], uint32(len(payload)))
	if err := writeFull(tunnel, header); err != nil {
		return fmt.Errorf("write traffic frame header: %w", err)
	}
	if err := writeFull(tunnel, payload); err != nil {
		return fmt.Errorf("write traffic frame payload: %w", err)
	}
	return nil
}

// ReadFrame 从 tunnel 读取下一帧。
func (protocol *LengthPrefixedTrafficProtocol) ReadFrame(tunnel transport.Tunnel) (TrafficFrame, error) {
	if tunnel == nil {
		return TrafficFrame{}, fmt.Errorf("read traffic frame: %w: nil tunnel", transport.ErrInvalidArgument)
	}
	header := make([]byte, trafficFrameHeaderBytes)
	if err := readFull(tunnel, header); err != nil {
		return TrafficFrame{}, fmt.Errorf("read traffic frame header: %w", err)
	}
	frameType, err := decodeWireType(header[0])
	if err != nil {
		return TrafficFrame{}, fmt.Errorf("read traffic frame type: %w", err)
	}

	payloadSize := int(binary.BigEndian.Uint32(header[1:]))
	if payloadSize < 0 || payloadSize > protocol.config.MaxFramePayloadBytes {
		return TrafficFrame{}, fmt.Errorf(
			"read traffic frame payload: %w: payload_size=%d max=%d",
			transport.ErrInvalidArgument,
			payloadSize,
			protocol.config.MaxFramePayloadBytes,
		)
	}
	payload := make([]byte, payloadSize)
	if err := readFull(tunnel, payload); err != nil {
		return TrafficFrame{}, fmt.Errorf("read traffic frame payload: %w", err)
	}
	frame, err := decodePayload(frameType, payload)
	if err != nil {
		return TrafficFrame{}, fmt.Errorf("decode traffic frame payload: %w", err)
	}
	if err := frame.Validate(); err != nil {
		return TrafficFrame{}, fmt.Errorf("decode traffic frame payload: %w", err)
	}
	return frame, nil
}

// writerLockForTunnel 返回指定 tunnel 的写锁。
func (protocol *LengthPrefixedTrafficProtocol) writerLockForTunnel(tunnelID string) *sync.Mutex {
	if value, ok := protocol.writerLocks.Load(tunnelID); ok {
		return value.(*sync.Mutex)
	}
	newLock := &sync.Mutex{}
	actual, _ := protocol.writerLocks.LoadOrStore(tunnelID, newLock)
	return actual.(*sync.Mutex)
}

// normalizeTunnelID 校验并归一化 tunnel ID。
func normalizeTunnelID(tunnel transport.Tunnel) (string, error) {
	if tunnel == nil {
		return "", fmt.Errorf("%w: nil tunnel", transport.ErrInvalidArgument)
	}
	normalizedTunnelID := strings.TrimSpace(tunnel.ID())
	if normalizedTunnelID == "" {
		return "", fmt.Errorf("%w: empty tunnel id", transport.ErrInvalidArgument)
	}
	return normalizedTunnelID, nil
}

// encodeWireType 将帧类型编码为线格式类型码。
func encodeWireType(frameType TrafficFrameType) (byte, error) {
	switch frameType {
	case TrafficFrameOpen:
		return trafficWireTypeOpen, nil
	case TrafficFrameOpenAck:
		return trafficWireTypeOpenAck, nil
	case TrafficFrameData:
		return trafficWireTypeData, nil
	case TrafficFrameClose:
		return trafficWireTypeClose, nil
	case TrafficFrameReset:
		return trafficWireTypeReset, nil
	default:
		return 0, fmt.Errorf("%w: unknown frame type=%q", transport.ErrInvalidArgument, frameType)
	}
}

// decodeWireType 将线格式类型码还原为帧类型。
func decodeWireType(wireType byte) (TrafficFrameType, error) {
	switch wireType {
	case trafficWireTypeOpen:
		return TrafficFrameOpen, nil
	case trafficWireTypeOpenAck:
		return TrafficFrameOpenAck, nil
	case trafficWireTypeData:
		return TrafficFrameData, nil
	case trafficWireTypeClose:
		return TrafficFrameClose, nil
	case trafficWireTypeReset:
		return TrafficFrameReset, nil
	default:
		return "", fmt.Errorf("%w: unknown wire type=%d", transport.ErrInvalidArgument, wireType)
	}
}

// encodePayload 按帧类型编码载荷。
func encodePayload(frame TrafficFrame) ([]byte, error) {
	switch frame.Type {
	case TrafficFrameOpen:
		return json.Marshal(frame.Open)
	case TrafficFrameOpenAck:
		return json.Marshal(frame.OpenAck)
	case TrafficFrameData:
		// Data 帧保持原始字节，不做额外 JSON 包装。
		return append([]byte(nil), frame.Data...), nil
	case TrafficFrameClose:
		return json.Marshal(frame.Close)
	case TrafficFrameReset:
		return json.Marshal(frame.Reset)
	default:
		return nil, fmt.Errorf("%w: unknown frame type=%q", transport.ErrInvalidArgument, frame.Type)
	}
}

// decodePayload 按帧类型解码载荷。
func decodePayload(frameType TrafficFrameType, payload []byte) (TrafficFrame, error) {
	switch frameType {
	case TrafficFrameOpen:
		var openPayload TrafficMeta
		if err := json.Unmarshal(payload, &openPayload); err != nil {
			return TrafficFrame{}, fmt.Errorf("decode open payload: %w", err)
		}
		return TrafficFrame{Type: TrafficFrameOpen, Open: &openPayload}, nil
	case TrafficFrameOpenAck:
		var openAckPayload TrafficOpenAck
		if err := json.Unmarshal(payload, &openAckPayload); err != nil {
			return TrafficFrame{}, fmt.Errorf("decode open_ack payload: %w", err)
		}
		return TrafficFrame{Type: TrafficFrameOpenAck, OpenAck: &openAckPayload}, nil
	case TrafficFrameData:
		return TrafficFrame{Type: TrafficFrameData, Data: append([]byte(nil), payload...)}, nil
	case TrafficFrameClose:
		var closePayload TrafficClose
		if err := json.Unmarshal(payload, &closePayload); err != nil {
			return TrafficFrame{}, fmt.Errorf("decode close payload: %w", err)
		}
		return TrafficFrame{Type: TrafficFrameClose, Close: &closePayload}, nil
	case TrafficFrameReset:
		var resetPayload TrafficReset
		if err := json.Unmarshal(payload, &resetPayload); err != nil {
			return TrafficFrame{}, fmt.Errorf("decode reset payload: %w", err)
		}
		return TrafficFrame{Type: TrafficFrameReset, Reset: &resetPayload}, nil
	default:
		return TrafficFrame{}, fmt.Errorf("%w: unknown frame type=%q", transport.ErrInvalidArgument, frameType)
	}
}

// writeFull 将 payload 全量写入 writer。
func writeFull(writer io.Writer, payload []byte) error {
	remaining := payload
	for len(remaining) > 0 {
		writtenSize, err := writer.Write(remaining)
		if err != nil {
			return err
		}
		if writtenSize <= 0 {
			return io.ErrShortWrite
		}
		// 递减已写入部分，继续写剩余字节。
		remaining = remaining[writtenSize:]
	}
	return nil
}

// readFull 从 reader 读取固定长度字节。
func readFull(reader io.Reader, payload []byte) error {
	_, err := io.ReadFull(reader, payload)
	if err == nil {
		return nil
	}
	if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		// 帧读取不足时统一归类为 tunnel 关闭类错误，便于上层收敛处理。
		return fmt.Errorf("%w: %w", transport.ErrTunnelClosed, err)
	}
	return err
}
