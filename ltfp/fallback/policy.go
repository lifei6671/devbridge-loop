package fallback

import (
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Signal 表示 hybrid fallback 判定信号。
type Signal string

const (
	// SignalResolveMiss 表示 route resolve miss。
	SignalResolveMiss Signal = "resolve_miss"
	// SignalServiceUnavailable 表示 connector service 不可用。
	SignalServiceUnavailable Signal = "service_unavailable"
	// SignalOpenAckFail 表示 TrafficOpenAck 失败。
	SignalOpenAckFail Signal = "open_ack_fail"
	// SignalPreOpenTimeout 表示 pre-open 阶段超时。
	SignalPreOpenTimeout Signal = "pre_open_timeout"
	// SignalOpenAckSuccess 表示收到 TrafficOpenAck success。
	SignalOpenAckSuccess Signal = "open_ack_success"
	// SignalWroteUpstreamBytes 表示已写出业务字节。
	SignalWroteUpstreamBytes Signal = "wrote_upstream_bytes"
	// SignalPartialResponse 表示已发送部分响应。
	SignalPartialResponse Signal = "partial_response"
	// SignalMidStreamReset 表示 mid-stream reset。
	SignalMidStreamReset Signal = "mid_stream_reset"
	// SignalPostOpenFailure 表示 post-open 阶段失败。
	SignalPostOpenFailure Signal = "post_open_failure"
)

// CanFallback 判断给定信号下是否允许 fallback。
func CanFallback(policy pb.FallbackPolicy, signal Signal) (bool, error) {
	// 当前协议仅支持 pre_open_only。
	if policy != pb.FallbackPolicyPreOpenOnly {
		return false, ltfperrors.New(ltfperrors.CodeUnsupportedValue, "unsupported fallback policy")
	}
	switch strings.TrimSpace(string(signal)) {
	case string(SignalResolveMiss), string(SignalServiceUnavailable), string(SignalOpenAckFail), string(SignalPreOpenTimeout):
		// pre-open 阶段失败允许 fallback。
		return true, nil
	case string(SignalOpenAckSuccess), string(SignalWroteUpstreamBytes), string(SignalPartialResponse), string(SignalMidStreamReset), string(SignalPostOpenFailure):
		// post-open 或成功打开后场景禁止 fallback。
		return false, ltfperrors.New(ltfperrors.CodeHybridFallbackForbidden, "fallback is forbidden after pre-open phase")
	default:
		return false, ltfperrors.New(ltfperrors.CodeUnsupportedValue, "unknown fallback signal")
	}
}
