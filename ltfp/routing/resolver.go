package routing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/discovery"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RegistryReader 定义 resolver 依赖的最小 registry 读取接口。
type RegistryReader interface {
	GetLogicalServiceByID(logicalServiceID string) (pb.LogicalService, bool)
	FindLogicalServiceByNameScope(serviceName string, scope pb.Scope) (pb.LogicalService, bool)
	ListServiceInstancesByLogicalService(logicalServiceID string) []pb.ServiceInstance
	GetConnectorByID(connectorID string) (pb.Connector, bool)
	FindActiveSessionByConnector(connectorID string) (pb.Session, bool)
}

// ResolveRequest 描述 route resolve 输入。
type ResolveRequest struct {
	Route                 pb.Route
	TrafficID             string
	SourceAddr            string
	ProtocolHint          string
	TraceID               string
	EndpointSelectionHint map[string]string
}

// ResolveResult 描述 route resolve 输出。
type ResolveResult struct {
	PathKind         pb.RouteTargetType
	RouteID          string
	LogicalServiceID string
	InstanceID       string
	ConnectorID      string
	SessionID        string
	SessionEpoch     uint64
	TrafficOpen      *pb.TrafficOpen
	ExternalQuery    *discovery.QueryRequest
	FallbackUsed     bool
	FallbackReason   string
	PrimaryErrorCode string
}

// Resolver 负责 route target 到数据路径的纯函数解析。
type Resolver struct {
	registry RegistryReader
}

// NewResolver 创建 route resolver。
func NewResolver(registry RegistryReader) *Resolver {
	return &Resolver{registry: registry}
}

// Resolve 根据 route target 解析 connector 或 direct proxy 执行计划。
func (resolver *Resolver) Resolve(ctx context.Context, request ResolveRequest) (ResolveResult, error) {
	_ = ctx
	route := request.Route
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		return resolver.resolveConnector(route, route.Target.ConnectorService, request)
	case pb.RouteTargetTypeExternalService:
		return resolver.resolveExternal(route, route.Target.ExternalService)
	default:
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported route target type: %s", route.Target.Type))
	}
}

// resolveConnector 解析 connector_service 路径并生成 TrafficOpen。
func (resolver *Resolver) resolveConnector(route pb.Route, target *pb.ConnectorServiceTarget, request ResolveRequest) (ResolveResult, error) {
	if target == nil {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "route.target.connectorService is required")
	}
	logicalService, err := resolver.resolveLogicalService(target.Selector, route.Scope)
	if err != nil {
		return ResolveResult{}, err
	}
	if err := validateRouteToLogicalServiceScope(route.Scope, logicalService.Scope); err != nil {
		return ResolveResult{}, err
	}
	if logicalService.Status != pb.ServiceStatusActive {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "logical service is not active")
	}
	instance, err := resolver.pickHealthyInstance(logicalService.LogicalServiceID, target.InstanceSelector)
	if err != nil {
		return ResolveResult{}, err
	}

	connector, exists := resolver.registry.GetConnectorByID(instance.ConnectorID)
	if !exists {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "connector is not found")
	}
	if isConnectorOffline(connector) {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "connector is offline")
	}
	session, exists := resolver.registry.FindActiveSessionByConnector(instance.ConnectorID)
	if !exists || session.State != pb.SessionStateActive {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveSessionNotActive, "connector session is not active")
	}

	trafficOpen := pb.TrafficOpen{
		TrafficID:             strings.TrimSpace(request.TrafficID),
		RouteID:               strings.TrimSpace(route.RouteID),
		LogicalServiceID:      strings.TrimSpace(logicalService.LogicalServiceID),
		InstanceID:            strings.TrimSpace(instance.InstanceID),
		SourceAddr:            strings.TrimSpace(request.SourceAddr),
		ProtocolHint:          strings.TrimSpace(request.ProtocolHint),
		TraceID:               strings.TrimSpace(request.TraceID),
		EndpointSelectionHint: request.EndpointSelectionHint,
	}
	return ResolveResult{
		PathKind:         pb.RouteTargetTypeConnectorService,
		RouteID:          strings.TrimSpace(route.RouteID),
		LogicalServiceID: strings.TrimSpace(logicalService.LogicalServiceID),
		InstanceID:       strings.TrimSpace(instance.InstanceID),
		ConnectorID:      strings.TrimSpace(instance.ConnectorID),
		SessionID:        strings.TrimSpace(session.SessionID),
		SessionEpoch:     session.SessionEpoch,
		TrafficOpen:      &trafficOpen,
	}, nil
}

