package httpapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

func TestStateDiagnosticsEndpointReturnsSnapshot(t *testing.T) {
	state := store.NewMemoryStore()
	if _, _, err := state.UpsertRegistration(domain.LocalRegistration{
		ServiceName: "user-service",
		Env:         "dev-alice",
		InstanceID:  "inst-user-1",
		TTLSeconds:  30,
		Endpoints: []domain.LocalEndpoint{
			{
				Protocol:   "http",
				ListenHost: "127.0.0.1",
				ListenPort: 18080,
				TargetHost: "127.0.0.1",
				TargetPort: 18080,
				Status:     "active",
			},
		},
	}, "evt-diag-http-upsert"); err != nil {
		t.Fatalf("upsert registration failed: %v", err)
	}
	state.AddError(domain.ErrorTunnelOffline, "tunnel down", map[string]string{"action": "heartbeat"})
	state.AddRequestSummary(domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "http",
		ServiceName:  "user-service",
		RequestedEnv: "dev-alice",
		ResolvedEnv:  "dev-alice",
		Resolution:   "dev-priority",
		Upstream:     "http://bridge.example.internal",
		StatusCode:   http.StatusOK,
		Result:       "success",
		LatencyMs:    12,
		OccurredAt:   time.Now().UTC(),
	})

	cfg := config.Config{
		RDName:  "alice",
		EnvName: "dev-alice",
		EnvResolve: config.EnvResolveConfig{
			Order: []string{"requestHeader", "payload", "runtimeDefault", "baseFallback"},
		},
		Registration: config.RegistrationConfig{
			DefaultTTLSeconds: 30,
			ScanInterval:      5 * time.Second,
		},
		Tunnel: config.TunnelConfig{
			BridgeAddress:  "http://127.0.0.1:18080",
			RequestTimeout: 5 * time.Second,
		},
	}

	handler := NewHandler(cfg, state, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/v1/state/diagnostics", nil)
	recorder := httptest.NewRecorder()
	handler.Router().ServeHTTP(recorder, req)

	if recorder.Code != http.StatusOK {
		t.Fatalf("unexpected status code: %d", recorder.Code)
	}

	var response domain.DiagnosticsSnapshot
	if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode diagnostics response failed: %v", err)
	}

	if response.Summary.RDName != "alice" {
		t.Fatalf("unexpected rdName: %s", response.Summary.RDName)
	}
	if response.Summary.RegistrationCount != 1 {
		t.Fatalf("unexpected registration count: %d", response.Summary.RegistrationCount)
	}
	if len(response.RecentErrors) != 1 {
		t.Fatalf("unexpected recent errors count: %d", len(response.RecentErrors))
	}
	if len(response.RecentRequests) != 1 {
		t.Fatalf("unexpected recent requests count: %d", len(response.RecentRequests))
	}
}
