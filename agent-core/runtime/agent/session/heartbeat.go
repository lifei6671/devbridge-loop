package session

import (
	"context"
	"time"
)

// HeartbeatScheduler 控制心跳发送节奏。
type HeartbeatScheduler struct {
	Interval time.Duration
}

// Sender 表示一次心跳发送动作。
type Sender func(ctx context.Context) error

// Run 在固定间隔内持续发送心跳，直到 ctx 结束。
func (s HeartbeatScheduler) Run(ctx context.Context, send Sender) error {
	if s.Interval <= 0 {
		// 非法间隔时直接退出，避免忙等。
		return nil
	}
	if send == nil {
		// 未提供发送函数时不执行。
		return nil
	}

	ticker := time.NewTicker(s.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			// 上层取消则退出循环。
			return ctx.Err()
		case <-ticker.C:
			// 发送心跳，错误留给调用方处理或记录。
			_ = send(ctx)
		}
	}
}
