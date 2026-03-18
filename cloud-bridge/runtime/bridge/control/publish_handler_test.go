package control

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
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
		ServiceKey:  "order-service/http",
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

// TestPublishHandlerRejectStaleInstancePublishAfterFullSync
// 验证 full-sync 后会话维度资源键也会回填版本，防止旧版本实例事件绕过回滚保护。
func TestPublishHandlerRejectStaleInstancePublishAfterFullSync(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-fs",
		ConnectorID: "connector-fs",
		Epoch:       5,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	handler.ReconcileFromFullSync(pb.FullSyncSnapshot{
		Completed: true,
		Services: []pb.Service{
			{
				ServiceID:       "svc-fs",
				ServiceKey:      "order-service/http",
				ConnectorID:     "connector-fs",
				Status:          pb.ServiceStatusActive,
				HealthStatus:    pb.HealthStatusHealthy,
				ResourceVersion: 10,
			},
		},
	})

	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-fs",
		SessionEpoch:    5,
		ConnectorID:     "connector-fs",
		EventID:         "evt-stale-after-full-sync",
		ResourceVersion: 5,
	}, pb.PublishService{
		ServiceID:   "svc-fs",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected stale publish rejected after full-sync")
	}
	if ack.ErrorCode != ltfperrors.CodeVersionRollback {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeVersionRollback)
	}
	if ack.CurrentResourceVersion != 10 {
		t.Fatalf("unexpected current resource version: got=%d want=10", ack.CurrentResourceVersion)
	}
	if currentVersion := handler.serviceRegistry.CurrentVersion("svc-fs", "order-service/http"); currentVersion != 10 {
		t.Fatalf("unexpected registry current version after stale publish: got=%d want=10", currentVersion)
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
		ServiceKey:  "order-service/http",
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
		ServiceKey: "order-service/http",
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

// TestPublishHandlerRejectMutationWhenSessionNotActive 验证非 ACTIVE 会话不能写入服务资源。
func TestPublishHandlerRejectMutationWhenSessionNotActive(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-draining",
		ConnectorID: "connector-1",
		Epoch:       3,
		State:       registry.SessionDraining,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
	})
	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-draining",
		SessionEpoch:    3,
		EventID:         "evt-draining",
		ResourceVersion: 1,
		ResourceID:      "svc-draining",
	}, pb.PublishService{
		ServiceID:   "svc-draining",
		ServiceKey:  "draining-service/http",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "draining-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if ack.Accepted {
		t.Fatalf("expected draining session publish rejected")
	}
	if ack.ErrorCode != ltfperrors.CodeInvalidStateTransition {
		t.Fatalf("unexpected error code: got=%s want=%s", ack.ErrorCode, ltfperrors.CodeInvalidStateTransition)
	}
}

// TestPublishHandlerBackfillsCanonicalServiceKey 验证 service_key 为空时会按 <service_name>/<protocol> 自动补全。
func TestPublishHandlerBackfillsCanonicalServiceKey(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-canonical",
		ConnectorID: "connector-canonical",
		Epoch:       7,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	ack := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-canonical",
		SessionEpoch:    7,
		EventID:         "evt-canonical-1",
		ResourceVersion: 1,
		ResourceID:      "service:canonical",
	}, pb.PublishService{
		ServiceID:   "",
		ServiceKey:  "",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: " HTTP ", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !ack.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", ack.ErrorCode)
	}
	if ack.ServiceKey != "order-service/http" {
		t.Fatalf("unexpected canonical service key: got=%s want=%s", ack.ServiceKey, "order-service/http")
	}
	if ack.ServiceID == "" {
		t.Fatalf("expected generated service id not empty")
	}
	serviceSnapshot, exists := handler.serviceRegistry.GetByServiceKey("order-service/http")
	if !exists {
		t.Fatalf("expected service snapshot exists by canonical key")
	}
	if serviceSnapshot.ServiceID != ack.ServiceID {
		t.Fatalf("unexpected service id mapping: got=%s want=%s", serviceSnapshot.ServiceID, ack.ServiceID)
	}
	if len(serviceSnapshot.Endpoints) != 1 || serviceSnapshot.Endpoints[0].Protocol != "http" {
		t.Fatalf("expected endpoint protocol normalized to lower-case, got=%+v", serviceSnapshot.Endpoints)
	}
}

