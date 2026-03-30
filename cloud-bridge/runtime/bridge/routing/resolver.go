package routing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

const (
	// RouteLookupMetadataTrafficIDKey 定义 route lookup metadata 中的 traffic_id 键。
	RouteLookupMetadataTrafficIDKey = "traffic_id"
	// RouteLookupMetadataClientIPKey 定义 route lookup metadata 中的 client_ip 键。
	RouteLookupMetadataClientIPKey = "client_ip"
	// RouteLookupMetadataScopeHeadersCompleteKey 标记入口是否完整提供了标准 scope headers。
	RouteLookupMetadataScopeHeadersCompleteKey = "request_scope_headers_complete"
)

type connectorSelectionPolicy struct {
	StickyBy string `json:"sticky_by,omitempty"`
}

// ResolverOptions 定义 Resolver 依赖。
type ResolverOptions struct {
	Matcher                          *Matcher
	Selector                         *Selector
	RouteRegistry                    *registry.RouteRegistry
	ServiceRegistry                  *registry.ServiceRegistry
	SessionRegistry                  *registry.SessionRegistry
	Metrics                          *obs.Metrics
	DefaultScope                     pb.Scope
	FallbackPolicies                 []pb.ScopeFallbackPolicy
	ServiceInstanceSelector          ServiceInstanceSelector
	ServiceInstanceSelectorAlgorithm string
	TrafficAffinityTTL               time.Duration
	TrafficAffinityCapacity          int
}

// ConnectorResolution 描述 connector_service 解析结果。
type ConnectorResolution struct {
	LogicalService pb.LogicalService
	Instance       pb.ServiceInstance
	Session        registry.SessionRuntime
}

// ResolveResult 描述 RouteResolver 输出。
type ResolveResult struct {
	Route              pb.Route
	TargetKind         pb.RouteTargetType
	IngressMode        pb.IngressMode
	Connector          *ConnectorResolution
	External           *pb.ExternalServiceTarget
	IsExternalFallback bool
	RequestScope       pb.Scope
	MatchedScope       pb.Scope
	ScopeFallbackPath  []pb.Scope
}

// Resolver 负责入口请求到目标类型的分类和过滤。
type Resolver struct {
	matcher                 *Matcher
	selector                *Selector
	routeRegistry           *registry.RouteRegistry
	serviceRegistry         *registry.ServiceRegistry
	sessionRegistry         *registry.SessionRegistry
	metrics                 *obs.Metrics
	serviceInstanceSelector ServiceInstanceSelector
	selectorByAlgorithm     map[string]ServiceInstanceSelector
	defaultSelectorKey      string
	trafficAffinity         *trafficAffinityStore
	defaultScope            pb.Scope
	fallbackPolicies        map[string]pb.ScopeFallbackPolicy
}

// NewResolver 创建 RouteResolver。
func NewResolver(options ResolverOptions) *Resolver {
	matcher := options.Matcher
	if matcher == nil {
		matcher = NewMatcher()
	}
	selector := options.Selector
	if selector == nil {
		selector = NewSelector()
	}
	routeRegistry := options.RouteRegistry
	if routeRegistry == nil {
		routeRegistry = registry.NewRouteRegistry()
	}
	serviceRegistry := options.ServiceRegistry
	if serviceRegistry == nil {
		serviceRegistry = registry.NewServiceRegistry()
	}
	sessionRegistry := options.SessionRegistry
	if sessionRegistry == nil {
		sessionRegistry = registry.NewSessionRegistry()
	}
	trafficAffinity := newTrafficAffinityStore(options.TrafficAffinityTTL, options.TrafficAffinityCapacity)
	return &Resolver{
		matcher:                 matcher,
		selector:                selector,
		routeRegistry:           routeRegistry,
		serviceRegistry:         serviceRegistry,
		sessionRegistry:         sessionRegistry,
		metrics:                 normalizeResolverMetrics(options.Metrics),
		serviceInstanceSelector: options.ServiceInstanceSelector,
		selectorByAlgorithm:     buildServiceInstanceSelectorSet(options.ServiceInstanceSelectorAlgorithm),
		defaultSelectorKey:      normalizeDefaultLoadBalancePolicy(options.ServiceInstanceSelectorAlgorithm),
		trafficAffinity:         trafficAffinity,
		defaultScope:            normalizeResolverScope(options.DefaultScope),
		fallbackPolicies:        buildFallbackPolicyIndex(options.FallbackPolicies),
	}
}

