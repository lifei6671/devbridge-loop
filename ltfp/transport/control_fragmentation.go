package transport

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

const (
	// ControlFrameTypeFragment 是控制面分块帧的保留类型。
	ControlFrameTypeFragment uint16 = 0xFFFF

	// DefaultControlFragmentMaxPayloadSize 是控制面分块的默认单帧上限。
	DefaultControlFragmentMaxPayloadSize = 64 * 1024

	controlFragmentVersion              = 1
	controlFragmentHeaderSize           = 24
	defaultControlFragmentReassemblyTTL = 30 * time.Second
	defaultControlFragmentMaxInFlight   = 256
)

var controlFragmentMagic = [4]byte{'L', 'T', 'F', 'P'}

// ControlFragmentationConfig 描述控制面大消息分块与重组配置。
type ControlFragmentationConfig struct {
	MaxPayloadSize     int
	ReassemblyTTL      time.Duration
	MaxInFlightMessage int
}

// NormalizeAndValidate 归一化并校验控制面分块配置。
func (config ControlFragmentationConfig) NormalizeAndValidate() (ControlFragmentationConfig, error) {
	normalizedConfig := config
	if normalizedConfig.MaxPayloadSize <= 0 {
		// 未显式配置时使用保守默认值，避免单条控制消息过大。
		normalizedConfig.MaxPayloadSize = DefaultControlFragmentMaxPayloadSize
	}
	if normalizedConfig.MaxPayloadSize <= controlFragmentHeaderSize {
		// 单帧容量必须大于头部开销，否则无法承载任何有效载荷。
		return ControlFragmentationConfig{}, fmt.Errorf(
			"normalize control fragmentation config: %w: max_payload_size=%d",
			ErrInvalidArgument,
			normalizedConfig.MaxPayloadSize,
		)
	}
	if normalizedConfig.ReassemblyTTL <= 0 {
		// 重组超时未配置时给出默认值，避免半包缓存无限滞留。
		normalizedConfig.ReassemblyTTL = defaultControlFragmentReassemblyTTL
	}
	if normalizedConfig.MaxInFlightMessage <= 0 {
		// 限制同时重组中的大消息数量，避免异常流量占满内存。
		normalizedConfig.MaxInFlightMessage = defaultControlFragmentMaxInFlight
	}
	return normalizedConfig, nil
}

// DefaultControlFragmentationConfig 返回默认控制面分块配置。
func DefaultControlFragmentationConfig() ControlFragmentationConfig {
	return ControlFragmentationConfig{
		MaxPayloadSize:     DefaultControlFragmentMaxPayloadSize,
		ReassemblyTTL:      defaultControlFragmentReassemblyTTL,
		MaxInFlightMessage: defaultControlFragmentMaxInFlight,
	}
}

// ControlFrameFragmenter 负责把大控制帧拆分成多个分块帧。
type ControlFrameFragmenter struct {
	config        ControlFragmentationConfig
	nextMessageID atomic.Uint64
}

// NewControlFrameFragmenter 创建控制面分块器。
func NewControlFrameFragmenter(config ControlFragmentationConfig) (*ControlFrameFragmenter, error) {
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &ControlFrameFragmenter{
		config: normalizedConfig,
	}, nil
}

