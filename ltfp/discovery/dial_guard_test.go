package discovery

import (
	"context"
	"errors"
	"testing"
	"time"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

// TestDialGuardConcurrencyLimit 验证并发限制达到上限会被拒绝。
func TestDialGuardConcurrencyLimit(t *testing.T) {
	t.Parallel()

	guard := NewDialGuard(1, 100*time.Millisecond)
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		err := guard.Do(context.Background(), func(ctx context.Context) error {
			// 首个拨号占用槽位，阻塞等待释放。
			close(started)
			<-release
			return nil
		})
		done <- err
	}()
	<-started

	err := guard.Do(context.Background(), func(ctx context.Context) error {
		return nil
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeDirectProxyConcurrencyLimit) {
		t.Fatalf("unexpected error: %v", err)
	}
	close(release)
	if goroutineErr := <-done; goroutineErr != nil {
		t.Fatalf("unexpected first dial error: %v", goroutineErr)
	}
}

// TestDialGuardTimeout 验证拨号超时会返回 timeout 错误码。
func TestDialGuardTimeout(t *testing.T) {
	t.Parallel()

	guard := NewDialGuard(1, 20*time.Millisecond)
	err := guard.Do(context.Background(), func(ctx context.Context) error {
		<-ctx.Done()
		return ctx.Err()
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeDirectProxyTimeout) {
		t.Fatalf("unexpected error: %v", err)
	}
}

// TestDialGuardDialFailed 验证非超时拨号失败会返回 dial failed 错误码。
func TestDialGuardDialFailed(t *testing.T) {
	t.Parallel()

	guard := NewDialGuard(1, 200*time.Millisecond)
	err := guard.Do(context.Background(), func(ctx context.Context) error {
		return errors.New("connection refused")
	})
	if !ltfperrors.IsCode(err, ltfperrors.CodeDirectProxyDialFailed) {
		t.Fatalf("unexpected error: %v", err)
	}
}
