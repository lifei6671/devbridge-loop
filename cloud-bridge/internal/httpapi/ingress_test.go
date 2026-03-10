package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/discovery"
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

type mockDiscoveryResolver struct {
	lastQuery discovery.Query
	endpoint  discovery.Endpoint
	matched   bool
	err       error
}

func (m *mockDiscoveryResolver) Name() string { return "mock-discovery" }

func (m *mockDiscoveryResolver) Resolve(_ context.Context, query discovery.Query) (discovery.Endpoint, bool, error) {
	m.lastQuery = query
	return m.endpoint, m.matched, m.err
}

type mockUpstreamForwarder struct {
	lastHTTPEndpoint discovery.Endpoint
	lastHTTPRequest  domain.BackflowHTTPRequest
	httpResp         domain.BackflowHTTPResponse
	httpErr          error

	lastGRPCEndpoint discovery.Endpoint
	lastGRPCRequest  domain.BackflowGRPCRequest
	grpcResp         domain.BackflowGRPCResponse
	grpcErr          error
}

func (m *mockUpstreamForwarder) ForwardHTTP(_ context.Context, endpoint discovery.Endpoint, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	m.lastHTTPEndpoint = endpoint
	m.lastHTTPRequest = request
	return m.httpResp, m.httpErr
}

func (m *mockUpstreamForwarder) ForwardGRPC(_ context.Context, endpoint discovery.Endpoint, request domain.BackflowGRPCRequest) (domain.BackflowGRPCResponse, error) {
	m.lastGRPCEndpoint = endpoint
	m.lastGRPCRequest = request
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

func TestIngressHTTPFallbackToDiscovery(t *testing.T) {
	stateStore := store.NewMemoryStore()
	backflowCaller := &mockBackflowCaller{}
	discoveryResolver := &mockDiscoveryResolver{
		endpoint: discovery.Endpoint{
			Host:   "127.0.0.1",
			Port:   8081,
			Source: "local",
		},
		matched: true,
	}
	upstreamForwarder := &mockUpstreamForwarder{
		httpResp: domain.BackflowHTTPResponse{
			StatusCode: http.StatusAccepted,
			Headers: map[string][]string{
				"Content-Type": {"application/json"},
			},
			Body: []byte(`{"from":"discovery"}`),
		},
	}

	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		stateStore,
		backflowCaller,
		"",
		WithServiceDiscoveryResolver(discoveryResolver),
		WithUpstreamForwarder(upstreamForwarder),
	)
	req := httptest.NewRequest(http.MethodGet, "http://user.base.internal/api/v1/ingress/http/orders/list?verbose=1", nil)
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)

	if resp.Code != http.StatusAccepted {
		t.Fatalf("unexpected response status: %d", resp.Code)
	}
	if discoveryResolver.lastQuery.Env != "base" || discoveryResolver.lastQuery.ServiceName != "user" || discoveryResolver.lastQuery.Protocol != "http" {
		t.Fatalf("unexpected discovery query: %+v", discoveryResolver.lastQuery)
	}
	if upstreamForwarder.lastHTTPEndpoint.Host != "127.0.0.1" || upstreamForwarder.lastHTTPEndpoint.Port != 8081 {
		t.Fatalf("unexpected discovered endpoint: %+v", upstreamForwarder.lastHTTPEndpoint)
	}
	if upstreamForwarder.lastHTTPRequest.Path != "/orders/list" {
		t.Fatalf("unexpected forwarded path: %+v", upstreamForwarder.lastHTTPRequest)
	}
	if got := resp.Header().Get("X-DevLoop-Route-Source"); got != "discovery:local" {
		t.Fatalf("unexpected route source header: %s", got)
	}
}

func TestIngressGRPCFallbackToDiscovery(t *testing.T) {
	stateStore := store.NewMemoryStore()
	backflowCaller := &mockBackflowCaller{}
	discoveryResolver := &mockDiscoveryResolver{
		endpoint: discovery.Endpoint{
			Host:   "127.0.0.1",
			Port:   9091,
			Source: "consul",
		},
		matched: true,
	}
	upstreamForwarder := &mockUpstreamForwarder{
		grpcResp: domain.BackflowGRPCResponse{
			Status:    "SERVING",
			Target:    "127.0.0.1:9091",
			LatencyMs: 3,
		},
	}

	handler := NewHandler(
		routing.NewPipeline([]string{"host"}),
		stateStore,
		backflowCaller,
		"",
		WithServiceDiscoveryResolver(discoveryResolver),
		WithUpstreamForwarder(upstreamForwarder),
	)
	req := httptest.NewRequest(http.MethodPost, "http://user.base.internal/api/v1/ingress/grpc", strings.NewReader(`{"healthService":"user.v1.UserService","timeoutMs":1500}`))
	resp := httptest.NewRecorder()

	handler.Router().ServeHTTP(resp, req)

	if resp.Code != http.StatusOK {
		t.Fatalf("unexpected response status: %d", resp.Code)
	}
	if discoveryResolver.lastQuery.Env != "base" || discoveryResolver.lastQuery.ServiceName != "user" || discoveryResolver.lastQuery.Protocol != "grpc" {
		t.Fatalf("unexpected discovery query: %+v", discoveryResolver.lastQuery)
	}
	if upstreamForwarder.lastGRPCEndpoint.Host != "127.0.0.1" || upstreamForwarder.lastGRPCEndpoint.Port != 9091 {
		t.Fatalf("unexpected grpc endpoint: %+v", upstreamForwarder.lastGRPCEndpoint)
	}
	if upstreamForwarder.lastGRPCRequest.HealthService != "user.v1.UserService" || upstreamForwarder.lastGRPCRequest.TimeoutMs != 1500 {
		t.Fatalf("unexpected grpc forward request: %+v", upstreamForwarder.lastGRPCRequest)
	}

	var payload domain.IngressGRPCResponse
	if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if payload.Target != "127.0.0.1:9091" || payload.Status != "SERVING" {
		t.Fatalf("unexpected grpc response payload: %+v", payload)
	}
	if got := resp.Header().Get("X-DevLoop-Route-Source"); got != "discovery:consul" {
		t.Fatalf("unexpected route source header: %s", got)
	}
}
