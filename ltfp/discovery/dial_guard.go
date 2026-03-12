package discovery

import (
	"context"
	"errors"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// DialGuard 负责 direct proxy 拨号的超时与并发保护。
type DialGuard struct {
	semaphore      chan struct{}
	connectTimeout time.Duration
}

// NewDialGuard 创建 direct proxy 拨号保护器。
func NewDialGuard(maxConcurrent int, connectTimeout time.Duration) *DialGuard {
	if maxConcurrent <= 0 {
		// 并发上限未配置时回退到 1，防止无限制抢占连接资源。
		maxConcurrent = 1
	}
	if connectTimeout <= 0 {
		// 超时未配置时使用 3 秒默认值，避免悬挂拨号。
		connectTimeout = 3 * time.Second
	}
	return &DialGuard{
		semaphore:      make(chan struct{}, maxConcurrent),
		connectTimeout: connectTimeout,
	}
}

// Do 在并发与超时限制下执行一次拨号动作。
func (guard *DialGuard) Do(ctx context.Context, dialFunc func(ctx context.Context) error) error {
	select {
	case guard.semaphore <- struct{}{}:
		// 成功拿到并发槽位后继续执行拨号。
		defer func() {
			<-guard.semaphore
		}()
	default:
		// 并发超过限制时立即拒绝，防止连接风暴。
		return ltfperrors.New(ltfperrors.CodeDirectProxyConcurrencyLimit, "direct proxy concurrent dial limit reached")
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, guard.connectTimeout)
	defer cancel()
	if err := dialFunc(timeoutCtx); err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(timeoutCtx.Err(), context.DeadlineExceeded) {
			// 超时场景返回明确错误码，便于区分慢连接与业务失败。
			return ltfperrors.Wrap(ltfperrors.CodeDirectProxyTimeout, "direct proxy dial timed out", err)
		}
		return ltfperrors.Wrap(ltfperrors.CodeDirectProxyDialFailed, "direct proxy dial failed", err)
	}
	return nil
}