// TestPublishHandlerReusesServiceIDByServiceKey 验证 service_id 为空时会复用同 key 既有 service_id。
func TestPublishHandlerReusesServiceIDByServiceKey(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-reuse",
		ConnectorID: "connector-reuse",
		Epoch:       9,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	baseEnvelope := pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-reuse",
		SessionEpoch:    9,
		EventID:         "evt-reuse-1",
		ResourceVersion: 1,
		ResourceID:      "service:reuse",
	}
	firstAck := handler.HandlePublish(baseEnvelope, pb.PublishService{
		ServiceID:   "",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected first publish accepted, got error=%s", firstAck.ErrorCode)
	}
	if firstAck.ServiceID == "" {
		t.Fatalf("expected first generated service id not empty")
	}

	secondEnvelope := baseEnvelope
	secondEnvelope.EventID = "evt-reuse-2"
	secondEnvelope.ResourceVersion = 2
	secondAck := handler.HandlePublish(secondEnvelope, pb.PublishService{
		ServiceID:   "",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "alice",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18081},
		},
	})
	if !secondAck.Accepted {
		t.Fatalf("expected second publish accepted, got error=%s", secondAck.ErrorCode)
	}
	if secondAck.ServiceID != firstAck.ServiceID {
		t.Fatalf("expected service id reused by key: first=%s second=%s", firstAck.ServiceID, secondAck.ServiceID)
	}
}

// TestPublishHandlerAllowsConcurrentPublishBySameServiceKey 验证同 key 多 connector 发布会归并到同一服务池并保留实例。
func TestPublishHandlerAllowsConcurrentPublishBySameServiceKey(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       11,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       12,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	firstAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-a",
		SessionEpoch:    11,
		ConnectorID:     "connector-a",
		EventID:         "evt-multi-a-1",
		ResourceVersion: 1,
	}, pb.PublishService{
		ServiceID:   "",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 18080},
		},
	})
	if !firstAck.Accepted {
		t.Fatalf("expected first publish accepted, got error=%s", firstAck.ErrorCode)
	}
	secondAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-b",
		SessionEpoch:    12,
		ConnectorID:     "connector-b",
		EventID:         "evt-multi-b-1",
		ResourceVersion: 1,
	}, pb.PublishService{
		ServiceID:   "",
		ServiceKey:  "order-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "order-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 19090},
		},
	})
	if !secondAck.Accepted {
		t.Fatalf("expected second publish accepted, got error=%s", secondAck.ErrorCode)
	}
	if secondAck.ServiceID != firstAck.ServiceID {
		t.Fatalf("expected pooled service_id reused: first=%s second=%s", firstAck.ServiceID, secondAck.ServiceID)
	}
	// 池级接口仍返回一条逻辑服务，避免破坏旧调用路径。
	if services := handler.serviceRegistry.List(); len(services) != 1 {
		t.Fatalf("unexpected service pool size: got=%d want=1", len(services))
	}
	instances := handler.serviceRegistry.ListInstancesByServiceKey("order-service/http")
	if len(instances) != 2 {
		t.Fatalf("unexpected service instance count: got=%d want=2", len(instances))
	}
}

