package routing

import (
	"context"
	"errors"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type routingTestConnectorDispatcher struct {
	result connectorproxy.DispatchResult
	err    error
	calls  int
}

func (dispatcher *routingTestConnectorDispatcher) Dispatch(
	ctx context.Context,
	request connectorproxy.DispatchRequest,
) (connectorproxy.DispatchResult, error) {
	_ = ctx
	_ = request
	dispatcher.calls++
	return dispatcher.result, dispatcher.err
}

type routingTestDirectExecutor struct {
	result     directproxy.ExecuteResult
	err        error
	calls      int
	lastTarget pb.ExternalServiceTarget
}

func (executor *routingTestDirectExecutor) Execute(
	ctx context.Context,
	request directproxy.ExecuteRequest,
) (directproxy.ExecuteResult, error) {
	_ = ctx
	executor.calls++
	executor.lastTarget = request.Target
	return executor.result, executor.err
}

// TestPathExecutorExternalServicePath 验证 external_service 直接走 direct executor。
func TestPathExecutorExternalServicePath(testingObject *testing.T) {
	testingObject.Parallel()
	connectorDispatcher := &routingTestConnectorDispatcher{}
	directExecutor := &routingTestDirectExecutor{
		result: directproxy.ExecuteResult{
			Endpoint: directproxy.ExternalEndpoint{
				EndpointID: "ep-1",
				Address:    "10.0.0.10:443",
			},
		},
	}
	executor, err := NewPathExecutor(PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		testingObject.Fatalf("new path executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), PathExecuteRequest{
		Resolution: ResolveResult{
			TargetKind: pb.RouteTargetTypeExternalService,
			External: &pb.ExternalServiceTarget{
				Provider:    "k8s",
				Namespace:   "dev",
				Environment: "alice",
				ServiceName: "pay",
			},
		},
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-external-1",
			ServiceID: "svc-1",
		},
	})
	if err != nil {
		testingObject.Fatalf("execute external path failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeExternalService {
		testingObject.Fatalf("unexpected target kind: %s", result.TargetKind)
	}
	if result.DirectResult == nil || result.DirectResult.Endpoint.EndpointID != "ep-1" {
		testingObject.Fatalf("unexpected direct result: %+v", result.DirectResult)
	}
	if connectorDispatcher.calls != 0 {
		testingObject.Fatalf("connector path should not be called in external_service")
	}
	if directExecutor.calls != 1 {
		testingObject.Fatalf("direct executor should be called once, got=%d", directExecutor.calls)
	}
}

// TestPathExecutorHybridFallbackPreOpenNoTunnel 验证 no-idle 触发 pre-open 未分配 fallback。
func TestPathExecutorHybridFallbackPreOpenNoTunnel(testingObject *testing.T) {
	testingObject.Parallel()
	connectorDispatcher := &routingTestConnectorDispatcher{
		result: connectorproxy.DispatchResult{
			HTTPStatus: 503,
			ErrorCode:  connectorproxy.FailureCodeNoIdleTunnel,
		},
		err: connectorproxy.ErrNoIdleTunnel,
	}
	directExecutor := &routingTestDirectExecutor{
		result: directproxy.ExecuteResult{
			Endpoint: directproxy.ExternalEndpoint{
				EndpointID: "ep-fallback-1",
				Address:    "10.0.0.20:443",
			},
		},
	}
	executor, err := NewPathExecutor(PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		testingObject.Fatalf("new path executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), PathExecuteRequest{
		Resolution: buildHybridResolution(pb.FallbackPolicyPreOpenOnly),
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-hybrid-no-idle",
			ServiceID: "svc-1",
		},
	})
	if err != nil {
		testingObject.Fatalf("hybrid fallback failed: %v", err)
	}
	if !result.UsedHybridFallback {
		testingObject.Fatalf("expected hybrid fallback used")
	}
	if result.HybridFallbackStage != HybridFallbackStagePreOpenNoTunnel {
		testingObject.Fatalf("unexpected fallback stage: %s", result.HybridFallbackStage)
	}
	if result.DirectResult == nil || result.DirectResult.Endpoint.EndpointID != "ep-fallback-1" {
		testingObject.Fatalf("unexpected direct fallback result: %+v", result.DirectResult)
	}
	if directExecutor.calls != 1 {
		testingObject.Fatalf("expected direct executor called once, got=%d", directExecutor.calls)
	}
}