// Resolve 执行入口请求的路由匹配、目标分类与过滤。
func (resolver *Resolver) Resolve(request ingress.RouteLookupRequest) (ResolveResult, error) {
	if resolver == nil {
		return ResolveResult{}, fmt.Errorf("resolve route: nil resolver")
	}
	candidates := resolver.matcher.Match(request, resolver.routeRegistry.List())
	if len(candidates) == 0 {
		return ResolveResult{}, ltfperrors.New(
			ltfperrors.CodeIngressRouteMismatch,
			"no route matches current ingress request",
		)
	}
	scopedCandidates := resolver.buildScopedRouteCandidates(request, candidates)
	if len(scopedCandidates) == 0 {
		return ResolveResult{}, ltfperrors.New(
			ltfperrors.CodeIngressRouteMismatch,
			"no route passes scope injection and fallback policy",
		)
	}
	sort.SliceStable(scopedCandidates, func(left, right int) bool {
		if scopedCandidates[left].ScopeIndex != scopedCandidates[right].ScopeIndex {
			return scopedCandidates[left].ScopeIndex < scopedCandidates[right].ScopeIndex
		}
		return false
	})
	var firstFilterError error
	var externalFallbackCandidates []scopedRouteCandidate
	maxScopeIndex := resolveMaxScopeIndex(scopedCandidates)
	for scopeIndex := 0; scopeIndex <= maxScopeIndex; scopeIndex++ {
		scopeCandidates := filterScopedRouteCandidatesByIndex(scopedCandidates, scopeIndex)
		connectorCandidates, externalCandidates := partitionScopedRouteCandidatesByTarget(scopeCandidates)
		externalFallbackCandidates = append(externalFallbackCandidates, externalCandidates...)
		for len(connectorCandidates) > 0 {
			routeCandidates := extractRoutesFromScopedCandidates(connectorCandidates)
			route, selected := resolver.selector.Select(routeCandidates)
			if !selected {
				break
			}
			scopedCandidate, exists := findScopedRouteCandidateByRouteID(connectorCandidates, route.RouteID)
			if !exists {
				connectorCandidates = removeScopedRouteCandidateByID(connectorCandidates, route.RouteID)
				continue
			}
			result, err := resolver.resolveTarget(route, request)
			if err != nil {
				if shouldContinueScopeFallback(err) {
					connectorCandidates = removeScopedRouteCandidateByID(connectorCandidates, route.RouteID)
					continue
				}
				if firstFilterError == nil {
					firstFilterError = err
				}
				break
			}
			result.Route = route
			result.IngressMode = resolveRouteIngressMode(route)
			result.RequestScope = scopedCandidate.RequestScope
			result.MatchedScope = scopedCandidate.MatchedScope
			result.ScopeFallbackPath = append([]pb.Scope(nil), scopedCandidate.ScopeFallbackPath...)
			if scopedCandidate.ScopeIndex > 0 && resolver.metrics != nil {
				resolver.metrics.IncBridgeScopeFallbackTotal()
			}
			return result, nil
		}
	}
	if len(externalFallbackCandidates) > 0 {
		result, err := resolver.resolveExternalFallback(externalFallbackCandidates, request)
		if err == nil {
			return result, nil
		}
		if firstFilterError == nil {
			firstFilterError = err
		}
	}
	if firstFilterError != nil {
		return ResolveResult{}, firstFilterError
	}
	return ResolveResult{}, ltfperrors.New(
		ltfperrors.CodeIngressRouteMismatch,
		"no route passes resolver filters",
	)
}

func (resolver *Resolver) resolveTarget(route pb.Route, request ingress.RouteLookupRequest) (ResolveResult, error) {
	targetType := normalizeRouteTargetType(route.Target)
	switch targetType {
	case pb.RouteTargetTypeConnectorService:
		connector, err := resolver.resolveConnectorTarget(route, route.Target.ConnectorService, request)
		if err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{
			TargetKind: pb.RouteTargetTypeConnectorService,
			Connector:  connector,
		}, nil
	case pb.RouteTargetTypeExternalService:
		external, err := resolveExternalTarget(route, route.Target.ExternalService)
		if err != nil {
			return ResolveResult{}, err
		}
		return ResolveResult{
			TargetKind: pb.RouteTargetTypeExternalService,
			External:   external,
		}, nil
	default:
		return ResolveResult{}, ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported route target type: %s", targetType),
		)
	}
}

