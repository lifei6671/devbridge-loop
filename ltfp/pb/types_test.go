package pb

import "testing"

// TestActivePayloadCount 验证 StreamPayload oneof 计数逻辑。
func TestActivePayloadCount(t *testing.T) {
	t.Parallel()

	payload := StreamPayload{
		OpenReq: &TrafficOpen{
			TrafficID: "traffic-001",
			ServiceID: "svc-001",
		},
	}
	// 仅设置 OpenReq 时应返回 1。
	if got := payload.ActivePayloadCount(); got != 1 {
		t.Fatalf("unexpected active payload count: got=%d want=1", got)
	}
}

// TestIsKnownControlMessageType 验证控制面消息类型白名单判断。
func TestIsKnownControlMessageType(t *testing.T) {
	t.Parallel()

	// 协议内消息类型应返回 true。
	if !IsKnownControlMessageType(ControlMessagePublishService) {
		t.Fatalf("expected known message type")
	}
	// 非协议内消息类型应返回 false。
	if IsKnownControlMessageType(ControlMessageType("Unknown")) {
		t.Fatalf("expected unknown message type")
	}
}
