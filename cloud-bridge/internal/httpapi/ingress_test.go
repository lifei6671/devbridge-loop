package httpapi

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/routing"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/store"
)

type mockBackflowCaller struct {
	lastBaseURL string
	lastReq     domain.BackflowHTTPRequest
	resp        domain.BackflowHTTPResponse
	err         error
}

func (m *mockBackflowCaller) ForwardHTTP(_ context.Context, baseURL string, request domain.BackflowHTTPRequest) (domain.BackflowHTTPResponse, error) {
	m.lastBaseURL = baseURL
	m.lastReq = request
	return m.resp, m.err
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
		resp: domain.BackflowHTTPResponse{
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
	if backflowCaller.lastReq.TargetPort != 18080 {
		t.Fatalf("unexpected target port: %+v", backflowCaller.lastReq)
	}
	if backflowCaller.lastReq.Path != "/order/list" {
		t.Fatalf("unexpected forward path: %+v", backflowCaller.lastReq)
	}
}