func (resolver *Resolver) resolveConnectorTarget(
	route pb.Route,
	target *pb.ConnectorServiceTarget,
	request ingress.RouteLookupRequest,
) (*ConnectorResolution, error) {
	if target == nil {
		return nil, ltfperrors.New(
			ltfperrors.CodeInvalidPayload,
			"connector_service target payload is empty",
		)
	}
	logicalService, err := resolver.resolveLogicalService(target.Selector, route.Scope)
	if err != nil {
		return nil, err
	}
	if err := validate.ValidateRouteScope(route.Scope, logicalService.Scope); err != nil {
		resolver.observeRouteFailure(logicalService.LogicalServiceID, "", err)
		return nil, err
	}
	if !matchLabels(logicalService.Labels, target.Selector.MatchLabels) {
		resolveErr := ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service does not match selector labels")
		resolver.observeRouteFailure(logicalService.LogicalServiceID, "", resolveErr)
		return nil, resolveErr
	}
	if logicalService.Status != pb.ServiceStatusActive {
		resolveErr := ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "logical service is not active")
		resolver.observeRouteFailure(logicalService.LogicalServiceID, "", resolveErr)
		return nil, resolveErr
	}

	instanceSelector := mergeStringMaps(target.Selector.InstanceLabels, target.InstanceSelector)
	serviceInstances := resolver.serviceRegistry.ListInstancesByLogicalServiceID(logicalService.LogicalServiceID)
	if len(serviceInstances) == 0 {
		resolveErr := ltfperrors.New(
			ltfperrors.CodeResolveServiceNotFound,
			fmt.Sprintf("no service instances found for logical_service_id=%s", logicalService.LogicalServiceID),
		)
		resolver.observeRouteFailure(logicalService.LogicalServiceID, "", resolveErr)
		return nil, resolveErr
	}

	var firstFilterError error
	candidates := make([]ConnectorResolution, 0, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		instance := serviceInstance.Instance
		if instance.InstanceStatus != pb.ServiceStatusActive || instance.HealthStatus != pb.HealthStatusHealthy {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveServiceUnavailable,
					fmt.Sprintf(
						"instance unavailable for logical_service_id=%s status=%s health=%s",
						logicalService.LogicalServiceID,
						instance.InstanceStatus,
						instance.HealthStatus,
					),
				)
			}
			continue
		}
		if !matchLabels(instance.Labels, instanceSelector) {
			continue
		}
		normalizedConnectorID := strings.TrimSpace(instance.ConnectorID)
		if normalizedConnectorID == "" {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveServiceUnavailable,
					fmt.Sprintf("instance=%s has empty connector_id", instance.InstanceID),
				)
			}
			continue
		}
		sessionSnapshot, sessionExists := resolver.resolveServiceSession(serviceInstance, normalizedConnectorID)
		if !sessionExists {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveServiceUnavailable,
					fmt.Sprintf("connector offline for connector_id=%s", normalizedConnectorID),
				)
			}
			continue
		}
		if sessionSnapshot.State != registry.SessionActive {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveSessionNotActive,
					fmt.Sprintf("session not active for connector_id=%s state=%s", normalizedConnectorID, sessionSnapshot.State),
				)
			}
			continue
		}
		candidates = append(candidates, ConnectorResolution{
			LogicalService: logicalService,
			Instance:       instance,
			Session:        sessionSnapshot,
		})
	}

	resolver.observeServiceAvailability(logicalService.LogicalServiceID, candidates)
	if len(candidates) == 0 {
		if firstFilterError != nil {
			resolver.observeRouteFailure(logicalService.LogicalServiceID, "", firstFilterError)
			return nil, firstFilterError
		}
		resolveErr := ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("logical service unavailable for logical_service_id=%s", logicalService.LogicalServiceID),
		)
		resolver.observeRouteFailure(logicalService.LogicalServiceID, "", resolveErr)
		return nil, resolveErr
	}
	selected, err := resolver.selectConnectorResolution(route, target, candidates, logicalService.LogicalServiceID, request)
	if err != nil {
		return nil, err
	}
	resolver.observeRouteHit(selected.LogicalService.LogicalServiceID, selected.Instance.InstanceID)
	return &selected, nil
}

// selectConnectorResolution 在候选实例内选择目标，并维持同 traffic_id 的实例粘性。
func (resolver *Resolver) selectConnectorResolution(
	route pb.Route,
	target *pb.ConnectorServiceTarget,
	candidates []ConnectorResolution,
	logicalServiceID string,
	request ingress.RouteLookupRequest,
) (ConnectorResolution, error) {
	if len(candidates) == 0 {
		return ConnectorResolution{}, ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("logical service unavailable for logical_service_id=%s", strings.TrimSpace(logicalServiceID)),
		)
	}
	orderedCandidates := append([]ConnectorResolution(nil), candidates...)
	sort.Slice(orderedCandidates, func(left, right int) bool {
		leftInstanceID := strings.TrimSpace(orderedCandidates[left].Instance.InstanceID)
		rightInstanceID := strings.TrimSpace(orderedCandidates[right].Instance.InstanceID)
		if leftInstanceID == rightInstanceID {
			return strings.TrimSpace(orderedCandidates[left].Session.ConnectorID) < strings.TrimSpace(orderedCandidates[right].Session.ConnectorID)
		}
		return leftInstanceID < rightInstanceID
	})
	trafficID := resolveLookupTrafficID(request)
	if trafficID != "" && resolver != nil && resolver.trafficAffinity != nil {
		if stickyInstanceID, exists := resolver.trafficAffinity.Load(trafficID, time.Now().UTC()); exists {
			for _, candidate := range orderedCandidates {
				if strings.TrimSpace(candidate.Instance.InstanceID) == stickyInstanceID {
					return candidate, nil
				}
			}
			resolveErr := ltfperrors.New(
				ltfperrors.CodeResolveServiceUnavailable,
				fmt.Sprintf(
					"sticky traffic target unavailable for traffic_id=%s logical_service_id=%s instance_id=%s",
					trafficID,
					strings.TrimSpace(logicalServiceID),
					stickyInstanceID,
				),
			)
			resolver.observeRouteFailure(logicalServiceID, stickyInstanceID, resolveErr)
			return ConnectorResolution{}, resolveErr
		}
	}
	selected := orderedCandidates[0]
	policy := normalizeLoadBalancePolicy(resolveTargetLoadBalancePolicy(target))
	selector := resolver.resolveServiceInstanceSelector(policy)
	if selector != nil {
		selected = selector.Select(orderedCandidates, ServiceInstanceSelectionRequest{
			Policy:    policy,
			StickyKey: resolveStickySelectionKey(route, request),
		})
	}
	if trafficID != "" && resolver != nil && resolver.trafficAffinity != nil && strings.TrimSpace(selected.Instance.InstanceID) != "" {
		resolver.trafficAffinity.Store(trafficID, strings.TrimSpace(selected.Instance.InstanceID), time.Now().UTC())
	}
	if resolver != nil && resolver.metrics != nil {
		resolver.metrics.ObserveBridgeInstanceSelectorPick(strings.TrimSpace(selected.Instance.InstanceID), policy)
	}
	return selected, nil
}

