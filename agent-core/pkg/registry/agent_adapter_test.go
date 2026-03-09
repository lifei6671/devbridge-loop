package registry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAgentAdapterRegisterHeartbeatUnregister(t *testing.T) {
	var registerCalled bool
	var heartbeatCalled bool
	var unregisterCalled bool

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/registrations":
			registerCalled = true
			var payload localRegistrationPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode register payload failed: %v", err)
			}
			if payload.Env != "dev-alice" {
				t.Fatalf("unexpected env: %+v", payload)
			}
			payload.InstanceID = "inst-1"
			_ = json.NewEncoder(w).Encode(payload)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/registrations/inst-1/heartbeat":
			heartbeatCalled = true
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/registrations/inst-1":
			unregisterCalled = true
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "deleted"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	adapter, err := NewAgentAdapter(AgentOptions{
		AgentAddr:  server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	registered, err := adapter.Register(context.Background(), Registration{
		ServiceName: "user-service",
		Endpoints: []Endpoint{
			{
				Protocol:   "http",
				ListenPort: 18080,
				TargetPort: 18080,
			},
		},
	})
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if strings.TrimSpace(registered.InstanceID) != "inst-1" {
		t.Fatalf("unexpected register result: %+v", registered)
	}

	if err := adapter.Heartbeat(context.Background(), "inst-1"); err != nil {
		t.Fatalf("heartbeat failed: %v", err)
	}
	if err := adapter.Unregister(context.Background(), "inst-1"); err != nil {
		t.Fatalf("unregister failed: %v", err)
	}

	if !registerCalled || !heartbeatCalled || !unregisterCalled {
		t.Fatalf("expected all endpoints to be called, register=%v heartbeat=%v unregister=%v", registerCalled, heartbeatCalled, unregisterCalled)
	}
}

func TestAgentAdapterDiscover(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/api/v1/discover" {
			http.NotFound(w, r)
			return
		}

		var payload discoverPayload
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatalf("decode discover payload failed: %v", err)
		}
		// 验证 discover 未传 env 时会自动使用运行时默认 env。
		if payload.Env != "dev-alice" {
			t.Fatalf("unexpected discover env: %+v", payload)
		}

		_ = json.NewEncoder(w).Encode(discoverResultPayload{
			Matched:      true,
			ResolvedEnv:  "dev-alice",
			Resolution:   "dev-priority",
			ResourceHint: "resourceVersion=8",
			RouteTarget: struct {
				TargetHost string `json:"targetHost"`
				TargetPort int    `json:"targetPort"`
			}{
				TargetHost: "127.0.0.1",
				TargetPort: 18080,
			},
		})
	}))
	defer server.Close()

	adapter, err := NewAgentAdapter(AgentOptions{
		AgentAddr:  server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	response, err := adapter.Discover(context.Background(), DiscoverRequest{
		ServiceName: "user-service",
		Protocol:    "http",
	})
	if err != nil {
		t.Fatalf("discover failed: %v", err)
	}
	if !response.Matched || response.TargetPort != 18080 || response.TargetHost != "127.0.0.1" {
		t.Fatalf("unexpected discover response: %+v", response)
	}
}

func TestAgentAdapterDiscoverRequiresFields(t *testing.T) {
	adapter, err := NewAgentAdapter(AgentOptions{})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	_, err = adapter.Discover(context.Background(), DiscoverRequest{})
	if err == nil {
		t.Fatalf("discover should fail when fields are missing")
	}
}
