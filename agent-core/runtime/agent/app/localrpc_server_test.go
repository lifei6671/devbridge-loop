package app

import (
	"context"
	"encoding/json"
	"testing"
	"time"
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
