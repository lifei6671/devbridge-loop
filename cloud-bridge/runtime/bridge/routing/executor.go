package routing

import (
	"context"
	"errors"
	"fmt"
	"log"
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
	// HybridFallbackStagePreOpenNoTunnel 表示 pre-open 失败且未分配 tunnel 的 fallback。
	HybridFallbackStagePreOpenNoTunnel = "pre_open_no_tunnel"
	// HybridFallbackStagePreOpenWithTunnel 表示 pre-open 失败且已分配 tunnel 的 fallback。
	HybridFallbackStagePreOpenWithTunnel = "pre_open_with_tunnel"
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
	TargetKind          pb.RouteTargetType
	ConnectorResult     *connectorproxy.DispatchResult
	DirectResult        *directproxy.ExecuteResult
	UsedHybridFallback  bool
	HybridFallbackStage string
	HTTPStatus          int
	ErrorCode           string
}

// PathExecutorOptions 定义路径执行器参数。
type PathExecutorOptions struct {
	Connector      ConnectorDispatcher
	Direct         DirectExecutor
	HybridResolver *HybridResolver
	FailureMapper  *FailureMapper
	Metrics        *obs.Metrics
}

// PathExecutor 统一编排 connector/direct/hybrid 三路径。
type PathExecutor struct {
	connector      ConnectorDispatcher
	direct         DirectExecutor
	hybridResolver *HybridResolver
	failureMapper  *FailureMapper
	metrics        *obs.Metrics
}

// NewPathExecutor 创建路径执行器。
func NewPathExecutor(options PathExecutorOptions) (*PathExecutor, error) {
	if options.Connector == nil || options.Direct == nil {
		return nil, ErrPathExecutorDependencyMissing
	}
	hybridResolver := options.HybridResolver
	if hybridResolver == nil {
		hybridResolver = NewHybridResolver(pb.FallbackPolicyPreOpenOnly)
	}
	failureMapper := options.FailureMapper
	if failureMapper == nil {
		failureMapper = NewFailureMapper()
	}
	return &PathExecutor{
		connector:      options.Connector,
		direct:         options.Direct,
		hybridResolver: hybridResolver,
		failureMapper:  failureMapper,
		metrics:        normalizeRoutingMetrics(options.Metrics),
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
	case pb.RouteTargetTypeHybridGroup:
		return executor.executeHybrid(normalizedContext, request)
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
		return executor.failResult(result, err)
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
		TrafficID: strings.TrimSpace(request.TrafficOpen.TrafficID),
		Target:    *request.Resolution.External,
	})
	result := PathExecuteResult{
		TargetKind:   pb.RouteTargetTypeExternalService,
		DirectResult: &directResult,
		HTTPStatus:   directResult.HTTPStatus,
		ErrorCode:    directResult.ErrorCode,
	}
	if err != nil {
		return executor.failResult(result, err)
	}
	if result.HTTPStatus <= 0 {
		result.HTTPStatus = 200
	}
	return result, nil
}

