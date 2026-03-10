package serviceregistry

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentCoreClientSDKRegisterDeregisterAndHeartbeat(t *testing.T) {
	var registerCalled bool
	var heartbeatCalled int
	var deregisterCalled bool
	var mu sync.Mutex

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/registrations":
			mu.Lock()
			registerCalled = true
			mu.Unlock()

			var payload agentCoreRegistrationPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode register payload failed: %v", err)
			}
			if payload.ServiceName != "user-service" || payload.Env != "dev-alice" {
				t.Fatalf("unexpected register payload: %+v", payload)
			}
			if len(payload.Endpoints) != 1 || payload.Endpoints[0].Protocol != "tcp" {
				t.Fatalf("unexpected register endpoints: %+v", payload.Endpoints)
			}
			payload.InstanceID = "inst-1"
			_ = json.NewEncoder(w).Encode(payload)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v1/registrations/inst-1/heartbeat":
			mu.Lock()
			heartbeatCalled++
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "ok"})
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v1/registrations/inst-1":
			mu.Lock()
			deregisterCalled = true
			mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]string{"result": "deleted"})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	sdk, err := NewAgentCoreClientSDK(AgentCoreClientOptions{
		BaseURL:    server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new sdk failed: %v", err)
	}
	defer sdk.Close()

	registered, err := sdk.Register(context.Background(), NewBasicService(
		"user-service",
		WithServiceMetadata(Metadata{
			"env":        "dev-alice",
			"ttlSeconds": 3,
		}),
		WithServiceEndpoints(Endpoints{
			NewBasicEndpoint("127.0.0.1", 18080),
		}),
	))
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if registered == nil || strings.TrimSpace(getStringMetadata(registered.GetMetadata(), "instanceId")) != "inst-1" {
		t.Fatalf("unexpected register result: %+v", registered)
	}

	time.Sleep(1300 * time.Millisecond)
	if err := sdk.Deregister(context.Background(), registered); err != nil {
		t.Fatalf("deregister failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !registerCalled || !deregisterCalled {
		t.Fatalf("expected register and deregister called, got register=%v deregister=%v", registerCalled, deregisterCalled)
	}
	if heartbeatCalled <= 0 {
		t.Fatalf("expected heartbeat called, got %d", heartbeatCalled)
	}
}

func TestAgentCoreClientSDKSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/registrations" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]agentCoreRegistrationPayload{
			{
				ServiceName: "user-service",
				Env:         "dev-alice",
				InstanceID:  "inst-a",
				Metadata: map[string]string{
					"env":     "dev-alice",
					"version": "v1.2.3",
				},
				Endpoints: []agentEndpoint{
					{
						Protocol:   "tcp",
						TargetHost: "127.0.0.1",
						TargetPort: 18080,
					},
				},
			},
			{
				ServiceName: "order-service",
				Env:         "base",
				InstanceID:  "inst-b",
				Metadata: map[string]string{
					"env":     "base",
					"version": "latest",
				},
				Endpoints: []agentEndpoint{
					{
						Protocol:   "tcp",
						TargetHost: "127.0.0.1",
						TargetPort: 18081,
					},
				},
			},
		})
	}))
	defer server.Close()

	sdk, err := NewAgentCoreClientSDK(AgentCoreClientOptions{
		BaseURL:    server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new sdk failed: %v", err)
	}
	defer sdk.Close()

	result, err := sdk.Search(context.Background(), SearchInput{
		Name:    "user-service",
		Version: "v1.2.3",
		Metadata: Metadata{
			"env": "dev-alice",
		},
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result) != 1 || result[0].GetName() != "user-service" {
		t.Fatalf("unexpected search result: %+v", result)
	}
}

func TestAgentCoreClientSDKWatch(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/registrations" {
			http.NotFound(w, r)
			return
		}

		callCount++
		if callCount == 1 {
			_ = json.NewEncoder(w).Encode([]agentCoreRegistrationPayload{})
			return
		}
		_ = json.NewEncoder(w).Encode([]agentCoreRegistrationPayload{
			{
				ServiceName: "user-service",
				Env:         "dev-alice",
				InstanceID:  "inst-a",
				Metadata: map[string]string{
					"env":     "dev-alice",
					"version": "latest",
				},
				Endpoints: []agentEndpoint{
					{
						Protocol:   "tcp",
						TargetHost: "127.0.0.1",
						TargetPort: 18080,
					},
				},
			},
		})
	}))
	defer server.Close()

	sdk, err := NewAgentCoreClientSDK(AgentCoreClientOptions{
		BaseURL:    server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		WatchEvery: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new sdk failed: %v", err)
	}
	defer sdk.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	watcher, err := sdk.Watch(ctx, "/services")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	defer watcher.Close()

	first, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("first proceed failed: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("unexpected first watch result: %+v", first)
	}

	second, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("second proceed failed: %v", err)
	}
	if len(second) != 1 || second[0].GetName() != "user-service" {
		t.Fatalf("unexpected second watch result: %+v", second)
	}
}
