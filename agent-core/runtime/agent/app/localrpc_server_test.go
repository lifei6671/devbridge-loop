package app

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/events"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
)

// TestDispatchRequestAppShutdown 验证 localrpc 的 app.shutdown 会触发 runtime 关闭。
func TestDispatchRequestAppShutdown(testingObject *testing.T) {
	testingObject.Parallel()
	runtimeInstance, err := BootstrapWithOptions(context.Background(), DefaultConfig(), BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	server := &localRPCServer{runtime: runtimeInstance}
	payload, failure := server.dispatchRequest(localRPCRequestBody{
		Method:  "app.shutdown",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch app.shutdown failed: code=%s message=%s", failure.code, failure.message)
	}
	resultPayload, ok := payload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected payload type: %T", payload)
	}
	accepted, _ := resultPayload["accepted"].(bool)
	if !accepted {
		testingObject.Fatalf("unexpected app.shutdown payload accepted=%v", resultPayload["accepted"])
	}
	select {
	case <-runtimeInstance.shutdownCh:
	case <-time.After(500 * time.Millisecond):
		testingObject.Fatalf("runtime shutdown was not triggered by app.shutdown")
	}
}

// TestDispatchRequestTrafficStatsSnapshot 验证 localrpc 会返回 runtime traffic 指标快照。
func TestDispatchRequestTrafficStatsSnapshot(testingObject *testing.T) {
	testingObject.Parallel()

	metrics := obs.NewMetrics()
	metrics.AddAgentTrafficUploadBytes(3000)
	metrics.AddAgentTrafficDownloadBytes(9000)
	runtimeInstance := &Runtime{
		metrics:             metrics,
		trafficStatsLastAt:  time.Now().UTC().Add(-time.Second),
		trafficUploadLast:   1000,
		trafficDownloadLast: 5000,
	}
	server := &localRPCServer{runtime: runtimeInstance}
	payload, failure := server.dispatchRequest(localRPCRequestBody{
		Method:  "traffic.stats.snapshot",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch traffic.stats.snapshot failed: code=%s message=%s", failure.code, failure.message)
	}
	resultPayload, ok := payload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected payload type: %T", payload)
	}
	if resultPayload["source"] != "agent.runtime.traffic" {
		testingObject.Fatalf("unexpected source: %+v", resultPayload["source"])
	}
	if resultPayload["upload_total_bytes"] != uint64(3000) {
		testingObject.Fatalf("unexpected upload_total_bytes: %+v", resultPayload["upload_total_bytes"])
	}
	if resultPayload["download_total_bytes"] != uint64(9000) {
		testingObject.Fatalf("unexpected download_total_bytes: %+v", resultPayload["download_total_bytes"])
	}
}

// TestDispatchRequestDiagnoseLogs 验证 localrpc diagnose.logs 返回 runtime 诊断事件源。
func TestDispatchRequestDiagnoseLogs(testingObject *testing.T) {
	testingObject.Parallel()

	runtimeInstance := &Runtime{
		cfg: Config{
			AgentID: "agent-u4",
		},
	}
	runtimeInstance.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:   events.EventError,
		Module:  events.ModuleAgentRuntimeBridge,
		Code:    events.CodeBridgeStateStale,
		Message: "heartbeat timeout",
	})
	server := &localRPCServer{runtime: runtimeInstance}
	payload, failure := server.dispatchRequest(localRPCRequestBody{
		Method:  "diagnose.logs",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch diagnose.logs failed: code=%s message=%s", failure.code, failure.message)
	}
	resultPayload, ok := payload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected payload type: %T", payload)
	}
	items, ok := resultPayload["items"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected items payload type: %T", resultPayload["items"])
	}
	if len(items) == 0 {
		testingObject.Fatalf("expected diagnose.logs returns runtime events")
	}
	if items[0]["code"] != events.CodeBridgeStateStale {
		testingObject.Fatalf("unexpected diagnose event code: %+v", items[0]["code"])
	}
	if resultPayload["source"] != "agent.runtime.diagnose" {
		testingObject.Fatalf("unexpected diagnose source: %+v", resultPayload["source"])
	}
}

