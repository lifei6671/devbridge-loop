package traffic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// acceptorTestReader 提供可控的 payload 读取行为。
type acceptorTestReader struct {
	payload pb.StreamPayload
	err     error
	wait    <-chan struct{}
}

// ReadPayload 返回预设 payload。
func (reader *acceptorTestReader) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	if reader.wait != nil {
		select {
		case <-ctx.Done():
			return pb.StreamPayload{}, ctx.Err()
		case <-reader.wait:
		}
	}
	if reader.err != nil {
		return pb.StreamPayload{}, reader.err
	}
	return reader.payload, nil
}

// TestAcceptorReaderExclusive 验证同一 tunnel 只能有一个 reader 所有权。
func TestAcceptorReaderExclusive(testingObject *testing.T) {
	testingObject.Parallel()
	acceptor := NewAcceptor()
	waitChannel := make(chan struct{})
	resultChannel := make(chan OpenHandoff, 1)
	errorChannel := make(chan error, 1)
	go func() {
		handoff, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-1", &acceptorTestReader{
			wait: waitChannel,
			payload: pb.StreamPayload{
				OpenReq: &pb.TrafficOpen{
					TrafficID: "traffic-1",
					ServiceID: "svc-1",
				},
			},
		})
		resultChannel <- handoff
		errorChannel <- err
	}()

	deadline := time.Now().Add(time.Second)
	for !acceptor.IsReaderOwned("tunnel-1") {
		if time.Now().After(deadline) {
			testingObject.Fatalf("expected tunnel reader owned by acceptor")
		}
		time.Sleep(5 * time.Millisecond)
	}
	_, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-1", &acceptorTestReader{
		payload: pb.StreamPayload{
			OpenReq: &pb.TrafficOpen{
				TrafficID: "traffic-2",
				ServiceID: "svc-1",
			},
		},
	})
	if !errors.Is(err, ErrTunnelReaderAlreadyOwned) {
		testingObject.Fatalf("expected reader already owned, got: %v", err)
	}

	close(waitChannel)
	handoff := <-resultChannel
	if err := <-errorChannel; err != nil {
		testingObject.Fatalf("wait traffic open failed: %v", err)
	}
	if handoff.Open.TrafficID != "traffic-1" {
		testingObject.Fatalf("unexpected traffic ID: %s", handoff.Open.TrafficID)
	}
	if !acceptor.IsReaderOwned("tunnel-1") {
		testingObject.Fatalf("expected reader lease still owned after handoff")
	}
	handoff.Lease.Release()
	if acceptor.IsReaderOwned("tunnel-1") {
		testingObject.Fatalf("expected reader ownership released")
	}
}

// TestAcceptorRejectNonOpenFirstFrame 验证首帧非 Open 时会拒绝并释放所有权。
func TestAcceptorRejectNonOpenFirstFrame(testingObject *testing.T) {
	testingObject.Parallel()
	acceptor := NewAcceptor()
	_, err := acceptor.WaitTrafficOpen(context.Background(), "tunnel-2", &acceptorTestReader{
		payload: pb.StreamPayload{
			Data: []byte("not-open"),
		},
	})
	if !errors.Is(err, ErrFirstFrameNotTrafficOpen) {
		testingObject.Fatalf("expected first frame not traffic open, got: %v", err)
	}
	if acceptor.IsReaderOwned("tunnel-2") {
		testingObject.Fatalf("expected reader ownership released on protocol error")
	}
}
