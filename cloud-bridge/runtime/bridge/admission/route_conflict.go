package admission

import (
	"fmt"
	"net"
	"sort"
	"strconv"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RouteConflictError 描述注册期检测到的路由冲突。
type RouteConflictError struct {
	ConflictRouteID string
	message         string
}

// Error 返回冲突错误文本。
func (err *RouteConflictError) Error() string {
	if err == nil {
		return ""
	}
	return err.message
}

// RouteConflictAdmission 负责对 RouteAssign 执行冲突检测与 shadow warning 判定。
type RouteConflictAdmission struct {
	routeRegistry   *registry.RouteRegistry
	serviceRegistry *registry.ServiceRegistry
}

// NewRouteConflictAdmission 创建路由冲突检测器。
func NewRouteConflictAdmission(
	routeRegistry *registry.RouteRegistry,
	serviceRegistry *registry.ServiceRegistry,
) *RouteConflictAdmission {
	return &RouteConflictAdmission{
		routeRegistry:   routeRegistry,
		serviceRegistry: serviceRegistry,
	}
}

// Admit 校验路由是否与现有配置发生冲突，并返回非致命 warning。
func (admission *RouteConflictAdmission) Admit(route pb.Route) ([]string, map[string]string, error) {
	if admission == nil || admission.routeRegistry == nil {
		return nil, nil, nil
	}
	normalizedRoute := normalizeAdmissionRoute(route)
	normalizedTargetIdentity := admission.resolveTargetIdentity(normalizedRoute)
	if normalizedTargetIdentity == "" {
		return nil, nil, ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			"route target identity cannot be resolved",
		)
	}
	warnings := make([]string, 0, 1)
	for _, existingRoute := range admission.routeRegistry.List() {
		normalizedExistingRoute := normalizeAdmissionRoute(existingRoute)
		if strings.TrimSpace(normalizedExistingRoute.RouteID) == strings.TrimSpace(normalizedRoute.RouteID) {
			continue
		}
		if !routesShareExactAdmissionMatch(normalizedExistingRoute, normalizedRoute) {
			continue
		}
		existingTargetIdentity := admission.resolveTargetIdentity(normalizedExistingRoute)
		if existingTargetIdentity == "" {
			continue
		}
		if normalizedTargetIdentity != "" && existingTargetIdentity != "" && normalizedTargetIdentity == existingTargetIdentity {
			continue
		}
		if normalizedExistingRoute.Priority != normalizedRoute.Priority {
			warnings = append(warnings, buildShadowWarning(normalizedRoute.RouteID, normalizedExistingRoute.RouteID))
			continue
		}
		return nil, map[string]string{
				"conflict_route_id": strings.TrimSpace(normalizedExistingRoute.RouteID),
			}, &RouteConflictError{
				ConflictRouteID: strings.TrimSpace(normalizedExistingRoute.RouteID),
				message: fmt.Sprintf(
					"route conflict: identical match conditions with existing route_id=%s",
					strings.TrimSpace(normalizedExistingRoute.RouteID),
				),
			}
	}
	return warnings, nil, nil
}

func buildShadowWarning(routeID string, existingRouteID string) string {
	return fmt.Sprintf(
		"route %s has identical match conditions with existing route %s but different priority; lower-priority route may never be matched",
		strings.TrimSpace(routeID),
		strings.TrimSpace(existingRouteID),
	)
}

func routesShareExactAdmissionMatch(left pb.Route, right pb.Route) bool {
	if resolveAdmissionIngressMode(left) != resolveAdmissionIngressMode(right) {
		return false
	}
	if normalizeAdmissionProtocol(left.Match.Protocol) != normalizeAdmissionProtocol(right.Match.Protocol) {
		return false
	}
	if normalizeAdmissionHost(left.Match.Host) != normalizeAdmissionHost(right.Match.Host) {
		return false
	}
	if normalizeAdmissionAuthority(left.Match.Authority) != normalizeAdmissionAuthority(right.Match.Authority) {
		return false
	}
	if normalizeAdmissionPathPrefix(left.Match.PathPrefix) != normalizeAdmissionPathPrefix(right.Match.PathPrefix) {
		return false
	}
	if normalizeAdmissionSNI(left.Match.SNI) != normalizeAdmissionSNI(right.Match.SNI) {
		return false
	}
	if left.Match.ListenPort != right.Match.ListenPort {
		return false
	}
	if buildHeaderMatcherSignature(left.Match.Headers) != buildHeaderMatcherSignature(right.Match.Headers) {
		return false
	}
	if buildQueryMatcherSignature(left.Match.Queries) != buildQueryMatcherSignature(right.Match.Queries) {
		return false
	}
	return routesOverlapUnderScopeRules(left, right)
}

