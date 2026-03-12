package transport

import "testing"

// TestNormalizeControlMessagePriority 验证优先级归一化逻辑。
func TestNormalizeControlMessagePriority(testingObject *testing.T) {
	// 未知优先级应回退到 normal，避免出现不可调度状态。
	normalizedPriority := NormalizeControlMessagePriority(ControlMessagePriority("unknown"))
	if normalizedPriority != ControlMessagePriorityNormal {
		testingObject.Fatalf("expected normal priority, got %s", normalizedPriority)
	}
}

// TestPriorityControlQueueDequeueOrder 验证队列出队顺序为 high -> normal -> low。
func TestPriorityControlQueueDequeueOrder(testingObject *testing.T) {
	controlQueue := NewPriorityControlQueue()
	controlQueue.Enqueue(PrioritizedControlFrame{
		Priority: ControlMessagePriorityNormal,
		Frame:    ControlFrame{Type: 2},
	})
	controlQueue.Enqueue(PrioritizedControlFrame{
		Priority: ControlMessagePriorityLow,
		Frame:    ControlFrame{Type: 3},
	})
	controlQueue.Enqueue(PrioritizedControlFrame{
		Priority: ControlMessagePriorityHigh,
		Frame:    ControlFrame{Type: 1},
	})

	firstFrame, ok := controlQueue.Dequeue()
	if !ok {
		testingObject.Fatalf("expected first dequeue success")
	}
	if firstFrame.Frame.Type != 1 {
		testingObject.Fatalf("expected high priority frame first, got type=%d", firstFrame.Frame.Type)
	}

	secondFrame, ok := controlQueue.Dequeue()
	if !ok {
		testingObject.Fatalf("expected second dequeue success")
	}
	if secondFrame.Frame.Type != 2 {
		testingObject.Fatalf("expected normal priority frame second, got type=%d", secondFrame.Frame.Type)
	}

	thirdFrame, ok := controlQueue.Dequeue()
	if !ok {
		testingObject.Fatalf("expected third dequeue success")
	}
	if thirdFrame.Frame.Type != 3 {
		testingObject.Fatalf("expected low priority frame third, got type=%d", thirdFrame.Frame.Type)
	}
}
