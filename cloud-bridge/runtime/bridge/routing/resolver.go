package routing

import (
	"fmt"
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
)

// ResolverOptions 定义 Resolver 依赖。
type ResolverOptions struct {
	Matcher                          *Matcher
	Selector                         *Selector
	HybridResolver                   *HybridResolver
	RouteRegistry                    *registry.RouteRegistry
	ServiceRegistry                  *registry.ServiceRegistry
	SessionRegistry                  *registry.SessionRegistry
	Metrics                          *obs.Metrics
	ServiceInstanceSelector          ServiceInstanceSelector
	ServiceInstanceSelectorAlgorithm string
	TrafficAffinityTTL               time.Duration
	TrafficAffinityCapacity          int
}

// ConnectorResolution 描述 connector_service 解析结果。
type ConnectorResolution struct {
	Service           pb.Service
	Session           registry.SessionRuntime
	ServiceInstanceID string
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
	matcher                 *Matcher
	selector                *Selector
	hybridResolver          *HybridResolver
	routeRegistry           *registry.RouteRegistry
	serviceRegistry         *registry.ServiceRegistry
	sessionRegistry         *registry.SessionRegistry
	metrics                 *obs.Metrics
	serviceInstanceSelector ServiceInstanceSelector
	trafficAffinity         *trafficAffinityStore
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
	serviceInstanceSelector := options.ServiceInstanceSelector
	if serviceInstanceSelector == nil {
		// 未显式注入选择器时按算法名构造，默认回退轮询。
		serviceInstanceSelector = NewServiceInstanceSelectorByAlgorithm(options.ServiceInstanceSelectorAlgorithm)
	}
	trafficAffinity := newTrafficAffinityStore(options.TrafficAffinityTTL, options.TrafficAffinityCapacity)
	return &Resolver{
		matcher:                 matcher,
		selector:                selector,
		hybridResolver:          hybridResolver,
		routeRegistry:           routeRegistry,
		serviceRegistry:         serviceRegistry,
		sessionRegistry:         sessionRegistry,
		metrics:                 normalizeResolverMetrics(options.Metrics),
		serviceInstanceSelector: serviceInstanceSelector,
		trafficAffinity:         trafficAffinity,
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
		result, err := resolver.resolveTarget(route, request)
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
	case pb.RouteTargetTypeHybridGroup:
		if route.Target.HybridGroup == nil {
			return ResolveResult{}, ltfperrors.New(
				ltfperrors.CodeInvalidPayload,
				"route target type is hybrid_group but hybrid_group payload is empty",
			)
		}
		primary, err := resolver.resolveConnectorTarget(route, &route.Target.HybridGroup.PrimaryConnectorService, request)
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
	request ingress.RouteLookupRequest,
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
	serviceInstances := resolver.serviceRegistry.ListInstancesByServiceKey(normalizedServiceKey)
	serviceIDForMetrics := resolveServiceIDFromInstanceSnapshots(serviceInstances)
	if serviceIDForMetrics == "" {
		if serviceSnapshot, exists := resolver.serviceRegistry.GetByServiceKey(normalizedServiceKey); exists {
			serviceIDForMetrics = strings.TrimSpace(serviceSnapshot.ServiceID)
		}
	}
	if len(serviceInstances) == 0 {
		resolveErr := ltfperrors.New(
			ltfperrors.CodeResolveServiceNotFound,
			fmt.Sprintf("service not found for key=%s", normalizedServiceKey),
		)
		// 无实例时只记录服务池维度失败原因，实例维度不存在。
		resolver.observeRouteFailure(serviceIDForMetrics, "", resolveErr)
		return nil, resolveErr
	}
	var firstFilterError error
	candidates := make([]ConnectorResolution, 0, len(serviceInstances))
	for _, serviceInstance := range serviceInstances {
		serviceSnapshot := serviceInstance.Service
		if err := validate.ValidateRouteScope(
			route.Namespace,
			route.Environment,
			serviceSnapshot.Namespace,
			serviceSnapshot.Environment,
		); err != nil {
			if firstFilterError == nil {
				firstFilterError = err
			}
			continue
		}
		if serviceSnapshot.Status != pb.ServiceStatusActive || serviceSnapshot.HealthStatus != pb.HealthStatusHealthy {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveServiceUnavailable,
					fmt.Sprintf(
						"service unavailable for key=%s status=%s health=%s",
						normalizedServiceKey,
						serviceSnapshot.Status,
						serviceSnapshot.HealthStatus,
					),
				)
			}
			continue
		}
		normalizedConnectorID := strings.TrimSpace(serviceSnapshot.ConnectorID)
		if normalizedConnectorID == "" {
			if firstFilterError == nil {
				firstFilterError = ltfperrors.New(
					ltfperrors.CodeResolveServiceUnavailable,
					fmt.Sprintf("service key=%s has empty connector_id", normalizedServiceKey),
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
			Service:           serviceSnapshot,
			Session:           sessionSnapshot,
			ServiceInstanceID: strings.TrimSpace(serviceInstance.ServiceInstanceID),
		})
	}
	// 每次解析都回刷服务池可用实例快照，保持可用数与路由过滤口径一致。
	resolver.observeServiceAvailability(serviceIDForMetrics, candidates)
	if len(candidates) == 0 {
		if firstFilterError != nil {
			resolver.observeRouteFailure(serviceIDForMetrics, "", firstFilterError)
			return nil, firstFilterError
		}
		resolveErr := ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("service unavailable for key=%s", normalizedServiceKey),
		)
		resolver.observeRouteFailure(serviceIDForMetrics, "", resolveErr)
		return nil, resolveErr
	}
	selected, err := resolver.selectConnectorResolution(candidates, normalizedServiceKey, request)
	if err != nil {
		return nil, err
	}
	resolver.observeRouteHit(strings.TrimSpace(selected.Service.ServiceID), strings.TrimSpace(selected.ServiceInstanceID))
	return &selected, nil
}