func routesOverlapUnderScopeRules(left pb.Route, right pb.Route) bool {
	leftPolicy := normalizeAdmissionScopeInjectPolicy(left.ScopeInjection.InjectPolicy)
	rightPolicy := normalizeAdmissionScopeInjectPolicy(right.ScopeInjection.InjectPolicy)
	if leftPolicy != pb.ScopeInjectPolicyDisabled || rightPolicy != pb.ScopeInjectPolicyDisabled {
		// 一旦任一路由开启作用域注入，route.scope 不再是可靠隔离键，需按同入口冲突处理。
		return true
	}
	return normalizeAdmissionScope(left.Scope) == normalizeAdmissionScope(right.Scope)
}

func normalizeAdmissionScopeInjectPolicy(injectPolicy pb.ScopeInjectPolicy) pb.ScopeInjectPolicy {
	switch strings.ToLower(strings.TrimSpace(string(injectPolicy))) {
	case "", string(pb.ScopeInjectPolicyDisabled):
		return pb.ScopeInjectPolicyDisabled
	case string(pb.ScopeInjectPolicyAlways):
		return pb.ScopeInjectPolicyAlways
	case string(pb.ScopeInjectPolicyMissingOnly):
		return pb.ScopeInjectPolicyMissingOnly
	default:
		return pb.ScopeInjectPolicyDisabled
	}
}

func normalizeAdmissionScopeInjection(scopeInjection pb.ScopeInjection) pb.ScopeInjection {
	return pb.ScopeInjection{
		InjectScope: normalizeAdmissionScope(scopeInjection.InjectScope),
		InjectPolicy: normalizeAdmissionScopeInjectPolicy(
			scopeInjection.InjectPolicy,
		),
	}
}

func normalizeAdmissionRoute(route pb.Route) pb.Route {
	normalizedRoute := route
	normalizedRoute.RouteID = strings.TrimSpace(route.RouteID)
	normalizedRoute.Scope = pb.Scope{
		Namespace:   strings.TrimSpace(route.Scope.Namespace),
		Environment: strings.TrimSpace(route.Scope.Environment),
	}
	normalizedRoute.ScopeInjection = normalizeAdmissionScopeInjection(route.ScopeInjection)
	normalizedRoute.Match.Protocol = normalizeAdmissionProtocol(route.Match.Protocol)
	normalizedRoute.Match.Host = normalizeAdmissionHost(route.Match.Host)
	normalizedRoute.Match.Authority = normalizeAdmissionAuthority(route.Match.Authority)
	normalizedRoute.Match.PathPrefix = normalizeAdmissionPathPrefix(route.Match.PathPrefix)
	normalizedRoute.Match.SNI = normalizeAdmissionSNI(route.Match.SNI)
	normalizedRoute.Match.Headers = normalizeAdmissionHeaderMatchers(route.Match.Headers)
	normalizedRoute.Match.Queries = normalizeAdmissionQueryMatchers(route.Match.Queries)
	return normalizedRoute
}

func normalizeAdmissionScope(scope pb.Scope) pb.Scope {
	return pb.Scope{
		Namespace:   strings.TrimSpace(scope.Namespace),
		Environment: strings.TrimSpace(scope.Environment),
	}
}

func normalizeAdmissionProtocol(protocol string) string {
	return strings.ToLower(strings.TrimSpace(protocol))
}

func normalizeAdmissionHost(host string) string {
	normalizedHost := strings.ToLower(strings.TrimSpace(host))
	if normalizedHost == "" {
		return ""
	}
	if parsedHost, _, err := net.SplitHostPort(normalizedHost); err == nil {
		normalizedHost = parsedHost
	} else if rawHost, rawPort, found := strings.Cut(normalizedHost, ":"); found && !strings.Contains(rawHost, ":") && isAdmissionNumericPort(rawPort) {
		normalizedHost = rawHost
	}
	normalizedHost = strings.TrimPrefix(normalizedHost, "[")
	normalizedHost = strings.TrimSuffix(normalizedHost, "]")
	return strings.TrimSuffix(normalizedHost, ".")
}

func normalizeAdmissionAuthority(authority string) string {
	normalizedAuthority := strings.ToLower(strings.TrimSpace(authority))
	if normalizedAuthority == "" {
		return ""
	}
	host, port, err := net.SplitHostPort(normalizedAuthority)
	if err != nil {
		if rawHost, rawPort, found := strings.Cut(normalizedAuthority, ":"); found && !strings.Contains(rawHost, ":") && isAdmissionNumericPort(rawPort) {
			return strings.TrimSuffix(strings.TrimSpace(rawHost), ".") + ":" + rawPort
		}
		return strings.TrimSuffix(normalizedAuthority, ".")
	}
	normalizedHost := normalizeAdmissionHost(host)
	if normalizedHost == "" {
		return ""
	}
	return net.JoinHostPort(normalizedHost, port)
}

