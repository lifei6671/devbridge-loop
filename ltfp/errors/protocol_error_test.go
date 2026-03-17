package errors

import (
	"context"
	stderrors "errors"
	"testing"
)

// TestIsCode 验证协议错误码匹配能力。
func TestIsCode(t *testing.T) {
	t.Parallel()

	err := New(CodeInvalidPayload, "invalid payload")
	// 直接错误对象应该能按错误码识别。
	if !IsCode(err, CodeInvalidPayload) {
		t.Fatalf("expected code match")
	}
	// 错误码不一致时应返回 false。
	if IsCode(err, CodeInvalidScope) {
		t.Fatalf("unexpected code match")
	}
}

// TestIsCodeFromWrappedChain 验证错误链场景下的错误码匹配。
func TestIsCodeFromWrappedChain(t *testing.T) {
	t.Parallel()

	root := stderrors.New("root cause")
	err := Wrap(CodeInvalidPayload, "decode failed", root)
	// 包装后错误仍应支持按错误码识别。
	if !IsCode(err, CodeInvalidPayload) {
		t.Fatalf("expected code match in wrapped chain")
	}
}

// TestTunnelRecycleErrorHelpers 验证 recycle 错误的构造、提取与消息抽取能力。
func TestTunnelRecycleErrorHelpers(t *testing.T) {
	t.Parallel()

	root := stderrors.New("flush failed")
	err := WrapTunnelRecycleError(CodeTunnelRecycleBufferDirty, "flush tunnel before recycle failed", root)
	if ExtractTunnelRecycleCode(err) != CodeTunnelRecycleBufferDirty {
		t.Fatalf("unexpected recycle code: got=%s", ExtractTunnelRecycleCode(err))
	}
	if !IsTunnelRecycleCode(err, CodeTunnelRecycleBufferDirty) {
		t.Fatalf("expected recycle code match")
	}
	if ExtractTunnelRecycleMessage(err) != "flush tunnel before recycle failed" {
		t.Fatalf("unexpected recycle message: got=%s", ExtractTunnelRecycleMessage(err))
	}
}

// TestExtractTunnelRecycleCodeMapsDeadline 验证普通 deadline 错误会映射为 deadline_hit。
func TestExtractTunnelRecycleCodeMapsDeadline(t *testing.T) {
	t.Parallel()

	if ExtractTunnelRecycleCode(context.DeadlineExceeded) != CodeTunnelRecycleDeadlineHit {
		t.Fatalf("unexpected recycle deadline code: got=%s", ExtractTunnelRecycleCode(context.DeadlineExceeded))
	}
}