// Fragment 按配置把控制帧拆成一个或多个分块帧。
func (fragmenter *ControlFrameFragmenter) Fragment(frame ControlFrame) ([]ControlFrame, error) {
	if fragmenter == nil {
		return nil, fmt.Errorf("fragment control frame: %w: nil fragmenter", ErrInvalidArgument)
	}
	if frame.Type == ControlFrameTypeFragment {
		// 保留帧类型只允许由分块器自身生成，避免业务侧伪造。
		return nil, fmt.Errorf("fragment control frame: %w: reserved frame_type=%d", ErrInvalidArgument, frame.Type)
	}
	if len(frame.Payload) <= fragmenter.config.MaxPayloadSize {
		// 小消息保持单帧发送，只做一次拷贝避免外部后续修改切片。
		return []ControlFrame{cloneControlFrame(frame)}, nil
	}

	maxFragmentPayloadSize := fragmenter.config.MaxPayloadSize - controlFragmentHeaderSize
	fragmentCount := (len(frame.Payload) + maxFragmentPayloadSize - 1) / maxFragmentPayloadSize
	if fragmentCount > int(^uint16(0)) {
		// 分块数超过 uint16 会破坏头部格式，直接拒绝。
		return nil, fmt.Errorf("fragment control frame: %w: fragment_count=%d", ErrInvalidArgument, fragmentCount)
	}
	if len(frame.Payload) > int(^uint32(0)) {
		// 总长度字段固定为 uint32，超过上限时不给出隐式截断。
		return nil, fmt.Errorf("fragment control frame: %w: payload_size=%d", ErrInvalidArgument, len(frame.Payload))
	}

	messageID := fragmenter.nextMessageID.Add(1)
	fragmentedFrames := make([]ControlFrame, 0, fragmentCount)
	for fragmentIndex := 0; fragmentIndex < fragmentCount; fragmentIndex++ {
		chunkOffset := fragmentIndex * maxFragmentPayloadSize
		chunkEnd := chunkOffset + maxFragmentPayloadSize
		if chunkEnd > len(frame.Payload) {
			chunkEnd = len(frame.Payload)
		}
		chunkPayload := frame.Payload[chunkOffset:chunkEnd]
		header := controlFragmentHeader{
			OriginalType:     frame.Type,
			MessageID:        messageID,
			FragmentIndex:    uint16(fragmentIndex),
			FragmentCount:    uint16(fragmentCount),
			TotalPayloadSize: uint32(len(frame.Payload)),
		}
		fragmentedFrames = append(fragmentedFrames, ControlFrame{
			Type:    ControlFrameTypeFragment,
			Payload: encodeControlFragment(header, chunkPayload),
		})
	}
	return fragmentedFrames, nil
}

// ControlFrameReassembler 负责把分块控制帧重组成原始控制帧。
type ControlFrameReassembler struct {
	config ControlFragmentationConfig

	mutex      sync.Mutex
	assemblies map[uint64]*controlFragmentAssembly
}

// NewControlFrameReassembler 创建控制面分块重组器。
func NewControlFrameReassembler(config ControlFragmentationConfig) (*ControlFrameReassembler, error) {
	normalizedConfig, err := config.NormalizeAndValidate()
	if err != nil {
		return nil, err
	}
	return &ControlFrameReassembler{
		config:     normalizedConfig,
		assemblies: make(map[uint64]*controlFragmentAssembly),
	}, nil
}

// Reassemble 尝试把单帧或分块帧重组为原始控制帧。
func (reassembler *ControlFrameReassembler) Reassemble(frame ControlFrame) (ControlFrame, bool, error) {
	return reassembler.reassembleAt(frame, time.Now().UTC())
}

// reassembleAt 在指定时间点执行重组，便于测试 TTL 与超时路径。
func (reassembler *ControlFrameReassembler) reassembleAt(
	frame ControlFrame,
	now time.Time,
) (ControlFrame, bool, error) {
	if reassembler == nil {
		return ControlFrame{}, false, fmt.Errorf("reassemble control frame: %w: nil reassembler", ErrInvalidArgument)
	}
	if frame.Type != ControlFrameTypeFragment {
		// 非分块帧直接透传，保持调用方处理逻辑简单。
		return cloneControlFrame(frame), true, nil
	}

	header, fragmentPayload, err := decodeControlFragment(frame.Payload)
	if err != nil {
		return ControlFrame{}, false, fmt.Errorf("reassemble control frame: %w", err)
	}
	if now.IsZero() {
		// 测试或调用方未传时间时统一回落到当前 UTC。
		now = time.Now().UTC()
	}

	reassembler.mutex.Lock()
	defer reassembler.mutex.Unlock()
	reassembler.cleanupExpiredLocked(now)

	assembly, err := reassembler.ensureAssemblyLocked(header, now)
	if err != nil {
		return ControlFrame{}, false, err
	}
	if err := assembly.addFragment(header, fragmentPayload, now); err != nil {
		delete(reassembler.assemblies, header.MessageID)
		return ControlFrame{}, false, fmt.Errorf("reassemble control frame: %w", err)
	}
	if !assembly.complete() {
		// 仍有分块未到齐时返回 ready=false，让上层继续读后续帧。
		return ControlFrame{}, false, nil
	}

	delete(reassembler.assemblies, header.MessageID)
	reassembledPayload, err := assembly.payload()
	if err != nil {
		return ControlFrame{}, false, fmt.Errorf("reassemble control frame: %w", err)
	}
	return ControlFrame{
		Type:    assembly.originalType,
		Payload: reassembledPayload,
	}, true, nil
}

