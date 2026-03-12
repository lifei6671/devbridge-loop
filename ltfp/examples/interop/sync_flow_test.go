package interop

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/codec"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// TestPublishServiceFlow 验证发布服务在协议库层的最小端到端流程。
func TestPublishServiceFlow(t *testing.T) {
	t.Parallel()

	jsonCodec := codec.NewJSONCodec()
	publish := testkit.GoldenPublishService()
	payload, err := jsonCodec.EncodePayload(publish)
	if err != nil {
		t.Fatalf("encode publish payload failed: %v", err)
	}

	envelope := pb.ControlEnvelope{
		VersionMajor:    2,
		VersionMinor:    1,
		MessageType:     pb.ControlMessagePublishService,
		SessionID:       "session-001",
		SessionEpoch:    1,
		EventID:         "event-publish-001",
		ResourceVersion: 10,
		Payload:         payload,
	}
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		t.Fatalf("validate envelope failed: %v", err)
	}

	var decoded pb.PublishService
	if err := jsonCodec.DecodePayload(envelope.Payload, &decoded); err != nil {
		t.Fatalf("decode publish payload failed: %v", err)
	}
	if err := validate.ValidatePublishService(decoded); err != nil {
		t.Fatalf("validate publish service failed: %v", err)
	}

	ack := consistency.BuildPublishServiceAck(pb.EventStatusAccepted, decoded.ServiceID, decoded.ServiceKey, 10, 10, "", "")
	// 成功发布场景下 ACK 必须为 accepted=true。
	if !ack.Accepted {
		t.Fatalf("expected publish ack accepted")
	}
}

// TestServiceHealthReportFlow 验证健康上报在协议库层的最小端到端流程。
func TestServiceHealthReportFlow(t *testing.T) {
	t.Parallel()

	report := pb.ServiceHealthReport{
		ServiceID:           "svc_01J8Z6C4X9K7M2P4",
		ServiceKey:          "dev/alice/order-service",
		ServiceHealthStatus: pb.HealthStatusHealthy,
		CheckTimeUnix:       time.Now().Unix(),
		EndpointStatuses: []pb.EndpointHealthStatus{
			{
				EndpointID:   "ep-1",
				HealthStatus: pb.HealthStatusHealthy,
			},
		},
	}

	jsonCodec := codec.NewJSONCodec()
	payload, err := jsonCodec.EncodePayload(report)
	if err != nil {
		t.Fatalf("encode health payload failed: %v", err)
	}

	envelope := pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageServiceHealthReport,
		SessionID:    "session-001",
		SessionEpoch: 1,
		Payload:      payload,
	}
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		t.Fatalf("validate envelope failed: %v", err)
	}

	var decoded pb.ServiceHealthReport
	if err := jsonCodec.DecodePayload(envelope.Payload, &decoded); err != nil {
		t.Fatalf("decode health payload failed: %v", err)
	}
	// 健康上报解码后应保持 endpoint 明细。
	if len(decoded.EndpointStatuses) != 1 {
		t.Fatalf("unexpected endpoint statuses length: %d", len(decoded.EndpointStatuses))
	}
}

// TestFullSyncFlow 验证 full-sync 请求与快照在协议库层的最小端到端流程。
func TestFullSyncFlow(t *testing.T) {
	t.Parallel()

	request := pb.FullSyncRequest{
		RequestID:            "full-sync-001",
		ConnectorID:          "connector-dev-01",
		SessionID:            "session-001",
		SessionEpoch:         1,
		SinceResourceVersion: 0,
	}
	if err := validate.ValidateFullSyncRequest(request); err != nil {
		t.Fatalf("validate full sync request failed: %v", err)
	}

	snapshot := testkit.GoldenFullSyncSnapshot()
	if err := validate.ValidateFullSyncSnapshot(snapshot); err != nil {
		t.Fatalf("validate full sync snapshot failed: %v", err)
	}
}
