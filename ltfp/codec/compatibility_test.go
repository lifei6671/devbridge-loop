package codec

import (
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/testkit"
)

// TestDecodeControlEnvelopeWithUnknownFields 验证解码器可兼容未知字段。
func TestDecodeControlEnvelopeWithUnknownFields(t *testing.T) {
	t.Parallel()

	codec := NewJSONCodec()
	raw := testkit.GoldenEnvelopeWithUnknownFields()
	decoded, err := codec.DecodeControlEnvelope(raw)
	if err != nil {
		t.Fatalf("decode control envelope failed: %v", err)
	}
	// 未知字段应被忽略，核心字段必须可用。
	if decoded.MessageType != pb.ControlMessageConnectorHello {
		t.Fatalf("unexpected message type: %s", decoded.MessageType)
	}
}
