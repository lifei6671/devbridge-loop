package control

import "context"

// Dispatcher 实现控制面消息的高优先级调度。
type Dispatcher struct {
	highCh   chan any
	normalCh chan any
}

// NewDispatcher 创建调度器，highBuffer/normalBuffer 为通道缓冲大小。
func NewDispatcher(highBuffer, normalBuffer int) *Dispatcher {
	if highBuffer <= 0 {
		// 给默认缓冲，避免生产者阻塞。
		highBuffer = 16
	}
	if normalBuffer <= 0 {
		// 默认普通队列也给缓冲。
		normalBuffer = 64
	}
	return &Dispatcher{
		highCh:   make(chan any, highBuffer),
		normalCh: make(chan any, normalBuffer),
	}
}

// EnqueueHigh 写入高优先级队列。
func (d *Dispatcher) EnqueueHigh(msg any) bool {
	select {
	case d.highCh <- msg:
		// 成功入队。
		return true
	default:
		// 队列满时直接丢弃，避免阻塞关键路径。
		return false
	}
}

// EnqueueNormal 写入普通优先级队列。
func (d *Dispatcher) EnqueueNormal(msg any) bool {
	select {
	case d.normalCh <- msg:
		// 成功入队。
		return true
	default:
		// 队列满时直接丢弃。
		return false
	}
}

// Run 启动调度循环，优先处理高优先级消息。
func (d *Dispatcher) Run(ctx context.Context, handle func(context.Context, any)) error {
	if handle == nil {
		// 未提供处理函数时直接退出。
		return nil
	}
	for {
		// 先尽可能处理高优先级消息。
		for {
			select {
			case <-ctx.Done():
				// 上层取消时退出。
				return ctx.Err()
			case msg := <-d.highCh:
				// 高优先级消息直接处理。
				handle(ctx, msg)
				continue
			default:
				// 没有高优先级消息时跳出。
			}
			break
		}

		// 再在高/普通中选择，保证高优先级有更高概率被取到。
		select {
		case <-ctx.Done():
			return ctx.Err()
		case msg := <-d.highCh:
			// 仍优先处理高优先级。
			handle(ctx, msg)
		case msg := <-d.normalCh:
			// 普通消息在高优先级为空时处理。
			handle(ctx, msg)
		}
	}
}