// ensureAssemblyLocked 获取或创建指定 message_id 对应的重组上下文。
func (reassembler *ControlFrameReassembler) ensureAssemblyLocked(
	header controlFragmentHeader,
	now time.Time,
) (*controlFragmentAssembly, error) {
	if assembly, exists := reassembler.assemblies[header.MessageID]; exists {
		// 已存在上下文时沿用，支持同一控制通道上分块与高优消息交错到达。
		return assembly, nil
	}
	if len(reassembler.assemblies) >= reassembler.config.MaxInFlightMessage {
		// 同时在组装的大消息过多时直接拒绝，避免异常端撑爆内存。
		return nil, fmt.Errorf(
			"reassemble control frame: %w: too many inflight fragment messages=%d",
			ErrInvalidArgument,
			len(reassembler.assemblies),
		)
	}
	maxFragmentPayloadSize := reassembler.config.MaxPayloadSize - controlFragmentHeaderSize
	maxPossibleTotalPayloadSize := uint64(header.FragmentCount) * uint64(maxFragmentPayloadSize)
	if uint64(header.TotalPayloadSize) > maxPossibleTotalPayloadSize {
		return nil, fmt.Errorf(
			"reassemble control frame: %w: total_payload_size=%d exceeds_max=%d fragment_count=%d",
			ErrInvalidArgument,
			header.TotalPayloadSize,
			maxPossibleTotalPayloadSize,
			header.FragmentCount,
		)
	}
	assembly := &controlFragmentAssembly{
		originalType:     header.OriginalType,
		totalPayloadSize: header.TotalPayloadSize,
		fragments:        make([][]byte, header.FragmentCount),
		lastUpdatedAt:    now,
	}
	reassembler.assemblies[header.MessageID] = assembly
	return assembly, nil
}

// cleanupExpiredLocked 清理长时间未完成的半包消息。
func (reassembler *ControlFrameReassembler) cleanupExpiredLocked(now time.Time) {
	expiredBefore := now.Add(-reassembler.config.ReassemblyTTL)
	for messageID, assembly := range reassembler.assemblies {
		if assembly.lastUpdatedAt.Before(expiredBefore) {
			// 超时未完成的重组上下文直接丢弃，避免后续混入旧包。
			delete(reassembler.assemblies, messageID)
		}
	}
}

// controlFragmentHeader 描述控制面分块头部。
type controlFragmentHeader struct {
	OriginalType     uint16
	MessageID        uint64
	FragmentIndex    uint16
	FragmentCount    uint16
	TotalPayloadSize uint32
}

// encodeControlFragment 将头部和分块数据编码成单个控制帧负载。
func encodeControlFragment(header controlFragmentHeader, fragmentPayload []byte) []byte {
	encodedPayload := make([]byte, controlFragmentHeaderSize+len(fragmentPayload))
	copy(encodedPayload[0:4], controlFragmentMagic[:])
	encodedPayload[4] = controlFragmentVersion
	encodedPayload[5] = 0
	binary.BigEndian.PutUint16(encodedPayload[6:8], header.OriginalType)
	binary.BigEndian.PutUint16(encodedPayload[8:10], header.FragmentCount)
	binary.BigEndian.PutUint16(encodedPayload[10:12], header.FragmentIndex)
	binary.BigEndian.PutUint64(encodedPayload[12:20], header.MessageID)
	binary.BigEndian.PutUint32(encodedPayload[20:24], header.TotalPayloadSize)
	copy(encodedPayload[24:], fragmentPayload)
	return encodedPayload
}

