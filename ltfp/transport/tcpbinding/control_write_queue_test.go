package tcpbinding

import (
	"context"
	"errors"
	"testing"
	"time"
)

// TestControlWriteOperationCancelBetweenFragments 验证分块间隙取消仅终止当前写操作，不误判为 active 写入中。
func TestControlWriteOperationCancelBetweenFragments(testingObject *testing.T) {
	writeContext, cancelWrite := context.WithCancel(context.Background())
	operation := newControlWriteOperation(writeContext, 2)

	if !operation.Begin() {
		testingObject.Fatalf("expected begin success")
	}
	operation.MarkIdle()
	operation.CompleteFragment()
	if operation.IsFinished() {
		testingObject.Fatalf("expected operation still pending after first fragment")
	}

	cancelWrite()
	operation.handleContextDone(nil)

	select {
	case <-operation.Done():
	case <-time.After(time.Second):
		testingObject.Fatalf("expected operation done after cancellation")
	}
	if !errors.Is(operation.Err(), context.Canceled) {
		testingObject.Fatalf("expected context canceled error, got %v", operation.Err())
	}
}