func (resolver *Resolver) resolveServiceInstanceSelector(policy string) ServiceInstanceSelector {
	if resolver == nil {
		return nil
	}
	if resolver.serviceInstanceSelector != nil {
		return resolver.serviceInstanceSelector
	}
	if strings.TrimSpace(policy) == "" {
		if selector, exists := resolver.selectorByAlgorithm[resolver.defaultSelectorKey]; exists && selector != nil {
			return selector
		}
		return resolver.selectorByAlgorithm[ServiceInstanceSelectorAlgorithmRoundRobin]
	}
	normalizedPolicy := normalizeLoadBalancePolicy(policy)
	if selector, exists := resolver.selectorByAlgorithm[normalizedPolicy]; exists && selector != nil {
		return selector
	}
	return resolver.selectorByAlgorithm[ServiceInstanceSelectorAlgorithmRoundRobin]
}

// resolveServiceSession 解析服务实例对应会话，优先命中实例记录的 session_id。
func (resolver *Resolver) resolveServiceSession(
	serviceInstance registry.ServiceInstanceSnapshot,
	connectorID string,
) (registry.SessionRuntime, bool) {
	normalizedSessionID := strings.TrimSpace(serviceInstance.Instance.SessionID)
	if normalizedSessionID != "" {
		sessionSnapshot, exists := resolver.sessionRegistry.GetBySession(normalizedSessionID)
		if !exists {
			return registry.SessionRuntime{}, false
		}
		if strings.TrimSpace(sessionSnapshot.ConnectorID) != strings.TrimSpace(connectorID) {
			return registry.SessionRuntime{}, false
		}
		return sessionSnapshot, true
	}
	return resolver.sessionRegistry.GetByConnector(connectorID)
}

func resolveExternalTarget(route pb.Route, target *pb.ExternalServiceTarget) (*pb.ExternalServiceTarget, error) {
	if target == nil {
		return nil, ltfperrors.New(
			ltfperrors.CodeInvalidPayload,
			"external_service target payload is empty",
		)
	}
	if strings.TrimSpace(target.ServiceName) == "" {
		return nil, ltfperrors.New(
			ltfperrors.CodeMissingRequiredField,
			"external_service.service_name is required",
		)
	}
	targetNamespace := strings.TrimSpace(target.Namespace)
	if targetNamespace == "" {
		targetNamespace = strings.TrimSpace(route.Scope.Namespace)
	}
	targetEnvironment := strings.TrimSpace(target.Environment)
	if targetEnvironment == "" {
		targetEnvironment = strings.TrimSpace(route.Scope.Environment)
	}
	if err := validate.ValidateRouteScope(route.Scope, pb.Scope{
		Namespace:   targetNamespace,
		Environment: targetEnvironment,
	}); err != nil {
		return nil, err
	}
	copied := *target
	copied.Namespace = targetNamespace
	copied.Environment = targetEnvironment
	copied.Selector = copyStringMap(target.Selector)
	return &copied, nil
}

func normalizeRouteTargetType(target pb.RouteTarget) pb.RouteTargetType {
	normalizedType := pb.RouteTargetType(strings.TrimSpace(string(target.Type)))
	switch normalizedType {
	case pb.RouteTargetTypeConnectorService, pb.RouteTargetTypeExternalService:
		return normalizedType
	}
	switch {
	case target.ConnectorService != nil:
		return pb.RouteTargetTypeConnectorService
	case target.ExternalService != nil:
		return pb.RouteTargetTypeExternalService
	default:
		return normalizedType
	}
}

func shouldContinueScopeFallback(err error) bool {
	switch ltfperrors.ExtractCode(err) {
	case ltfperrors.CodeResolveServiceNotFound:
		return true
	default:
		return false
	}
}

type scopedRouteCandidate struct {
	Route             pb.Route
	MatchedScope      pb.Scope
	ScopeIndex        int
	RequestScope      pb.Scope
	ScopeFallbackPath []pb.Scope
}

func partitionScopedRouteCandidatesByTarget(candidates []scopedRouteCandidate) ([]scopedRouteCandidate, []scopedRouteCandidate) {
	if len(candidates) == 0 {
		return nil, nil
	}
	connectorRoutes := make([]scopedRouteCandidate, 0, len(candidates))
	externalRoutes := make([]scopedRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		switch normalizeRouteTargetType(candidate.Route.Target) {
		case pb.RouteTargetTypeConnectorService:
			connectorRoutes = append(connectorRoutes, candidate)
		case pb.RouteTargetTypeExternalService:
			externalRoutes = append(externalRoutes, candidate)
		}
	}
	return connectorRoutes, externalRoutes
}

