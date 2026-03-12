package transport

import (
	"testing"
	"time"
)

// TestTunnelRefillRequestValidate 验证补池请求字段校验逻辑。
func TestTunnelRefillRequestValidate(testingObject *testing.T) {
	validRequest := TunnelRefillRequest{
		SessionID:          "session-1",
		SessionEpoch:       1,
		RequestID:          "request-1",
		RequestedIdleDelta: 2,
		Reason:             TunnelRefillReasonLowWatermark,
		Timestamp:          time.Now().UTC(),
	}
	if err := validRequest.Validate(); err != nil {
		testingObject.Fatalf("expected valid request, got err=%v", err)
	}

	invalidRequest := validRequest
	invalidRequest.RequestID = ""
	if err := invalidRequest.Validate(); err == nil {
		testingObject.Fatalf("expected error for empty request_id")
	}
}

// TestRefillRequestDeduplicatorMarkSeen 验证补池请求去重语义。
func TestRefillRequestDeduplicatorMarkSeen(testingObject *testing.T) {
	deduplicator := NewRefillRequestDeduplicator(time.Minute)
	currentTime := time.Now().UTC()

	if isFirstSeen := deduplicator.MarkSeen("request-1", currentTime); !isFirstSeen {
		testingObject.Fatalf("expected first seen request to return true")
	}
	if isFirstSeen := deduplicator.MarkSeen("request-1", currentTime.Add(2*time.Second)); isFirstSeen {
		testingObject.Fatalf("expected duplicate request to return false")
	}
	if isFirstSeen := deduplicator.MarkSeen("request-2", currentTime.Add(2*time.Second)); !isFirstSeen {
		testingObject.Fatalf("expected new request to return true")
	}
	if isFirstSeen := deduplicator.MarkSeen("request-1", currentTime.Add(2*time.Minute)); !isFirstSeen {
		testingObject.Fatalf("expected expired request record to be treated as first seen again")
	}
}
