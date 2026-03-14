package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestPublishHandlerHandlePublish 验证发布处理器的幂等与版本比较行为。
func TestPublishHandlerHandlePublish(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})

	message := pb.PublishService{
		ServiceID:   "svc-1",
		ServiceKey:  "dev/alice/order-service",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	}

	testCases := []struct {
		name               string
		envelope           pb.ControlEnvelope
		expectAccepted     bool
		expectErrorCode    string
		expectCurrentVer   uint64
		expectRegistrySize int
	}{
		{
			name: "accepted new version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-1",
				ResourceVersion: 1,
				ResourceID:      "svc-1",
			},
			expectAccepted:     true,
			expectCurrentVer:   1,
			expectRegistrySize: 1,
		},
		{
			name: "accepted newer version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-2",
				ResourceVersion: 2,
				ResourceID:      "svc-1",
			},
			expectAccepted:     true,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "reject old resource version",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-3",
				ResourceVersion: 1,
				ResourceID:      "svc-1",
			},
			expectAccepted:     false,
			expectErrorCode:    ltfperrors.CodeVersionRollback,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "duplicate replay event id",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    3,
				EventID:         "evt-2",
				ResourceVersion: 3,
				ResourceID:      "svc-1",
			},
			expectAccepted:     true,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
		{
			name: "reject stale epoch",
			envelope: pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       "session-1",
				SessionEpoch:    2,
				EventID:         "evt-4",
				ResourceVersion: 3,
				ResourceID:      "svc-1",
			},
			expectAccepted:     false,
			expectErrorCode:    ltfperrors.CodeStaleEpochEvent,
			expectCurrentVer:   2,
			expectRegistrySize: 1,
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			ack := handler.HandlePublish(testCase.envelope, message)
			if ack.Accepted != testCase.expectAccepted {
				t.Fatalf("unexpected accepted: got=%v want=%v", ack.Accepted, testCase.expectAccepted)
			}
			if testCase.expectErrorCode != "" && ack.ErrorCode != testCase.expectErrorCode {
				t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, testCase.expectErrorCode)
			}
			if ack.CurrentResourceVersion != testCase.expectCurrentVer {
				t.Fatalf("unexpected current version: got=%d want=%d", ack.CurrentResourceVersion, testCase.expectCurrentVer)
			}
			if services := handler.serviceRegistry.List(); len(services) != testCase.expectRegistrySize {
				t.Fatalf("unexpected registry size: got=%d want=%d", len(services), testCase.expectRegistrySize)
			}
		})
	}

	serviceSnapshot, exists := handler.serviceRegistry.GetByServiceID("svc-1")
	if !exists {
		t.Fatalf("expected service snapshot exists after publish flow")
	}
	if serviceSnapshot.ConnectorID != "connector-1" {
		t.Fatalf("unexpected connector_id: got=%s want=connector-1", serviceSnapshot.ConnectorID)
	}
}

// TestPublishHandlerHandleUnpublish 验证下线处理器的幂等与删除行为。
func TestPublishHandlerHandleUnpublish(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	publishMessage := pb.PublishService{
		ServiceID:   "svc-1",
		ServiceKey:  "dev/alice/order-service",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	}

	// 先发布服务，构造可下线的前置状态。
	publishAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-1",
		ResourceVersion: 1,
		ResourceID:      "svc-1",
	}, publishMessage)
	if !publishAck.Accepted {
		t.Fatalf("publish should be accepted, got error=%s", publishAck.ErrorCode)
	}

	unpublishMessage := pb.UnpublishService{
		ServiceID:  "svc-1",
		ServiceKey: "dev/alice/order-service",
	}
	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-2",
		ResourceVersion: 2,
		ResourceID:      "svc-1",
	}, unpublishMessage)
	if !unpublishAck.Accepted {
		t.Fatalf("unpublish should be accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if services := handler.serviceRegistry.List(); len(services) != 0 {
		t.Fatalf("service should be removed, got=%d", len(services))
	}

	// 重放同一事件应走 duplicate 幂等分支，且保持无副作用。
	dupAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-1",
		SessionEpoch:    3,
		EventID:         "evt-2",
		ResourceVersion: 999,
		ResourceID:      "svc-1",
	}, unpublishMessage)
	if !dupAck.Accepted {
		t.Fatalf("duplicate unpublish should be accepted")
	}
	if dupAck.CurrentResourceVersion != 2 {
		t.Fatalf("unexpected current version: got=%d want=2", dupAck.CurrentResourceVersion)
	}
}