func (resolver *Resolver) buildScopedRouteCandidates(
	request ingress.RouteLookupRequest,
	routes []pb.Route,
) []scopedRouteCandidate {
	if len(routes) == 0 {
		return nil
	}
	baseRequestScope := resolver.resolveRequestScope(request)
	scopeHeadersComplete := resolveRequestScopeHeadersComplete(request)
	candidates := make([]scopedRouteCandidate, 0, len(routes))
	for _, route := range routes {
		effectiveRequestScope := resolveEffectiveRequestScope(baseRequestScope, scopeHeadersComplete, route.ScopeInjection)
		scopeChain := resolver.buildScopeChain(effectiveRequestScope)
		scopeIndex, matched := findScopeIndex(scopeChain, route.Scope)
		if !matched {
			continue
		}
		candidates = append(candidates, scopedRouteCandidate{
			Route:             route,
			MatchedScope:      normalizeResolverScope(route.Scope),
			ScopeIndex:        scopeIndex,
			RequestScope:      effectiveRequestScope,
			ScopeFallbackPath: append([]pb.Scope(nil), scopeChain[:scopeIndex+1]...),
		})
	}
	return candidates
}

func resolveEffectiveRequestScope(
	baseRequestScope pb.Scope,
	scopeHeadersComplete bool,
	scopeInjection pb.ScopeInjection,
) pb.Scope {
	normalizedBaseScope := normalizeResolverScope(baseRequestScope)
	normalizedScopeInjection := normalizeScopeInjection(scopeInjection)
	switch normalizedScopeInjection.InjectPolicy {
	case pb.ScopeInjectPolicyAlways:
		if isValidResolverScope(normalizedScopeInjection.InjectScope) {
			return normalizedScopeInjection.InjectScope
		}
	case pb.ScopeInjectPolicyMissingOnly:
		if scopeHeadersComplete {
			return normalizedBaseScope
		}
		if isValidResolverScope(normalizedScopeInjection.InjectScope) {
			return normalizedScopeInjection.InjectScope
		}
	}
	return normalizedBaseScope
}

func normalizeScopeInjection(scopeInjection pb.ScopeInjection) pb.ScopeInjection {
	return pb.ScopeInjection{
		InjectScope:  normalizeResolverScope(scopeInjection.InjectScope),
		InjectPolicy: normalizeScopeInjectPolicy(scopeInjection.InjectPolicy),
	}
}