// TestPathExecutorHybridFallbackPreOpenWithTunnel 验证 pre-open 已分配 tunnel 失败允许 fallback。
func TestPathExecutorHybridFallbackPreOpenWithTunnel(testingObject *testing.T) {
	testingObject.Parallel()
	connectorDispatcher := &routingTestConnectorDispatcher{
		result: connectorproxy.DispatchResult{
			TunnelID:   "tunnel-1",
			HTTPStatus: 504,
			ErrorCode:  connectorproxy.FailureCodeOpenAckTimeout,
		},
		err: connectorproxy.ErrOpenAckTimeout,
	}
	directExecutor := &routingTestDirectExecutor{
		result: directproxy.ExecuteResult{
			Endpoint: directproxy.ExternalEndpoint{
				EndpointID: "ep-fallback-2",
				Address:    "10.0.0.21:443",
			},
		},
	}
	executor, err := NewPathExecutor(PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		testingObject.Fatalf("new path executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), PathExecuteRequest{
		Resolution: buildHybridResolution(pb.FallbackPolicyPreOpenOnly),
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-hybrid-open-timeout",
			ServiceID: "svc-1",
		},
	})
	if err != nil {
		testingObject.Fatalf("hybrid fallback with tunnel failed: %v", err)
	}
	if !result.UsedHybridFallback {
		testingObject.Fatalf("expected hybrid fallback used")
	}
	if result.HybridFallbackStage != HybridFallbackStagePreOpenWithTunnel {
		testingObject.Fatalf("unexpected fallback stage: %s", result.HybridFallbackStage)
	}
}

// TestPathExecutorHybridPostOpenFailureNoFallback 验证 post-open 失败禁止 fallback。
func TestPathExecutorHybridPostOpenFailureNoFallback(testingObject *testing.T) {
	testingObject.Parallel()
	connectorDispatcher := &routingTestConnectorDispatcher{
		result: connectorproxy.DispatchResult{
			TunnelID: "tunnel-active-1",
			OpenAck: &pb.TrafficOpenAck{
				TrafficID: "traffic-hybrid-post-open",
				Success:   true,
			},
			HTTPStatus: 502,
			ErrorCode:  connectorproxy.FailureCodeRelayFailed,
		},
		err: connectorproxy.ErrRelayReset,
	}
	directExecutor := &routingTestDirectExecutor{}
	executor, err := NewPathExecutor(PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		testingObject.Fatalf("new path executor failed: %v", err)
	}
	result, err := executor.Execute(context.Background(), PathExecuteRequest{
		Resolution: buildHybridResolution(pb.FallbackPolicyPreOpenOnly),
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-hybrid-post-open",
			ServiceID: "svc-1",
		},
	})
	if !errors.Is(err, connectorproxy.ErrRelayReset) {
		testingObject.Fatalf("expected post-open primary error, got=%v", err)
	}
	if result.UsedHybridFallback {
		testingObject.Fatalf("post-open failure must not fallback")
	}
	if directExecutor.calls != 0 {
		testingObject.Fatalf("direct fallback should not be called on post-open failure")
	}
}

// TestPathExecutorHybridPolicyForbidden 验证 non-pre_open_only 策略会被拒绝。
func TestPathExecutorHybridPolicyForbidden(testingObject *testing.T) {
	testingObject.Parallel()
	connectorDispatcher := &routingTestConnectorDispatcher{}
	directExecutor := &routingTestDirectExecutor{}
	executor, err := NewPathExecutor(PathExecutorOptions{
		Connector: connectorDispatcher,
		Direct:    directExecutor,
	})
	if err != nil {
		testingObject.Fatalf("new path executor failed: %v", err)
	}
	_, err = executor.Execute(context.Background(), PathExecuteRequest{
		Resolution: buildHybridResolution(pb.FallbackPolicy("forbidden_policy")),
		TrafficOpen: pb.TrafficOpen{
			TrafficID: "traffic-hybrid-policy-forbidden",
			ServiceID: "svc-1",
		},
	})
	if ltfperrors.ExtractCode(err) != ltfperrors.CodeHybridFallbackForbidden {
		testingObject.Fatalf("unexpected error code: got=%s err=%v", ltfperrors.ExtractCode(err), err)
	}
	if connectorDispatcher.calls != 0 || directExecutor.calls != 0 {
		testingObject.Fatalf("forbidden policy should short-circuit before path execution")
	}
}

func buildHybridResolution(policy pb.FallbackPolicy) ResolveResult {
	return ResolveResult{
		TargetKind: pb.RouteTargetTypeHybridGroup,
		Hybrid: &HybridResolution{
			Primary: ConnectorResolution{
				Service: pb.Service{
					ServiceID:   "svc-1",
					ServiceKey:  "dev/alice/order",
					ConnectorID: "connector-1",
				},
				Session: registry.SessionRuntime{
					SessionID:   "session-1",
					ConnectorID: "connector-1",
					State:       registry.SessionActive,
				},
			},
			Fallback: pb.ExternalServiceTarget{
				Provider:    "k8s",
				Namespace:   "dev",
				Environment: "alice",
				ServiceName: "pay-fallback",
			},
			FallbackPolicy: policy,
		},
	}
}
