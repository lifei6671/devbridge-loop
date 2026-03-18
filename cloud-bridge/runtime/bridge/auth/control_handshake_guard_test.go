package auth

import (
	"testing"
	"time"
)

// TestControlHandshakeGuardConnectionBudget 验证未认证连接预算可生效并支持释放后复用。
func TestControlHandshakeGuardConnectionBudget(testingObject *testing.T) {
	testingObject.Parallel()

	guard := newControlHandshakeGuard(controlHandshakeGuardOptions{
		unauthenticatedConnectionBudget: 1,
	})
	if !guard.TryAcquireUnauthenticatedConnection() {
		testingObject.Fatalf("expected first unauthenticated connection acquire success")
	}
	if guard.TryAcquireUnauthenticatedConnection() {
		testingObject.Fatalf("expected second unauthenticated connection acquire rejected by budget")
	}
	guard.ReleaseUnauthenticatedConnection()
	if !guard.TryAcquireUnauthenticatedConnection() {
		testingObject.Fatalf("expected acquire success after connection budget release")
	}
}

// TestControlHandshakeGuardAuthConcurrencyBudget 验证认证并发预算可生效并支持释放后复用。
func TestControlHandshakeGuardAuthConcurrencyBudget(testingObject *testing.T) {
	testingObject.Parallel()

	guard := newControlHandshakeGuard(controlHandshakeGuardOptions{
		authConcurrencyBudget: 1,
	})
	if !guard.TryAcquireAuthConcurrency() {
		testingObject.Fatalf("expected first auth concurrency acquire success")
	}
	if guard.TryAcquireAuthConcurrency() {
		testingObject.Fatalf("expected second auth concurrency acquire rejected by budget")
	}
	guard.ReleaseAuthConcurrency()
	if !guard.TryAcquireAuthConcurrency() {
		testingObject.Fatalf("expected acquire success after auth concurrency budget release")
	}
}

// TestSlidingWindowRateLimiterCleanupExpiredKeys 验证滑窗限流器会清理已过期键，避免 map 长期膨胀。
func TestSlidingWindowRateLimiterCleanupExpiredKeys(testingObject *testing.T) {
	testingObject.Parallel()

	limiter := newSlidingWindowRateLimiter(time.Second, 2)
	baseTime := time.Date(2026, 3, 18, 10, 0, 0, 0, time.UTC)
	if !limiter.Allow("source-a", baseTime) {
		testingObject.Fatalf("expected source-a allowed")
	}
	if !limiter.Allow("source-b", baseTime) {
		testingObject.Fatalf("expected source-b allowed")
	}
	if got := len(limiter.history); got != 2 {
		testingObject.Fatalf("unexpected limiter key count before cleanup: got=%d want=2", got)
	}

	if !limiter.Allow("source-c", baseTime.Add(2*time.Second)) {
		testingObject.Fatalf("expected source-c allowed after cleanup window")
	}
	if got := len(limiter.history); got != 1 {
		testingObject.Fatalf("unexpected limiter key count after cleanup: got=%d want=1", got)
	}
	if _, exists := limiter.history["source-c"]; !exists {
		testingObject.Fatalf("expected source-c key remains after cleanup")
	}
	if _, exists := limiter.history["source-a"]; exists {
		testingObject.Fatalf("expected source-a key cleaned up")
	}
	if _, exists := limiter.history["source-b"]; exists {
		testingObject.Fatalf("expected source-b key cleaned up")
	}
}
