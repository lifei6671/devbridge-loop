package transport

import (
	"bytes"
	"errors"
	"strings"
	"testing"
	"time"
)

// TestControlFrameFragmenterFragmentAndReassemblerRoundTrip 验证大控制帧可完成分块与重组闭环。
func TestControlFrameFragmenterFragmentAndReassemblerRoundTrip(testingObject *testing.T) {
	fragmenter, err := NewControlFrameFragmenter(ControlFragmentationConfig{
		MaxPayloadSize: 80,
	})
	if err != nil {
		testingObject.Fatalf("create control fragmenter failed: %v", err)
	}
	reassembler, err := NewControlFrameReassembler(ControlFragmentationConfig{
		MaxPayloadSize: 80,
	})
	if err != nil {
		testingObject.Fatalf("create control reassembler failed: %v", err)
	}

	originalFrame := ControlFrame{
		Type:    7,
		Payload: bytes.Repeat([]byte("a"), 200),
	}
	fragmentedFrames, err := fragmenter.Fragment(originalFrame)
	if err != nil {
		testingObject.Fatalf("fragment control frame failed: %v", err)
	}
	if len(fragmentedFrames) <= 1 {
		testingObject.Fatalf("expected fragmented frames, got %d", len(fragmentedFrames))
	}

	for fragmentIndex, fragmentedFrame := range fragmentedFrames {
		reassembledFrame, ready, err := reassembler.Reassemble(fragmentedFrame)
		if err != nil {
			testingObject.Fatalf("reassemble fragment %d failed: %v", fragmentIndex, err)
		}
		if fragmentIndex < len(fragmentedFrames)-1 {
			if ready {
				testingObject.Fatalf("expected fragment %d not ready", fragmentIndex)
			}
			continue
		}
		if !ready {
			testingObject.Fatalf("expected final fragment ready")
		}
		if reassembledFrame.Type != originalFrame.Type {
			testingObject.Fatalf("unexpected frame type: got=%d want=%d", reassembledFrame.Type, originalFrame.Type)
		}
		if !bytes.Equal(reassembledFrame.Payload, originalFrame.Payload) {
			testingObject.Fatalf("reassembled payload mismatch")
		}
	}
}

// TestControlFrameFragmenterKeepsSmallFrameSingle 验证小控制帧保持单帧发送。
func TestControlFrameFragmenterKeepsSmallFrameSingle(testingObject *testing.T) {
	fragmenter, err := NewControlFrameFragmenter(DefaultControlFragmentationConfig())
	if err != nil {
		testingObject.Fatalf("create control fragmenter failed: %v", err)
	}

	originalFrame := ControlFrame{
		Type:    11,
		Payload: []byte("small-control-message"),
	}
	fragmentedFrames, err := fragmenter.Fragment(originalFrame)
	if err != nil {
		testingObject.Fatalf("fragment small control frame failed: %v", err)
	}
	if len(fragmentedFrames) != 1 {
		testingObject.Fatalf("expected single frame, got %d", len(fragmentedFrames))
	}
	if fragmentedFrames[0].Type != originalFrame.Type {
		testingObject.Fatalf("unexpected frame type: got=%d want=%d", fragmentedFrames[0].Type, originalFrame.Type)
	}
	if !bytes.Equal(fragmentedFrames[0].Payload, originalFrame.Payload) {
		testingObject.Fatalf("unexpected payload after single frame clone")
	}
}

// TestControlFrameReassemblerAllowsInterleavedNonFragmentFrame 验证高优消息可在大消息分块之间穿插而不破坏重组。
func TestControlFrameReassemblerAllowsInterleavedNonFragmentFrame(testingObject *testing.T) {
	fragmenter, err := NewControlFrameFragmenter(ControlFragmentationConfig{
		MaxPayloadSize: 72,
	})
	if err != nil {
		testingObject.Fatalf("create control fragmenter failed: %v", err)
	}
	reassembler, err := NewControlFrameReassembler(ControlFragmentationConfig{
		MaxPayloadSize: 72,
	})
	if err != nil {
		testingObject.Fatalf("create control reassembler failed: %v", err)
	}

	fragmentedFrames, err := fragmenter.Fragment(ControlFrame{
		Type:    19,
		Payload: bytes.Repeat([]byte("b"), 160),
	})
	if err != nil {
		testingObject.Fatalf("fragment control frame failed: %v", err)
	}
	if _, ready, err := reassembler.Reassemble(fragmentedFrames[0]); err != nil {
		testingObject.Fatalf("reassemble first fragment failed: %v", err)
	} else if ready {
		testingObject.Fatalf("expected first fragment not ready")
	}

	highPriorityFrame := ControlFrame{
		Type:    3,
		Payload: []byte("heartbeat"),
	}
	reassembledFrame, ready, err := reassembler.Reassemble(highPriorityFrame)
	if err != nil {
		testingObject.Fatalf("reassemble heartbeat frame failed: %v", err)
	}
	if !ready {
		testingObject.Fatalf("expected heartbeat frame ready immediately")
	}
	if reassembledFrame.Type != highPriorityFrame.Type || !bytes.Equal(reassembledFrame.Payload, highPriorityFrame.Payload) {
		testingObject.Fatalf("unexpected heartbeat passthrough frame: %+v", reassembledFrame)
	}

	for fragmentIndex := 1; fragmentIndex < len(fragmentedFrames); fragmentIndex++ {
		reassembledFrame, ready, err = reassembler.Reassemble(fragmentedFrames[fragmentIndex])
		if err != nil {
			testingObject.Fatalf("reassemble later fragment %d failed: %v", fragmentIndex, err)
		}
		if fragmentIndex < len(fragmentedFrames)-1 {
			if ready {
				testingObject.Fatalf("expected fragment %d not ready", fragmentIndex)
			}
			continue
		}
		if !ready {
			testingObject.Fatalf("expected final fragment ready")
		}
		if reassembledFrame.Type != 19 || len(reassembledFrame.Payload) != 160 {
			testingObject.Fatalf("unexpected reassembled frame: %+v", reassembledFrame)
		}
	}
}

