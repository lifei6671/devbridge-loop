package directproxy

import (
	"errors"
	"fmt"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

const (
	// FailureCodeExternalProxyFailed 表示 external path 通用失败。
	FailureCodeExternalProxyFailed = "EXTERNAL_PROXY_FAILED"
)

const (
	// FailureKindDiscoveryMiss 表示 cache miss 后 discovery 失败。
	FailureKindDiscoveryMiss FailureKind = "discovery_miss"
	// FailureKindDiscoveryRefresh 表示 stale 刷新失败。
	FailureKindDiscoveryRefresh FailureKind = "discovery_refresh"
	// FailureKindDirectDial 表示 direct dial 失败。
	FailureKindDirectDial FailureKind = "direct_dial"
	// FailureKindRelay 表示 relay 失败。
	FailureKindRelay FailureKind = "relay"
	// FailureKindClose 表示上游 close 失败。
	FailureKindClose FailureKind = "close"
)

// FailureKind 描述 external path 的失败阶段。
type FailureKind string

// MappedFailure 描述 direct path 失败映射结果。
type MappedFailure struct {
	HTTPStatus int
	Code       string
	Message    string
}

// FailureMapper maps direct proxy errors to client responses.
type FailureMapper struct{}

// NewFailureMapper 创建 direct path 默认失败映射器。
func NewFailureMapper() *FailureMapper {
	return &FailureMapper{}
}

// Map 把 external path 错误映射为标准响应语义。
func (mapper *FailureMapper) Map(err error, kind FailureKind) MappedFailure {
	_ = mapper
	mapped := MappedFailure{
		HTTPStatus: 502,
		Code:       FailureCodeExternalProxyFailed,
	}
	if err == nil {
		return mapped
	}
	mapped.Message = err.Error()

	switch kind {
	case FailureKindDiscoveryMiss:
		return mapDiscoveryFailure(err, mapped)
	case FailureKindDiscoveryRefresh:
		mapped.HTTPStatus = 503
		mapped.Code = ltfperrors.CodeDiscoveryRefreshFailed
		return mapped
	case FailureKindDirectDial:
		mapped.HTTPStatus = 502
		mapped.Code = ltfperrors.CodeDirectProxyDialFailed
		return mapped
	case FailureKindRelay, FailureKindClose:
		mapped.HTTPStatus = 502
		mapped.Code = ltfperrors.CodeDirectProxyRelayFailed
		return mapped
	}

	if errors.Is(err, ErrDiscoveryNoEndpoint) {
		mapped.HTTPStatus = 503
		mapped.Code = ltfperrors.CodeDiscoveryNoEndpoint
		return mapped
	}
	if errors.Is(err, ErrDirectDialFailed) {
		mapped.HTTPStatus = 502
		mapped.Code = ltfperrors.CodeDirectProxyDialFailed
		return mapped
	}
	return mapped
}

func mapDiscoveryFailure(err error, mapped MappedFailure) MappedFailure {
	switch code := ltfperrors.ExtractCode(err); code {
	case ltfperrors.CodeDiscoveryNoEndpoint, ltfperrors.CodeDiscoveryProviderUnavailable:
		mapped.HTTPStatus = 503
		mapped.Code = code
		return mapped
	case ltfperrors.CodeDiscoveryProviderNotAllowed,
		ltfperrors.CodeDiscoveryNamespaceNotAllowed,
		ltfperrors.CodeDiscoveryServiceNotAllowed,
		ltfperrors.CodeDiscoveryEndpointDenied:
		mapped.HTTPStatus = 403
		mapped.Code = code
		return mapped
	case ltfperrors.CodeMissingRequiredField,
		ltfperrors.CodeInvalidPayload,
		ltfperrors.CodeInvalidScope,
		ltfperrors.CodeUnsupportedValue:
		mapped.HTTPStatus = 400
		mapped.Code = code
		return mapped
	}
	if errors.Is(err, ErrDiscoveryNoEndpoint) {
		mapped.HTTPStatus = 503
		mapped.Code = ltfperrors.CodeDiscoveryNoEndpoint
		return mapped
	}
	mapped.HTTPStatus = 503
	mapped.Code = ltfperrors.CodeDiscoveryProviderUnavailable
	return mapped
}

// Error 返回结构化失败文本，便于日志输出。
func (mappedFailure MappedFailure) Error() string {
	return fmt.Sprintf("http=%d code=%s message=%s", mappedFailure.HTTPStatus, mappedFailure.Code, mappedFailure.Message)
}
