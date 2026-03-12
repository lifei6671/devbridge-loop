package transport

import "context"

// ControlFrame 描述控制面的最小帧结构。
type ControlFrame struct {
	Type    uint16
	Payload []byte
}

// ControlMessagePriority 表示控制面消息优先级。
type ControlMessagePriority string

const (
	// ControlMessagePriorityHigh 表示高优先级消息，例如 heartbeat、auth、refill。
	ControlMessagePriorityHigh ControlMessagePriority = "high"
	// ControlMessagePriorityNormal 表示常规优先级消息。
	ControlMessagePriorityNormal ControlMessagePriority = "normal"
	// ControlMessagePriorityLow 表示低优先级消息，例如大体积状态同步。
	ControlMessagePriorityLow ControlMessagePriority = "low"
)

// PrioritizedControlFrame 描述带优先级的控制帧。
type PrioritizedControlFrame struct {
	Priority ControlMessagePriority
	Frame    ControlFrame
}

// NormalizeControlMessagePriority 归一化控制面优先级取值。
func NormalizeControlMessagePriority(priority ControlMessagePriority) ControlMessagePriority {
	switch priority {
	case ControlMessagePriorityHigh, ControlMessagePriorityNormal, ControlMessagePriorityLow:
		// 已知优先级直接原样返回。
		return priority
	default:
		// 未知值按 normal 处理，避免出现空优先级。
		return ControlMessagePriorityNormal
	}
}

// ControlChannel 定义控制面的最小读写能力。
type ControlChannel interface {
	WriteControlFrame(ctx context.Context, frame ControlFrame) error
	ReadControlFrame(ctx context.Context) (ControlFrame, error)

	Close(ctx context.Context) error

	Done() <-chan struct{}
	Err() error
}

// PrioritizedControlChannel 为支持优先级发送调度的可选控制面能力。
type PrioritizedControlChannel interface {
	ControlChannel

	WritePrioritizedControlFrame(ctx context.Context, frame PrioritizedControlFrame) error
}
