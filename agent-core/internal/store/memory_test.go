package store

import (
	"errors"
	"testing"

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
