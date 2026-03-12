package errors

import (
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
