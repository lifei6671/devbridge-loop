package session

import (
	"context"
	"testing"
	"time"
)

// TestHeartbeatSchedulerRunSendsUntilContextCanceled 验证心跳循环会按间隔发送并在取消后退出。
func TestHeartbeatSchedulerRunSendsUntilContextCanceled(testingObject *testing.T) {
	testingObject.Parallel()
	scheduler := HeartbeatScheduler{Interval: 5 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sendSignal := make(chan struct{}, 4)
	runResult := make(chan error, 1)
	sendCount := 0
	go func() {
		runResult <- scheduler.Run(ctx, func(sendContext context.Context) error {
			_ = sendContext
			// 每次发送都记录信号，方便主协程等待至少三次心跳。
			sendCount++
			sendSignal <- struct{}{}
			if sendCount >= 3 {
				// 满足最小发送次数后主动取消，验证退出语义。
				cancel()
			}
			return nil
		})
	}()

	for index := 0; index < 3; index++ {
		select {
		case <-sendSignal:
		case <-time.After(200 * time.Millisecond):
			testingObject.Fatalf("timed out waiting heartbeat send #%d", index+1)
		}
	}
	select {
	case err := <-runResult:
		// 取消上下文后应按约定返回 context canceled。
		if err == nil || err != context.Canceled {
			testingObject.Fatalf("unexpected scheduler exit err=%v", err)
		}
	case <-time.After(200 * time.Millisecond):
		testingObject.Fatalf("timed out waiting scheduler exit")
	}
}

// TestHeartbeatSchedulerRunNoopOnInvalidConfig 验证非法配置或空 sender 时不会执行发送逻辑。
func TestHeartbeatSchedulerRunNoopOnInvalidConfig(testingObject *testing.T) {
	testingObject.Parallel()
	called := false

	// 心跳间隔非法时应直接返回，避免忙等循环。
	if err := (HeartbeatScheduler{Interval: 0}).Run(context.Background(), func(context.Context) error {
		called = true
		return nil
	}); err != nil {
		testingObject.Fatalf("unexpected error for zero interval: %v", err)
	}
	// 空 sender 场景也应无副作用返回。
	if err := (HeartbeatScheduler{Interval: 5 * time.Millisecond}).Run(context.Background(), nil); err != nil {
		testingObject.Fatalf("unexpected error for nil sender: %v", err)
	}
	if called {
		testingObject.Fatalf("expected sender not called when interval is invalid")
	}
}
