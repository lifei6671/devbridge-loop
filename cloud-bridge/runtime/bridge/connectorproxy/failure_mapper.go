package connectorproxy

import (
	"errors"
	"fmt"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

const (
	// FailureCodeNoIdleTunnel 表示分配 idle tunnel 超时失败。
	FailureCodeNoIdleTunnel = "CONNECTOR_NO_IDLE_TUNNEL"
	// FailureCodeOpenRejected 表示 agent 显式拒绝 open。
	FailureCodeOpenRejected = "CONNECTOR_OPEN_REJECTED"
	// FailureCodeOpenAckTimeout 表示 open_ack 超时。
	FailureCodeOpenAckTimeout = "CONNECTOR_OPEN_ACK_TIMEOUT"
	// FailureCodeDialFailed 表示 Agent 侧 upstream dial 失败。
	FailureCodeDialFailed = "CONNECTOR_DIAL_FAILED"
	// FailureCodeRelayFailed 表示 relay 过程失败。
	FailureCodeRelayFailed = "CONNECTOR_RELAY_FAILED"
	// FailureCodeConnectorProxyFailed 表示 connector path 通用失败。
	FailureCodeConnectorProxyFailed = "CONNECTOR_PROXY_FAILED"
)

// MappedFailure 描述失败映射结果。
type MappedFailure struct {
	HTTPStatus int
	Code       string
	Message    string
}

// FailureMapper maps connector errors to client responses.
type FailureMapper struct{}

// NewFailureMapper 创建默认失败映射器。
func NewFailureMapper() *FailureMapper {
	return &FailureMapper{}
}

// Map 把 connector path 错误映射为标准响应语义。
func (mapper *FailureMapper) Map(err error) MappedFailure {
	_ = mapper
	mapped := MappedFailure{
		HTTPStatus: 502,
		Code:       FailureCodeConnectorProxyFailed,
	}
	if err == nil {
		return mapped
	}
	mapped.Message = err.Error()
	switch {
	case errors.Is(err, ErrNoIdleTunnel):
		mapped.HTTPStatus = 503
		mapped.Code = FailureCodeNoIdleTunnel
	case isConnectorDialFailed(err):
		mapped.HTTPStatus = 502
		mapped.Code = FailureCodeDialFailed
	case errors.Is(err, ErrTrafficOpenRejected):
		mapped.HTTPStatus = 503
		mapped.Code = FailureCodeOpenRejected
	case errors.Is(err, ErrOpenAckTimeout):
		mapped.HTTPStatus = 504
		mapped.Code = FailureCodeOpenAckTimeout
	case errors.Is(err, ErrRelayReset):
		mapped.HTTPStatus = 502
		mapped.Code = FailureCodeRelayFailed
	}
	return mapped
}

func isConnectorDialFailed(err error) bool {
	var openRejectedError *OpenRejectedError
	if !errors.As(err, &openRejectedError) || openRejectedError == nil {
		return false
	}
	return openRejectedError.Ack.ErrorCode == ltfperrors.CodeConnectorDialFailed
}

// Error 返回结构化错误文本，便于日志输出。
func (mappedFailure MappedFailure) Error() string {
	return fmt.Sprintf("http=%d code=%s message=%s", mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message)
}