// selectConnectorResolution 在候选实例内选择目标，并维持同 traffic_id 的实例粘性。
func (resolver *Resolver) selectConnectorResolution(
	candidates []ConnectorResolution,
	serviceKey string,
	request ingress.RouteLookupRequest,
) (ConnectorResolution, error) {
	if len(candidates) == 0 {
		return ConnectorResolution{}, ltfperrors.New(
			ltfperrors.CodeResolveServiceUnavailable,
			fmt.Sprintf("service unavailable for key=%s", strings.TrimSpace(serviceKey)),
		)
	}
	trafficID := resolveLookupTrafficID(request)
	if trafficID != "" && resolver != nil && resolver.trafficAffinity != nil {
		// 已建立粘性映射时必须复用同一实例，避免单条 traffic 在生命周期中漂移。
		if stickyInstanceID, exists := resolver.trafficAffinity.Load(trafficID, time.Now().UTC()); exists {
			for _, candidate := range candidates {
				if strings.TrimSpace(candidate.ServiceInstanceID) == stickyInstanceID {
					return candidate, nil
				}
			}
			resolveErr := ltfperrors.New(
				ltfperrors.CodeResolveServiceUnavailable,
				fmt.Sprintf(
					"sticky traffic target unavailable for traffic_id=%s service_key=%s service_instance_id=%s",
					trafficID,
					strings.TrimSpace(serviceKey),
					stickyInstanceID,
				),
			)
			// 粘性实例失活时补记实例级失败原因，便于排障定位。
			resolver.observeRouteFailure(resolveServiceIDFromCandidates(candidates), stickyInstanceID, resolveErr)
			return ConnectorResolution{}, resolveErr
		}
	}
	selected := candidates[0]
	if resolver != nil && resolver.serviceInstanceSelector != nil {
		selected = resolver.serviceInstanceSelector.Select(candidates)
	}
	if trafficID != "" &&
		resolver != nil &&
		resolver.trafficAffinity != nil &&
		strings.TrimSpace(selected.ServiceInstanceID) != "" {
		// 首次命中后写入粘性映射，后续同 traffic_id 解析将固定到同一实例。
		resolver.trafficAffinity.Store(
			trafficID,
			strings.TrimSpace(selected.ServiceInstanceID),
			time.Now().UTC(),
		)
	}
	return selected, nil
}

