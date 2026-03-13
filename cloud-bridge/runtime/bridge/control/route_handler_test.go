package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestRouteHandlerHandleAssignAndRevoke 验证路由事件的幂等与版本语义。
func TestRouteHandlerHandleAssignAndRevoke(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewRouteHandler(RouteHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})

	assign := pb.RouteAssign{
		RouteID:     "route-1",
		Namespace:   "dev",
		Environment: "alice",
		Target: pb.RouteTarget{
			Type: pb.RouteTargetTypeConnectorService,
		},
	}
	assignAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 1,
		ResourceID:      "route-1",
	}, assign)
	if !assignAck.Accepted {
		t.Fatalf("assign should be accepted, got code=%s", assignAck.ErrorCode)
	}

	// 重放同一事件应返回 duplicate 语义。
	dupAck := handler.HandleAssign(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteAssign,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 2,
		ResourceID:      "route-1",
	}, assign)
	if !dupAck.Accepted {
		t.Fatalf("duplicate assign should be accepted")
	}
	if dupAck.CurrentResourceVersion != 1 {
		t.Fatalf("unexpected current version: got=%d want=1", dupAck.CurrentResourceVersion)
	}

	revoke := pb.RouteRevoke{RouteID: "route-1"}
	revokeAck := handler.HandleRevoke(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageRouteRevoke,
		SessionID:       "session-1",
		SessionEpoch:    2,
		EventID:         "evt-2",
		ResourceVersion: 2,
		ResourceID:      "route-1",
	}, revoke)
	if revokeAck.Accepted {
		t.Fatalf("stale epoch revoke should be rejected")
	}
	if revokeAck.ErrorCode != ltfperrors.CodeStaleEpochEvent {
		t.Fatalf("unexpected revoke error code: got=%s want=%s", revokeAck.ErrorCode, ltfperrors.CodeStaleEpochEvent)
	}
}
