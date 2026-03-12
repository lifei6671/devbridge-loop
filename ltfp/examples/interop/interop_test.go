package interop

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/codec"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/session"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

// agentSideBuildHello 模拟 agent 侧构建 HELLO 控制面消息。
func agentSideBuildHello(t *testing.T, transport codec.TransportBinding) []byte {
	t.Helper()

	jsonCodec := codec.NewJSONCodec()
	hello := testkit.GoldenConnectorHello()
	rawPayload, err := jsonCodec.EncodePayload(hello)
	if err != nil {
		t.Fatalf("agent encode hello payload failed: %v", err)
	}
	envelope := pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		SessionEpoch: 1,
		Payload:      rawPayload,
	}
	encoded, err := codec.EncodeControlEnvelopeForBinding(transport, envelope)
	if err != nil {
		t.Fatalf("agent encode control envelope failed: %v", err)
	}
	return encoded
}

// bridgeSideDecodeAndValidateHello 模拟 bridge 侧解码并校验 HELLO。
func bridgeSideDecodeAndValidateHello(t *testing.T, transport codec.TransportBinding, raw []byte) pb.ControlEnvelope {
	t.Helper()

	envelope, err := codec.DecodeControlEnvelopeForBinding(transport, raw)
	if err != nil {
		t.Fatalf("bridge decode control envelope failed: %v", err)
	}
	if err := validate.ValidateControlEnvelope(envelope); err != nil {
		t.Fatalf("bridge validate control envelope failed: %v", err)
	}

	var hello pb.ConnectorHello
	jsonCodec := codec.NewJSONCodec()
	if err := jsonCodec.DecodePayload(envelope.Payload, &hello); err != nil {
		t.Fatalf("bridge decode hello payload failed: %v", err)
	}
	if err := validate.ValidateConnectorHello(hello); err != nil {
		t.Fatalf("bridge validate hello payload failed: %v", err)
	}
	return envelope
}

// TestAgentBridgeProtocolInterop 验证最小双端协议交互示例。
func TestAgentBridgeProtocolInterop(t *testing.T) {
	t.Parallel()

	transport := codec.TransportBindingHTTP
	rawHello := agentSideBuildHello(t, transport)
	envelope := bridgeSideDecodeAndValidateHello(t, transport, rawHello)

	sequence := []pb.ControlMessageType{
		pb.ControlMessageConnectorHello,
		pb.ControlMessageConnectorWelcome,
		pb.ControlMessageConnectorAuth,
		pb.ControlMessageConnectorAuthAck,
		pb.ControlMessageHeartbeat,
	}
	if err := session.ValidateHandshakeSequence(sequence); err != nil {
		t.Fatalf("validate handshake sequence failed: %v", err)
	}

	welcome := pb.ConnectorWelcome{
		AssignedSessionEpoch: envelope.SessionEpoch,
	}
	authAck := pb.ConnectorAuthAck{
		Success:      true,
		SessionEpoch: envelope.SessionEpoch,
	}
	if err := session.ValidateAuthEpochAuthority(welcome, authAck); err != nil {
		t.Fatalf("validate auth epoch authority failed: %v", err)
	}

	state, err := session.NextState(pb.SessionStateConnecting, session.EventConnected)
	if err != nil {
		t.Fatalf("state transition failed: %v", err)
	}
	state, err = session.NextState(state, session.EventAuthSuccess)
	if err != nil {
		t.Fatalf("state transition failed: %v", err)
	}
	// 认证成功后状态应进入 ACTIVE，满足控制面与新流量承载要求。
	if state != pb.SessionStateActive {
		t.Fatalf("unexpected session state: %s", state)
	}

	lastHeartbeat := time.Now().UTC().Add(-2 * time.Second)
	// 2 秒心跳间隔、2 倍容忍窗口下，2 秒内不应判定超时。
	if session.IsHeartbeatTimeout(lastHeartbeat, time.Now().UTC(), 2, 2) {
		t.Fatalf("unexpected heartbeat timeout")
	}
}
