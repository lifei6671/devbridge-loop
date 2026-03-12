package codec

import (
	"testing"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestEncodeDecodeControlEnvelopeForBinding 验证 http/masque 两种 binding 控制面兼容。
func TestEncodeDecodeControlEnvelopeForBinding(t *testing.T) {
	t.Parallel()

	envelope, err := testkit.GoldenControlEnvelope(pb.ControlMessageConnectorHello, testkit.GoldenConnectorHello())
	if err != nil {
		t.Fatalf("build golden envelope failed: %v", err)
	}

	testCases := []struct {
		name    string
		binding TransportBinding
	}{
		{name: "http", binding: TransportBindingHTTP},
		{name: "masque", binding: TransportBindingMASQUE},
	}

	for _, testCase := range testCases {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			raw, err := EncodeControlEnvelopeForBinding(testCase.binding, envelope)
			if err != nil {
				t.Fatalf("encode failed: %v", err)
			}
			decoded, err := DecodeControlEnvelopeForBinding(testCase.binding, raw)
			if err != nil {
				t.Fatalf("decode failed: %v", err)
			}
			// 校验核心字段保持一致，确保 binding 仅影响承载层。
			if decoded.MessageType != envelope.MessageType {
				t.Fatalf("message type mismatch: got=%s want=%s", decoded.MessageType, envelope.MessageType)
			}
		})
	}
}

// TestBindingRejectUnsupported 验证不支持的 binding 会被拒绝。
func TestBindingRejectUnsupported(t *testing.T) {
	t.Parallel()

	envelope, err := testkit.GoldenControlEnvelope(pb.ControlMessageConnectorHello, testkit.GoldenConnectorHello())
	if err != nil {
		t.Fatalf("build golden envelope failed: %v", err)
	}
	_, err = EncodeControlEnvelopeForBinding(TransportBinding("quic_native"), envelope)
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
	// 不支持 binding 应返回统一 unsupported 值错误码。
	if !ltfperrors.IsCode(err, ltfperrors.CodeUnsupportedValue) {
		t.Fatalf("unexpected error: %v", err)
	}
}