func normalizeAdmissionPathPrefix(pathPrefix string) string {
	normalizedPathPrefix := strings.TrimSpace(pathPrefix)
	if normalizedPathPrefix == "" {
		return "/"
	}
	if !strings.HasPrefix(normalizedPathPrefix, "/") {
		return "/" + normalizedPathPrefix
	}
	return normalizedPathPrefix
}

func normalizeAdmissionSNI(sni string) string {
	return strings.ToLower(strings.TrimSpace(sni))
}

func normalizeAdmissionHeaderMatchers(matchers []pb.HeaderMatcher) []pb.HeaderMatcher {
	if len(matchers) == 0 {
		return nil
	}
	normalized := make([]pb.HeaderMatcher, 0, len(matchers))
	for _, matcher := range matchers {
		normalizedName := strings.ToLower(strings.TrimSpace(matcher.Name))
		if normalizedName == "" {
			continue
		}
		normalized = append(normalized, pb.HeaderMatcher{
			Name:    normalizedName,
			Exact:   strings.TrimSpace(matcher.Exact),
			Prefix:  strings.TrimSpace(matcher.Prefix),
			Regex:   strings.TrimSpace(matcher.Regex),
			Present: matcher.Present,
		})
	}
	sort.Slice(normalized, func(left, right int) bool {
		return buildHeaderMatcherEntry(normalized[left]) < buildHeaderMatcherEntry(normalized[right])
	})
	return normalized
}

func normalizeAdmissionQueryMatchers(matchers []pb.QueryMatcher) []pb.QueryMatcher {
	if len(matchers) == 0 {
		return nil
	}
	normalized := make([]pb.QueryMatcher, 0, len(matchers))
	for _, matcher := range matchers {
		normalizedName := strings.TrimSpace(matcher.Name)
		if normalizedName == "" {
			continue
		}
		normalized = append(normalized, pb.QueryMatcher{
			Name:    normalizedName,
			Exact:   strings.TrimSpace(matcher.Exact),
			Prefix:  strings.TrimSpace(matcher.Prefix),
			Regex:   strings.TrimSpace(matcher.Regex),
			Present: matcher.Present,
		})
	}
	sort.Slice(normalized, func(left, right int) bool {
		return buildQueryMatcherEntry(normalized[left]) < buildQueryMatcherEntry(normalized[right])
	})
	return normalized
}

func buildHeaderMatcherSignature(matchers []pb.HeaderMatcher) string {
	if len(matchers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(matchers))
	for _, matcher := range normalizeAdmissionHeaderMatchers(matchers) {
		parts = append(parts, buildHeaderMatcherEntry(matcher))
	}
	return strings.Join(parts, "|")
}

func buildHeaderMatcherEntry(matcher pb.HeaderMatcher) string {
	present := ""
	if matcher.Present != nil {
		present = strconv.FormatBool(*matcher.Present)
	}
	return strings.Join([]string{
		strings.ToLower(strings.TrimSpace(matcher.Name)),
		strings.TrimSpace(matcher.Exact),
		strings.TrimSpace(matcher.Prefix),
		strings.TrimSpace(matcher.Regex),
		present,
	}, ":")
}

func buildQueryMatcherSignature(matchers []pb.QueryMatcher) string {
	if len(matchers) == 0 {
		return ""
	}
	parts := make([]string, 0, len(matchers))
	for _, matcher := range normalizeAdmissionQueryMatchers(matchers) {
		parts = append(parts, buildQueryMatcherEntry(matcher))
	}
	return strings.Join(parts, "|")
}

func buildQueryMatcherEntry(matcher pb.QueryMatcher) string {
	present := ""
	if matcher.Present != nil {
		present = strconv.FormatBool(*matcher.Present)
	}
	return strings.Join([]string{
		strings.TrimSpace(matcher.Name),
		strings.TrimSpace(matcher.Exact),
		strings.TrimSpace(matcher.Prefix),
		strings.TrimSpace(matcher.Regex),
		present,
	}, ":")
}

func resolveAdmissionIngressMode(route pb.Route) pb.IngressMode {
	if modeValue, exists := route.Metadata["ingress_mode"]; exists {
		switch pb.IngressMode(strings.TrimSpace(modeValue)) {
		case pb.IngressModeL7Shared, pb.IngressModeTLSSNIShared, pb.IngressModeL4DedicatedPort:
			return pb.IngressMode(strings.TrimSpace(modeValue))
		}
	}
	normalizedProtocol := normalizeAdmissionProtocol(route.Match.Protocol)
	if normalizeAdmissionSNI(route.Match.SNI) != "" {
		return pb.IngressModeTLSSNIShared
	}
	if route.Match.ListenPort != 0 &&
		normalizeAdmissionHost(route.Match.Host) == "" &&
		normalizeAdmissionAuthority(route.Match.Authority) == "" &&
		normalizeAdmissionPathPrefix(route.Match.PathPrefix) == "/" &&
		(normalizedProtocol == "" || normalizedProtocol == "tcp") {
		return pb.IngressModeL4DedicatedPort
	}
	return pb.IngressModeL7Shared
}

