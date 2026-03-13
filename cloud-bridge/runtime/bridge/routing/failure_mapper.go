package routing

import (
	"errors"
	"fmt"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

const (
	// FailureCodePathExecutionFailed 表示路径执行通用失败。
	FailureCodePathExecutionFailed = "PATH_EXECUTION_FAILED"
)

// MappedFailure 描述路径执行失败映射结果。
type MappedFailure struct {
	HTTPStatus int
	Code       string
	Message    string
}

// FailureMapper maps routing errors to client responses.
type FailureMapper struct{}

// NewFailureMapper 创建路径失败映射器。
func NewFailureMapper() *FailureMapper {
	return &FailureMapper{}
}

// Map 把 resolver/path 错误映射成统一 HTTP + 错误码。
func (mapper *FailureMapper) Map(err error, result PathExecuteResult) MappedFailure {
	_ = mapper
	mapped := MappedFailure{
		HTTPStatus: 502,
		Code:       FailureCodePathExecutionFailed,
	}
	if err == nil {
		mapped.HTTPStatus = 200
		return mapped
	}
	mapped.Message = err.Error()

	if result.DirectResult != nil && result.DirectResult.ErrorCode != "" {
		mapped.HTTPStatus = normalizeHTTPStatus(result.DirectResult.HTTPStatus, 502)
		mapped.Code = result.DirectResult.ErrorCode
		return mapped
	}
	if result.ConnectorResult != nil && result.ConnectorResult.ErrorCode != "" {
		mapped.HTTPStatus = normalizeHTTPStatus(result.ConnectorResult.HTTPStatus, 502)
		mapped.Code = result.ConnectorResult.ErrorCode
		return mapped
	}

	switch code := ltfperrors.ExtractCode(err); code {
	case ltfperrors.CodeIngressRouteMismatch, ltfperrors.CodeResolveServiceNotFound:
		mapped.HTTPStatus = 404
		mapped.Code = code
		return mapped
	case ltfperrors.CodeResolveServiceUnavailable, ltfperrors.CodeResolveSessionNotActive,
		ltfperrors.CodeDiscoveryNoEndpoint, ltfperrors.CodeDiscoveryProviderUnavailable, ltfperrors.CodeDiscoveryRefreshFailed,
		ltfperrors.CodeHybridFallbackForbidden:
		mapped.HTTPStatus = 503
		mapped.Code = code
		return mapped
	case ltfperrors.CodeDirectProxyDialFailed, ltfperrors.CodeDirectProxyRelayFailed:
		mapped.HTTPStatus = 502
		mapped.Code = code
		return mapped
	case ltfperrors.CodeInvalidPayload, ltfperrors.CodeMissingRequiredField, ltfperrors.CodeInvalidScope, ltfperrors.CodeUnsupportedValue:
		mapped.HTTPStatus = 400
		mapped.Code = code
		return mapped
	}

	switch {
	case errors.Is(err, connectorproxy.ErrNoIdleTunnel):
		mapped.HTTPStatus = 503
		mapped.Code = connectorproxy.FailureCodeNoIdleTunnel
	case errors.Is(err, connectorproxy.ErrTrafficOpenRejected):
		mapped.HTTPStatus = 503
		mapped.Code = connectorproxy.FailureCodeOpenRejected
	case errors.Is(err, connectorproxy.ErrOpenAckTimeout):
		mapped.HTTPStatus = 504
		mapped.Code = connectorproxy.FailureCodeOpenAckTimeout
	case errors.Is(err, connectorproxy.ErrRelayReset):
		mapped.HTTPStatus = 502
		mapped.Code = connectorproxy.FailureCodeRelayFailed
	case errors.Is(err, directproxy.ErrDirectDialFailed):
		mapped.HTTPStatus = 502
		mapped.Code = ltfperrors.CodeDirectProxyDialFailed
	}
	return mapped
}

// Error 返回结构化失败文本，便于日志输出。
func (mappedFailure MappedFailure) Error() string {
	return fmt.Sprintf("http=%d code=%s message=%s", mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message)
}

func normalizeHTTPStatus(statusCode int, fallback int) int {
	if statusCode <= 0 {
		return fallback
	}
	return statusCode
}
