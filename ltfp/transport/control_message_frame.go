package transport

import (
	"encoding/json"
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// ControlFrameTypeConnectorHello 表示 ConnectorHello 业务控制帧。
	ControlFrameTypeConnectorHello uint16 = 0x0101
	// ControlFrameTypeConnectorWelcome 表示 ConnectorWelcome 业务控制帧。
	ControlFrameTypeConnectorWelcome uint16 = 0x0102
	// ControlFrameTypeConnectorAuth 表示 ConnectorAuth 业务控制帧。
	ControlFrameTypeConnectorAuth uint16 = 0x0103
	// ControlFrameTypeConnectorAuthAck 表示 ConnectorAuthAck 业务控制帧。
	ControlFrameTypeConnectorAuthAck uint16 = 0x0104
	// ControlFrameTypeControlHeartbeat 表示控制面业务心跳帧（区别于 ping/pong 保活帧）。
	ControlFrameTypeControlHeartbeat uint16 = 0x0105

	// ControlFrameTypePublishService 表示 PublishService 业务控制帧。
	ControlFrameTypePublishService uint16 = 0x0110
	// ControlFrameTypePublishServiceAck 表示 PublishServiceAck 业务控制帧。
	ControlFrameTypePublishServiceAck uint16 = 0x0111
	// ControlFrameTypeUnpublishService 表示 UnpublishService 业务控制帧。
	ControlFrameTypeUnpublishService uint16 = 0x0112
	// ControlFrameTypeUnpublishServiceAck 表示 UnpublishServiceAck 业务控制帧。
	ControlFrameTypeUnpublishServiceAck uint16 = 0x0113
	// ControlFrameTypeServiceHealthReport 表示 ServiceHealthReport 业务控制帧。
	ControlFrameTypeServiceHealthReport uint16 = 0x0114
	// ControlFrameTypeTunnelPoolReport 表示 TunnelPoolReport 业务控制帧。
	ControlFrameTypeTunnelPoolReport uint16 = 0x0115
	// ControlFrameTypeTunnelRefillRequest 表示 TunnelRefillRequest 业务控制帧。
	ControlFrameTypeTunnelRefillRequest uint16 = 0x0116

	// ControlFrameTypeRouteAssign 表示 RouteAssign 业务控制帧。
	ControlFrameTypeRouteAssign uint16 = 0x0120
	// ControlFrameTypeRouteAssignAck 表示 RouteAssignAck 业务控制帧。
	ControlFrameTypeRouteAssignAck uint16 = 0x0121
	// ControlFrameTypeRouteRevoke 表示 RouteRevoke 业务控制帧。
	ControlFrameTypeRouteRevoke uint16 = 0x0122
	// ControlFrameTypeRouteRevokeAck 表示 RouteRevokeAck 业务控制帧。
	ControlFrameTypeRouteRevokeAck uint16 = 0x0123
	// ControlFrameTypeRouteStatusReport 表示 RouteStatusReport 业务控制帧。
	ControlFrameTypeRouteStatusReport uint16 = 0x0124

	// ControlFrameTypeControlError 表示 ControlError 业务控制帧。
	ControlFrameTypeControlError uint16 = 0x01F0
)

// ControlFrameTypeForMessageType 将控制面消息类型映射到控制帧类型。
func ControlFrameTypeForMessageType(messageType pb.ControlMessageType) (uint16, error) {
	normalizedType := pb.ControlMessageType(strings.TrimSpace(string(messageType)))
	// 先做协议白名单校验，避免未知消息类型污染传输层。
	if !pb.IsKnownControlMessageType(normalizedType) {
		return 0, ltfperrors.New(
			ltfperrors.CodeUnknownMessageType,
			fmt.Sprintf("unknown control message type: %s", messageType),
		)
	}
	switch normalizedType {
	case pb.ControlMessageConnectorHello:
		return ControlFrameTypeConnectorHello, nil
	case pb.ControlMessageConnectorWelcome:
		return ControlFrameTypeConnectorWelcome, nil
	case pb.ControlMessageConnectorAuth:
		return ControlFrameTypeConnectorAuth, nil
	case pb.ControlMessageConnectorAuthAck:
		return ControlFrameTypeConnectorAuthAck, nil
	case pb.ControlMessageHeartbeat:
		return ControlFrameTypeControlHeartbeat, nil
	case pb.ControlMessagePublishService:
		return ControlFrameTypePublishService, nil
	case pb.ControlMessagePublishServiceAck:
		return ControlFrameTypePublishServiceAck, nil
	case pb.ControlMessageUnpublishService:
		return ControlFrameTypeUnpublishService, nil
	case pb.ControlMessageUnpublishServiceAck:
		return ControlFrameTypeUnpublishServiceAck, nil
	case pb.ControlMessageServiceHealthReport:
		return ControlFrameTypeServiceHealthReport, nil
	case pb.ControlMessageTunnelPoolReport:
		return ControlFrameTypeTunnelPoolReport, nil
	case pb.ControlMessageTunnelRefillRequest:
		return ControlFrameTypeTunnelRefillRequest, nil
	case pb.ControlMessageRouteAssign:
		return ControlFrameTypeRouteAssign, nil
	case pb.ControlMessageRouteAssignAck:
		return ControlFrameTypeRouteAssignAck, nil
	case pb.ControlMessageRouteRevoke:
		return ControlFrameTypeRouteRevoke, nil
	case pb.ControlMessageRouteRevokeAck:
		return ControlFrameTypeRouteRevokeAck, nil
	case pb.ControlMessageRouteStatusReport:
		return ControlFrameTypeRouteStatusReport, nil
	case pb.ControlMessageControlError:
		return ControlFrameTypeControlError, nil
	default:
		// 正常不应命中该分支，保留兜底防止后续扩展遗漏映射。
		return 0, ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("control message type has no frame mapping: %s", normalizedType),
		)
	}
}