// decodeControlFragment 解析单个控制面分块帧负载。
func decodeControlFragment(encodedPayload []byte) (controlFragmentHeader, []byte, error) {
	if len(encodedPayload) < controlFragmentHeaderSize {
		// 头部不完整时直接拒绝，防止后续读取越界。
		return controlFragmentHeader{}, nil, fmt.Errorf(
			"decode control fragment: %w: payload_size=%d",
			ErrInvalidArgument,
			len(encodedPayload),
		)
	}
	if !bytes.Equal(encodedPayload[0:4], controlFragmentMagic[:]) {
		// magic 不匹配说明该帧不是 transport 约定的分块格式。
		return controlFragmentHeader{}, nil, fmt.Errorf("decode control fragment: %w: invalid magic", ErrInvalidArgument)
	}
	if encodedPayload[4] != controlFragmentVersion {
		// 版本不兼容时拒绝继续解析，避免静默读错头部。
		return controlFragmentHeader{}, nil, fmt.Errorf(
			"decode control fragment: %w: version=%d",
			ErrInvalidArgument,
			encodedPayload[4],
		)
	}

	header := controlFragmentHeader{
		OriginalType:     binary.BigEndian.Uint16(encodedPayload[6:8]),
		FragmentCount:    binary.BigEndian.Uint16(encodedPayload[8:10]),
		FragmentIndex:    binary.BigEndian.Uint16(encodedPayload[10:12]),
		MessageID:        binary.BigEndian.Uint64(encodedPayload[12:20]),
		TotalPayloadSize: binary.BigEndian.Uint32(encodedPayload[20:24]),
	}
	if header.OriginalType == ControlFrameTypeFragment {
		// 禁止嵌套分块，避免无限递归重组。
		return controlFragmentHeader{}, nil, fmt.Errorf(
			"decode control fragment: %w: original_type=%d",
			ErrInvalidArgument,
			header.OriginalType,
		)
	}
	if header.FragmentCount == 0 {
		// 分块总数至少为 1，0 说明头部非法。
		return controlFragmentHeader{}, nil, fmt.Errorf("decode control fragment: %w: fragment_count=0", ErrInvalidArgument)
	}
	if header.FragmentIndex >= header.FragmentCount {
		// 下标越界会破坏重组顺序，必须显式拒绝。
		return controlFragmentHeader{}, nil, fmt.Errorf(
			"decode control fragment: %w: fragment_index=%d fragment_count=%d",
			ErrInvalidArgument,
			header.FragmentIndex,
			header.FragmentCount,
		)
	}
	if header.TotalPayloadSize == 0 {
		// 控制面大消息分块不应产生空总载荷，避免无意义状态。
		return controlFragmentHeader{}, nil, fmt.Errorf("decode control fragment: %w: total_payload_size=0", ErrInvalidArgument)
	}
	return header, append([]byte(nil), encodedPayload[controlFragmentHeaderSize:]...), nil
}

// controlFragmentAssembly 维护单条大消息的重组过程。
type controlFragmentAssembly struct {
	originalType     uint16
	totalPayloadSize uint32
	fragments        [][]byte
	receivedCount    int
	receivedBytes    int
	lastUpdatedAt    time.Time
}

