package transport

import (
	"encoding/json"
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestControlFrameTypeMappingRoundTrip 验证控制消息类型与帧类型映射可双向闭环。
func TestControlFrameTypeMappingRoundTrip(testingObject *testing.T) {
	testingObject.Parallel()

	cases := []pb.ControlMessageType{
		pb.ControlMessageConnectorHello,
		pb.ControlMessageConnectorWelcome,
		pb.ControlMessageConnectorAuth,
		pb.ControlMessageConnectorAuthAck,
		pb.ControlMessageHeartbeat,
		pb.ControlMessagePublishService,
		pb.ControlMessagePublishServiceAck,
		pb.ControlMessageUnpublishService,
		pb.ControlMessageUnpublishServiceAck,
		pb.ControlMessageServiceHealthReport,
		pb.ControlMessageTunnelPoolReport,
		pb.ControlMessageTunnelRefillRequest,
		pb.ControlMessageRouteAssign,
		pb.ControlMessageRouteAssignAck,
		pb.ControlMessageRouteRevoke,
		pb.ControlMessageRouteRevokeAck,
		pb.ControlMessageRouteStatusReport,
		pb.ControlMessageControlError,
	}
	for _, messageType := range cases {
		frameType, err := ControlFrameTypeForMessageType(messageType)
		if err != nil {
			testingObject.Fatalf("map message type to frame type failed: message_type=%s err=%v", messageType, err)
		}
		decodedType, err := ControlMessageTypeForFrameType(frameType)
		if err != nil {
			testingObject.Fatalf("map frame type to message type failed: frame_type=%d err=%v", frameType, err)
		}
		if decodedType != messageType {
			testingObject.Fatalf(
				"unexpected mapping result: frame_type=%d got=%s want=%s",
				frameType,
				decodedType,
				messageType,
			)
		}
	}
}

// TestEncodeDecodeBusinessControlEnvelopeFrame 验证业务控制面封装可完成帧级编解码闭环。
func TestEncodeDecodeBusinessControlEnvelopeFrame(testingObject *testing.T) {
	testingObject.Parallel()

	reportPayload := pb.TunnelPoolReport{
		SessionID:       "session-001",
		SessionEpoch:    7,
		IdleCount:       3,
		InUseCount:      9,
		TargetIdleCount: 12,
		Trigger:         "periodic",
		TimestampUnix:   1718000000,
	}
	encodedPayload, err := json.Marshal(reportPayload)
	if err != nil {
		testingObject.Fatalf("marshal report payload failed: %v", err)
	}
	envelope := pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageTunnelPoolReport,
		SessionID:    "session-001",
		SessionEpoch: 7,
		RequestID:    "req-001",
		Payload:      encodedPayload,
	}

	frame, err := EncodeBusinessControlEnvelopeFrame(envelope)
	if err != nil {
		testingObject.Fatalf("encode business control envelope failed: %v", err)
	}
	if frame.Type != ControlFrameTypeTunnelPoolReport {
		testingObject.Fatalf(
			"unexpected frame type: got=%d want=%d",
			frame.Type,
			ControlFrameTypeTunnelPoolReport,
		)
	}

	decodedEnvelope, err := DecodeBusinessControlEnvelopeFrame(frame)
	if err != nil {
		testingObject.Fatalf("decode business control envelope failed: %v", err)
	}
	if decodedEnvelope.MessageType != pb.ControlMessageTunnelPoolReport {
		testingObject.Fatalf(
			"unexpected decoded message type: got=%s want=%s",
			decodedEnvelope.MessageType,
			pb.ControlMessageTunnelPoolReport,
		)
	}

	var decodedPayload pb.TunnelPoolReport
	if err := json.Unmarshal(decodedEnvelope.Payload, &decodedPayload); err != nil {
		testingObject.Fatalf("unmarshal decoded payload failed: %v", err)
	}
	if decodedPayload.IdleCount != reportPayload.IdleCount || decodedPayload.TargetIdleCount != reportPayload.TargetIdleCount {
		testingObject.Fatalf(
			"unexpected payload counters: got idle=%d target=%d",
			decodedPayload.IdleCount,
			decodedPayload.TargetIdleCount,
		)
	}
}

// TestDecodeBusinessControlEnvelopeFrameRejectMessageTypeMismatch 验证帧类型与载荷 message_type 不一致会被拒绝。
func TestDecodeBusinessControlEnvelopeFrameRejectMessageTypeMismatch(testingObject *testing.T) {
	testingObject.Parallel()

	envelope := pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessagePublishService,
		SessionID:    "session-001",
		SessionEpoch: 1,
		RequestID:    "req-001",
	}
	rawEnvelope, err := json.Marshal(envelope)
	if err != nil {
		testingObject.Fatalf("marshal mismatched envelope failed: %v", err)
	}
	// 人为构造“帧类型是 RouteAssign、载荷类型是 PublishService”的冲突场景。
	_, err = DecodeBusinessControlEnvelopeFrame(ControlFrame{
		Type:    ControlFrameTypeRouteAssign,
		Payload: rawEnvelope,
	})
	if err == nil {
		testingObject.Fatalf("expected message type mismatch error")
	}
	protocolErr, ok := err.(*ltfperrors.ProtocolError)
	if !ok {
		testingObject.Fatalf("expected protocol error, got=%T", err)
	}
	if protocolErr.Code != ltfperrors.CodeInvalidPayload {
		testingObject.Fatalf("unexpected error code: got=%s want=%s", protocolErr.Code, ltfperrors.CodeInvalidPayload)
	}
}

// TestEncodeBusinessControlEnvelopeFrameRejectUnknownType 验证未知控制消息类型会被拒绝。
func TestEncodeBusinessControlEnvelopeFrameRejectUnknownType(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := EncodeBusinessControlEnvelopeFrame(pb.ControlEnvelope{
		VersionMajor: 1,
		VersionMinor: 0,
		MessageType:  pb.ControlMessageType("UnknownType"),
	})
	if err == nil {
		testingObject.Fatalf("expected unknown message type error")
	}
	protocolErr, ok := err.(*ltfperrors.ProtocolError)
	if !ok {
		testingObject.Fatalf("expected protocol error, got=%T", err)
	}
	if protocolErr.Code != ltfperrors.CodeUnknownMessageType {
		testingObject.Fatalf(
			"unexpected error code: got=%s want=%s",
			protocolErr.Code,
			ltfperrors.CodeUnknownMessageType,
		)
	}
}

// TestDecodeBusinessControlEnvelopeFrameRejectEmptyPayload 验证空 payload 会被拒绝。
func TestDecodeBusinessControlEnvelopeFrameRejectEmptyPayload(testingObject *testing.T) {
	testingObject.Parallel()

	_, err := DecodeBusinessControlEnvelopeFrame(ControlFrame{
		Type:    ControlFrameTypePublishService,
		Payload: nil,
	})
	if err == nil {
		testingObject.Fatalf("expected empty payload error")
	}
	protocolErr, ok := err.(*ltfperrors.ProtocolError)
	if !ok {
		testingObject.Fatalf("expected protocol error, got=%T", err)
	}
	if protocolErr.Code != ltfperrors.CodeMissingRequiredField {
		testingObject.Fatalf(
			"unexpected error code: got=%s want=%s",
			protocolErr.Code,
			ltfperrors.CodeMissingRequiredField,
		)
	}
}