// TestPublishHandlerUnpublishRemovesOnlyMatchedInstance 验证按 connector/session 下线时仅删除目标实例。
func TestPublishHandlerUnpublishRemovesOnlyMatchedInstance(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-a",
		ConnectorID: "connector-a",
		Epoch:       21,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-b",
		ConnectorID: "connector-b",
		Epoch:       22,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700000000, 0).UTC() },
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-a-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		ServiceKey:  "pay-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "pay-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 28080},
		},
	})
	handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-b",
		SessionEpoch:    22,
		ConnectorID:     "connector-b",
		EventID:         "evt-unpub-b-pub",
		ResourceVersion: 1,
	}, pb.PublishService{
		ServiceKey:  "pay-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "pay-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 29090},
		},
	})

	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-a",
		SessionEpoch:    21,
		ConnectorID:     "connector-a",
		EventID:         "evt-unpub-a-1",
		ResourceVersion: 2,
	}, pb.UnpublishService{
		ServiceKey: "pay-service/http",
	})
	if !unpublishAck.Accepted {
		t.Fatalf("expected unpublish accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if instances := handler.serviceRegistry.ListInstancesByServiceKey("pay-service/http"); len(instances) != 1 {
		t.Fatalf("unexpected remaining instance count: got=%d want=1", len(instances))
	}
	if services := handler.serviceRegistry.List(); len(services) != 1 {
		t.Fatalf("expected pooled service still exists, got=%d", len(services))
	}
}

// TestPublishHandlerConcurrentPublishRepublishAndRecoverByServiceKey
// 验证同 key 多 connector 并发发布、重复 republish、实例摘除与恢复的闭环行为。
func TestPublishHandlerConcurrentPublishRepublishAndRecoverByServiceKey(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-1",
		ConnectorID: "connector-1",
		Epoch:       31,
		State:       registry.SessionActive,
	})
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-2",
		ConnectorID: "connector-2",
		Epoch:       32,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700001000, 0).UTC() },
	})

	// 两个 connector 在同一服务 key 下并发发布，模拟控制面同时接收多实例上报。
	startSignal := make(chan struct{})
	type publishResult struct {
		ack pb.PublishServiceAck
	}
	results := make(chan publishResult, 2)
	var waitGroup sync.WaitGroup
	publish := func(sessionID string, sessionEpoch uint64, connectorID string, eventID string, port uint32) {
		defer waitGroup.Done()
		<-startSignal
		results <- publishResult{
			ack: handler.HandlePublish(pb.ControlEnvelope{
				VersionMajor:    2,
				VersionMinor:    1,
				MessageType:     pb.ControlMessagePublishService,
				SessionID:       sessionID,
				SessionEpoch:    sessionEpoch,
				ConnectorID:     connectorID,
				EventID:         eventID,
				ResourceVersion: 1,
			}, pb.PublishService{
				ServiceID:   "svc-regression",
				ServiceKey:  "inventory-service/http",
				Namespace:   "dev",
				Environment: "demo",
				ServiceName: "inventory-service",
				ServiceType: "http",
				Endpoints: []pb.ServiceEndpoint{
					{Protocol: "http", Host: "127.0.0.1", Port: port},
				},
			}),
		}
	}
	waitGroup.Add(2)
	go publish("session-1", 31, "connector-1", "evt-concurrent-1", 30080)
	go publish("session-2", 32, "connector-2", "evt-concurrent-2", 30090)
	close(startSignal)
	waitGroup.Wait()
	close(results)
	for result := range results {
		if !result.ack.Accepted {
			t.Fatalf("expected concurrent publish accepted, got error=%s", result.ack.ErrorCode)
		}
		if result.ack.ServiceID != "svc-regression" {
			t.Fatalf("unexpected pooled service_id: got=%s want=svc-regression", result.ack.ServiceID)
		}
	}
	if instances := handler.serviceRegistry.ListInstancesByServiceID("svc-regression"); len(instances) != 2 {
		t.Fatalf("unexpected instance count after concurrent publish: got=%d want=2", len(instances))
	}

	// 同一 connector 连续 republish 只应覆盖该实例，不应新增实例条目。
	republishAckVersion2 := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-1",
		SessionEpoch:    31,
		ConnectorID:     "connector-1",
		EventID:         "evt-republish-connector-1-v2",
		ResourceVersion: 2,
	}, pb.PublishService{
		ServiceID:   "svc-regression",
		ServiceKey:  "inventory-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "inventory-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 30180},
		},
	})
	if !republishAckVersion2.Accepted {
		t.Fatalf("expected republish version2 accepted, got error=%s", republishAckVersion2.ErrorCode)
	}
	republishAckVersion3 := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-1",
		SessionEpoch:    31,
		ConnectorID:     "connector-1",
		EventID:         "evt-republish-connector-1-v3",
		ResourceVersion: 3,
	}, pb.PublishService{
		ServiceID:   "svc-regression",
		ServiceKey:  "inventory-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "inventory-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 30280},
		},
	})
	if !republishAckVersion3.Accepted {
		t.Fatalf("expected republish version3 accepted, got error=%s", republishAckVersion3.ErrorCode)
	}
	instancesAfterRepublish := handler.serviceRegistry.ListInstancesByServiceID("svc-regression")
	if len(instancesAfterRepublish) != 2 {
		t.Fatalf("republish should not create new instances: got=%d want=2", len(instancesAfterRepublish))
	}
	foundSessionOne := false
	for _, instance := range instancesAfterRepublish {
		if instance.SessionID != "session-1" {
			continue
		}
		foundSessionOne = true
		if len(instance.Service.Endpoints) != 1 || instance.Service.Endpoints[0].Port != 30280 {
			t.Fatalf("expected republish endpoint applied to session-1 instance, got=%+v", instance.Service.Endpoints)
		}
	}
	if !foundSessionOne {
		t.Fatalf("expected session-1 instance exists after republish")
	}

	// 摘除 connector-2 实例后应只剩 1 条实例。
	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-2",
		SessionEpoch:    32,
		ConnectorID:     "connector-2",
		EventID:         "evt-remove-connector-2",
		ResourceVersion: 2,
	}, pb.UnpublishService{
		ServiceID:  "svc-regression",
		ServiceKey: "inventory-service/http",
	})
	if !unpublishAck.Accepted {
		t.Fatalf("expected unpublish accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if instances := handler.serviceRegistry.ListInstancesByServiceID("svc-regression"); len(instances) != 1 {
		t.Fatalf("unexpected instance count after remove: got=%d want=1", len(instances))
	}

	// connector-2 恢复 republish 后实例池应回到 2 条，完成摘除/恢复闭环。
	recoveryAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-2",
		SessionEpoch:    32,
		ConnectorID:     "connector-2",
		EventID:         "evt-recover-connector-2",
		ResourceVersion: 3,
	}, pb.PublishService{
		ServiceID:   "svc-regression",
		ServiceKey:  "inventory-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "inventory-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 30390},
		},
	})
	if !recoveryAck.Accepted {
		t.Fatalf("expected recovery publish accepted, got error=%s", recoveryAck.ErrorCode)
	}
	if instances := handler.serviceRegistry.ListInstancesByServiceID("svc-regression"); len(instances) != 2 {
		t.Fatalf("unexpected instance count after recovery: got=%d want=2", len(instances))
	}
}

