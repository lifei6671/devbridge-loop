package validate

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestValidateControlEnvelope 验证控制面封装基础校验规则。
func TestValidateControlEnvelope(t *testing.T) {
	t.Parallel()

	codecPayload, err := testkit.GoldenControlEnvelope(pb.ControlMessagePublishService, testkit.GoldenPublishService())
	if err != nil {
		t.Fatalf("build golden envelope failed: %v", err)
	}
	if err := ValidateControlEnvelope(codecPayload); err != nil {
		t.Fatalf("validate control envelope failed: %v", err)
	}
}

// TestValidateControlEnvelopeRejectUnknownType 验证未知消息类型会被拒绝。
func TestValidateControlEnvelopeRejectUnknownType(t *testing.T) {
	t.Parallel()

	envelope := pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageType("Unknown"),
	}
	err := ValidateControlEnvelope(envelope)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 错误码断言确保调用方可以可靠分支。
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnknownMessageType) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidatePublishService 验证服务发布消息的关键字段校验。
func TestValidatePublishService(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	if err := ValidatePublishService(message); err != nil {
		t.Fatalf("validate publish service failed: %v", err)
	}
}

// TestValidatePublishServiceRejectMissingEndpoint 验证缺少 endpoint 会被拒绝。
func TestValidatePublishServiceRejectMissingEndpoint(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.Endpoints = nil
	err := ValidatePublishService(message)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 缺字段场景应返回缺失字段错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateRouteScope 验证 route 与 target scope 一致性校验。
func TestValidateRouteScope(t *testing.T) {
	t.Parallel()

	if err := ValidateRouteScope("dev", "alice", "dev", "alice"); err != nil {
		t.Fatalf("validate route scope failed: %v", err)
	}
}

// TestValidateRouteScopeRejectMismatch 验证跨 scope 场景会被拒绝。
func TestValidateRouteScopeRejectMismatch(t *testing.T) {
	t.Parallel()

	err := ValidateRouteScope("dev", "alice", "prod", "alice")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 跨 scope 属于硬约束违规。
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidScope) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateStreamPayloadRejectInvalidOneof 验证 oneof 冲突会被拒绝。
func TestValidateStreamPayloadRejectInvalidOneof(t *testing.T) {
	t.Parallel()

	payload := pb.StreamPayload{
		OpenReq: &pb.TrafficOpen{
			TrafficID: "traffic-001",
			ServiceID: "svc-001",
		},
		Close: &pb.TrafficClose{
			TrafficID: "traffic-001",
		},
	}
	err := ValidateStreamPayload(payload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// oneof 冲突应返回数据面 oneof 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeTrafficInvalidOneof) {
		t.Fatalf("unexpected error: %v", err)
	}
}
