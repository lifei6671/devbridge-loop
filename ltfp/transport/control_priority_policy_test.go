package transport

import "testing"

// TestRecommendControlFramePriority 验证控制帧优先级策略映射。
func TestRecommendControlFramePriority(testingObject *testing.T) {
	testingObject.Parallel()

	testCases := []struct {
		name             string
		frameType        uint16
		expectedPriority ControlMessagePriority
	}{
		{
			name:             "heartbeat ping should be high",
			frameType:        ControlFrameTypeHeartbeatPing,
			expectedPriority: ControlMessagePriorityHigh,
		},
		{
			name:             "auth ack should be high",
			frameType:        ControlFrameTypeConnectorAuthAck,
			expectedPriority: ControlMessagePriorityHigh,
		},
		{
			name:             "refill request should be high",
			frameType:        ControlFrameTypeTunnelRefillRequest,
			expectedPriority: ControlMessagePriorityHigh,
		},
		{
			name:             "publish service should be low",
			frameType:        ControlFrameTypePublishService,
			expectedPriority: ControlMessagePriorityLow,
		},
		{
			name:             "route assign ack should be normal",
			frameType:        ControlFrameTypeRouteAssignAck,
			expectedPriority: ControlMessagePriorityNormal,
		},
		{
			name:             "unknown type should be normal",
			frameType:        0x9ABC,
			expectedPriority: ControlMessagePriorityNormal,
		},
	}
	for _, testCase := range testCases {
		if got := RecommendControlFramePriority(testCase.frameType); got != testCase.expectedPriority {
			testingObject.Fatalf(
				"unexpected control frame priority: name=%s frame_type=%d got=%s want=%s",
				testCase.name,
				testCase.frameType,
				got,
				testCase.expectedPriority,
			)
		}
	}
}
