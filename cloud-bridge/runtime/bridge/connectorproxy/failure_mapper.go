package connectorproxy

import (
	"errors"
	"fmt"
)

const (
	// FailureCodeNoIdleTunnel 表示分配 idle tunnel 超时失败。
	FailureCodeNoIdleTunnel = "CONNECTOR_NO_IDLE_TUNNEL"
	// FailureCodeOpenRejected 表示 agent 显式拒绝 open。
	FailureCodeOpenRejected = "CONNECTOR_OPEN_REJECTED"
	// FailureCodeOpenAckTimeout 表示 open_ack 超时。
	FailureCodeOpenAckTimeout = "CONNECTOR_OPEN_ACK_TIMEOUT"
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

// Error 返回结构化错误文本，便于日志输出。
func (mappedFailure MappedFailure) Error() string {
	return fmt.Sprintf("http=%d code=%s message=%s", mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message)
}
