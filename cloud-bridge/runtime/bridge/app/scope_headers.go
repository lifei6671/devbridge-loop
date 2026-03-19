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

type ingressScopeResolution struct {
	Scope                 pb.Scope
	HeaderScopeIsComplete bool
}

// resolveIngressScope 从标准 header 解析请求 scope，缺失字段时回退默认值。
func resolveIngressScope(headers http.Header, defaultScope pb.Scope) (ingressScopeResolution, error) {
	if hasLegacyScopeHeaders(headers) {
		return ingressScopeResolution{}, ltfperrors.New(
			ltfperrors.CodeUnsupportedLegacyProtocol,
			fmt.Sprintf(
				"legacy scope headers are not supported, use %s and %s",
				ingressScopeNamespaceHeader,
				ingressScopeEnvironmentHeader,
			),
		)
	}
	namespaceHeaderValue := strings.TrimSpace(headers.Get(ingressScopeNamespaceHeader))
	environmentHeaderValue := strings.TrimSpace(headers.Get(ingressScopeEnvironmentHeader))
	requestScope := pb.Scope{
		Namespace:   namespaceHeaderValue,
		Environment: environmentHeaderValue,
	}
	if requestScope.Namespace == "" {
		requestScope.Namespace = strings.TrimSpace(defaultScope.Namespace)
	}
	if requestScope.Environment == "" {
		requestScope.Environment = strings.TrimSpace(defaultScope.Environment)
	}
	return ingressScopeResolution{
		Scope:                 requestScope,
		HeaderScopeIsComplete: namespaceHeaderValue != "" && environmentHeaderValue != "",
	}, nil
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

// applyIngressScopeHeaders 使用解析后的有效 scope 回写标准请求头，确保下游看到一致作用域。
func applyIngressScopeHeaders(headers http.Header, scope pb.Scope) {
	if headers == nil {
		return
	}
	normalizedNamespace := strings.TrimSpace(scope.Namespace)
	normalizedEnvironment := strings.TrimSpace(scope.Environment)
	if normalizedNamespace == "" {
		headers.Del(ingressScopeNamespaceHeader)
	} else {
		headers.Set(ingressScopeNamespaceHeader, normalizedNamespace)
	}
	if normalizedEnvironment == "" {
		headers.Del(ingressScopeEnvironmentHeader)
	} else {
		headers.Set(ingressScopeEnvironmentHeader, normalizedEnvironment)
	}
}
