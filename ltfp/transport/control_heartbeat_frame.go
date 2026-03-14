package transport

const (
	// ControlFrameTypeHeartbeatPing 表示控制面 heartbeat ping 帧。
	ControlFrameTypeHeartbeatPing uint16 = 0xFFF0
	// ControlFrameTypeHeartbeatPong 表示控制面 heartbeat pong 帧。
	ControlFrameTypeHeartbeatPong uint16 = 0xFFF1
)