// TestPublishHandlerServiceIdentityConsistencyAcrossAckAuditRuntime
// 验证 service_id/service_key/service_instance_id 在 ACK、audit、runtime 三处保持一致。
func TestPublishHandlerServiceIdentityConsistencyAcrossAckAuditRuntime(t *testing.T) {
	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-consistency",
		ConnectorID: "connector-consistency",
		Epoch:       41,
		State:       registry.SessionActive,
	})
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Now:             func() time.Time { return time.Unix(1700002000, 0).UTC() },
	})

	// 捕获审计日志并在测试结束后恢复默认 logger，避免影响其他用例。
	var logBuffer bytes.Buffer
	originalLogger := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&logBuffer, &slog.HandlerOptions{})))
	defer slog.SetDefault(originalLogger)

	publishAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-consistency",
		SessionEpoch:    41,
		ConnectorID:     "connector-consistency",
		EventID:         "evt-consistency-publish",
		ResourceVersion: 1,
		ResourceID:      "svc-consistency",
	}, pb.PublishService{
		ServiceID:   "svc-consistency",
		ServiceKey:  "consistency-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "consistency-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 31080},
		},
	})
	if !publishAck.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", publishAck.ErrorCode)
	}
	if publishAck.ServiceID == "" || publishAck.ServiceKey == "" {
		t.Fatalf("expected ack service identity not empty, ack=%+v", publishAck)
	}
	instances := handler.serviceRegistry.ListInstancesByServiceID(publishAck.ServiceID)
	if len(instances) != 1 {
		t.Fatalf("unexpected runtime instance count: got=%d want=1", len(instances))
	}
	runtimeInstanceID := strings.TrimSpace(instances[0].ServiceInstanceID)
	if runtimeInstanceID == "" {
		t.Fatalf("expected runtime service_instance_id not empty")
	}
	publishAuditEntry := decodeLastServiceResourceAuditEntry(t, logBuffer.Bytes(), "publish", "session-consistency")
	if gotServiceID, _ := publishAuditEntry["service_id"].(string); gotServiceID != publishAck.ServiceID {
		t.Fatalf("unexpected publish audit service_id: got=%v want=%s", publishAuditEntry["service_id"], publishAck.ServiceID)
	}
	if gotServiceKey, _ := publishAuditEntry["service_key"].(string); gotServiceKey != publishAck.ServiceKey {
		t.Fatalf("unexpected publish audit service_key: got=%v want=%s", publishAuditEntry["service_key"], publishAck.ServiceKey)
	}
	if gotInstanceID, _ := publishAuditEntry["service_instance_id"].(string); gotInstanceID != runtimeInstanceID {
		t.Fatalf("unexpected publish audit service_instance_id: got=%v want=%s", publishAuditEntry["service_instance_id"], runtimeInstanceID)
	}

	unpublishAck := handler.HandleUnpublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessageUnpublishService,
		SessionID:       "session-consistency",
		SessionEpoch:    41,
		ConnectorID:     "connector-consistency",
		EventID:         "evt-consistency-unpublish",
		ResourceVersion: 2,
		ResourceID:      "svc-consistency",
	}, pb.UnpublishService{
		ServiceID:  publishAck.ServiceID,
		ServiceKey: publishAck.ServiceKey,
	})
	if !unpublishAck.Accepted {
		t.Fatalf("expected unpublish accepted, got error=%s", unpublishAck.ErrorCode)
	}
	if unpublishAck.ServiceID != publishAck.ServiceID || unpublishAck.ServiceKey != publishAck.ServiceKey {
		t.Fatalf(
			"unexpected unpublish ack identity: got service_id=%s service_key=%s want service_id=%s service_key=%s",
			unpublishAck.ServiceID,
			unpublishAck.ServiceKey,
			publishAck.ServiceID,
			publishAck.ServiceKey,
		)
	}
	unpublishAuditEntry := decodeLastServiceResourceAuditEntry(t, logBuffer.Bytes(), "unpublish", "session-consistency")
	if gotServiceID, _ := unpublishAuditEntry["service_id"].(string); gotServiceID != publishAck.ServiceID {
		t.Fatalf("unexpected unpublish audit service_id: got=%v want=%s", unpublishAuditEntry["service_id"], publishAck.ServiceID)
	}
	if gotServiceKey, _ := unpublishAuditEntry["service_key"].(string); gotServiceKey != publishAck.ServiceKey {
		t.Fatalf("unexpected unpublish audit service_key: got=%v want=%s", unpublishAuditEntry["service_key"], publishAck.ServiceKey)
	}
	if gotInstanceID, _ := unpublishAuditEntry["service_instance_id"].(string); gotInstanceID != runtimeInstanceID {
		t.Fatalf("unexpected unpublish audit service_instance_id: got=%v want=%s", unpublishAuditEntry["service_instance_id"], runtimeInstanceID)
	}

	// runtime 视图应与 unpublish ACK 对齐，删除目标实例后不再保留服务池。
	if remaining := handler.serviceRegistry.ListInstancesByServiceID(publishAck.ServiceID); len(remaining) != 0 {
		t.Fatalf("expected runtime instances removed after unpublish, got=%d", len(remaining))
	}
}

