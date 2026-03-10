package registry

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

func TestAgentAdapterRegisterDeregisterAndAutoHeartbeat(t *testing.T) {
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

			var payload localRegistrationPayload
			if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
				t.Fatalf("decode register payload failed: %v", err)
			}
			if payload.ServiceName != "user-service" || payload.Env != "dev-alice" {
				t.Fatalf("unexpected register payload: %+v", payload)
			}
			if len(payload.Endpoints) != 1 || payload.Endpoints[0].Protocol != "tcp" {
				t.Fatalf("unexpected register endpoint payload: %+v", payload.Endpoints)
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

	adapter, err := NewAgentAdapter(AgentOptions{
		AgentAddr:  server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	registered, err := adapter.Register(context.Background(), NewService(
		"user-service",
		WithServiceMetadata(Metadata{
			"env":        "dev-alice",
			"ttlSeconds": 9,
		}),
		WithServiceEndpoints(Endpoints{
			NewEndpoint("127.0.0.1", 18080),
		}),
	))
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if registered == nil || strings.TrimSpace(metadataString(registered.GetMetadata(), "instanceId")) != "inst-1" {
		t.Fatalf("unexpected register result: %+v", registered)
	}

	// TTL=9 时心跳周期为 3s，这里等待一次自动心跳触发。
	time.Sleep(3200 * time.Millisecond)
	if err := adapter.Deregister(context.Background(), registered); err != nil {
		t.Fatalf("deregister failed: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !registerCalled || !deregisterCalled {
		t.Fatalf("expected register and deregister called, register=%v deregister=%v", registerCalled, deregisterCalled)
	}
	if heartbeatCalled <= 0 {
		t.Fatalf("expected auto heartbeat to be called, got %d", heartbeatCalled)
	}
}

func TestAgentAdapterSearch(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/registrations" {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode([]localRegistrationPayload{
			{
				ServiceName: "user-service",
				Env:         "dev-alice",
				InstanceID:  "inst-a",
				Metadata: map[string]string{
					"env":     "dev-alice",
					"version": "v1.2.3",
				},
				Endpoints: []endpointPayload{
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
				Endpoints: []endpointPayload{
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

	adapter, err := NewAgentAdapter(AgentOptions{
		AgentAddr:  server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
	})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	result, err := adapter.Search(context.Background(), SearchInput{
		Name:    "user-service",
		Version: "v1.2.3",
		Metadata: Metadata{
			"env": "dev-alice",
		},
	})
	if err != nil {
		t.Fatalf("search failed: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("unexpected search result length: %d", len(result))
	}
	if result[0].GetName() != "user-service" || result[0].GetVersion() != "v1.2.3" {
		t.Fatalf("unexpected service result: name=%s version=%s", result[0].GetName(), result[0].GetVersion())
	}
}

func TestAgentAdapterWatch(t *testing.T) {
	var callCount int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet || r.URL.Path != "/api/v1/registrations" {
			http.NotFound(w, r)
			return
		}
		callCount++
		if callCount == 1 {
			_ = json.NewEncoder(w).Encode([]localRegistrationPayload{})
			return
		}
		_ = json.NewEncoder(w).Encode([]localRegistrationPayload{
			{
				ServiceName: "user-service",
				Env:         "dev-alice",
				InstanceID:  "inst-a",
				Metadata: map[string]string{
					"env":     "dev-alice",
					"version": "latest",
				},
				Endpoints: []endpointPayload{
					{Protocol: "tcp", TargetHost: "127.0.0.1", TargetPort: 18080},
				},
			},
		})
	}))
	defer server.Close()

	adapter, err := NewAgentAdapter(AgentOptions{
		AgentAddr:  server.URL,
		RuntimeEnv: "dev-alice",
		HTTPClient: &http.Client{Timeout: 2 * time.Second},
		WatchEvery: 10 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new agent adapter failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	watcher, err := adapter.Watch(ctx, "/services")
	if err != nil {
		t.Fatalf("watch failed: %v", err)
	}
	defer watcher.Close()

	first, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("watch proceed first failed: %v", err)
	}
	if len(first) != 0 {
		t.Fatalf("unexpected first watch result: %+v", first)
	}

	second, err := watcher.Proceed()
	if err != nil {
		t.Fatalf("watch proceed second failed: %v", err)
	}
	if len(second) != 1 || second[0].GetName() != "user-service" {
		t.Fatalf("unexpected second watch result: %+v", second)
	}
}
