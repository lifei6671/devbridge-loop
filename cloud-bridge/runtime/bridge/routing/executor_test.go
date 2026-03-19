package routing

import (
	"context"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/connectorproxy"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/directproxy"
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
func TestPathExecutorExternalServicePath(t *testing.T) {
	t.Parallel()

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
		t.Fatalf("new path executor failed: %v", err)
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
			TrafficID:        "traffic-external-1",
			LogicalServiceID: "ls-1",
			InstanceID:       "inst-1",
		},
	})
	if err != nil {
		t.Fatalf("execute external path failed: %v", err)
	}
	if result.TargetKind != pb.RouteTargetTypeExternalService {
		t.Fatalf("unexpected target kind: %s", result.TargetKind)
	}
	if result.DirectResult == nil || result.DirectResult.Endpoint.EndpointID != "ep-1" {
		t.Fatalf("unexpected direct result: %+v", result.DirectResult)
	}
	if connectorDispatcher.calls != 0 {
		t.Fatalf("connector path should not be called in external_service")
	}
	if directExecutor.calls != 1 {
		t.Fatalf("direct executor should be called once, got=%d", directExecutor.calls)
	}
}
