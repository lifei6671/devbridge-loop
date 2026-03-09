package store

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

func TestResolveRouteForIngress(t *testing.T) {
	s := NewMemoryStoreWithBridge("bridge.devloop.internal", 8443)

	_, err := s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageHELLO,
		SessionEpoch:    1,
		ResourceVersion: 1,
		EventID:         "evt-hello-1",
		Payload: map[string]any{
			"tunnelId":        "tunnel-a",
			"rdName":          "alice",
			"connId":          "conn-a1",
			"backflowBaseUrl": "http://127.0.0.1:19090",
		},
	})
	if err != nil {
		t.Fatalf("process HELLO failed: %v", err)
	}

	_, err = s.ProcessTunnelEvent(domain.TunnelEvent{
		Type:            domain.TunnelMessageRegisterUpsert,
		SessionEpoch:    1,
		ResourceVersion: 2,
		EventID:         "evt-upsert-1",
		Payload: map[string]any{
			"tunnelId":    "tunnel-a",
			"env":         "dev-alice",
			"serviceName": "user",
			"protocol":    "http",
			"instanceId":  "inst-1",
			"targetPort":  18080,
		},
	})
	if err != nil {
		t.Fatalf("process REGISTER_UPSERT failed: %v", err)
	}

	route, session, ok := s.ResolveRouteForIngress("dev-alice", "user", "http")
	if !ok {
		t.Fatalf("route should be found")
	}
	if route.TargetPort != 18080 || route.TunnelID != "tunnel-a" {
		t.Fatalf("unexpected route: %+v", route)
	}
	if session.BackflowBaseURL != "http://127.0.0.1:19090" {
		t.Fatalf("unexpected session backflow url: %+v", session)
	}
}
