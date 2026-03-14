package app

import (
	"context"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

type testPrioritizedControlChannel struct {
	lastFrame transport.PrioritizedControlFrame
	doneChan  chan struct{}
}

func newTestPrioritizedControlChannel() *testPrioritizedControlChannel {
	return &testPrioritizedControlChannel{
		doneChan: make(chan struct{}),
	}
}

func (channel *testPrioritizedControlChannel) WriteControlFrame(
	_ context.Context,
	frame transport.ControlFrame,
) error {
	channel.lastFrame = transport.PrioritizedControlFrame{
		Priority: transport.ControlMessagePriorityNormal,
		Frame:    frame,
	}
	return nil
}

func (channel *testPrioritizedControlChannel) WritePrioritizedControlFrame(
	_ context.Context,
	frame transport.PrioritizedControlFrame,
) error {
	channel.lastFrame = frame
	return nil
}

func (channel *testPrioritizedControlChannel) ReadControlFrame(ctx context.Context) (transport.ControlFrame, error) {
	<-ctx.Done()
	return transport.ControlFrame{}, ctx.Err()
}

func (channel *testPrioritizedControlChannel) Close(_ context.Context) error {
	select {
	case <-channel.doneChan:
	default:
		close(channel.doneChan)
	}
	return nil
}

func (channel *testPrioritizedControlChannel) Done() <-chan struct{} {
	return channel.doneChan
}

func (channel *testPrioritizedControlChannel) Err() error {
	return nil
}

// TestComputeBridgeRetryBackoffWithJitter 验证指数退避基线为 1/2/4/8 秒。
func TestComputeBridgeRetryBackoffWithJitter(testingObject *testing.T) {
	testCases := []struct {
		failStreak uint32
		expected   time.Duration
	}{
		{failStreak: 0, expected: 0},
		{failStreak: 1, expected: time.Second},
		{failStreak: 2, expected: 2 * time.Second},
		{failStreak: 3, expected: 4 * time.Second},
		{failStreak: 4, expected: 8 * time.Second},
		{failStreak: 5, expected: 8 * time.Second},
	}

	for _, testCase := range testCases {
		actual := computeBridgeRetryBackoffWithJitter(testCase.failStreak, 0)
		if actual != testCase.expected {
			testingObject.Fatalf(
				"unexpected backoff fail_streak=%d got=%s want=%s",
				testCase.failStreak,
				actual,
				testCase.expected,
			)
		}
	}
}

// TestComputeBridgeRetryBackoffWithJitterClamp 验证抖动与上下界钳制策略。
func TestComputeBridgeRetryBackoffWithJitterClamp(testingObject *testing.T) {
	if got := computeBridgeRetryBackoffWithJitter(1, -1); got != time.Second {
		testingObject.Fatalf("expected first retry backoff to stay at 1s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(3, -1); got != 3200*time.Millisecond {
		testingObject.Fatalf("expected 4s with -20%% jitter to be 3.2s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(3, 1); got != 4800*time.Millisecond {
		testingObject.Fatalf("expected 4s with +20%% jitter to be 4.8s, got=%s", got)
	}
	if got := computeBridgeRetryBackoffWithJitter(10, 1); got != 8*time.Second {
		testingObject.Fatalf("expected max retry backoff clamp to 8s, got=%s", got)
	}
}

// TestSendControlHeartbeatPingWritesHighPriorityFrame 验证 ping 走高优先级控制帧。
func TestSendControlHeartbeatPingWritesHighPriorityFrame(testingObject *testing.T) {
	runtime := &Runtime{}
	controlChannel := newTestPrioritizedControlChannel()

	if err := runtime.sendControlHeartbeatPing(context.Background(), controlChannel); err != nil {
		testingObject.Fatalf("send heartbeat ping failed: %v", err)
	}
	if controlChannel.lastFrame.Priority != transport.ControlMessagePriorityHigh {
		testingObject.Fatalf("expected ping priority=high, got=%s", controlChannel.lastFrame.Priority)
	}
	if controlChannel.lastFrame.Frame.Type != transport.ControlFrameTypeHeartbeatPing {
		testingObject.Fatalf(
			"expected ping frame type=%d, got=%d",
			transport.ControlFrameTypeHeartbeatPing,
			controlChannel.lastFrame.Frame.Type,
		)
	}
	if len(controlChannel.lastFrame.Frame.Payload) != 0 {
		testingObject.Fatalf("expected empty ping payload")
	}
}

// TestSendControlHeartbeatPongWritesHighPriorityFrame 验证 pong 走高优先级控制帧。
func TestSendControlHeartbeatPongWritesHighPriorityFrame(testingObject *testing.T) {
	runtime := &Runtime{}
	controlChannel := newTestPrioritizedControlChannel()

	if err := runtime.sendControlHeartbeatPong(context.Background(), controlChannel); err != nil {
		testingObject.Fatalf("send heartbeat pong failed: %v", err)
	}
	if controlChannel.lastFrame.Priority != transport.ControlMessagePriorityHigh {
		testingObject.Fatalf("expected pong priority=high, got=%s", controlChannel.lastFrame.Priority)
	}
	if controlChannel.lastFrame.Frame.Type != transport.ControlFrameTypeHeartbeatPong {
		testingObject.Fatalf(
			"expected pong frame type=%d, got=%d",
			transport.ControlFrameTypeHeartbeatPong,
			controlChannel.lastFrame.Frame.Type,
		)
	}
	if len(controlChannel.lastFrame.Frame.Payload) != 0 {
		testingObject.Fatalf("expected empty pong payload")
	}
}

// TestInitTransportSupportsGRPCH2 验证 grpc_h2 已接入 runtime 初始化路径。
func TestInitTransportSupportsGRPCH2(testingObject *testing.T) {
	testingObject.Parallel()
	runtime := &Runtime{
		cfg: Config{
			BridgeTransport: transport.BindingTypeGRPCH2.String(),
			ControlChannel: ControlChannelConfig{
				DialTimeout: time.Second,
			},
		},
	}
	if err := runtime.initTransport(); err != nil {
		testingObject.Fatalf("init grpc_h2 transport failed: %v", err)
	}
	if runtime.grpcTransport == nil {
		testingObject.Fatalf("expected grpc transport initialized")
	}
}
