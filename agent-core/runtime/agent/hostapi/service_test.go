package hostapi

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

type testRuntimeHost struct {
	shutdownCalled       bool
	serviceListResult    map[string]any
	trafficResult        map[string]any
	diagnoseLogResult    map[string]any
	configSnapshotResult map[string]any
	updateConfigInput    ConfigUpdateInput
	updateConfigResult   map[string]any
	addServiceInput      AddServiceInput
	addServiceResult     map[string]any
	deleteServiceInput   DeleteServiceInput
	deleteServiceResult  map[string]any
}

func (host *testRuntimeHost) AgentSnapshot(context.Context) (map[string]any, error) {
	return map[string]any{"source": "agent.runtime"}, nil
}

func (host *testRuntimeHost) SessionSnapshot(context.Context) (map[string]any, error) {
	return map[string]any{"source": "agent.runtime"}, nil
}

func (host *testRuntimeHost) RequestBridgeReconnect(context.Context, bool) error {
	return nil
}

func (host *testRuntimeHost) RequestBridgeDrain(context.Context) error {
	return nil
}

func (host *testRuntimeHost) Shutdown(context.Context) error {
	host.shutdownCalled = true
	return nil
}

func (host *testRuntimeHost) AddService(_ context.Context, input AddServiceInput) (map[string]any, error) {
	host.addServiceInput = input
	if host.addServiceResult == nil {
		return map[string]any{"accepted": true}, nil
	}
	return host.addServiceResult, nil
}

func (host *testRuntimeHost) ListServices(context.Context) (map[string]any, error) {
	if host.serviceListResult == nil {
		return map[string]any{"services": []map[string]any{}}, nil
	}
	return host.serviceListResult, nil
}

func (host *testRuntimeHost) DeleteService(_ context.Context, input DeleteServiceInput) (map[string]any, error) {
	host.deleteServiceInput = input
	if host.deleteServiceResult == nil {
		return map[string]any{"accepted": true}, nil
	}
	return host.deleteServiceResult, nil
}

func (host *testRuntimeHost) ListTunnels(context.Context) (map[string]any, error) {
	return map[string]any{"tunnels": []map[string]any{}}, nil
}

func (host *testRuntimeHost) TrafficStatsSnapshot(context.Context) (map[string]any, error) {
	if host.trafficResult == nil {
		return map[string]any{"source": "agent.runtime.traffic"}, nil
	}
	return host.trafficResult, nil
}

func (host *testRuntimeHost) ConfigSnapshot(context.Context, ConfigSnapshotInput) (map[string]any, error) {
	if host.configSnapshotResult == nil {
		return map[string]any{"source": "agent.runtime"}, nil
	}
	return host.configSnapshotResult, nil
}

func (host *testRuntimeHost) UpdateConfig(_ context.Context, input ConfigUpdateInput) (map[string]any, error) {
	host.updateConfigInput = input
	if host.updateConfigResult == nil {
		return map[string]any{"accepted": true}, nil
	}
	return host.updateConfigResult, nil
}

func (host *testRuntimeHost) DiagnoseSnapshot(context.Context) (map[string]any, error) {
	return map[string]any{"source": "agent.runtime.diagnose"}, nil
}

func (host *testRuntimeHost) DiagnoseLogs(context.Context) (map[string]any, error) {
	if host.diagnoseLogResult == nil {
		return map[string]any{"items": []map[string]any{}}, nil
	}
	return host.diagnoseLogResult, nil
}

func TestHandleAppShutdown(testingObject *testing.T) {
	testingObject.Parallel()

	host := &testRuntimeHost{}
	service := NewService(host)
	response, failure := service.Handle(context.Background(), Request{
		Method:  MethodAppShutdown,
		Payload: json.RawMessage(`{}`),
	})
	if failure != nil {
		testingObject.Fatalf("expected no failure, got=%+v", *failure)
	}
	if !host.shutdownCalled {
		testingObject.Fatalf("expected shutdown to be called")
	}
	accepted, _ := response.Payload.(map[string]any)["accepted"].(bool)
	if !accepted {
		testingObject.Fatalf("expected accepted=true, got=%+v", response.Payload)
	}
}

