package codec

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestJSONCodecControlEnvelopeRoundTrip 验证控制面封装可完成编码解码闭环。
func TestJSONCodecControlEnvelopeRoundTrip(t *testing.T) {
	t.Parallel()

	codec := NewJSONCodec()
	testCases := []struct {
		name        string
		messageType pb.ControlMessageType
		payload     any
	}{
		{
			name:        "connector_hello",
			messageType: pb.ControlMessageConnectorHello,
			payload:     testkit.GoldenConnectorHello(),
		},
		{
			name:        "publish_service",
			messageType: pb.ControlMessagePublishService,
			payload:     testkit.GoldenPublishService(),
		},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			envelope, err := testkit.GoldenControlEnvelope(testCase.messageType, testCase.payload)
			if err != nil {
				t.Fatalf("build golden envelope failed: %v", err)
			}

			encoded, err := codec.EncodeControlEnvelope(envelope)
			if err != nil {
				t.Fatalf("encode control envelope failed: %v", err)
			}

			decoded, err := codec.DecodeControlEnvelope(encoded)
			if err != nil {
				t.Fatalf("decode control envelope failed: %v", err)
			}

			// 保证核心元信息不会在编码链路中丢失。
			if decoded.MessageType != envelope.MessageType {
				t.Fatalf("message type mismatch: got=%s want=%s", decoded.MessageType, envelope.MessageType)
			}
			if decoded.SessionEpoch != envelope.SessionEpoch {
				t.Fatalf("session epoch mismatch: got=%d want=%d", decoded.SessionEpoch, envelope.SessionEpoch)
			}
		})
	}
}

// TestJSONCodecStreamPayloadRoundTrip 验证数据面 oneof 编解码闭环。
func TestJSONCodecStreamPayloadRoundTrip(t *testing.T) {
	t.Parallel()

	codec := NewJSONCodec()
	payload := pb.StreamPayload{
		OpenReq: &pb.TrafficOpen{
			TrafficID: "traffic-001",
			ServiceID: "svc-001",
		},
	}

	encoded, err := codec.EncodeStreamPayload(payload)
	if err != nil {
		t.Fatalf("encode stream payload failed: %v", err)
	}
	decoded, err := codec.DecodeStreamPayload(encoded)
	if err != nil {
		t.Fatalf("decode stream payload failed: %v", err)
	}

	// oneof 字段应保持为 OpenReq 且字段值一致。
	if decoded.OpenReq == nil {
		t.Fatalf("openReq should not be nil")
	}
	if decoded.OpenReq.TrafficID != payload.OpenReq.TrafficID {
		t.Fatalf("traffic id mismatch: got=%s want=%s", decoded.OpenReq.TrafficID, payload.OpenReq.TrafficID)
	}
}

// TestJSONCodecStreamPayloadRejectInvalidOneof 验证 oneof 冲突会被拒绝。
func TestJSONCodecStreamPayloadRejectInvalidOneof(t *testing.T) {
	t.Parallel()

	codec := NewJSONCodec()
	invalidPayload := pb.StreamPayload{
		OpenReq: &pb.TrafficOpen{
			TrafficID: "traffic-001",
			ServiceID: "svc-001",
		},
		Close: &pb.TrafficClose{
			TrafficID: "traffic-001",
		},
	}

	_, err := codec.EncodeStreamPayload(invalidPayload)
	if err == nil {
		t.Fatalf("expected invalid oneof error, got nil")
	}
	// 使用错误码断言，确保调用方可稳定分类处理。
	if !ltfperrors.IsCode(err, ltfperrors.CodeTrafficInvalidOneof) {
		t.Fatalf("unexpected error code: %v", err)
	}
}