// TestPublishHandlerRecordsServicePublishMetrics 验证发布路径会记录服务池/实例维度发布指标。
func TestPublishHandlerRecordsServicePublishMetrics(t *testing.T) {
	t.Parallel()

	sessionRegistry := registry.NewSessionRegistry()
	sessionRegistry.Upsert(time.Now().UTC(), registry.SessionRuntime{
		SessionID:   "session-metric",
		ConnectorID: "connector-metric",
		Epoch:       7,
		State:       registry.SessionActive,
	})
	metrics := obs.NewMetrics()
	handler := NewPublishHandler(PublishHandlerOptions{
		SessionRegistry: sessionRegistry,
		Metrics:         metrics,
		Now:             func() time.Time { return time.Unix(1700003000, 0).UTC() },
	})

	publishAck := handler.HandlePublish(pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-metric",
		SessionEpoch:    7,
		ConnectorID:     "connector-metric",
		EventID:         "evt-metric-publish",
		ResourceVersion: 1,
		ResourceID:      "svc-metric",
	}, pb.PublishService{
		ServiceID:   "svc-metric",
		ServiceKey:  "metric-service/http",
		Namespace:   "dev",
		Environment: "demo",
		ServiceName: "metric-service",
		ServiceType: "http",
		Endpoints: []pb.ServiceEndpoint{
			{Protocol: "http", Host: "127.0.0.1", Port: 32080},
		},
	})
	if !publishAck.Accepted {
		t.Fatalf("expected publish accepted, got error=%s", publishAck.ErrorCode)
	}
	instances := handler.serviceRegistry.ListInstancesByServiceID("svc-metric")
	if len(instances) != 1 {
		t.Fatalf("unexpected instance count: got=%d want=1", len(instances))
	}
	serviceInstanceID := strings.TrimSpace(instances[0].ServiceInstanceID)
	if serviceInstanceID == "" {
		t.Fatalf("expected service_instance_id not empty")
	}
	if metrics.BridgeServicePublishTotal("svc-metric") != 1 {
		t.Fatalf("unexpected service publish total: got=%d want=1", metrics.BridgeServicePublishTotal("svc-metric"))
	}
	if metrics.BridgeServiceInstancePublishTotal("svc-metric", serviceInstanceID) != 1 {
		t.Fatalf(
			"unexpected service instance publish total: got=%d want=1",
			metrics.BridgeServiceInstancePublishTotal("svc-metric", serviceInstanceID),
		)
	}
}