// addFragment 把一个分块写入当前重组上下文。
func (assembly *controlFragmentAssembly) addFragment(
	header controlFragmentHeader,
	fragmentPayload []byte,
	now time.Time,
) error {
	if assembly == nil {
		return fmt.Errorf("add control fragment: %w: nil assembly", ErrInvalidArgument)
	}
	if assembly.originalType != header.OriginalType {
		// 同一个 message_id 不允许混入不同原始 frame_type。
		return fmt.Errorf(
			"add control fragment: %w: original_type=%d expected=%d",
			ErrInvalidArgument,
			header.OriginalType,
			assembly.originalType,
		)
	}
	if len(assembly.fragments) != int(header.FragmentCount) {
		// 同一条大消息的 fragment_count 必须保持一致。
		return fmt.Errorf(
			"add control fragment: %w: fragment_count=%d expected=%d",
			ErrInvalidArgument,
			header.FragmentCount,
			len(assembly.fragments),
		)
	}
	if assembly.totalPayloadSize != header.TotalPayloadSize {
		// 同一 message_id 的 total_payload_size 必须一致，避免拼包越界。
		return fmt.Errorf(
			"add control fragment: %w: total_payload_size=%d expected=%d",
			ErrInvalidArgument,
			header.TotalPayloadSize,
			assembly.totalPayloadSize,
		)
	}

	existingFragmentPayload := assembly.fragments[header.FragmentIndex]
	if existingFragmentPayload != nil {
		if bytes.Equal(existingFragmentPayload, fragmentPayload) {
			// 重复分块内容完全一致时按幂等处理，不重复累计长度。
			assembly.lastUpdatedAt = now
			return nil
		}
		return fmt.Errorf(
			"add control fragment: %w: duplicate fragment_index=%d with different payload",
			ErrInvalidArgument,
			header.FragmentIndex,
		)
	}
	if uint32(assembly.receivedBytes+len(fragmentPayload)) > assembly.totalPayloadSize {
		// 总字节数超出头部声明时直接终止该消息组装。
		return fmt.Errorf(
			"add control fragment: %w: payload overflow received=%d total=%d",
			ErrInvalidArgument,
			assembly.receivedBytes+len(fragmentPayload),
			assembly.totalPayloadSize,
		)
	}

	assembly.fragments[header.FragmentIndex] = append([]byte(nil), fragmentPayload...)
	assembly.receivedCount++
	assembly.receivedBytes += len(fragmentPayload)
	assembly.lastUpdatedAt = now
	return nil
}

// complete 判断当前大消息是否已经收齐全部分块。
func (assembly *controlFragmentAssembly) complete() bool {
	if assembly == nil {
		return false
	}
	return assembly.receivedCount == len(assembly.fragments)
}

// payload 在全部分块到齐后按顺序拼出完整负载。
func (assembly *controlFragmentAssembly) payload() ([]byte, error) {
	if assembly == nil {
		return nil, fmt.Errorf("control fragment payload: %w: nil assembly", ErrInvalidArgument)
	}
	if !assembly.complete() {
		return nil, fmt.Errorf("control fragment payload: %w: incomplete assembly", ErrNotReady)
	}
	if assembly.receivedBytes != int(assembly.totalPayloadSize) {
		return nil, fmt.Errorf(
			"control fragment payload: %w: payload_size=%d expected=%d",
			ErrInvalidArgument,
			assembly.receivedBytes,
			assembly.totalPayloadSize,
		)
	}
	reassembledPayload := make([]byte, 0, int(assembly.totalPayloadSize))
	for fragmentIndex, fragmentPayload := range assembly.fragments {
		if fragmentPayload == nil {
			// 防御式检查，避免并发或异常输入导致拼包产生空洞。
			return nil, fmt.Errorf(
				"control fragment payload: %w: missing fragment_index=%d",
				ErrInvalidArgument,
				fragmentIndex,
			)
		}
		reassembledPayload = append(reassembledPayload, fragmentPayload...)
	}
	if len(reassembledPayload) != int(assembly.totalPayloadSize) {
		// 拼接结果与头部声明不一致时直接拒绝，避免静默截断。
		return nil, fmt.Errorf(
			"control fragment payload: %w: payload_size=%d expected=%d",
			ErrInvalidArgument,
			len(reassembledPayload),
			assembly.totalPayloadSize,
		)
	}
	return reassembledPayload, nil
}

// cloneControlFrame 返回控制帧的深拷贝，避免外部共享底层切片。
func cloneControlFrame(frame ControlFrame) ControlFrame {
	return ControlFrame{
		Type:    frame.Type,
		Payload: append([]byte(nil), frame.Payload...),
	}
}
