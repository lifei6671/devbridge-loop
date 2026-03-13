package grpcbinding

import "testing"

// TestAcquireAndReleaseGRPCPayloadBuffer 验证小 payload 可通过对象池复用。
func TestAcquireAndReleaseGRPCPayloadBuffer(testingObject *testing.T) {
	buffer, pooled := acquireGRPCPayloadBuffer(128)
	if len(buffer) != 128 {
		testingObject.Fatalf("expected buffer size 128, got %d", len(buffer))
	}
	if !pooled {
		testingObject.Fatalf("expected pooled buffer for small payload")
	}
	releaseGRPCPayloadBuffer(buffer)

	reusedBuffer, pooled := acquireGRPCPayloadBuffer(64)
	if len(reusedBuffer) != 64 {
		testingObject.Fatalf("expected buffer size 64, got %d", len(reusedBuffer))
	}
	if !pooled {
		testingObject.Fatalf("expected pooled buffer on second acquire")
	}
	releaseGRPCPayloadBuffer(reusedBuffer)
}

// TestAcquireGRPCPayloadBufferLargePayload 验证大 payload 不进入池化路径。
func TestAcquireGRPCPayloadBufferLargePayload(testingObject *testing.T) {
	buffer, pooled := acquireGRPCPayloadBuffer(maxPooledPayloadBufferCapacity + 1)
	if len(buffer) != maxPooledPayloadBufferCapacity+1 {
		testingObject.Fatalf("unexpected buffer size: %d", len(buffer))
	}
	if pooled {
		testingObject.Fatalf("expected non-pooled buffer for large payload")
	}
}