func normalizeScopeInjectPolicy(injectPolicy pb.ScopeInjectPolicy) pb.ScopeInjectPolicy {
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

func resolveRequestScopeHeadersComplete(request ingress.RouteLookupRequest) bool {
	if len(request.Metadata) == 0 {
		return strings.TrimSpace(request.Namespace) != "" && strings.TrimSpace(request.Environment) != ""
	}
	normalizedValue := strings.TrimSpace(request.Metadata[RouteLookupMetadataScopeHeadersCompleteKey])
	if normalizedValue == "" {
		return strings.TrimSpace(request.Namespace) != "" && strings.TrimSpace(request.Environment) != ""
	}
	parsed, err := strconv.ParseBool(normalizedValue)
	if err != nil {
		return false
	}
	return parsed
}

func findScopeIndex(scopes []pb.Scope, targetScope pb.Scope) (int, bool) {
	normalizedTargetScope := normalizeResolverScope(targetScope)
	if !isValidResolverScope(normalizedTargetScope) {
		return -1, false
	}
	for index, scope := range scopes {
		if scopesEqual(scope, normalizedTargetScope) {
			return index, true
		}
	}
	return -1, false
}

func isValidResolverScope(scope pb.Scope) bool {
	return strings.TrimSpace(scope.Namespace) != "" && strings.TrimSpace(scope.Environment) != ""
}

func resolveMaxScopeIndex(candidates []scopedRouteCandidate) int {
	maxScopeIndex := -1
	for _, candidate := range candidates {
		if candidate.ScopeIndex > maxScopeIndex {
			maxScopeIndex = candidate.ScopeIndex
		}
	}
	return maxScopeIndex
}

func filterScopedRouteCandidatesByIndex(candidates []scopedRouteCandidate, scopeIndex int) []scopedRouteCandidate {
	if len(candidates) == 0 {
		return nil
	}
	filtered := make([]scopedRouteCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if candidate.ScopeIndex != scopeIndex {
			continue
		}
		filtered = append(filtered, candidate)
	}
	return filtered
}

func extractRoutesFromScopedCandidates(candidates []scopedRouteCandidate) []pb.Route {
	if len(candidates) == 0 {
		return nil
	}
	routes := make([]pb.Route, 0, len(candidates))
	for _, candidate := range candidates {
		routes = append(routes, candidate.Route)
	}
	return routes
}

func findScopedRouteCandidateByRouteID(candidates []scopedRouteCandidate, routeID string) (scopedRouteCandidate, bool) {
	normalizedRouteID := strings.TrimSpace(routeID)
	if normalizedRouteID == "" {
		return scopedRouteCandidate{}, false
	}
	for _, candidate := range candidates {
		if strings.TrimSpace(candidate.Route.RouteID) != normalizedRouteID {
			continue
		}
		return candidate, true
	}
	return scopedRouteCandidate{}, false
}

func removeScopedRouteCandidateByID(candidates []scopedRouteCandidate, routeID string) []scopedRouteCandidate {
	normalizedRouteID := strings.TrimSpace(routeID)
	if normalizedRouteID == "" || len(candidates) == 0 {
		return candidates
	}
	for index, candidate := range candidates {
		if strings.TrimSpace(candidate.Route.RouteID) != normalizedRouteID {
			continue
		}
		return append(candidates[:index], candidates[index+1:]...)
	}
	return candidates
}

func copyStringMap(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	copied := make(map[string]string, len(source))
	for key, value := range source {
		copied[key] = value
	}
	return copied
}

func resolveLookupTrafficID(request ingress.RouteLookupRequest) string {
	if len(request.Metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(request.Metadata[RouteLookupMetadataTrafficIDKey])
}

func normalizeResolverMetrics(metrics *obs.Metrics) *obs.Metrics {
	if metrics == nil {
		return obs.DefaultMetrics
	}
	return metrics
}

func buildFallbackPolicyIndex(policies []pb.ScopeFallbackPolicy) map[string]pb.ScopeFallbackPolicy {
	if len(policies) == 0 {
		return nil
	}
	index := make(map[string]pb.ScopeFallbackPolicy, len(policies))
	for _, policy := range policies {
		normalizedNamespace := strings.TrimSpace(policy.Namespace)
		if normalizedNamespace == "" {
			continue
		}
		copiedPolicy := policy
		copiedPolicy.PolicyID = strings.TrimSpace(policy.PolicyID)
		copiedPolicy.Namespace = normalizedNamespace
		copiedPolicy.Chain = append([]pb.FallbackStep(nil), policy.Chain...)
		copiedPolicy.External = policy.External
		index[normalizedNamespace] = copiedPolicy
	}
	return index
}

func normalizeResolverScope(scope pb.Scope) pb.Scope {
	return pb.Scope{
		Namespace:   strings.TrimSpace(scope.Namespace),
		Environment: strings.TrimSpace(scope.Environment),
	}
}

func scopesEqual(left pb.Scope, right pb.Scope) bool {
	normalizedLeft := normalizeResolverScope(left)
	normalizedRight := normalizeResolverScope(right)
	return normalizedLeft.Namespace == normalizedRight.Namespace &&
		normalizedLeft.Environment == normalizedRight.Environment
}

func (resolver *Resolver) resolveRequestScope(request ingress.RouteLookupRequest) pb.Scope {
	requestScope := pb.Scope{
		Namespace:   strings.TrimSpace(request.Namespace),
		Environment: strings.TrimSpace(request.Environment),
	}
	defaultScope := normalizeResolverScope(resolver.defaultScope)
	if requestScope.Namespace == "" {
		requestScope.Namespace = defaultScope.Namespace
	}
	if requestScope.Environment == "" {
		requestScope.Environment = defaultScope.Environment
	}
	return requestScope
}

func (resolver *Resolver) buildScopeChain(requestScope pb.Scope) []pb.Scope {
	normalizedRequestScope := normalizeResolverScope(requestScope)
	if normalizedRequestScope.Namespace == "" || normalizedRequestScope.Environment == "" {
		return nil
	}
	chain := []pb.Scope{normalizedRequestScope}
	if resolver == nil || len(resolver.fallbackPolicies) == 0 {
		return chain
	}
	policy, exists := resolver.fallbackPolicies[normalizedRequestScope.Namespace]
	if !exists || !policy.Enabled {
		return chain
	}
	seenScopes := map[string]struct{}{
		buildResolverScopeKey(normalizedRequestScope): {},
	}
	for _, step := range policy.Chain {
		normalizedTargetScope := normalizeResolverScope(step.TargetScope)
		if normalizedTargetScope.Namespace == "" || normalizedTargetScope.Environment == "" {
			continue
		}
		targetScopeKey := buildResolverScopeKey(normalizedTargetScope)
		if _, exists := seenScopes[targetScopeKey]; exists {
			continue
		}
		seenScopes[targetScopeKey] = struct{}{}
		chain = append(chain, normalizedTargetScope)
	}
	return chain
}

func (resolver *Resolver) lookupFallbackPolicy(scope pb.Scope) (pb.ScopeFallbackPolicy, bool) {
	if resolver == nil || len(resolver.fallbackPolicies) == 0 {
		return pb.ScopeFallbackPolicy{}, false
	}
	normalizedScope := normalizeResolverScope(scope)
	if normalizedScope.Namespace == "" {
		return pb.ScopeFallbackPolicy{}, false
	}
	policy, exists := resolver.fallbackPolicies[normalizedScope.Namespace]
	if !exists {
		return pb.ScopeFallbackPolicy{}, false
	}
	return policy, true
}

func (resolver *Resolver) isExternalFallbackEnabled(requestScope pb.Scope) bool {
	policy, exists := resolver.lookupFallbackPolicy(requestScope)
	if !exists || !policy.Enabled {
		return false
	}
	return policy.External.Enabled
}

func buildResolverScopeKey(scope pb.Scope) string {
	normalizedScope := normalizeResolverScope(scope)
	return normalizedScope.Namespace + "|" + normalizedScope.Environment
}

func (resolver *Resolver) observeRouteHit(logicalServiceID string, serviceInstanceID string) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	resolver.metrics.ObserveBridgeRouteHit(logicalServiceID, serviceInstanceID)
}

func (resolver *Resolver) observeRouteFailure(logicalServiceID string, serviceInstanceID string, err error) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	reason := ltfperrors.ExtractCode(err)
	if strings.TrimSpace(reason) == "" {
		reason = "resolve_failed"
	}
	resolver.metrics.ObserveBridgeRouteFailureReason(logicalServiceID, serviceInstanceID, reason)
}

