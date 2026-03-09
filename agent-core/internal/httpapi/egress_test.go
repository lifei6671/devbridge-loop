package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

func TestEgressHTTPDevPriority(t *testing.T) {
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/ingress/http/hello" {
			t.Fatalf("unexpected ingress path: %s", r.URL.Path)
		}
		if r.Host != "user.dev-alice.internal" {
			t.Fatalf("unexpected ingress host: %s", r.Host)
		}
		if env := r.Header.Get("x-env"); env != "dev-alice" {
			t.Fatalf("unexpected env header: %s", env)
		}
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte("bridge-ok"))
	}))
	defer bridge.Close()

	cfg := config.Config{
		EnvName: "dev-alice",
		Tunnel: config.TunnelConfig{
			BridgeAddress:  bridge.URL,
			RequestTimeout: 2 * time.Second,
		},
	}
	state := store.NewMemoryStore()
	_, _, err := state.UpsertRegistration(domain.LocalRegistration{
		ServiceName: "user",
		Env:         "dev-alice",
		InstanceID:  "inst-user-1",
		TTLSeconds:  30,
		Endpoints: []domain.LocalEndpoint{
			{Protocol: "http", TargetPort: 18080},
		},
	}, "evt-upsert-1")
	if err != nil {
		t.Fatalf("setup registration failed: %v", err)
	}

	handler := NewHandler(cfg, state, nil, nil)
	payload, _ := json.Marshal(domain.EgressHTTPRequest{
		ServiceName: "user",
		Env:         "dev-alice",
		Method:      http.MethodGet,
		Path:        "/hello",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/egress/http", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusAccepted {
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, string(body))
	}
	if strings.TrimSpace(string(body)) != "bridge-ok" {
		t.Fatalf("unexpected body: %s", string(body))
	}
	if resp.Header.Get("X-DevLoop-Upstream") != "bridge" {
		t.Fatalf("unexpected upstream header: %s", resp.Header.Get("X-DevLoop-Upstream"))
	}

	requests := state.ListRequestSummaries()
	if len(requests) == 0 {
		t.Fatalf("request summary should be recorded")
	}
	if requests[0].Resolution != "dev-priority" {
		t.Fatalf("unexpected resolution: %+v", requests[0])
	}
}

func TestEgressHTTPBaseFallback(t *testing.T) {
	base := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/ping" {
			t.Fatalf("unexpected base path: %s", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("base-ok"))
	}))
	defer base.Close()

	cfg := config.Config{
		EnvName: "dev-alice",
		Tunnel: config.TunnelConfig{
			BridgeAddress:  "http://127.0.0.1:18080",
			RequestTimeout: 2 * time.Second,
		},
	}
	state := store.NewMemoryStore()
	handler := NewHandler(cfg, state, nil, nil)

	payload, _ := json.Marshal(domain.EgressHTTPRequest{
		ServiceName: "inventory",
		Env:         "dev-alice",
		Method:      http.MethodGet,
		Path:        "/api/ping",
		BaseURL:     base.URL,
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/egress/http", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, string(body))
	}
	if strings.TrimSpace(string(body)) != "base-ok" {
		t.Fatalf("unexpected body: %s", string(body))
	}
	if resp.Header.Get("X-DevLoop-Upstream") != "base" {
		t.Fatalf("unexpected upstream header: %s", resp.Header.Get("X-DevLoop-Upstream"))
	}

	requests := state.ListRequestSummaries()
	if len(requests) == 0 {
		t.Fatalf("request summary should be recorded")
	}
	if requests[0].Resolution != "base-fallback" {
		t.Fatalf("unexpected resolution: %+v", requests[0])
	}
}
