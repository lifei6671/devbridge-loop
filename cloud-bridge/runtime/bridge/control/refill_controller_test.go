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
