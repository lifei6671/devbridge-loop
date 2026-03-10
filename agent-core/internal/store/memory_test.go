package store

import (
	"errors"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
)

func TestUpsertRegistrationIdempotentByEventID(t *testing.T) {
	s := NewMemoryStore()
	reg := sampleRegistration("dev-alice", "user", "inst-1", 18080)

	_, duplicated, err := s.UpsertRegistration(reg, "evt-1")
	if err != nil {
		t.Fatalf("first upsert returned error: %v", err)
	}
	if duplicated {
		t.Fatalf("first upsert should not be duplicated")
	}
	firstRV := s.TunnelState("bridge").ResourceVersion

	_, duplicated, err = s.UpsertRegistration(reg, "evt-1")
	if err != nil {
		t.Fatalf("second upsert returned error: %v", err)
	}
	if !duplicated {
		t.Fatalf("second upsert should be deduplicated")
	}
	secondRV := s.TunnelState("bridge").ResourceVersion
	if secondRV != firstRV {
		t.Fatalf("resourceVersion should remain unchanged on duplicate event, got %d want %d", secondRV, firstRV)
	}
}

func TestDeleteRegistrationIdempotentByEventID(t *testing.T) {
	s := NewMemoryStore()
	reg := sampleRegistration("dev-alice", "order", "inst-2", 19080)
	if _, _, err := s.UpsertRegistration(reg, "evt-upsert"); err != nil {
		t.Fatalf("upsert returned error: %v", err)
	}

	deleted, duplicated, err := s.DeleteRegistration("inst-2", "evt-delete")
	if err != nil {
		t.Fatalf("first delete returned error: %v", err)
	}
	if !deleted || duplicated {
		t.Fatalf("first delete result unexpected: deleted=%v duplicated=%v", deleted, duplicated)
	}
	firstRV := s.TunnelState("bridge").ResourceVersion

	deleted, duplicated, err = s.DeleteRegistration("inst-2", "evt-delete")
	if err != nil {
		t.Fatalf("second delete returned error: %v", err)
	}
	if deleted || !duplicated {
		t.Fatalf("second delete should be duplicate no-op: deleted=%v duplicated=%v", deleted, duplicated)
	}
	secondRV := s.TunnelState("bridge").ResourceVersion
	if secondRV != firstRV {
		t.Fatalf("resourceVersion should remain unchanged on duplicate delete event, got %d want %d", secondRV, firstRV)
	}
}

func TestInstanceIDConflictAcrossServiceOrEnv(t *testing.T) {
	s := NewMemoryStore()
	if _, _, err := s.UpsertRegistration(sampleRegistration("dev-alice", "user", "inst-3", 20080), "evt-a"); err != nil {
		t.Fatalf("first upsert returned error: %v", err)
	}

	_, _, err := s.UpsertRegistration(sampleRegistration("dev-bob", "user", "inst-3", 20081), "evt-b")
	if !errors.Is(err, ErrInstanceConflict) {
		t.Fatalf("expected ErrInstanceConflict, got %v", err)
	}
}

func TestDiscoverDevPriorityAndBaseFallback(t *testing.T) {
	s := NewMemoryStore()
	if _, _, err := s.UpsertRegistration(sampleRegistration("dev-alice", "payment", "inst-dev", 21080), "evt-dev"); err != nil {
		t.Fatalf("dev upsert returned error: %v", err)
	}
	if _, _, err := s.UpsertRegistration(sampleRegistration("base", "payment", "inst-base", 31080), "evt-base"); err != nil {
		t.Fatalf("base upsert returned error: %v", err)
	}

	devResult := s.Discover(domain.DiscoverRequest{
		ServiceName: "payment",
		Env:         "dev-alice",
		Protocol:    "http",
	}, "dev-alice")

	if !devResult.Matched {
		t.Fatalf("expected dev discover to match")
	}
	if devResult.ResolvedEnv != "dev-alice" {
		t.Fatalf("expected resolved env dev-alice, got %s", devResult.ResolvedEnv)
	}
	if devResult.Resolution != "dev-priority" {
		t.Fatalf("expected dev-priority resolution, got %s", devResult.Resolution)
	}
	if devResult.RouteTarget.TargetPort != 21080 {
		t.Fatalf("expected dev target port 21080, got %d", devResult.RouteTarget.TargetPort)
	}
	if devResult.ServiceKey == "" || devResult.InstanceKey == "" || devResult.EndpointKey == "" {
		t.Fatalf("expected discover result to include service/instance/endpoint keys")
	}

	fallbackResult := s.Discover(domain.DiscoverRequest{
		ServiceName: "payment",
		Env:         "dev-charlie",
		Protocol:    "http",
	}, "dev-charlie")
	if !fallbackResult.Matched {
		t.Fatalf("expected fallback discover to match base")
	}
	if fallbackResult.ResolvedEnv != "base" {
		t.Fatalf("expected fallback env base, got %s", fallbackResult.ResolvedEnv)
	}
	if fallbackResult.Resolution != "base-fallback" {
		t.Fatalf("expected base-fallback resolution, got %s", fallbackResult.Resolution)
	}
	if fallbackResult.RouteTarget.TargetPort != 31080 {
		t.Fatalf("expected base target port 31080, got %d", fallbackResult.RouteTarget.TargetPort)
	}
}