// decodeLastServiceResourceAuditEntry 从日志缓冲中提取指定 action/session 的最新 service 资源审计记录。
func decodeLastServiceResourceAuditEntry(
	testingObject *testing.T,
	rawLogs []byte,
	action string,
	sessionID string,
) map[string]any {
	testingObject.Helper()

	normalizedAction := strings.TrimSpace(action)
	normalizedSessionID := strings.TrimSpace(sessionID)
	lines := bytes.Split(bytes.TrimSpace(rawLogs), []byte{'\n'})
	for index := len(lines) - 1; index >= 0; index-- {
		line := bytes.TrimSpace(lines[index])
		if len(line) == 0 {
			continue
		}
		var entry map[string]any
		if err := json.Unmarshal(line, &entry); err != nil {
			testingObject.Fatalf("unmarshal service audit log entry failed: %v", err)
		}
		message, _ := entry["msg"].(string)
		logAction, _ := entry["action"].(string)
		logSessionID, _ := entry["session_id"].(string)
		if message == "service resource audit" &&
			logAction == normalizedAction &&
			logSessionID == normalizedSessionID {
			return entry
		}
	}
	testingObject.Fatalf(
		"expected service resource audit log entry for action=%s session_id=%s, got=%s",
		normalizedAction,
		normalizedSessionID,
		string(rawLogs),
	)
	return nil
}
