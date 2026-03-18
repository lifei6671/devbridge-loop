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

// TestValidateControlEnvelopeRejectMissingSessionID 验证资源级消息缺失 sessionID 会被拒绝。
func TestValidateControlEnvelopeRejectMissingSessionID(t *testing.T) {
	t.Parallel()

	codecPayload, err := testkit.GoldenControlEnvelope(pb.ControlMessagePublishService, testkit.GoldenPublishService())
	if err != nil {
		t.Fatalf("build golden envelope failed: %v", err)
	}
	// 清空 sessionID，触发资源级元信息校验失败。
	codecPayload.SessionID = ""
	err = ValidateControlEnvelope(codecPayload)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 缺失必填字段应返回统一错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidateConnectorHelloAcceptsOptionalScope 验证 ConnectorHello 的 namespace/environment 可选。
func TestValidateConnectorHelloAcceptsOptionalScope(t *testing.T) {
	t.Parallel()

	hello := testkit.GoldenConnectorHello()
	// 显式清空 scope 字段，验证握手校验不再将其视为必填。
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
	// connectorId 是接入主身份键，缺失时必须报错。
	hello.ConnectorID = ""
	err := ValidateConnectorHello(hello)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
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

// TestValidatePublishServiceAcceptsEmptyServiceKey 验证 serviceKey 为空时允许由服务端按 canonical 规则补全。
func TestValidatePublishServiceAcceptsEmptyServiceKey(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.ServiceKey = ""
	if err := ValidatePublishService(message); err != nil {
		t.Fatalf("validate publish service with empty service key failed: %v", err)
	}
}

// TestValidatePublishServiceAcceptsSNIWithoutScope 验证无 namespace/environment 时，只要有 SNI 仍可发布。
func TestValidatePublishServiceAcceptsSNIWithoutScope(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.Namespace = ""
	message.Environment = ""
	message.Exposure.SNIName = "order.dev.example.com"
	if err := ValidatePublishService(message); err != nil {
		t.Fatalf("validate publish service with sni-only failed: %v", err)
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

// TestValidatePublishServiceRejectNonCanonicalServiceKey 验证显式 service_key 不符合 canonical 规则时会被拒绝。
func TestValidatePublishServiceRejectNonCanonicalServiceKey(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	// 人为构造旧格式 key，验证新规则会拒绝非 canonical 值。
	message.ServiceKey = "dev/alice/order-service"
	err := ValidatePublishService(message)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedValue) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestValidatePublishServiceRejectMissingScopeAndSNI 验证 namespace/environment/sni 全空时会被拒绝。
func TestValidatePublishServiceRejectMissingScopeAndSNI(t *testing.T) {
	t.Parallel()

	message := testkit.GoldenPublishService()
	message.Namespace = ""
	message.Environment = ""
	message.Exposure.SNIName = ""
	for index := range message.Endpoints {
		message.Endpoints[index].ServerName = ""
	}
	err := ValidatePublishService(message)
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

	if err := ValidateRouteScope("dev", "alice", "dev", "alice"); err != nil {
		t.Fatalf("validate route scope failed: %v", err)
	}
}

// TestValidateRouteScopeAllowsPartialScope 验证 route/target 一侧 scope 为空时按“不约束”处理。
func TestValidateRouteScopeAllowsPartialScope(t *testing.T) {
	t.Parallel()

	if err := ValidateRouteScope("", "alice", "dev", "alice"); err != nil {
		t.Fatalf("validate partial route scope failed: %v", err)
	}
	if err := ValidateRouteScope("dev", "", "dev", "prod"); err != nil {
		t.Fatalf("validate partial environment scope failed: %v", err)
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
	// code 缺失应返回 missing required 错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeMissingRequiredField) {
		t.Fatalf("unexpected error: %v", err)
	}
}
