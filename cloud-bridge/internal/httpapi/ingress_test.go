package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

type mockBackflowCaller struct {
	lastHTTPBaseURL string
	lastHTTPReq     domain.BackflowHTTPRequest
	httpResp        domain.BackflowHTTPResponse
	httpErr         error

	lastGRPCBaseURL string
	lastGRPCReq     domain.BackflowGRPCRequest
	grpcResp        domain.BackflowGRPCResponse
	grpcErr         error
}

func (m *mockBackflowCaller) ForwardHTTP(_ context.Context, baseURL string, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	m.lastHTTPBaseURL = baseURL
	m.lastHTTPReq = request
	return m.httpResp, m.httpErr
}

func (m *mockBackflowCaller) ForwardGRPC(_ context.Context, baseURL string, request domain.BackflowGRPCRequest) (domain.BackflowGRPCResponse, error) {
	m.lastGRPCBaseURL = baseURL
	m.lastGRPCReq = request
	return m.grpcResp, m.grpcErr
}

func TestIngressHTTP(t *testing.T) {
	stateStore := store.NewMemoryStore()
	_, err := stateStore.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    1,
		ResourceVersion: 1,
		EventID:         "evt-hello-1",
		Payload: map[string]any{
			"tunnelId":        "tunnel-a",
			"backflowBaseUrl": "http://127.0.0.1:19090",
		},
	})
	if err != nil {
		t.Fatalf("process HELLO failed: %v", err)
	}

	_, err = stateStore.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterUpsert,
		SessionEpoch:    1,
		ResourceVersion: 2,
		EventID:         "evt-upsert-1",
		Payload: map[string]any{
			"tunnelId":    "tunnel-a",
			"env":         "dev-alice",
			"serviceName": "user",
			"protocol":    "http",
			"instanceId":  "inst-1",
			"targetPort":  18080,
		},
	})
	if err != nil {
		t.Fatalf("process REGISTER_UPSERT failed: %v", err)
	}

	backflowCaller := &mockBackflowCaller{
		httpResp: domain.BackflowHTTPResponse{
			StatusCode: http.StatusCreated,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: []byte(`{"ok":true}`),
		},
	}

	handler := NewHandler(routing.NewPipeline([]string{"host"}), stateStore, backflowCaller, "")
	req := httptest.NewRequest(http.MethodGet, "http://user.dev-alice.internal/api/v1/ingress/http/order/list?with=detail", nil)
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)

	if resp.Code != http.StatusCreated {
		t.Fatalf("unexpected response status: %d", resp.Code)
	}
	if backflowCaller.lastHTTPReq.TargetPort != 18080 {
		t.Fatalf("unexpected target port: %+v", backflowCaller.lastHTTPReq)
	}
	if backflowCaller.lastHTTPReq.Path != "/order/list" {
		t.Fatalf("unexpected forward path: %+v", backflowCaller.lastHTTPReq)
	}
}

func TestIngressGRPC(t *testing.T) {
	stateStore := store.NewMemoryStore()
	_, err := stateStore.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    1,
		ResourceVersion: 1,
		EventID:         "evt-hello-grpc",
		Payload: map[string]any{
			"tunnelId":        "tunnel-a",
			"backflowBaseUrl": "http://127.0.0.1:19090",
		},
	})
	if err != nil {
		t.Fatalf("process HELLO failed: %v", err)
	}

	_, err = stateStore.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterUpsert,
		SessionEpoch:    1,
		ResourceVersion: 2,
		EventID:         "evt-upsert-grpc",
		Payload: map[string]any{
			"tunnelId":    "tunnel-a",
			"env":         "dev-alice",
			"serviceName": "user",
			"protocol":    "grpc",
			"instanceId":  "inst-1",
			"targetPort":  19090,
		},
	})
	if err != nil {
		t.Fatalf("process REGISTER_UPSERT failed: %v", err)
	}

	backflowCaller := &mockBackflowCaller{
		grpcResp: domain.BackflowGRPCResponse{
			Status:    "SERVING",
			Target:    "127.0.0.1:19090",
			LatencyMs: 8,
		},
	}

	handler := NewHandler(routing.NewPipeline([]string{"host"}), stateStore, backflowCaller, "")
	req := httptest.NewRequest(http.MethodPost, "http://user.dev-alice.internal/api/v1/ingress/grpc", strings.NewReader(`{"healthService":"user.v1.UserService","timeoutMs":1200}`))
	resp := httptest.NewRecorder()
	handler.Router().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected response status: %d", resp.Code)
	}

	var payload domain.IngressGRPCResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Status != "SERVING" {
		t.Fatalf("unexpected grpc status: %+v", payload)
	}
	if payload.ResolvedEnv != "dev-alice" || payload.ServiceName != "user" || payload.Protocol != "grpc" {
		t.Fatalf("unexpected ingress grpc payload: %+v", payload)
	}
	if backflowCaller.lastGRPCReq.TargetPort != 19090 || backflowCaller.lastGRPCReq.Env != "dev-alice" {
		t.Fatalf("unexpected grpc backflow request: %+v", backflowCaller.lastGRPCReq)
	}
	if backflowCaller.lastGRPCReq.HealthService != "user.v1.UserService" || backflowCaller.lastGRPCReq.TimeoutMs != 1200 {
		t.Fatalf("unexpected grpc backflow options: %+v", backflowCaller.lastGRPCReq)
	}
}
