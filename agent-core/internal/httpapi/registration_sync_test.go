package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

type mockTunnelSyncPublisher struct {
	upsertRegs []domain.LocalRegistration
	deleteRegs []domain.LocalRegistration
}

func (m *mockTunnelSyncPublisher) EnqueueRegisterUpsert(_ context.Context, reg domain.LocalRegistration, _ string) error {
	m.upsertRegs = append(m.upsertRegs, reg)
	return nil
}

func (m *mockTunnelSyncPublisher) EnqueueRegisterDelete(_ context.Context, reg domain.LocalRegistration, _ string) error {
	m.deleteRegs = append(m.deleteRegs, reg)
	return nil
}

func (m *mockTunnelSyncPublisher) RequestReconnect(_ context.Context) error {
	return nil
}

func (m *mockTunnelSyncPublisher) CurrentProtocol() string {
	return "http"
}

func TestRegisterUpdateSyncRemovedEndpoints(t *testing.T) {
	cfg := config.Config{
		EnvName: "dev-alice",
		Registration: config.RegistrationConfig{
			DefaultTTLSeconds: 30,
			ScanInterval:      5 * time.Second,
		},
		Tunnel: config.TunnelConfig{
			RequestTimeout: 2 * time.Second,
		},
	}
	stateStore := store.NewMemoryStore()
	publisher := &mockTunnelSyncPublisher{}
	handler := NewHandler(cfg, stateStore, publisher, nil)

	firstPayload := domain.LocalRegistration{
		ServiceName: "user",
		Env:         "dev-alice",
		InstanceID:  "inst-1",
		Endpoints: []domain.LocalEndpoint{
			{Protocol: "http", ListenPort: 18080, TargetPort: 18080},
			{Protocol: "grpc", ListenPort: 19090, TargetPort: 19090},
		},
	}
	sendRegisterRequest(t, handler, firstPayload, "evt-reg-1")
	if len(publisher.upsertRegs) != 1 {
		t.Fatalf("expected 1 upsert sync call, got %d", len(publisher.upsertRegs))
	}
	if len(publisher.upsertRegs[0].Endpoints) != 2 {
		t.Fatalf("expected first upsert contains 2 endpoints, got %d", len(publisher.upsertRegs[0].Endpoints))
	}
	if len(publisher.deleteRegs) != 0 {
		t.Fatalf("expected no delete sync on first register, got %d", len(publisher.deleteRegs))
	}

	// 第二次注册使用同一 instanceId，仅保留一个新端口，预期删除旧 endpoint 并 upsert 新 endpoint。
	secondPayload := domain.LocalRegistration{
		ServiceName: "user",
		Env:         "dev-alice",
		InstanceID:  "inst-1",
		Endpoints: []domain.LocalEndpoint{
			{Protocol: "http", ListenPort: 28080, TargetPort: 28080},
		},
	}
	sendRegisterRequest(t, handler, secondPayload, "evt-reg-2")

	if len(publisher.upsertRegs) != 2 {
		t.Fatalf("expected 2 upsert sync calls after update, got %d", len(publisher.upsertRegs))
	}
	if len(publisher.upsertRegs[1].Endpoints) != 1 || publisher.upsertRegs[1].Endpoints[0].TargetPort != 28080 {
		t.Fatalf("unexpected second upsert payload: %+v", publisher.upsertRegs[1])
	}

	if len(publisher.deleteRegs) != 1 {
		t.Fatalf("expected 1 delete sync call after update, got %d", len(publisher.deleteRegs))
	}
	removed := publisher.deleteRegs[0]
	if len(removed.Endpoints) != 2 {
		t.Fatalf("expected 2 removed endpoints, got %d payload=%+v", len(removed.Endpoints), removed)
	}

	if !containsEndpoint(removed.Endpoints, "http", 18080) || !containsEndpoint(removed.Endpoints, "grpc", 19090) {
		t.Fatalf("unexpected removed endpoints: %+v", removed.Endpoints)
	}
}

func sendRegisterRequest(t *testing.T, handler *Handler, payload domain.LocalRegistration, eventID string) {
	t.Helper()
	body, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal register payload failed: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/v1/registrations", bytes.NewReader(body))
	req.Header.Set("X-Event-Id", eventID)
	recorder := httptest.NewRecorder()
	handler.Router().ServeHTTP(recorder, req)
	if recorder.Code != http.StatusOK {
		t.Fatalf("register request failed, status=%d body=%s", recorder.Code, recorder.Body.String())
	}
}

func containsEndpoint(endpoints []domain.LocalEndpoint, protocol string, targetPort int) bool {
	for _, endpoint := range endpoints {
		if endpoint.Protocol == protocol && endpoint.TargetPort == targetPort {
			return true
		}
	}
	return false
}