func TestHandleServiceAdd(testingObject *testing.T) {
	testingObject.Parallel()

	host := &testRuntimeHost{
		addServiceResult: map[string]any{"accepted": true, "service_name": "order-service"},
	}
	service := NewService(host)
	response, failure := service.Handle(context.Background(), Request{
		Method: MethodServiceAdd,
		Payload: json.RawMessage(`{
			"instance_id":"inst-order-service",
			"service_name":"order-service",
			"scope":{"namespace":"dev","environment":"demo"},
			"protocol":"https",
			"host":"127.0.0.1",
			"port":18080,
			"sni_name":"order.dev.example.com",
			"exposure":{
				"ingress_mode":"l7_shared",
				"host":"api.dev.example.com",
				"path_prefix":"/orders",
				"allow_export":true
			},
			"route_hint":{
				"priority":9,
				"match_headers":[{"name":"x-tenant","exact":"demo"}],
				"match_queries":[{"name":"version","prefix":"v2"}]
			},
			"health_check_interval_sec":15,
			"health_check_mode":"http",
			"health_check_path":"healthz"
		}`),
	})
	if failure != nil {
		testingObject.Fatalf("expected no failure, got=%+v", *failure)
	}
	if response.Payload.(map[string]any)["service_name"] != "order-service" {
		testingObject.Fatalf("unexpected response payload: %+v", response.Payload)
	}
	if host.addServiceInput.ServiceName != "order-service" {
		testingObject.Fatalf("unexpected service_name: %s", host.addServiceInput.ServiceName)
	}
	if host.addServiceInput.Scope != (pb.Scope{Namespace: "dev", Environment: "demo"}) {
		testingObject.Fatalf("unexpected scope: %+v", host.addServiceInput.Scope)
	}
	if host.addServiceInput.RouteHint.Priority != 9 {
		testingObject.Fatalf("unexpected route hint: %+v", host.addServiceInput.RouteHint)
	}
	if host.addServiceInput.Exposure.Host != "api.dev.example.com" {
		testingObject.Fatalf("unexpected exposure: %+v", host.addServiceInput.Exposure)
	}
}

func TestHandleServiceDelete(testingObject *testing.T) {
	testingObject.Parallel()

	host := &testRuntimeHost{
		deleteServiceResult: map[string]any{"accepted": true, "deleted": true},
	}
	service := NewService(host)
	response, failure := service.Handle(context.Background(), Request{
		Method:  MethodServiceDelete,
		Payload: json.RawMessage(`{"instance_id":"inst-order-service"}`),
	})
	if failure != nil {
		testingObject.Fatalf("expected no failure, got=%+v", *failure)
	}
	if response.Payload.(map[string]any)["deleted"] != true {
		testingObject.Fatalf("unexpected response payload: %+v", response.Payload)
	}
	if host.deleteServiceInput.InstanceID != "inst-order-service" {
		testingObject.Fatalf("unexpected delete input: %+v", host.deleteServiceInput)
	}
}

func TestHandleConfigUpdate(testingObject *testing.T) {
	testingObject.Parallel()

	host := &testRuntimeHost{
		updateConfigResult: map[string]any{"accepted": true, "config_file_path": "/tmp/agent.yaml"},
	}
	service := NewService(host)
	response, failure := service.Handle(context.Background(), Request{
		Method: MethodConfigUpdate,
		Payload: json.RawMessage(`{
			"updated_by":"admin",
			"config":{
				"agent_id":"agent-web",
				"bridge_addr":"127.0.0.1:49081",
				"bridge_transport":"tcp_framed",
				"session":{"auth_method":"token","auth_token":"yaml-token"},
				"ui":{"web":{"enabled":true,"listen_addr":"127.0.0.1:49082","auth":{"username":"admin","password":"change-me"}}}
			}
		}`),
	})
	if failure != nil {
		testingObject.Fatalf("expected no failure, got=%+v", *failure)
	}
	if response.Payload.(map[string]any)["config_file_path"] != "/tmp/agent.yaml" {
		testingObject.Fatalf("unexpected response payload: %+v", response.Payload)
	}
	if host.updateConfigInput.UpdatedBy != "admin" {
		testingObject.Fatalf("unexpected updated_by: %s", host.updateConfigInput.UpdatedBy)
	}
	if string(host.updateConfigInput.Config) == "" || !json.Valid(host.updateConfigInput.Config) {
		testingObject.Fatalf("unexpected config payload: %s", string(host.updateConfigInput.Config))
	}
	if string(host.updateConfigInput.Config) == "{}" {
		testingObject.Fatalf("unexpected empty config payload")
	}
}

func TestHandleReturnsMethodNotAllowed(testingObject *testing.T) {
	testingObject.Parallel()

	service := NewService(&testRuntimeHost{})
	_, failure := service.Handle(context.Background(), Request{
		Method:  Method("unknown.method"),
		Payload: json.RawMessage(`{}`),
	})
	if failure == nil {
		testingObject.Fatalf("expected failure for unknown method")
	}
	if failure.Code != "METHOD_NOT_ALLOWED" {
		testingObject.Fatalf("unexpected failure code: %+v", *failure)
	}
}
