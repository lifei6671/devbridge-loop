package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
	"google.golang.org/grpc"
	"google.golang.org/grpc/health"
	grpc_health_v1 "google.golang.org/grpc/health/grpc_health_v1"
)

func TestEgressGRPCDevPriority(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen grpc failed: %v", err)
	}
	defer listener.Close()

	grpcHealth := health.NewServer()
	grpcHealth.SetServingStatus("user", grpc_health_v1.HealthCheckResponse_SERVING)
	grpcServer := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(grpcServer, grpcHealth)
	defer grpcServer.Stop()
	go func() {
		_ = grpcServer.Serve(listener)
	}()

	targetPort := listener.Addr().(*net.TCPAddr).Port
	cfg := config.Config{
		EnvName: "dev-alice",
		Tunnel: config.TunnelConfig{
			RequestTimeout: 2 * time.Second,
		},
	}
	state := store.NewMemoryStore()
	_, _, err = state.UpsertRegistration(domain.LocalRegistration{
		ServiceName: "user",
		Env:         "dev-alice",
		InstanceID:  "inst-user-grpc-1",
		TTLSeconds:  30,
		Endpoints: []domain.LocalEndpoint{
			{Protocol: "grpc", TargetHost: "127.0.0.1", TargetPort: targetPort},
		},
	}, "evt-upsert-grpc-1")
	if err != nil {
		t.Fatalf("setup registration failed: %v", err)
	}

	handler := NewHandler(cfg, state, nil, nil)
	payload, _ := json.Marshal(domain.EgressGRPCRequest{
		ServiceName:   "user",
		Env:           "dev-alice",
		HealthService: "user",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/egress/grpc", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, string(body))
	}

	var result domain.EgressGRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if result.Status != grpc_health_v1.HealthCheckResponse_SERVING.String() {
		t.Fatalf("unexpected grpc health status: %+v", result)
	}
	if result.Upstream != "dev" {
		t.Fatalf("unexpected upstream: %+v", result)
	}

	requests := state.ListRequestSummaries()
	if len(requests) == 0 {
		t.Fatalf("request summary should be recorded")
	}
	if requests[0].Protocol != "grpc" || requests[0].Resolution != "dev-priority" {
		t.Fatalf("unexpected summary: %+v", requests[0])
	}
}

func TestEgressGRPCBaseFallbackMissingAddress(t *testing.T) {
	cfg := config.Config{
		EnvName: "dev-alice",
		Tunnel: config.TunnelConfig{
			RequestTimeout: 2 * time.Second,
		},
	}
	state := store.NewMemoryStore()
	handler := NewHandler(cfg, state, nil, nil)

	payload, _ := json.Marshal(domain.EgressGRPCRequest{
		ServiceName: "inventory",
		Env:         "dev-alice",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/egress/grpc", bytes.NewReader(payload))
	rec := httptest.NewRecorder()

	handler.Router().ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("unexpected status: %d body=%s", resp.StatusCode, string(body))
	}

	var result domain.EgressGRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode response failed: %v", err)
	}
	if result.ErrorCode != domain.ErrorRouteNotFound {
		t.Fatalf("unexpected error code: %+v", result)
	}
	if !strings.Contains(result.Message, "baseAddress") {
		t.Fatalf("unexpected message: %+v", result)
	}
}
