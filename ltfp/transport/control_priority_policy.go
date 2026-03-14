package transport

// RecommendControlFramePriority 按控制帧类型返回建议发送优先级。
func RecommendControlFramePriority(frameType uint16) ControlMessagePriority {
	switch frameType {
	case ControlFrameTypeHeartbeatPing, ControlFrameTypeHeartbeatPong:
		// 传输层保活帧必须高优先级，避免被大消息阻塞导致误判失活。
		return ControlMessagePriorityHigh
	case ControlFrameTypeConnectorHello, ControlFrameTypeConnectorWelcome, ControlFrameTypeConnectorAuth, ControlFrameTypeConnectorAuthAck:
		// 握手与鉴权阶段消息属于会话建立关键路径，使用高优先级。
		return ControlMessagePriorityHigh
	case ControlFrameTypeTunnelRefillRequest, ControlFrameTypeControlError:
		// 补池请求与控制错误需要快速到达，避免放大故障窗口。
		return ControlMessagePriorityHigh
	case ControlFrameTypePublishService, ControlFrameTypeTunnelPoolReport:
		// 发布与池状态上报可能体积较大，默认降为低优先级规避 HOL。
		return ControlMessagePriorityLow
	default:
		// 其余控制消息默认走常规优先级。
		return ControlMessagePriorityNormal
	}
}
