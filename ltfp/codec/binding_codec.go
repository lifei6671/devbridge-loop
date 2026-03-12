package codec

import (
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TransportBinding 表示当前消息承载使用的 tunnel binding 类型。
type TransportBinding string

const (
	// TransportBindingHTTP 表示通过 HTTP tunnel 承载协议消息。
	TransportBindingHTTP TransportBinding = "http"
	// TransportBindingMASQUE 表示通过 MASQUE tunnel 承载协议消息。
	TransportBindingMASQUE TransportBinding = "masque"
)

// IsSupportedTransportBinding 判断 binding 是否受当前编解码器支持。
func IsSupportedTransportBinding(binding TransportBinding) bool {
	normalized := TransportBinding(strings.TrimSpace(strings.ToLower(string(binding))))
	// 目前只声明支持 http/masque 两种现网 binding。
	return normalized == TransportBindingHTTP || normalized == TransportBindingMASQUE
}

// EncodeControlEnvelopeForBinding 按指定 binding 编码控制面消息。
func EncodeControlEnvelopeForBinding(binding TransportBinding, envelope pb.ControlEnvelope) ([]byte, error) {
	// binding 非法时直接拒绝，避免上层传入未支持承载协议。
	if !IsSupportedTransportBinding(binding) {
		return nil, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported transport binding: %s", binding))
	}
	codec := NewJSONCodec()
	// 现阶段 http/masque 共用 JSON 负载语义，仅承载层不同。
	return codec.EncodeControlEnvelope(envelope)
}

// DecodeControlEnvelopeForBinding 按指定 binding 解码控制面消息。
func DecodeControlEnvelopeForBinding(binding TransportBinding, raw []byte) (pb.ControlEnvelope, error) {
	// binding 非法时直接拒绝，避免上层传入未支持承载协议。
	if !IsSupportedTransportBinding(binding) {
		return pb.ControlEnvelope{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported transport binding: %s", binding))
	}
	codec := NewJSONCodec()
	// 现阶段 http/masque 共用 JSON 负载语义，仅承载层不同。
	return codec.DecodeControlEnvelope(raw)
}

// EncodeStreamPayloadForBinding 按指定 binding 编码数据面消息。
func EncodeStreamPayloadForBinding(binding TransportBinding, payload pb.StreamPayload) ([]byte, error) {
	// binding 非法时直接拒绝，避免上层传入未支持承载协议。
	if !IsSupportedTransportBinding(binding) {
		return nil, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported transport binding: %s", binding))
	}
	codec := NewJSONCodec()
	// 现阶段 http/masque 共用 JSON 负载语义，仅承载层不同。
	return codec.EncodeStreamPayload(payload)
}

// DecodeStreamPayloadForBinding 按指定 binding 解码数据面消息。
func DecodeStreamPayloadForBinding(binding TransportBinding, raw []byte) (pb.StreamPayload, error) {
	// binding 非法时直接拒绝，避免上层传入未支持承载协议。
	if !IsSupportedTransportBinding(binding) {
		return pb.StreamPayload{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported transport binding: %s", binding))
	}
	codec := NewJSONCodec()
	// 现阶段 http/masque 共用 JSON 负载语义，仅承载层不同。
	return codec.DecodeStreamPayload(raw)
}
