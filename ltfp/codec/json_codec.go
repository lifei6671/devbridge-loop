package codec

import (
	"encoding/json"
	"fmt"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// JSONCodec 提供 LTFP 协议对象的 JSON 编解码能力。
type JSONCodec struct{}

// NewJSONCodec 创建 JSON 编解码器实例。
func NewJSONCodec() *JSONCodec {
	// 当前无状态实现返回零值实例即可。
	return &JSONCodec{}
}

// EncodeControlEnvelope 将控制面封装编码为 JSON 字节。
func (codec *JSONCodec) EncodeControlEnvelope(envelope pb.ControlEnvelope) ([]byte, error) {
	// 先校验消息类型，防止未知类型进入链路。
	if !pb.IsKnownControlMessageType(envelope.MessageType) {
		return nil, ltfperrors.New(ltfperrors.CodeUnknownMessageType, fmt.Sprintf("unknown control message type: %s", envelope.MessageType))
	}
	encoded, err := json.Marshal(envelope)
	if err != nil {
		// 编码失败时保留底层错误，便于排查字段结构问题。
		return nil, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "marshal control envelope failed", err)
	}
	return encoded, nil
}

// DecodeControlEnvelope 将 JSON 字节解码为控制面封装对象。
func (codec *JSONCodec) DecodeControlEnvelope(raw []byte) (pb.ControlEnvelope, error) {
	var envelope pb.ControlEnvelope
	if err := json.Unmarshal(raw, &envelope); err != nil {
		// 解码失败说明对端负载格式不合法。
		return pb.ControlEnvelope{}, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "unmarshal control envelope failed", err)
	}
	// 解码后继续校验消息类型，防止脏数据流入后续逻辑。
	if !pb.IsKnownControlMessageType(envelope.MessageType) {
		return pb.ControlEnvelope{}, ltfperrors.New(ltfperrors.CodeUnknownMessageType, fmt.Sprintf("unknown control message type: %s", envelope.MessageType))
	}
	return envelope, nil
}

// EncodeStreamPayload 将数据面 oneof 结构编码为 JSON 字节。
func (codec *JSONCodec) EncodeStreamPayload(payload pb.StreamPayload) ([]byte, error) {
	// oneof 语义必须保证只有一个字段生效。
	if payload.ActivePayloadCount() != 1 {
		return nil, ltfperrors.New(ltfperrors.CodeTrafficInvalidOneof, "stream payload must contain exactly one active field")
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 编码失败时使用统一错误码，便于调用方兜底处理。
		return nil, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "marshal stream payload failed", err)
	}
	return encoded, nil
}

// DecodeStreamPayload 将 JSON 字节解码为数据面 oneof 结构。
func (codec *JSONCodec) DecodeStreamPayload(raw []byte) (pb.StreamPayload, error) {
	var payload pb.StreamPayload
	if err := json.Unmarshal(raw, &payload); err != nil {
		// 解码错误直接视为非法 payload。
		return pb.StreamPayload{}, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "unmarshal stream payload failed", err)
	}
	// 解码后再次校验 oneof 语义，避免运行期歧义。
	if payload.ActivePayloadCount() != 1 {
		return pb.StreamPayload{}, ltfperrors.New(ltfperrors.CodeTrafficInvalidOneof, "stream payload must contain exactly one active field")
	}
	return payload, nil
}

// EncodePayload 将任意协议对象编码为可嵌入 envelope 的 RawMessage。
func (codec *JSONCodec) EncodePayload(payload any) (json.RawMessage, error) {
	encoded, err := json.Marshal(payload)
	if err != nil {
		// 保留原始错误便于排查字段类型不匹配问题。
		return nil, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "marshal payload failed", err)
	}
	return json.RawMessage(encoded), nil
}

// DecodePayload 将 envelope 的 RawMessage 解码为目标对象。
func (codec *JSONCodec) DecodePayload(raw json.RawMessage, out any) error {
	if len(raw) == 0 {
		// 空 payload 明确返回缺失错误，避免上层隐式零值。
		return ltfperrors.New(ltfperrors.CodeMissingRequiredField, "payload is required")
	}
	if err := json.Unmarshal(raw, out); err != nil {
		// 解码失败时包装错误，确保调用侧可按错误码分类。
		return ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "unmarshal payload failed", err)
	}
	return nil
}