// resolveServiceSession 解析服务实例对应会话，优先命中实例记录的 session_id。
func (resolver *Resolver) resolveServiceSession(
	serviceInstance registry.ServiceInstanceSnapshot,
	connectorID string,
) (registry.SessionRuntime, bool) {
	normalizedSessionID := strings.TrimSpace(serviceInstance.SessionID)
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
	// 兼容历史记录：实例未带 session_id 时回退 connector_id 反查。
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
	routeNamespace := strings.TrimSpace(route.Namespace)
	if normalizedNamespace != "" && routeNamespace != "" && normalizedNamespace != routeNamespace {
		return ltfperrors.New(
			ltfperrors.CodeInvalidScope,
			fmt.Sprintf("request namespace=%s mismatches route namespace=%s", normalizedNamespace, route.Namespace),
		)
	}
	normalizedEnvironment := strings.TrimSpace(request.Environment)
	routeEnvironment := strings.TrimSpace(route.Environment)
	if normalizedEnvironment != "" && routeEnvironment != "" && normalizedEnvironment != routeEnvironment {
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

// resolveLookupTrafficID 从 route lookup metadata 提取 traffic_id。
func resolveLookupTrafficID(request ingress.RouteLookupRequest) string {
	if len(request.Metadata) == 0 {
		return ""
	}
	return strings.TrimSpace(request.Metadata[RouteLookupMetadataTrafficIDKey])
}

// normalizeResolverMetrics 归一化 resolver 指标依赖，未注入时回落默认容器。
func normalizeResolverMetrics(metrics *obs.Metrics) *obs.Metrics {
	if metrics == nil {
		return obs.DefaultMetrics
	}
	return metrics
}

// observeRouteHit 记录路由命中指标（服务池+实例维度）。
func (resolver *Resolver) observeRouteHit(serviceID string, serviceInstanceID string) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	resolver.metrics.ObserveBridgeRouteHit(serviceID, serviceInstanceID)
}

// observeRouteFailure 记录路由失败原因指标（服务池+实例维度）。
func (resolver *Resolver) observeRouteFailure(serviceID string, serviceInstanceID string, err error) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	reason := ltfperrors.ExtractCode(err)
	if strings.TrimSpace(reason) == "" {
		reason = "resolve_failed"
	}
	resolver.metrics.ObserveBridgeRouteFailureReason(serviceID, serviceInstanceID, reason)
}

// observeServiceAvailability 刷新服务池当前可用实例快照。
func (resolver *Resolver) observeServiceAvailability(serviceID string, candidates []ConnectorResolution) {
	if resolver == nil || resolver.metrics == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	availableServiceInstanceIDs := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		normalizedServiceInstanceID := strings.TrimSpace(candidate.ServiceInstanceID)
		if normalizedServiceInstanceID == "" {
			continue
		}
		availableServiceInstanceIDs = append(availableServiceInstanceIDs, normalizedServiceInstanceID)
	}
	resolver.metrics.SetBridgeServiceAvailableInstances(normalizedServiceID, availableServiceInstanceIDs)
}

// resolveServiceIDFromInstanceSnapshots 从实例快照切片提取服务池主键。
func resolveServiceIDFromInstanceSnapshots(serviceInstances []registry.ServiceInstanceSnapshot) string {
	for _, serviceInstance := range serviceInstances {
		normalizedServiceID := strings.TrimSpace(serviceInstance.Service.ServiceID)
		if normalizedServiceID != "" {
			return normalizedServiceID
		}
	}
	return ""
}

// resolveServiceIDFromCandidates 从候选实例切片提取服务池主键。
func resolveServiceIDFromCandidates(candidates []ConnectorResolution) string {
	for _, candidate := range candidates {
		normalizedServiceID := strings.TrimSpace(candidate.Service.ServiceID)
		if normalizedServiceID != "" {
			return normalizedServiceID
		}
	}
	return ""
}
