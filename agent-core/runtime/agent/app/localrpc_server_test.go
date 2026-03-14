package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"

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