func TestDiagnosticsSnapshotAggregatesSummaryErrorsAndRequests(t *testing.T) {
	s := NewMemoryStore()
	reg := sampleRegistration("dev-alice", "order-service", "inst-order", 18081)
	if _, _, err := s.UpsertRegistration(reg, "evt-diag-upsert"); err != nil {
		t.Fatalf("upsert returned error: %v", err)
	}

	s.AddError(domain.ErrorTunnelOffline, "tunnel disconnected", map[string]string{"step": "heartbeat"})
	s.AddError(domain.ErrorUpstreamTimeout, "upstream timeout", map[string]string{"service": "payment"})

	s.AddRequestSummary(domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "http",
		ServiceName:  "user-service",
		RequestedEnv: "dev-alice",
		ResolvedEnv:  "dev-alice",
		Resolution:   "dev-priority",
		Upstream:     "http://bridge.example.internal",
		StatusCode:   200,
		Result:       "success",
		LatencyMs:    18,
		OccurredAt:   time.Now().UTC().Add(-2 * time.Second),
	})
	s.AddRequestSummary(domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "grpc",
		ServiceName:  "payment-service",
		RequestedEnv: "dev-alice",
		ResolvedEnv:  "base",
		Resolution:   "base-fallback",
		Upstream:     "grpc://base.payment.internal:50051",
		StatusCode:   503,
		Result:       "error",
		ErrorCode:    domain.ErrorUpstreamTimeout,
		LatencyMs:    1200,
		OccurredAt:   time.Now().UTC().Add(-1 * time.Second),
	})

	diagnostics := s.Diagnostics("alice", "dev-alice", 30, 5*time.Second)

	if diagnostics.Summary.RegistrationCount != 1 {
		t.Fatalf("unexpected registration count: %d", diagnostics.Summary.RegistrationCount)
	}
	if diagnostics.Summary.CurrentEnv != "dev-alice" {
		t.Fatalf("unexpected current env: %s", diagnostics.Summary.CurrentEnv)
	}
	if diagnostics.Summary.RDName != "alice" {
		t.Fatalf("unexpected rd name: %s", diagnostics.Summary.RDName)
	}
	if diagnostics.Summary.TunnelStatus != "disconnected" {
		t.Fatalf("unexpected tunnel status: %s", diagnostics.Summary.TunnelStatus)
	}

	if len(diagnostics.RecentErrors) != 2 {
		t.Fatalf("unexpected recent errors size: %d", len(diagnostics.RecentErrors))
	}
	if diagnostics.RecentErrors[0].Code != domain.ErrorUpstreamTimeout {
		t.Fatalf("expected newest error at first position, got %s", diagnostics.RecentErrors[0].Code)
	}

	if len(diagnostics.RecentRequests) != 2 {
		t.Fatalf("unexpected recent requests size: %d", len(diagnostics.RecentRequests))
	}
	if diagnostics.RecentRequests[0].ServiceName != "payment-service" {
		t.Fatalf("expected newest request at first position, got %s", diagnostics.RecentRequests[0].ServiceName)
	}
	if diagnostics.GeneratedAt.IsZero() {
		t.Fatalf("generatedAt should be set")
	}
}

func TestClearDiagnosticsClearsRecentLogs(t *testing.T) {
	s := NewMemoryStore()
	s.AddError(domain.ErrorTunnelOffline, "tunnel disconnected", map[string]string{"step": "heartbeat"})
	s.AddRequestSummary(domain.RequestSummary{
		Direction:    "egress",
		Protocol:     "http",
		ServiceName:  "user-service",
		RequestedEnv: "dev-alice",
		ResolvedEnv:  "dev-alice",
		Resolution:   "dev-priority",
		Upstream:     "http://bridge.example.internal",
		StatusCode:   200,
		Result:       "success",
		LatencyMs:    16,
		OccurredAt:   time.Now().UTC(),
	})

	clearedErrors, clearedRequests := s.ClearDiagnostics()
	if clearedErrors != 1 || clearedRequests != 1 {
		t.Fatalf("unexpected clear result: errors=%d requests=%d", clearedErrors, clearedRequests)
	}

	if len(s.ListErrors()) != 0 {
		t.Fatalf("expected errors to be cleared")
	}
	if len(s.ListRequestSummaries()) != 0 {
		t.Fatalf("expected requests to be cleared")
	}
}

func TestSetSessionEpochAtLeast(t *testing.T) {
	s := NewMemoryStore()

	state := s.BeginTunnelReconnect()
	if state.SessionEpoch != 1 {
		t.Fatalf("expected sessionEpoch=1 after first reconnect begin, got %d", state.SessionEpoch)
	}

	state = s.SetSessionEpochAtLeast(8)
	if state.SessionEpoch != 8 {
		t.Fatalf("expected sessionEpoch fast-forward to 8, got %d", state.SessionEpoch)
	}

	state = s.SetSessionEpochAtLeast(3)
	if state.SessionEpoch != 8 {
		t.Fatalf("sessionEpoch should not rollback, got %d", state.SessionEpoch)
	}
}

func sampleRegistration(env, service, instanceID string, port int) domain.LocalRegistration {
	return domain.LocalRegistration{
		ServiceName: service,
		Env:         env,
		InstanceID:  instanceID,
		TTLSeconds:  30,
		Endpoints: []domain.LocalEndpoint{
			{
				Protocol:   "http",
				ListenHost: "127.0.0.1",
				ListenPort: port,
				TargetHost: "127.0.0.1",
				TargetPort: port,
				Status:     "active",
			},
		},
	}
}
