package store

import (
	"errors"
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

func TestProcessTunnelEventHELLOAndDuplicate(t *testing.T) {
	s := NewMemoryStore()

	hello := domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    1,
		ResourceVersion: 10,
		EventID:         "evt-hello-1",
		Payload: map[string]any{
			"tunnelId": "tunnel-a",
			"rdName":   "alice",
			"connId":   "conn-1",
		},
	}

	reply, err := s.ProcessTunnelEvent(hello)
	if err != nil {
		t.Fatalf("HELLO should be accepted, got error: %v", err)
	}
	if reply.Status != domain.EventStatusAccepted || reply.Deduplicated {
		t.Fatalf("unexpected HELLO reply: %+v", reply)
	}

	sessions := s.ListSessions()
	if len(sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions))
	}
	if sessions[0].SessionEpoch != 1 || sessions[0].RDName != "alice" {
		t.Fatalf("unexpected session after HELLO: %+v", sessions[0])
	}

	reply, err = s.ProcessTunnelEvent(hello)
	if err != nil {
		t.Fatalf("duplicate HELLO should not return error, got %v", err)
	}
	if reply.Status != domain.EventStatusDuplicate || !reply.Deduplicated {
		t.Fatalf("duplicate HELLO should be deduplicated, got %+v", reply)
	}
}

func TestProcessTunnelEventRejectsStaleEpoch(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    2,
		ResourceVersion: 20,
		EventID:         "evt-hello-2",
		Payload: map[string]any{
			"tunnelId": "tunnel-b",
			"rdName":   "bob",
		},
	})
	if err != nil {
		t.Fatalf("HELLO should succeed: %v", err)
	}

	_, err = s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterUpsert,
		SessionEpoch:    1,
		ResourceVersion: 21,
		EventID:         "evt-stale",
		Payload: map[string]any{
			"tunnelId":    "tunnel-b",
			"env":         "dev-bob",
			"serviceName": "user",
			"protocol":    "http",
			"instanceId":  "inst-1",
			"targetPort":  18080,
		},
	})

	var reject *EventRejectError
	if !errors.As(err, &reject) {
		t.Fatalf("expected EventRejectError, got %v", err)
	}
	if reject.Code != domain.SyncErrorStaleEpochEvent {
		t.Fatalf("expected stale epoch code, got %s", reject.Code)
	}
}

func TestProcessTunnelEventRequiresHELLOOnEpochTransition(t *testing.T) {
	s := NewMemoryStore()

	_, err := s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    3,
		ResourceVersion: 30,
		EventID:         "evt-hello-3",
		Payload: map[string]any{
			"tunnelId": "tunnel-c",
		},
	})
	if err != nil {
		t.Fatalf("HELLO should succeed: %v", err)
	}

	_, err = s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterUpsert,
		SessionEpoch:    4,
		ResourceVersion: 31,
		EventID:         "evt-transition",
		Payload: map[string]any{
			"tunnelId":    "tunnel-c",
			"env":         "dev-charlie",
			"serviceName": "order",
			"protocol":    "grpc",
			"instanceId":  "inst-2",
			"targetPort":  19090,
		},
	})

	var reject *EventRejectError
	if !errors.As(err, &reject) {
		t.Fatalf("expected EventRejectError, got %v", err)
	}
	if reject.Code != domain.SyncErrorEpochTransition {
		t.Fatalf("expected epoch transition code, got %s", reject.Code)
	}
}

func TestProcessTunnelEventFullSyncAndIncrementalDelete(t *testing.T) {
	s := NewMemoryStoreWithBridge("bridge.devloop.internal", 8443)

	_, err := s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    5,
		ResourceVersion: 50,
		EventID:         "evt-hello-5",
		Payload: map[string]any{
			"tunnelId": "tunnel-d",
			"rdName":   "dora",
			"connId":   "conn-d-1",
		},
	})
	if err != nil {
		t.Fatalf("HELLO should succeed: %v", err)
	}

	reply, err := s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageFullSyncSnapshot,
		SessionEpoch:    5,
		ResourceVersion: 51,
		EventID:         "evt-snapshot-1",
		Payload: map[string]any{
			"tunnelId": "tunnel-d",
			"rdName":   "dora",
			"registrations": []map[string]any{
				{
					"env":         "dev-dora",
					"serviceName": "user",
					"instanceId":  "inst-user-1",
					"endpoints": []map[string]any{
						{"protocol": "http", "targetPort": 18080, "status": "active"},
						{"protocol": "grpc", "targetPort": 19090, "status": "active"},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("FULL_SYNC_SNAPSHOT should succeed: %v", err)
	}
	if reply.Status != domain.EventStatusAccepted {
		t.Fatalf("snapshot should be accepted, got %+v", reply)
	}

	intercepts := s.ListIntercepts()
	routes := s.ListRoutes()
	if len(intercepts) != 2 || len(routes) != 2 {
		t.Fatalf("expected 2 intercepts/routes after snapshot, got intercepts=%d routes=%d", len(intercepts), len(routes))
	}

	_, err = s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterDelete,
		SessionEpoch:    5,
		ResourceVersion: 52,
		EventID:         "evt-delete-1",
		Payload: map[string]any{
			"tunnelId":    "tunnel-d",
			"env":         "dev-dora",
			"serviceName": "user",
			"protocol":    "grpc",
			"targetPort":  19090,
		},
	})
	if err != nil {
		t.Fatalf("REGISTER_DELETE should succeed: %v", err)
	}

	intercepts = s.ListIntercepts()
	routes = s.ListRoutes()
	if len(intercepts) != 1 || len(routes) != 1 {
		t.Fatalf("expected 1 intercept/route after delete, got intercepts=%d routes=%d", len(intercepts), len(routes))
	}

	if routes[0].BridgeHost != "bridge.devloop.internal" || routes[0].BridgePort != 8443 {
		t.Fatalf("unexpected route bridge address: %+v", routes[0])
	}
}