// TestDispatchRequestServiceAdd 验证 localrpc service.add 可写入 runtime 服务目录并对外可见。
func TestDispatchRequestServiceAdd(testingObject *testing.T) {
	testingObject.Parallel()

	runtimeInstance, err := BootstrapWithOptions(context.Background(), DefaultConfig(), BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	server := &localRPCServer{runtime: runtimeInstance}
	_, failure := server.dispatchRequest(localRPCRequestBody{
		Method: "service.add",
		Payload: json.RawMessage(`{
			"instance_id":"inst-order-service",
			"service_name":"order-service",
			"scope":{"namespace":"dev","environment":"demo"},
			"protocol":"http",
			"host":"127.0.0.1",
			"port":18080,
			"sni_name":"order.dev.example.com",
			"health_check_interval_sec":15,
			"health_check_mode":"http",
			"health_check_path":"healthz"
		}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.add failed: code=%s message=%s", failure.code, failure.message)
	}

	payload, failure := server.dispatchRequest(localRPCRequestBody{
		Method:  "service.list",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.list failed: code=%s message=%s", failure.code, failure.message)
	}
	resultPayload, ok := payload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected payload type: %T", payload)
	}
	services, ok := resultPayload["services"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected services payload type: %T", resultPayload["services"])
	}
	if len(services) != 1 {
		testingObject.Fatalf("unexpected service size: got=%d want=1", len(services))
	}
	if services[0]["service_name"] != "order-service" {
		testingObject.Fatalf("unexpected service_name: %+v", services[0]["service_name"])
	}
	if services[0]["sni_name"] != "order.dev.example.com" {
		testingObject.Fatalf("unexpected sni_name: %+v", services[0]["sni_name"])
	}
	if services[0]["health_check_mode"] != "http" {
		testingObject.Fatalf("unexpected health_check_mode: %+v", services[0]["health_check_mode"])
	}
	if services[0]["health_check_interval_sec"] != uint32(15) {
		testingObject.Fatalf("unexpected health_check_interval_sec: %+v", services[0]["health_check_interval_sec"])
	}
	if services[0]["health_check_path"] != "/healthz" {
		testingObject.Fatalf("unexpected health_check_path: %+v", services[0]["health_check_path"])
	}
	if services[0]["instance_id"] != "inst-order-service" {
		testingObject.Fatalf("unexpected instance_id: %+v", services[0]["instance_id"])
	}
}

// TestDispatchRequestServiceDelete 验证 localrpc service.delete 可删除本地服务目录项。
func TestDispatchRequestServiceDelete(testingObject *testing.T) {
	testingObject.Parallel()

	runtimeInstance, err := BootstrapWithOptions(context.Background(), DefaultConfig(), BootstrapOptions{})
	if err != nil {
		testingObject.Fatalf("bootstrap runtime failed: %v", err)
	}
	server := &localRPCServer{runtime: runtimeInstance}
	_, failure := server.dispatchRequest(localRPCRequestBody{
		Method: "service.add",
		Payload: json.RawMessage(`{
			"instance_id":"inst-order-service",
			"service_name":"order-service",
			"scope":{"namespace":"dev","environment":"demo"},
			"protocol":"http",
			"host":"127.0.0.1",
			"port":18080
		}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.add failed: code=%s message=%s", failure.code, failure.message)
	}

	listPayload, failure := server.dispatchRequest(localRPCRequestBody{
		Method:  "service.list",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.list failed: code=%s message=%s", failure.code, failure.message)
	}
	listBody, ok := listPayload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected list payload type: %T", listPayload)
	}
	services, ok := listBody["services"].([]map[string]any)
	if !ok || len(services) != 1 {
		testingObject.Fatalf("unexpected services payload: %+v", listBody["services"])
	}
	instanceID, _ := services[0]["instance_id"].(string)
	if instanceID == "" {
		testingObject.Fatalf("unexpected empty instance_id: %+v", services[0]["instance_id"])
	}

	_, failure = server.dispatchRequest(localRPCRequestBody{
		Method: "service.delete",
		Payload: json.RawMessage(fmt.Sprintf(`{
			"instance_id":"%s"
		}`, instanceID)),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.delete failed: code=%s message=%s", failure.code, failure.message)
	}

	listPayload, failure = server.dispatchRequest(localRPCRequestBody{
		Method:  "service.list",
		Payload: json.RawMessage(`{}`),
	}, &localRPCConnectionAuthState{authenticated: true})
	if failure != nil {
		testingObject.Fatalf("dispatch service.list failed: code=%s message=%s", failure.code, failure.message)
	}
	listBody, ok = listPayload.(map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected payload type after delete: %T", listPayload)
	}
	services, ok = listBody["services"].([]map[string]any)
	if !ok {
		testingObject.Fatalf("unexpected services payload type after delete: %T", listBody["services"])
	}
	if len(services) != 0 {
		testingObject.Fatalf("expected service catalog empty after delete, got=%d", len(services))
	}
}