// TestControlFrameReassemblerDropsExpiredAssembly 验证超时半包会被清理，避免旧分块污染新消息。
func TestControlFrameReassemblerDropsExpiredAssembly(testingObject *testing.T) {
	fragmenter, err := NewControlFrameFragmenter(ControlFragmentationConfig{
		MaxPayloadSize: 72,
		ReassemblyTTL:  10 * time.Millisecond,
	})
	if err != nil {
		testingObject.Fatalf("create control fragmenter failed: %v", err)
	}
	reassembler, err := NewControlFrameReassembler(ControlFragmentationConfig{
		MaxPayloadSize: 72,
		ReassemblyTTL:  10 * time.Millisecond,
	})
	if err != nil {
		testingObject.Fatalf("create control reassembler failed: %v", err)
	}

	fragmentedFrames, err := fragmenter.Fragment(ControlFrame{
		Type:    29,
		Payload: bytes.Repeat([]byte("c"), 120),
	})
	if err != nil {
		testingObject.Fatalf("fragment control frame failed: %v", err)
	}

	baseTime := time.Now().UTC()
	if _, ready, err := reassembler.reassembleAt(fragmentedFrames[0], baseTime); err != nil {
		testingObject.Fatalf("reassemble first fragment failed: %v", err)
	} else if ready {
		testingObject.Fatalf("expected first fragment not ready")
	}
	if _, ready, err := reassembler.reassembleAt(fragmentedFrames[1], baseTime.Add(50*time.Millisecond)); err != nil {
		testingObject.Fatalf("reassemble second fragment after cleanup failed: %v", err)
	} else if ready {
		testingObject.Fatalf("expected second fragment alone not ready after cleanup")
	}
}

// TestControlFrameReassemblerRejectsBrokenHeader 验证非法分块头会被显式拒绝。
func TestControlFrameReassemblerRejectsBrokenHeader(testingObject *testing.T) {
	reassembler, err := NewControlFrameReassembler(DefaultControlFragmentationConfig())
	if err != nil {
		testingObject.Fatalf("create control reassembler failed: %v", err)
	}

	_, _, err = reassembler.Reassemble(ControlFrame{
		Type:    ControlFrameTypeFragment,
		Payload: []byte("bad-header"),
	})
	if err == nil {
		testingObject.Fatalf("expected broken header error")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestControlFrameReassemblerRejectsImpossibleTotalPayloadSize 验证声明总长度超过单消息上界时会被拒绝。
func TestControlFrameReassemblerRejectsImpossibleTotalPayloadSize(testingObject *testing.T) {
	reassembler, err := NewControlFrameReassembler(ControlFragmentationConfig{
		MaxPayloadSize: 72,
	})
	if err != nil {
		testingObject.Fatalf("create control reassembler failed: %v", err)
	}

	// MaxPayloadSize=72 时，单分块最大 payload=48；此处故意声明更大的 total_payload_size。
	encodedFragment := encodeControlFragment(controlFragmentHeader{
		OriginalType:     17,
		MessageID:        99,
		FragmentIndex:    0,
		FragmentCount:    1,
		TotalPayloadSize: 128,
	}, []byte("x"))
	_, _, err = reassembler.Reassemble(ControlFrame{
		Type:    ControlFrameTypeFragment,
		Payload: encodedFragment,
	})
	if err == nil {
		testingObject.Fatalf("expected impossible total payload size rejection")
	}
	if !errors.Is(err, ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "total_payload_size") {
		testingObject.Fatalf("expected total_payload_size in error, got %v", err)
	}
}
