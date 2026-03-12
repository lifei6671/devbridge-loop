package routing

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/discovery"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/fallback"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RegistryReader 定义 resolver 依赖的最小 registry 读取接口。
type RegistryReader interface {
	GetServiceByKey(serviceKey string) (pb.Service, bool)
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
	ServiceID        string
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
	_ = ctx // 当前实现无阻塞操作，先保留 context 便于后续扩展。
	route := request.Route
	switch route.Target.Type {
	case pb.RouteTargetTypeConnectorService:
		// connector_service 由 server 选择 service 与 connector，不选 endpoint。
		return resolver.resolveConnector(route, route.Target.ConnectorService, request)
	case pb.RouteTargetTypeExternalService:
		// external_service 由 server 自己查询并直连代理，不向 agent 发 TrafficOpen。
		return resolver.resolveExternal(route, route.Target.ExternalService)
	case pb.RouteTargetTypeHybridGroup:
		// hybrid_group 先走 connector，满足 pre-open 信号才允许回退 external。
		return resolver.resolveHybrid(route, request)
	default:
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeUnsupportedValue, fmt.Sprintf("unsupported route target type: %s", route.Target.Type))
	}
}

// resolveConnector 解析 connector_service 路径并生成 TrafficOpen。
func (resolver *Resolver) resolveConnector(route pb.Route, target *pb.ConnectorServiceTarget, request ResolveRequest) (ResolveResult, error) {
	if target == nil {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "route.target.connectorService is required")
	}
	serviceKey := strings.TrimSpace(target.ServiceKey)
	if serviceKey == "" {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "connectorService.serviceKey is required")
	}

	service, exists := resolver.registry.GetServiceByKey(serviceKey)
	if !exists {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceNotFound, "service is not found by serviceKey")
	}
	if strings.TrimSpace(service.Namespace) != strings.TrimSpace(route.Namespace) || strings.TrimSpace(service.Environment) != strings.TrimSpace(route.Environment) {
		// route 与 service scope 不一致时必须拒绝，避免跨 scope 引用。
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeInvalidScope, "route scope does not match service scope")
	}
	if service.Status != pb.ServiceStatusActive || service.HealthStatus != pb.HealthStatusHealthy {
		// service 非 ACTIVE 或健康非 HEALTHY 时不得参与 connector 解析。
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "service is not active or healthy")
	}

	connector, exists := resolver.registry.GetConnectorByID(service.ConnectorID)
	if !exists {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "connector is not found")
	}
	if isConnectorOffline(connector) {
		// connector 离线时必须按 service unavailable 处理。
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveServiceUnavailable, "connector is offline")
	}

	session, exists := resolver.registry.FindActiveSessionByConnector(service.ConnectorID)
	if !exists || session.State != pb.SessionStateActive {
		// session 非 ACTIVE 时不得给 agent 下发新流量。
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeResolveSessionNotActive, "connector session is not active")
	}

	trafficOpen := pb.TrafficOpen{
		TrafficID:             strings.TrimSpace(request.TrafficID),
		RouteID:               strings.TrimSpace(route.RouteID),
		ServiceID:             strings.TrimSpace(service.ServiceID),
		SourceAddr:            strings.TrimSpace(request.SourceAddr),
		ProtocolHint:          strings.TrimSpace(request.ProtocolHint),
		TraceID:               strings.TrimSpace(request.TraceID),
		EndpointSelectionHint: request.EndpointSelectionHint,
	}
	return ResolveResult{
		PathKind:     pb.RouteTargetTypeConnectorService,
		RouteID:      strings.TrimSpace(route.RouteID),
		ServiceID:    strings.TrimSpace(service.ServiceID),
		ConnectorID:  strings.TrimSpace(service.ConnectorID),
		SessionID:    strings.TrimSpace(session.SessionID),
		SessionEpoch: session.SessionEpoch,
		TrafficOpen:  &trafficOpen,
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
		// target 未显式声明 namespace 时默认继承 route scope。
		namespace = strings.TrimSpace(route.Namespace)
	}
	environment := strings.TrimSpace(target.Environment)
	if environment == "" {
		// target 未显式声明 environment 时默认继承 route scope。
		environment = strings.TrimSpace(route.Environment)
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

// resolveHybrid 解析 hybrid_group 并在 pre-open 场景下执行 fallback 判定。
func (resolver *Resolver) resolveHybrid(route pb.Route, request ResolveRequest) (ResolveResult, error) {
	if route.Target.HybridGroup == nil {
		return ResolveResult{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "route.target.hybridGroup is required")
	}
	hybrid := route.Target.HybridGroup
	primaryResult, primaryErr := resolver.resolveConnector(route, &hybrid.PrimaryConnectorService, request)
	if primaryErr == nil {
		// primary 解析成功时保持 connector 路径。
		primaryResult.PathKind = pb.RouteTargetTypeConnectorService
		return primaryResult, nil
	}

	signal, signalErr := mapFallbackSignal(primaryErr)
	if signalErr != nil {
		return ResolveResult{}, primaryErr
	}
	allowed, allowErr := fallback.CanFallback(hybrid.FallbackPolicy, signal)
	if allowErr != nil || !allowed {
		// policy 不允许回退时直接返回 primary 错误，避免静默降级。
		return ResolveResult{}, primaryErr
	}

	fallbackResult, fallbackErr := resolver.resolveExternal(route, &hybrid.FallbackExternalService)
	if fallbackErr != nil {
		return ResolveResult{}, fallbackErr
	}
	fallbackResult.FallbackUsed = true
	fallbackResult.FallbackReason = string(signal)
	fallbackResult.PrimaryErrorCode = ltfperrors.ExtractCode(primaryErr)
	fallbackResult.PathKind = pb.RouteTargetTypeExternalService
	return fallbackResult, nil
}

// mapFallbackSignal 将 primary 失败映射为 hybrid fallback 信号。
func mapFallbackSignal(resolveErr error) (fallback.Signal, error) {
	switch {
	case ltfperrors.IsCode(resolveErr, ltfperrors.CodeResolveServiceNotFound):
		// service miss 属于 route resolve miss，可参与 pre-open fallback。
		return fallback.SignalResolveMiss, nil
	case ltfperrors.IsCode(resolveErr, ltfperrors.CodeResolveServiceUnavailable):
		// service unavailable 属于 pre-open 信号，可参与 fallback。
		return fallback.SignalServiceUnavailable, nil
	case ltfperrors.IsCode(resolveErr, ltfperrors.CodeResolveSessionNotActive):
		// session 非 ACTIVE 也视为 service unavailable 信号。
		return fallback.SignalServiceUnavailable, nil
	default:
		return "", ltfperrors.New(ltfperrors.CodeUnsupportedValue, "resolve error is not fallback eligible")
	}
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
