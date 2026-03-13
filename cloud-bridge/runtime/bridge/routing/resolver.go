package routing

import (
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// ResolverOptions 定义 Resolver 依赖。
type ResolverOptions struct {
	Matcher         *Matcher
	Selector        *Selector
	HybridResolver  *HybridResolver
	RouteRegistry   *registry.RouteRegistry
	ServiceRegistry *registry.ServiceRegistry
	SessionRegistry *registry.SessionRegistry
}

// ConnectorResolution 描述 connector_service 解析结果。
type ConnectorResolution struct {
	Service pb.Service
	Session registry.SessionRuntime
}

// HybridResolution 描述 hybrid_group 解析结果。
type HybridResolution struct {
	Primary        ConnectorResolution
	Fallback       pb.ExternalServiceTarget
	FallbackPolicy pb.FallbackPolicy
}

// ResolveResult 描述 RouteResolver 输出。
type ResolveResult struct {
	Route       pb.Route
	TargetKind  pb.RouteTargetType
	IngressMode pb.IngressMode
	Connector   *ConnectorResolution
	External    *pb.ExternalServiceTarget
	Hybrid      *HybridResolution
}

// Resolver 负责入口请求到目标类型的分类和过滤。
type Resolver struct {
	matcher         *Matcher
	selector        *Selector
	hybridResolver  *HybridResolver
	routeRegistry   *registry.RouteRegistry
	serviceRegistry *registry.ServiceRegistry
	sessionRegistry *registry.SessionRegistry
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
	hybridResolver := options.HybridResolver
	if hybridResolver == nil {
		hybridResolver = NewHybridResolver(pb.FallbackPolicyPreOpenOnly)
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
	return &Resolver{
		matcher:         matcher,
		selector:        selector,
		hybridResolver:  hybridResolver,
		routeRegistry:   routeRegistry,
		serviceRegistry: serviceRegistry,
		sessionRegistry: sessionRegistry,
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
	orderedCandidates := make([]pb.Route, 0, len(candidates))
	orderedCandidates = append(orderedCandidates, candidates...)
	var firstFilterError error
	for len(orderedCandidates) > 0 {
		route, selected := resolver.selector.Select(orderedCandidates)
		if !selected {
			break
		}
		if err := validateRequestScope(route, request); err != nil {
			if firstFilterError == nil {
				firstFilterError = err
			}
			orderedCandidates = orderedCandidates[1:]
			continue
		}
		result, err := resolver.resolveTarget(route)
		if err != nil {
			if firstFilterError == nil {
				firstFilterError = err
			}
			orderedCandidates = orderedCandidates[1:]
			continue
		}
		result.Route = route
		result.IngressMode = resolveRouteIngressMode(route)
		return result, nil
	}
	if firstFilterError != nil {
		return ResolveResult{}, firstFilterError
	}
	return ResolveResult{}, ltfperrors.New(
		ltfperrors.CodeIngressRouteMismatch,
		"no route passes resolver filters",
	)
}

func (resolver *Resolver) resolveTarget(route pb.Route) (ResolveResult, error) {
	targetType := normalizeRouteTargetType(route.Target)
	switch targetType {
	case pb.RouteTargetTypeConnectorService:
		connector, err := resolver.resolveConnectorTarget(route, route.Target.ConnectorService)
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
	case pb.RouteTargetTypeHybridGroup:
		if route.Target.HybridGroup == nil {
			return ResolveResult{}, ltfperrors.New(
				ltfperrors.CodeInvalidPayload,
				"route target type is hybrid_group but hybrid_group payload is empty",
			)
		}
		primary, err := resolver.resolveConnectorTarget(route, &route.Target.HybridGroup.PrimaryConnectorService)
		if err != nil {
			return ResolveResult{}, err
		}
		fallback, err := resolveExternalTarget(route, &route.Target.HybridGroup.FallbackExternalService)
		if err != nil {
			return ResolveResult{}, err
		}
		fallbackPolicy := route.Target.HybridGroup.FallbackPolicy
		if !resolver.hybridResolver.AllowPreOpenFallback(fallbackPolicy) {
			return ResolveResult{}, ltfperrors.New(
				ltfperrors.CodeHybridFallbackForbidden,
				"hybrid fallback policy forbids pre-open fallback",
			)
		}
		return ResolveResult{
			TargetKind: pb.RouteTargetTypeHybridGroup,
			Hybrid: &HybridResolution{
				Primary:        *primary,
				Fallback:       *fallback,
				FallbackPolicy: fallbackPolicy,
			},
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
) (*ConnectorResolution, error) {
	if target == nil {
		return nil, ltfperrors.New(
			ltfperrors.CodeInvalidPayload,
			"connector_service target payload is empty",
		)
	}
	normalizedServiceKey := strings.TrimSpace(target.ServiceKey)
	if normalizedServiceKey == "" {
		return nil, ltfperrors.New(
			ltfperrors.CodeMissingRequiredField,
			"connector_service.service_key is required",
		)
	}
	serviceSnapshot, exists := resolver.serviceRegistry.GetByServiceKey(normalizedServiceKey)
	if !exists {
		return nil, ltfperrors.New(
			ltfperrors.CodeResolveServiceNotFound,
			fmt.Sprintf("service not found for key=%s", normalizedServiceKey),
		)
	}
	if err := validate.ValidateRouteScope(
		route.Namespace,
		route.Environment,
		serviceSnapshot.Namespace,
		serviceSnapshot.Environment,
	); err != nil {
		return nil, err
	}
	if serviceSnapshot.Status != pb.ServiceStatusActive || serviceSnapshot.HealthStatus != pb.HealthStatusHealthy {
		return nil, ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf(
				"service unavailable for key=%s status=%s health=%s",
				normalizedServiceKey,
				serviceSnapshot.Status,
				serviceSnapshot.HealthStatus,
			),
		)
	}
	normalizedConnectorID := strings.TrimSpace(serviceSnapshot.ConnectorID)
	if normalizedConnectorID == "" {
		return nil, ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("service key=%s has empty connector_id", normalizedServiceKey),
		)
	}
	sessionSnapshot, exists := resolver.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !exists {
		return nil, ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("connector offline for connector_id=%s", normalizedConnectorID),
		)
	}
	if sessionSnapshot.State != registry.SessionActive {
		return nil, ltfperrors.New(
			ltfperrors.CodeResolveSessionNotActive,
			fmt.Sprintf("session not active for connector_id=%s state=%s", normalizedConnectorID, sessionSnapshot.State),
		)
	}
	return &ConnectorResolution{
		Service: serviceSnapshot,
		Session: sessionSnapshot,
	}, nil
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
		targetNamespace = strings.TrimSpace(route.Namespace)
	}
	targetEnvironment := strings.TrimSpace(target.Environment)
	if targetEnvironment == "" {
		targetEnvironment = strings.TrimSpace(route.Environment)
	}
	if err := validate.ValidateRouteScope(route.Namespace, route.Environment, targetNamespace, targetEnvironment); err != nil {
		return nil, err
	}
	copied := *target
	copied.Namespace = targetNamespace
	copied.Environment = targetEnvironment
	copied.Selector = copyStringMap(target.Selector)
	return &copied, nil
}

func validateRequestScope(route pb.Route, request ingress.RouteLookupRequest) error {
	normalizedNamespace := strings.TrimSpace(request.Namespace)
	if normalizedNamespace != "" && normalizedNamespace != strings.TrimSpace(route.Namespace) {
		return ltfperrors.New(
			ltfperrors.CodeInvalidScope,
			fmt.Sprintf("request namespace=%s mismatches route namespace=%s", normalizedNamespace, route.Namespace),
		)
	}
	normalizedEnvironment := strings.TrimSpace(request.Environment)
	if normalizedEnvironment != "" && normalizedEnvironment != strings.TrimSpace(route.Environment) {
		return ltfperrors.New(
			ltfperrors.CodeInvalidScope,
			fmt.Sprintf("request environment=%s mismatches route environment=%s", normalizedEnvironment, route.Environment),
		)
	}
	return nil
}

func normalizeRouteTargetType(target pb.RouteTarget) pb.RouteTargetType {
	normalizedType := pb.RouteTargetType(strings.TrimSpace(string(target.Type)))
	switch normalizedType {
	case pb.RouteTargetTypeConnectorService, pb.RouteTargetTypeExternalService, pb.RouteTargetTypeHybridGroup:
		return normalizedType
	}
	// 兼容早期 route target 未设置 type 的场景，按 payload 推断。
	switch {
	case target.ConnectorService != nil:
		return pb.RouteTargetTypeConnectorService
	case target.ExternalService != nil:
		return pb.RouteTargetTypeExternalService
	case target.HybridGroup != nil:
		return pb.RouteTargetTypeHybridGroup
	default:
		return normalizedType
	}
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