func (executor *PathExecutor) executeHybrid(ctx context.Context, request PathExecuteRequest) (PathExecuteResult, error) {
	if request.Resolution.Hybrid == nil {
		return PathExecuteResult{}, ErrPathExecutorDependencyMissing
	}
	if !executor.hybridResolver.AllowPreOpenFallback(request.Resolution.Hybrid.FallbackPolicy) {
		return PathExecuteResult{}, ltfperrors.New(
			ltfperrors.CodeHybridFallbackForbidden,
			"hybrid fallback policy forbids pre-open fallback",
		)
	}
	primaryConnectorID := strings.TrimSpace(request.Resolution.Hybrid.Primary.Session.ConnectorID)
	primaryResult, primaryErr := executor.connector.Dispatch(ctx, connectorproxy.DispatchRequest{
		ConnectorID: primaryConnectorID,
		TrafficOpen: request.TrafficOpen,
	})
	result := PathExecuteResult{
		TargetKind:      pb.RouteTargetTypeHybridGroup,
		ConnectorResult: &primaryResult,
		HTTPStatus:      primaryResult.HTTPStatus,
		ErrorCode:       primaryResult.ErrorCode,
	}
	if primaryErr == nil {
		if result.HTTPStatus <= 0 {
			result.HTTPStatus = 200
		}
		return result, nil
	}

	fallbackStage, allowFallback := classifyHybridFallback(primaryResult, primaryErr)
	if !allowFallback {
		return executor.failResult(result, primaryErr)
	}
	fallbackResult, fallbackErr := executor.direct.Execute(ctx, directproxy.ExecuteRequest{
		TrafficID: strings.TrimSpace(request.TrafficOpen.TrafficID),
		Target:    request.Resolution.Hybrid.Fallback,
	})
	result.DirectResult = &fallbackResult
	if fallbackResult.HTTPStatus > 0 {
		result.HTTPStatus = fallbackResult.HTTPStatus
		result.ErrorCode = fallbackResult.ErrorCode
	}
	if fallbackErr != nil {
		log.Printf(
			"bridge hybrid fallback failed event=hybrid_fallback_execute_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID: strings.TrimSpace(request.TrafficOpen.TrafficID),
				ServiceID: strings.TrimSpace(request.TrafficOpen.ServiceID),
			}),
			fallbackErr,
		)
		return executor.failResult(result, errors.Join(primaryErr, fallbackErr))
	}
	executor.metrics.IncBridgeHybridFallbackTotal()
	result.UsedHybridFallback = true
	result.HybridFallbackStage = fallbackStage
	result.HTTPStatus = 200
	result.ErrorCode = ""
	log.Printf(
		"bridge hybrid fallback success event=hybrid_fallback_used stage=%s %s",
		fallbackStage,
		obs.FormatLogFields(obs.LogFields{
			TrafficID: strings.TrimSpace(request.TrafficOpen.TrafficID),
			ServiceID: strings.TrimSpace(request.TrafficOpen.ServiceID),
		}),
	)
	return result, nil
}

func (executor *PathExecutor) failResult(result PathExecuteResult, err error) (PathExecuteResult, error) {
	if executor == nil || executor.failureMapper == nil {
		return result, err
	}
	mappedFailure := executor.failureMapper.Map(err, result)
	result.HTTPStatus = mappedFailure.HTTPStatus
	result.ErrorCode = mappedFailure.Code
	return result, err
}

func classifyHybridFallback(dispatchResult connectorproxy.DispatchResult, dispatchErr error) (string, bool) {
	if dispatchErr == nil {
		return "", false
	}
	if dispatchResult.OpenAck != nil && dispatchResult.OpenAck.Success {
		return "", false
	}
	if errors.Is(dispatchErr, connectorproxy.ErrNoIdleTunnel) {
		return HybridFallbackStagePreOpenNoTunnel, true
	}
	if errors.Is(dispatchErr, connectorproxy.ErrTrafficOpenRejected) || errors.Is(dispatchErr, connectorproxy.ErrOpenAckTimeout) {
		return HybridFallbackStagePreOpenWithTunnel, true
	}
	// 保守兜底：有 tunnel_id 且未进入 open_ack success，可按 pre-open 已分配处理。
	if strings.TrimSpace(dispatchResult.TunnelID) != "" && (dispatchResult.OpenAck == nil || !dispatchResult.OpenAck.Success) {
		return HybridFallbackStagePreOpenWithTunnel, true
	}
	return "", false
}

// normalizeRoutingMetrics 归一化 PathExecutor 指标依赖，未注入时回落默认指标容器。
func normalizeRoutingMetrics(metrics *obs.Metrics) *obs.Metrics {
	if metrics == nil {
		return obs.DefaultMetrics
	}
	return metrics
}