func (admission *RouteConflictAdmission) resolveTargetIdentity(route pb.Route) string {
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		if route.Target.ConnectorService == nil {
			return ""
		}
		selector := route.Target.ConnectorService.Selector
		if normalizedLogicalServiceID := strings.TrimSpace(selector.LogicalServiceID); normalizedLogicalServiceID != "" {
			return "logical_service_id:" + normalizedLogicalServiceID
		}
		scope := selector.Scope
		if strings.TrimSpace(scope.Namespace) == "" {
			scope.Namespace = route.Scope.Namespace
		}
		if strings.TrimSpace(scope.Environment) == "" {
			scope.Environment = route.Scope.Environment
		}
		if admission != nil && admission.serviceRegistry != nil {
			if strings.TrimSpace(selector.ServiceName) != "" {
				if logicalService, exists := admission.serviceRegistry.FindLogicalServiceByNameScope(selector.ServiceName, scope); exists {
					return "logical_service_id:" + strings.TrimSpace(logicalService.LogicalServiceID)
				}
			}
			if len(selector.MatchLabels) > 0 {
				for _, logicalService := range admission.serviceRegistry.List() {
					if strings.TrimSpace(logicalService.Scope.Namespace) != strings.TrimSpace(scope.Namespace) ||
						strings.TrimSpace(logicalService.Scope.Environment) != strings.TrimSpace(scope.Environment) {
						continue
					}
					if matchAdmissionLabels(logicalService.Labels, selector.MatchLabels) {
						return "logical_service_id:" + strings.TrimSpace(logicalService.LogicalServiceID)
					}
				}
			}
		}
		if strings.TrimSpace(selector.ServiceName) != "" {
			return fmt.Sprintf(
				"service_name:%s/%s/%s",
				strings.TrimSpace(scope.Namespace),
				strings.TrimSpace(scope.Environment),
				strings.TrimSpace(selector.ServiceName),
			)
		}
		if len(selector.MatchLabels) > 0 {
			return fmt.Sprintf(
				"selector_labels:%s/%s/%s",
				strings.TrimSpace(scope.Namespace),
				strings.TrimSpace(scope.Environment),
				buildSortedStringMap(selector.MatchLabels),
			)
		}
	case pb.RouteTargetTypeExternalService:
		if route.Target.ExternalService == nil {
			return ""
		}
		target := route.Target.ExternalService
		namespace := strings.TrimSpace(target.Namespace)
		if namespace == "" {
			namespace = strings.TrimSpace(route.Scope.Namespace)
		}
		environment := strings.TrimSpace(target.Environment)
		if environment == "" {
			environment = strings.TrimSpace(route.Scope.Environment)
		}
		return fmt.Sprintf(
			"external:%s/%s/%s/%s/%s",
			strings.TrimSpace(target.Provider),
			namespace,
			environment,
			strings.TrimSpace(target.ServiceName),
			buildSortedStringMap(target.Selector),
		)
	}
	return ""
}

func buildSortedStringMap(values map[string]string) string {
	if len(values) == 0 {
		return ""
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, key := range keys {
		parts = append(parts, strings.TrimSpace(key)+"="+strings.TrimSpace(values[key]))
	}
	return strings.Join(parts, "&")
}

func matchAdmissionLabels(labels map[string]string, selector map[string]string) bool {
	for key, expected := range selector {
		if strings.TrimSpace(labels[key]) != strings.TrimSpace(expected) {
			return false
		}
	}
	return true
}

func isAdmissionNumericPort(value string) bool {
	normalizedValue := strings.TrimSpace(value)
	if normalizedValue == "" {
		return false
	}
	parsedPort, err := strconv.Atoi(normalizedValue)
	if err != nil {
		return false
	}
	return parsedPort >= 0 && parsedPort <= 65535
}

// ExtractConflictRouteID 提取冲突错误中的 route_id。
func ExtractConflictRouteID(err error) string {
	conflictErr, ok := err.(*RouteConflictError)
	if !ok || conflictErr == nil {
		return ""
	}
	return strings.TrimSpace(conflictErr.ConflictRouteID)
}

// IsConflictError 判断错误是否为路由冲突。
func IsConflictError(err error) bool {
	_, ok := err.(*RouteConflictError)
	return ok || ltfperrors.IsCode(err, ltfperrors.CodeIngressRouteMismatch)
}
