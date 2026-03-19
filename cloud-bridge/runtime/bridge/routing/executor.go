package routing

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrPathExecutorDependencyMissing 表示路径执行器依赖缺失。
	ErrPathExecutorDependencyMissing = errors.New("path executor dependency missing")
)

const (
	// trafficOpenMetadataInstanceIDKey 定义 TrafficOpen 元数据中的 instance_id 键。
	trafficOpenMetadataInstanceIDKey = "instance_id"
)

// ConnectorDispatcher 定义 connector 路径执行入口。
type ConnectorDispatcher interface {
	Dispatch(ctx context.Context, request connectorproxy.DispatchRequest) (connectorproxy.DispatchResult, error)
}

// DirectExecutor 定义 external_service 路径执行入口。
type DirectExecutor interface {
	Execute(ctx context.Context, request directproxy.ExecuteRequest) (directproxy.ExecuteResult, error)
}

// PathExecuteRequest 描述路径执行请求。
type PathExecuteRequest struct {
	Resolution  ResolveResult
	TrafficOpen pb.TrafficOpen
}

// PathExecuteResult 描述路径执行结果。
type PathExecuteResult struct {
	TargetKind      pb.RouteTargetType
	ConnectorResult *connectorproxy.DispatchResult
	DirectResult    *directproxy.ExecuteResult
	HTTPStatus      int
	ErrorCode       string
}

// PathExecutorOptions 定义路径执行器参数。
type PathExecutorOptions struct {
	Connector     ConnectorDispatcher
	Direct        DirectExecutor
	FailureMapper *FailureMapper
	Metrics       *obs.Metrics
}

// PathExecutor 统一编排 connector/direct/hybrid 三路径。
type PathExecutor struct {
	connector     ConnectorDispatcher
	direct        DirectExecutor
	failureMapper *FailureMapper
	metrics       *obs.Metrics
}

// NewPathExecutor 创建路径执行器。
func NewPathExecutor(options PathExecutorOptions) (*PathExecutor, error) {
	if options.Connector == nil || options.Direct == nil {
		return nil, ErrPathExecutorDependencyMissing
	}
	failureMapper := options.FailureMapper
	if failureMapper == nil {
		failureMapper = NewFailureMapper()
	}
	return &PathExecutor{
		connector:     options.Connector,
		direct:        options.Direct,
		failureMapper: failureMapper,
		metrics:       normalizeRoutingMetrics(options.Metrics),
	}, nil
}

// Execute 执行目标路径并处理 hybrid fallback。
func (executor *PathExecutor) Execute(ctx context.Context, request PathExecuteRequest) (PathExecuteResult, error) {
	if executor == nil || executor.connector == nil || executor.direct == nil {
		return PathExecuteResult{}, ErrPathExecutorDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	switch request.Resolution.TargetKind {
	case pb.RouteTargetTypeConnectorService:
		return executor.executeConnector(normalizedContext, request)
	case pb.RouteTargetTypeExternalService:
		return executor.executeExternal(normalizedContext, request)
	default:
		return PathExecuteResult{}, ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported target kind for path executor: %s", request.Resolution.TargetKind),
		)
	}
}

func (executor *PathExecutor) executeConnector(ctx context.Context, request PathExecuteRequest) (PathExecuteResult, error) {
	if request.Resolution.Connector == nil {
		return PathExecuteResult{}, ErrPathExecutorDependencyMissing
	}
	connectorID := strings.TrimSpace(request.Resolution.Connector.Session.ConnectorID)
	dispatchResult, err := executor.connector.Dispatch(ctx, connectorproxy.DispatchRequest{
		ConnectorID: connectorID,
		TrafficOpen: request.TrafficOpen,
	})
	result := PathExecuteResult{
		TargetKind:      pb.RouteTargetTypeConnectorService,
		ConnectorResult: &dispatchResult,
		HTTPStatus:      dispatchResult.HTTPStatus,
		ErrorCode:       dispatchResult.ErrorCode,
	}
	if err != nil {
		return executor.failResult(result, err, request.TrafficOpen)
	}
	if result.HTTPStatus <= 0 {
		result.HTTPStatus = 200
	}
	return result, nil
}

func (executor *PathExecutor) executeExternal(ctx context.Context, request PathExecuteRequest) (PathExecuteResult, error) {
	if request.Resolution.External == nil {
		return PathExecuteResult{}, ErrPathExecutorDependencyMissing
	}
	directResult, err := executor.direct.Execute(ctx, directproxy.ExecuteRequest{
		TrafficID:          strings.TrimSpace(request.TrafficOpen.TrafficID),
		Target:             *request.Resolution.External,
		RequestScope:       request.Resolution.RequestScope,
		MatchedScope:       request.Resolution.MatchedScope,
		RouteID:            strings.TrimSpace(request.Resolution.Route.RouteID),
		IsExternalFallback: request.Resolution.IsExternalFallback,
	})
	result := PathExecuteResult{
		TargetKind:   pb.RouteTargetTypeExternalService,
		DirectResult: &directResult,
		HTTPStatus:   directResult.HTTPStatus,
		ErrorCode:    directResult.ErrorCode,
	}
	if err != nil {
		return executor.failResult(result, err, request.TrafficOpen)
	}
	if result.HTTPStatus <= 0 {
		result.HTTPStatus = 200
	}
	return result, nil
}

func (executor *PathExecutor) failResult(
	result PathExecuteResult,
	err error,
	trafficOpen pb.TrafficOpen,
) (PathExecuteResult, error) {
	if executor == nil || executor.failureMapper == nil {
		return result, err
	}
	mappedFailure := executor.failureMapper.Map(err, result)
	result.HTTPStatus = mappedFailure.HTTPStatus
	result.ErrorCode = mappedFailure.Code
	// 路由执行失败统一记录失败原因维度，便于按服务池/实例做故障归因。
	executor.metrics.ObserveBridgeRouteFailureReason(
		strings.TrimSpace(trafficOpen.LogicalServiceID),
		strings.TrimSpace(trafficOpen.InstanceID),
		strings.TrimSpace(result.ErrorCode),
	)
	return result, err
}

// normalizeRoutingMetrics 归一化 PathExecutor 指标依赖，未注入时回落默认指标容器。
func normalizeRoutingMetrics(metrics *obs.Metrics) *obs.Metrics {
	if metrics == nil {
		return obs.DefaultMetrics
	}
	return metrics
}