// ControlMessageTypeForFrameType 将业务控制帧类型映射为控制面消息类型。
func ControlMessageTypeForFrameType(frameType uint16) (pb.ControlMessageType, error) {
	switch frameType {
	case ControlFrameTypeConnectorHello:
		return pb.ControlMessageConnectorHello, nil
	case ControlFrameTypeConnectorWelcome:
		return pb.ControlMessageConnectorWelcome, nil
	case ControlFrameTypeConnectorAuth:
		return pb.ControlMessageConnectorAuth, nil
	case ControlFrameTypeConnectorAuthAck:
		return pb.ControlMessageConnectorAuthAck, nil
	case ControlFrameTypeControlHeartbeat:
		return pb.ControlMessageHeartbeat, nil
	case ControlFrameTypePublishService:
		return pb.ControlMessagePublishService, nil
	case ControlFrameTypePublishServiceAck:
		return pb.ControlMessagePublishServiceAck, nil
	case ControlFrameTypeUnpublishService:
		return pb.ControlMessageUnpublishService, nil
	case ControlFrameTypeUnpublishServiceAck:
		return pb.ControlMessageUnpublishServiceAck, nil
	case ControlFrameTypeServiceHealthReport:
		return pb.ControlMessageServiceHealthReport, nil
	case ControlFrameTypeTunnelPoolReport:
		return pb.ControlMessageTunnelPoolReport, nil
	case ControlFrameTypeTunnelRefillRequest:
		return pb.ControlMessageTunnelRefillRequest, nil
	case ControlFrameTypeRouteAssign:
		return pb.ControlMessageRouteAssign, nil
	case ControlFrameTypeRouteAssignAck:
		return pb.ControlMessageRouteAssignAck, nil
	case ControlFrameTypeRouteRevoke:
		return pb.ControlMessageRouteRevoke, nil
	case ControlFrameTypeRouteRevokeAck:
		return pb.ControlMessageRouteRevokeAck, nil
	case ControlFrameTypeRouteStatusReport:
		return pb.ControlMessageRouteStatusReport, nil
	case ControlFrameTypeControlError:
		return pb.ControlMessageControlError, nil
	default:
		return "", ltfperrors.New(
			ltfperrors.CodeUnsupportedValue,
			fmt.Sprintf("unsupported control frame type: %d", frameType),
		)
	}
}

// EncodeBusinessControlEnvelopeFrame 将业务控制面封装编码为控制帧。
func EncodeBusinessControlEnvelopeFrame(envelope pb.ControlEnvelope) (ControlFrame, error) {
	normalizedType := pb.ControlMessageType(strings.TrimSpace(string(envelope.MessageType)))
	frameType, err := ControlFrameTypeForMessageType(normalizedType)
	if err != nil {
		return ControlFrame{}, err
	}
	// 将规范化后的消息类型回写，确保编码产物字段稳定。
	envelope.MessageType = normalizedType
	encoded, err := json.Marshal(envelope)
	if err != nil {
		return ControlFrame{}, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "marshal business control envelope failed", err)
	}
	return ControlFrame{
		Type:    frameType,
		Payload: encoded,
	}, nil
}

// DecodeBusinessControlEnvelopeFrame 将业务控制帧解码为控制面封装。
func DecodeBusinessControlEnvelopeFrame(frame ControlFrame) (pb.ControlEnvelope, error) {
	expectedType, err := ControlMessageTypeForFrameType(frame.Type)
	if err != nil {
		return pb.ControlEnvelope{}, err
	}
	if len(frame.Payload) == 0 {
		return pb.ControlEnvelope{}, ltfperrors.New(ltfperrors.CodeMissingRequiredField, "control frame payload is required")
	}
	var envelope pb.ControlEnvelope
	if err := json.Unmarshal(frame.Payload, &envelope); err != nil {
		return pb.ControlEnvelope{}, ltfperrors.Wrap(ltfperrors.CodeInvalidPayload, "unmarshal business control envelope failed", err)
	}

	decodedType := pb.ControlMessageType(strings.TrimSpace(string(envelope.MessageType)))
	if decodedType == "" {
		// 兼容旧端遗漏 message_type 的场景，按帧类型补齐。
		envelope.MessageType = expectedType
		return envelope, nil
	}
	if decodedType != expectedType {
		return pb.ControlEnvelope{}, ltfperrors.New(
			ltfperrors.CodeInvalidPayload,
			fmt.Sprintf("control message type mismatch: frame=%s payload=%s", expectedType, decodedType),
		)
	}
	return envelope, nil
}
