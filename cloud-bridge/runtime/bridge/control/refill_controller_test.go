package control

import (
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TestRefillControllerBuildRefillRequest 验证低水位上报会触发补池请求。
func TestRefillControllerBuildRefillRequest(t *testing.T) {
	t.Parallel()

	controller := NewRefillController(RefillControllerOptions{
		Config: RefillControllerConfig{
			TriggerThreshold: 2,
			RequestCooldown:  time.Second,
			MinRequestDelta:  1,
			MaxRequestDelta:  16,
		},
		Now: func() time.Time { return time.Unix(1700001000, 0).UTC() },
	})
	request, shouldSend := controller.BuildRefillRequest(
		"session-1",
		5,
		pb.TunnelPoolReport{
			SessionID:       "session-1",
			SessionEpoch:    5,
			IdleCount:       1,
			InUseCount:      2,
			TargetIdleCount: 8,
			Trigger:         "event:idle_low",
		},
	)
	if !shouldSend {
		t.Fatalf("expected refill request to be generated")
	}
	if request.RequestedIdleDelta != 7 {
		t.Fatalf("unexpected requested delta: got=%d want=7", request.RequestedIdleDelta)
	}
	if request.Reason != "low_watermark" {
		t.Fatalf("unexpected refill reason: %s", request.Reason)
	}
	if request.RequestID == "" {
		t.Fatalf("expected non-empty request id")
	}
}

// TestRefillControllerSuppressDuplicate 验证冷却窗口内相同增量会被抑制。
func TestRefillControllerSuppressDuplicate(t *testing.T) {
	t.Parallel()

	current := time.Unix(1700002000, 0).UTC()
	controller := NewRefillController(RefillControllerOptions{
		Config: RefillControllerConfig{
			TriggerThreshold: 2,
			RequestCooldown:  2 * time.Second,
			MinRequestDelta:  1,
			MaxRequestDelta:  16,
		},
		Now: func() time.Time { return current },
	})
	firstRequest, firstShouldSend := controller.BuildRefillRequest(
		"session-2",
		3,
		pb.TunnelPoolReport{
			IdleCount:       0,
			InUseCount:      1,
			TargetIdleCount: 6,
			Trigger:         "event:idle_low",
		},
	)
	if !firstShouldSend {
		t.Fatalf("expected first refill request to be generated")
	}

	secondRequest, secondShouldSend := controller.BuildRefillRequest(
		"session-2",
		3,
		pb.TunnelPoolReport{
			IdleCount:       0,
			InUseCount:      1,
			TargetIdleCount: 6,
			Trigger:         "event:idle_low",
		},
	)
	if secondShouldSend {
		t.Fatalf("expected duplicate request to be suppressed: %+v", secondRequest)
	}
	if firstRequest.RequestID == "" {
		t.Fatalf("expected first request id not empty")
	}
}

// TestRefillControllerSkipWhenNoInUseTraffic 验证无占用且非 acquire_timeout 时不会触发补池。
func TestRefillControllerSkipWhenNoInUseTraffic(t *testing.T) {
	t.Parallel()

	controller := NewRefillController(RefillControllerOptions{
		Config: RefillControllerConfig{
			TriggerThreshold: 2,
			RequestCooldown:  time.Second,
			MinRequestDelta:  1,
			MaxRequestDelta:  16,
		},
		Now: func() time.Time { return time.Unix(1700003000, 0).UTC() },
	})
	_, shouldSend := controller.BuildRefillRequest(
		"session-3",
		7,
		pb.TunnelPoolReport{
			IdleCount:       0,
			InUseCount:      0,
			TargetIdleCount: 8,
			Trigger:         "event:pool_changed",
		},
	)
	if shouldSend {
		t.Fatalf("expected no refill request when in_use=0 and trigger is not acquire_timeout")
	}
}

// TestRefillControllerAllowAcquireTimeoutWithoutInUse 验证 acquire_timeout 触发可绕过 in_use=0 限制。
func TestRefillControllerAllowAcquireTimeoutWithoutInUse(t *testing.T) {
	t.Parallel()

	controller := NewRefillController(RefillControllerOptions{
		Config: RefillControllerConfig{
			TriggerThreshold: 2,
			RequestCooldown:  time.Second,
			MinRequestDelta:  1,
			MaxRequestDelta:  16,
		},
		Now: func() time.Time { return time.Unix(1700004000, 0).UTC() },
	})
	_, shouldSend := controller.BuildRefillRequest(
		"session-4",
		3,
		pb.TunnelPoolReport{
			IdleCount:       0,
			InUseCount:      0,
			TargetIdleCount: 8,
			Trigger:         "event:acquire_timeout",
		},
	)
	if !shouldSend {
		t.Fatalf("expected refill request for acquire_timeout trigger even when in_use=0")
	}
}

// TestRefillControllerSkipSessionActiveTrigger 验证 session_active 事件不会触发补池请求。
func TestRefillControllerSkipSessionActiveTrigger(t *testing.T) {
	t.Parallel()

	controller := NewRefillController(RefillControllerOptions{
		Config: RefillControllerConfig{
			TriggerThreshold: 2,
			RequestCooldown:  time.Second,
			MinRequestDelta:  1,
			MaxRequestDelta:  16,
		},
		Now: func() time.Time { return time.Unix(1700005000, 0).UTC() },
	})
	_, shouldSend := controller.BuildRefillRequest(
		"session-5",
		4,
		pb.TunnelPoolReport{
			IdleCount:       0,
			InUseCount:      3,
			TargetIdleCount: 8,
			Trigger:         "event:session_active",
		},
	)
	if shouldSend {
		t.Fatalf("expected no refill request for session_active trigger")
	}
}