func (resolver *Resolver) observeServiceAvailability(logicalServiceID string, candidates []ConnectorResolution) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	normalizedLogicalServiceID := strings.TrimSpace(logicalServiceID)
	if normalizedLogicalServiceID == "" {
		return
	}
	availableServiceInstanceIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalizedServiceInstanceID := strings.TrimSpace(candidate.Instance.InstanceID)
		if normalizedServiceInstanceID == "" {
			continue
		}
		availableServiceInstanceIDs = append(availableServiceInstanceIDs, normalizedServiceInstanceID)
	}
	resolver.metrics.SetBridgeServiceAvailableInstances(normalizedLogicalServiceID, availableServiceInstanceIDs)
}

func (resolver *Resolver) resolveLogicalService(selector pb.ServiceSelector, fallbackScope pb.Scope) (pb.LogicalService, error) {
	if strings.TrimSpace(selector.LogicalServiceID) != "" {
		service, exists := resolver.serviceRegistry.GetLogicalServiceByID(selector.LogicalServiceID)
		if !exists {
			return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service is not found by logicalServiceId")
		}
		return service, nil
	}
	if strings.TrimSpace(selector.ServiceName) == "" {
		if len(selector.MatchLabels) == 0 {
			return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connector_service.selector is required")
		}
		return resolver.resolveLogicalServiceByLabels(selector, fallbackScope)
	}
	scope := selector.Scope
	if strings.TrimSpace(scope.Namespace) == "" {
		scope.Namespace = strings.TrimSpace(fallbackScope.Namespace)
	}
	if strings.TrimSpace(scope.Environment) == "" {
		scope.Environment = strings.TrimSpace(fallbackScope.Environment)
	}
	service, exists := resolver.serviceRegistry.FindLogicalServiceByNameScope(selector.ServiceName, scope)
	if !exists {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service is not found by selector")
	}
	return service, nil
}

func (resolver *Resolver) resolveLogicalServiceByLabels(selector pb.ServiceSelector, fallbackScope pb.Scope) (pb.LogicalService, error) {
	scope := selector.Scope
	if strings.TrimSpace(scope.Namespace) == "" {
		scope.Namespace = strings.TrimSpace(fallbackScope.Namespace)
	}
	if strings.TrimSpace(scope.Environment) == "" {
		scope.Environment = strings.TrimSpace(fallbackScope.Environment)
	}
	if strings.TrimSpace(scope.Namespace) == "" || strings.TrimSpace(scope.Environment) == "" {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "selector.scope is required for match_labels lookup")
	}
	matchedServices := make([]pb.LogicalService, 0, 1)
	for _, logicalService := range resolver.serviceRegistry.List() {
		if strings.TrimSpace(logicalService.Scope.Namespace) != strings.TrimSpace(scope.Namespace) ||
			strings.TrimSpace(logicalService.Scope.Environment) != strings.TrimSpace(scope.Environment) {
			continue
		}
		if !matchLabels(logicalService.Labels, selector.MatchLabels) {
			continue
		}
		matchedServices = append(matchedServices, logicalService)
	}
	if len(matchedServices) == 0 {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service is not found by selector.match_labels")
	}
	if len(matchedServices) > 1 {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, "selector.match_labels matches multiple logical services")
	}
	return matchedServices[0], nil
}

func (resolver *Resolver) resolveExternalFallback(
	candidates []scopedRouteCandidate,
	request ingress.RouteLookupRequest,
) (ResolveResult, error) {
	if len(candidates) == 0 {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeIngressRouteMismatch, "no external fallback route candidates")
	}
	disabledNamespaceSet := make(map[string]struct{})
	for _, candidate := range candidates {
		if !resolver.isExternalFallbackEnabled(candidate.RequestScope) {
			disabledNamespace := strings.TrimSpace(candidate.RequestScope.Namespace)
			if disabledNamespace != "" {
				disabledNamespaceSet[disabledNamespace] = struct{}{}
			}
			continue
		}
		result, err := resolver.resolveTarget(candidate.Route, request)
		if err != nil {
			if shouldContinueScopeFallback(err) {
				continue
			}
			return ResolveResult{}, err
		}
		result.Route = candidate.Route
		result.IngressMode = resolveRouteIngressMode(candidate.Route)
		result.IsExternalFallback = true
		result.RequestScope = candidate.RequestScope
		result.MatchedScope = candidate.MatchedScope
		result.ScopeFallbackPath = append([]pb.Scope(nil), candidate.ScopeFallbackPath...)
		if candidate.ScopeIndex > 0 && resolver.metrics != nil {
			resolver.metrics.IncBridgeScopeFallbackTotal()
		}
		return result, nil
	}
	if len(disabledNamespaceSet) > 0 {
		disabledNamespaces := make([]string, 0, len(disabledNamespaceSet))
		for disabledNamespace := range disabledNamespaceSet {
			disabledNamespaces = append(disabledNamespaces, disabledNamespace)
		}
		sort.Strings(disabledNamespaces)
		return ResolveResult{}, ltfperrors.New(
			ltfperrors.CodeIngressRouteMismatch,
			fmt.Sprintf("external fallback is disabled for namespace(s)=%s", strings.Join(disabledNamespaces, ",")),
		)
	}
	return ResolveResult{}, ltfperrors.New(
		ltfperrors.CodeIngressRouteMismatch,
		"no external fallback route passes resolver filters",
	)
}