// resolveExternal 解析 external_service 路径并生成 discovery 查询参数。
func (resolver *Resolver) resolveExternal(route pb.Route, target *pb.ExternalServiceTarget) (ResolveResult, error) {
	if target == nil {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "route.target.externalService is required")
	}
	serviceName := strings.TrimSpace(target.ServiceName)
	if serviceName == "" {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "externalService.serviceName is required")
	}

	namespace := strings.TrimSpace(target.Namespace)
	if namespace == "" {
		namespace = strings.TrimSpace(route.Scope.Namespace)
	}
	environment := strings.TrimSpace(target.Environment)
	if environment == "" {
		environment = strings.TrimSpace(route.Scope.Environment)
	}

	query := discovery.QueryRequest{
		Provider:        strings.TrimSpace(target.Provider),
		Namespace:       namespace,
		Environment:     environment,
		ServiceName:     serviceName,
		Group:           strings.TrimSpace(target.Group),
		CacheTTL:        time.Duration(target.CacheTTLSeconds) * time.Second,
		StaleIfErrorTTL: time.Duration(target.StaleIfErrorSec) * time.Second,
	}
	return ResolveResult{
		PathKind:      pb.RouteTargetTypeExternalService,
		RouteID:       strings.TrimSpace(route.RouteID),
		ExternalQuery: &query,
	}, nil
}

// resolveLogicalService 根据 selector 查找逻辑服务。
func (resolver *Resolver) resolveLogicalService(selector pb.ServiceSelector, fallbackScope pb.Scope) (pb.LogicalService, error) {
	if strings.TrimSpace(selector.LogicalServiceID) != "" {
		service, exists := resolver.registry.GetLogicalServiceByID(selector.LogicalServiceID)
		if !exists {
			return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service is not found by logicalServiceId")
		}
		return service, nil
	}
	if strings.TrimSpace(selector.ServiceName) == "" {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connectorService.selector is required")
	}
	scope := selector.Scope
	if strings.TrimSpace(scope.Namespace) == "" {
		scope.Namespace = strings.TrimSpace(fallbackScope.Namespace)
	}
	if strings.TrimSpace(scope.Environment) == "" {
		scope.Environment = strings.TrimSpace(fallbackScope.Environment)
	}
	service, exists := resolver.registry.FindLogicalServiceByNameScope(selector.ServiceName, scope)
	if !exists {
		return pb.LogicalService{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "logical service is not found by selector")
	}
	return service, nil
}

// pickHealthyInstance 选择一个可路由实例。
func (resolver *Resolver) pickHealthyInstance(logicalServiceID string, instanceSelector map[string]string) (pb.ServiceInstance, error) {
	instances := resolver.registry.ListServiceInstancesByLogicalService(logicalServiceID)
	for _, instance := range instances {
		if instance.InstanceStatus != pb.ServiceStatusActive || instance.HealthStatus != pb.HealthStatusHealthy {
			continue
		}
		if !matchInstanceLabels(instance.Labels, instanceSelector) {
			continue
		}
		return instance, nil
	}
	return pb.ServiceInstance{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "no healthy instance is available")
}

// validateRouteToLogicalServiceScope 校验 route 与 logical service scope 一致性。
func validateRouteToLogicalServiceScope(routeScope pb.Scope, serviceScope pb.Scope) error {
	normalizedRouteNamespace := strings.TrimSpace(routeScope.Namespace)
	normalizedServiceNamespace := strings.TrimSpace(serviceScope.Namespace)
	if normalizedRouteNamespace != "" && normalizedServiceNamespace != "" && normalizedRouteNamespace != normalizedServiceNamespace {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route scope does not match logical service scope")
	}
	normalizedRouteEnvironment := strings.TrimSpace(routeScope.Environment)
	normalizedServiceEnvironment := strings.TrimSpace(serviceScope.Environment)
	if normalizedRouteEnvironment != "" && normalizedServiceEnvironment != "" && normalizedRouteEnvironment != normalizedServiceEnvironment {
		return ltfperrors.New(ltfperrors.CodeInvalidScope, "route scope does not match logical service scope")
	}
	return nil
}

// matchInstanceLabels 校验实例标签是否满足选择条件。
func matchInstanceLabels(labels map[string]string, selector map[string]string) bool {
	for key, expected := range selector {
		if strings.TrimSpace(labels[key]) != strings.TrimSpace(expected) {
			return false
		}
	}
	return true
}

// isConnectorOffline 判断 connector 当前是否离线。
func isConnectorOffline(connector pb.Connector) bool {
	status := strings.ToLower(strings.TrimSpace(connector.Status))
	switch status {
	case "offline", "stale", "closed":
		return true
	default:
		return false
	}
}
