package app

import (
	"fmt"
	"net/http"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// ingressScopeNamespaceHeader 定义统一的 namespace 请求头名称。
	ingressScopeNamespaceHeader = "X-Bridge-Namespace"
	// ingressScopeEnvironmentHeader 定义统一的 environment 请求头名称。
	ingressScopeEnvironmentHeader = "X-Bridge-Environment"

	legacyIngressScopeNamespaceHeaderOne     = "X-DevBridge-Namespace"
	legacyIngressScopeNamespaceHeaderTwo     = "X-Namespace"
	legacyIngressScopeEnvironmentHeaderOne   = "X-DevBridge-Environment"
	legacyIngressScopeEnvironmentHeaderTwo   = "X-Environment"
	legacyIngressScopeEnvironmentHeaderThree = "X-Env"
)

// resolveIngressScope 从标准 header 解析请求 scope，缺失字段时回退默认值。
func resolveIngressScope(headers http.Header, defaultScope pb.Scope) (pb.Scope, error) {
	if hasLegacyScopeHeaders(headers) {
		return pb.Scope{}, ltfperrors.New(
			ltfperrors.CodeUnsupportedLegacyProtocol,
			fmt.Sprintf(
				"legacy scope headers are not supported, use %s and %s",
				ingressScopeNamespaceHeader,
				ingressScopeEnvironmentHeader,
			),
		)
	}
	requestScope := pb.Scope{
		Namespace:   strings.TrimSpace(headers.Get(ingressScopeNamespaceHeader)),
		Environment: strings.TrimSpace(headers.Get(ingressScopeEnvironmentHeader)),
	}
	if requestScope.Namespace == "" {
		requestScope.Namespace = strings.TrimSpace(defaultScope.Namespace)
	}
	if requestScope.Environment == "" {
		requestScope.Environment = strings.TrimSpace(defaultScope.Environment)
	}
	return requestScope, nil
}

func hasLegacyScopeHeaders(headers http.Header) bool {
	if headers == nil {
		return false
	}
	legacyHeaderNames := []string{
		legacyIngressScopeNamespaceHeaderOne,
		legacyIngressScopeNamespaceHeaderTwo,
		legacyIngressScopeEnvironmentHeaderOne,
		legacyIngressScopeEnvironmentHeaderTwo,
		legacyIngressScopeEnvironmentHeaderThree,
	}
	for _, headerName := range legacyHeaderNames {
		if strings.TrimSpace(headers.Get(headerName)) != "" {
			return true
		}
	}
	return false
}
