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
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnknownMessageType) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateControlEnvelopeRejectMissingSessionID 验证资源级消息缺失 sessionID 会被拒绝。
func TestValidateControlEnvelopeRejectMissingSessionID(t *testing.T) {
	t.Parallel()

	codecPayload, err := testkit.GoldenControlEnvelope(pb.ControlMessagePublishService, testkit.GoldenPublishService())
	if err != nil {
		t.Fatalf("build golden envelope failed: %v", err)
	}
	codecPayload.SessionID = ""
	err = ValidateControlEnvelope(codecPayload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateConnectorHelloAcceptsOptionalScope 验证 ConnectorHello 的 namespace/environment 可选。
func TestValidateConnectorHelloAcceptsOptionalScope(t *testing.T) {
	t.Parallel()

	hello := testkit.GoldenConnectorHello()
	hello.Namespace = ""
	hello.Environment = ""
	if err := ValidateConnectorHello(hello); err != nil {
		t.Fatalf("validate connector hello failed: %v", err)
	}
}

// TestValidateConnectorHelloRejectMissingConnectorID 验证 connectorId 缺失仍会被拒绝。
func TestValidateConnectorHelloRejectMissingConnectorID(t *testing.T) {
	t.Parallel()

	hello := testkit.GoldenConnectorHello()
	hello.ConnectorID = ""
	err := ValidateConnectorHello(hello)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateNoLegacyFieldsRejectServiceKey 验证原始负载携带旧字段时会被拒绝。
func TestValidateNoLegacyFieldsRejectServiceKey(t *testing.T) {
	t.Parallel()

	err := ValidateNoLegacyFields([]byte(`{"serviceKey":"dev/alice/order-service"}`), "serviceKey", "service_id")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedLegacyProtocol) {
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

// TestValidatePublishServiceRejectMissingScope 验证 scope 缺失会被拒绝。
func TestValidatePublishServiceRejectMissingScope(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.Scope.Namespace = ""
	err := ValidatePublishService(message)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
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
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidatePublishServiceRejectMixedEndpointProtocol 验证单条 PublishService 仅允许单协议 endpoint。
func TestValidatePublishServiceRejectMixedEndpointProtocol(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.Endpoints = append(message.Endpoints, pb.ServiceEndpoint{
		EndpointID: "ep-2",
		Protocol:   "grpc",
		Host:       "127.0.0.1",
		Port:       19090,
	})
	err := ValidatePublishService(message)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedValue) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateUnpublishService 验证下线消息支持实例或逻辑服务定位。
func TestValidateUnpublishService(t *testing.T) {
	t.Parallel()

	message := pb.UnpublishService{
		InstanceID: "si-001",
	}
	if err := ValidateUnpublishService(message); err != nil {
		t.Fatalf("validate unpublish service failed: %v", err)
	}
}

// TestValidateUnpublishServiceRejectMissingIdentifiers 验证缺失定位信息会被拒绝。
func TestValidateUnpublishServiceRejectMissingIdentifiers(t *testing.T) {
	t.Parallel()

	err := ValidateUnpublishService(pb.UnpublishService{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateServiceHealthReportRejectMissingInstanceID 验证健康上报缺少 instanceId 会被拒绝。
func TestValidateServiceHealthReportRejectMissingInstanceID(t *testing.T) {
	t.Parallel()

	err := ValidateServiceHealthReport(pb.ServiceHealthReport{})
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateRouteScope 验证 route 与 target scope 一致性校验。
func TestValidateRouteScope(t *testing.T) {
	t.Parallel()

	if err := ValidateRouteScope(
		pb.Scope{Namespace: "dev", Environment: "alice"},
		pb.Scope{Namespace: "dev", Environment: "alice"},
	); err != nil {
		t.Fatalf("validate route scope failed: %v", err)
	}
}

// TestValidateRouteScopeAllowsPartialScope 验证 route/target 一侧 scope 为空时按“不约束”处理。
func TestValidateRouteScopeAllowsPartialScope(t *testing.T) {
	t.Parallel()

	if err := ValidateRouteScope(
		pb.Scope{Environment: "alice"},
		pb.Scope{Namespace: "dev", Environment: "alice"},
	); err != nil {
		t.Fatalf("validate partial route scope failed: %v", err)
	}
	if err := ValidateRouteScope(
		pb.Scope{Namespace: "dev"},
		pb.Scope{Namespace: "dev", Environment: "prod"},
	); err != nil {
		t.Fatalf("validate partial environment scope failed: %v", err)
	}
}

// TestValidateRouteScopeRejectMismatch 验证跨 scope 场景会被拒绝。
func TestValidateRouteScopeRejectMismatch(t *testing.T) {
	t.Parallel()

	err := ValidateRouteScope(
		pb.Scope{Namespace: "dev", Environment: "alice"},
		pb.Scope{Namespace: "prod", Environment: "alice"},
	)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeInvalidScope) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateStreamPayloadRejectInvalidOneof 验证 oneof 冲突会被拒绝。
func TestValidateStreamPayloadRejectInvalidOneof(t *testing.T) {
	t.Parallel()

	payload := pb.StreamPayload{
		OpenReq: &pb.TrafficOpen{
			TrafficID:        "traffic-001",
			LogicalServiceID: "ls-001",
			InstanceID:       "si-001",
		},
		Close: &pb.TrafficClose{
			TrafficID: "traffic-001",
		},
	}
	err := ValidateStreamPayload(payload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeTrafficInvalidOneof) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateTrafficOpenRejectMissingLogicalServiceID 验证缺失 logicalServiceId 会被拒绝。
func TestValidateTrafficOpenRejectMissingLogicalServiceID(t *testing.T) {
	t.Parallel()

	open := testkit.GoldenTrafficOpen()
	open.LogicalServiceID = ""
	err := ValidateTrafficOpen(open)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeTrafficInvalidLogicalServiceID) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateControlError 验证控制面错误消息校验。
func TestValidateControlError(t *testing.T) {
	t.Parallel()

	message := pb.ControlError{
		Code:    "NEGOTIATION_UNSUPPORTED_FEATURE",
		Message: "required feature missing",
	}
	if err := ValidateControlError(message); err != nil {
		t.Fatalf("validate control error failed: %v", err)
	}
}

// TestValidateControlErrorRejectMissingCode 验证缺失 code 会被拒绝。
func TestValidateControlErrorRejectMissingCode(t *testing.T) {
	t.Parallel()

	message := pb.ControlError{
		Message: "required feature missing",
	}
	err := ValidateControlError(message)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}