func matchLabels(labels map[string]string, selector map[string]string) bool {
	for key, expected := range selector {
		if strings.TrimSpace(labels[key]) != strings.TrimSpace(expected) {
			return false
		}
	}
	return true
}

func mergeStringMaps(left map[string]string, right map[string]string) map[string]string {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	merged := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func buildServiceInstanceSelectorSet(defaultAlgorithm string) map[string]ServiceInstanceSelector {
	return map[string]ServiceInstanceSelector{
		ServiceInstanceSelectorAlgorithmRoundRobin: NewRoundRobinServiceInstanceSelector(),
		ServiceInstanceSelectorAlgorithmRandom:     NewRandomServiceInstanceSelector(),
		ServiceInstanceSelectorAlgorithmSticky:     NewStickyServiceInstanceSelector(),
		ServiceInstanceSelectorAlgorithmWeighted:   NewWeightedServiceInstanceSelector(),
	}
}

func normalizeDefaultLoadBalancePolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case ServiceInstanceSelectorAlgorithmRandom:
		return ServiceInstanceSelectorAlgorithmRandom
	case ServiceInstanceSelectorAlgorithmSticky:
		return ServiceInstanceSelectorAlgorithmSticky
	case ServiceInstanceSelectorAlgorithmWeighted:
		return ServiceInstanceSelectorAlgorithmWeighted
	default:
		return ServiceInstanceSelectorAlgorithmRoundRobin
	}
}

func resolveTargetLoadBalancePolicy(target *pb.ConnectorServiceTarget) string {
	if target == nil {
		return ""
	}
	return strings.TrimSpace(target.LoadBalancePolicy)
}

func normalizeLoadBalancePolicy(policy string) string {
	switch strings.ToLower(strings.TrimSpace(policy)) {
	case ServiceInstanceSelectorAlgorithmRandom:
		return ServiceInstanceSelectorAlgorithmRandom
	case ServiceInstanceSelectorAlgorithmSticky:
		return ServiceInstanceSelectorAlgorithmSticky
	case ServiceInstanceSelectorAlgorithmWeighted:
		return ServiceInstanceSelectorAlgorithmWeighted
	case "", ServiceInstanceSelectorAlgorithmRoundRobin:
		return ServiceInstanceSelectorAlgorithmRoundRobin
	default:
		return ServiceInstanceSelectorAlgorithmRoundRobin
	}
}

func resolveStickySelectionKey(route pb.Route, request ingress.RouteLookupRequest) string {
	stickyBy := "client_ip"
	if parsedPolicy, ok := parseConnectorSelectionPolicy(route.PolicyJSON); ok && strings.TrimSpace(parsedPolicy.StickyBy) != "" {
		stickyBy = strings.TrimSpace(parsedPolicy.StickyBy)
	}
	switch {
	case strings.EqualFold(stickyBy, "client_ip"):
		return strings.TrimSpace(request.Metadata[RouteLookupMetadataClientIPKey])
	case strings.HasPrefix(strings.ToLower(stickyBy), "header:"):
		return resolveHeaderStickyValue(request.Headers, strings.TrimSpace(stickyBy[len("header:"):]))
	case strings.HasPrefix(strings.ToLower(stickyBy), "cookie:"):
		return resolveCookieStickyValue(request.Headers, strings.TrimSpace(stickyBy[len("cookie:"):]))
	default:
		return ""
	}
}

func parseConnectorSelectionPolicy(rawPolicy string) (connectorSelectionPolicy, bool) {
	normalizedPolicy := strings.TrimSpace(rawPolicy)
	if normalizedPolicy == "" {
		return connectorSelectionPolicy{}, false
	}
	var parsed connectorSelectionPolicy
	if err := json.Unmarshal([]byte(normalizedPolicy), &parsed); err != nil {
		return connectorSelectionPolicy{}, false
	}
	return parsed, true
}

func resolveHeaderStickyValue(headers map[string][]string, headerName string) string {
	normalizedHeaderName := strings.TrimSpace(headerName)
	if normalizedHeaderName == "" {
		return ""
	}
	for key, values := range headers {
		if !strings.EqualFold(strings.TrimSpace(key), normalizedHeaderName) {
			continue
		}
		for _, value := range values {
			normalizedValue := strings.TrimSpace(value)
			if normalizedValue != "" {
				return normalizedValue
			}
		}
	}
	return ""
}

func resolveCookieStickyValue(headers map[string][]string, cookieName string) string {
	normalizedCookieName := strings.TrimSpace(cookieName)
	if normalizedCookieName == "" {
		return ""
	}
	cookieHeaderValue := resolveHeaderStickyValue(headers, "Cookie")
	if cookieHeaderValue == "" {
		return ""
	}
	cookieRequest := &http.Request{Header: http.Header{"Cookie": []string{cookieHeaderValue}}}
	cookie, err := cookieRequest.Cookie(normalizedCookieName)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cookie.Value)
}
