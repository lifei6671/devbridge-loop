package transport

import "sync"

// PriorityControlQueue 提供控制面发送队列的最小优先级调度实现。
type PriorityControlQueue struct {
	mutex sync.Mutex

	highPriorityFrames   []PrioritizedControlFrame
	normalPriorityFrames []PrioritizedControlFrame
	lowPriorityFrames    []PrioritizedControlFrame
}

// NewPriorityControlQueue 创建优先级控制队列。
func NewPriorityControlQueue() *PriorityControlQueue {
	return &PriorityControlQueue{
		highPriorityFrames:   make([]PrioritizedControlFrame, 0),
		normalPriorityFrames: make([]PrioritizedControlFrame, 0),
		lowPriorityFrames:    make([]PrioritizedControlFrame, 0),
	}
}

// Enqueue 按优先级将控制帧入队。
func (queue *PriorityControlQueue) Enqueue(frame PrioritizedControlFrame) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	normalizedPriority := NormalizeControlMessagePriority(frame.Priority)
	frame.Priority = normalizedPriority
	switch normalizedPriority {
	case ControlMessagePriorityHigh:
		// 高优先级帧优先排队，避免被大消息阻塞。
		queue.highPriorityFrames = append(queue.highPriorityFrames, frame)
	case ControlMessagePriorityLow:
		// 低优先级帧放入低优桶，最后消费。
		queue.lowPriorityFrames = append(queue.lowPriorityFrames, frame)
	default:
		// 其他情况统一落到 normal 桶。
		queue.normalPriorityFrames = append(queue.normalPriorityFrames, frame)
	}
}

// Dequeue 按 high -> normal -> low 的顺序出队。
func (queue *PriorityControlQueue) Dequeue() (PrioritizedControlFrame, bool) {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	if frame, ok := queue.dequeueBucketLocked(&queue.highPriorityFrames); ok {
		// 高优桶有数据时优先返回，降低 heartbeat HOL 风险。
		return frame, true
	}
	if frame, ok := queue.dequeueBucketLocked(&queue.normalPriorityFrames); ok {
		// 没有高优时返回 normal 桶。
		return frame, true
	}
	frame, ok := queue.dequeueBucketLocked(&queue.lowPriorityFrames)
	return frame, ok
}

// Len 返回当前队列总长度。
func (queue *PriorityControlQueue) Len() int {
	queue.mutex.Lock()
	defer queue.mutex.Unlock()
	// 三个桶长度相加即总待发送数量。
	return len(queue.highPriorityFrames) + len(queue.normalPriorityFrames) + len(queue.lowPriorityFrames)
}

// dequeueBucketLocked 从指定桶头部弹出一条消息。
func (queue *PriorityControlQueue) dequeueBucketLocked(bucket *[]PrioritizedControlFrame) (PrioritizedControlFrame, bool) {
	if len(*bucket) == 0 {
		// 桶为空时返回 false。
		return PrioritizedControlFrame{}, false
	}
	frame := (*bucket)[0]
	// 头出队后保留剩余元素顺序，维持同优先级 FIFO。
	*bucket = (*bucket)[1:]
	return frame, true
}
