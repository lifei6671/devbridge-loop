package grpcbinding

import (
	"context"
	"errors"
	"testing"

	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

type fakeGeneratedControlBidiStream struct {
	base *fakeControlChannelStream
}

func (stream *fakeGeneratedControlBidiStream) Send(frame *transportgen.ControlFrameEnvelope) error {
	return stream.base.Send(frame)
}

func (stream *fakeGeneratedControlBidiStream) Recv() (*transportgen.ControlFrameEnvelope, error) {
	return stream.base.Recv()
}

func (stream *fakeGeneratedControlBidiStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (stream *fakeGeneratedControlBidiStream) Trailer() metadata.MD {
	return metadata.MD{}
}

func (stream *fakeGeneratedControlBidiStream) CloseSend() error {
	return stream.base.CloseSend()
}

func (stream *fakeGeneratedControlBidiStream) Context() context.Context {
	return stream.base.Context()
}

func (stream *fakeGeneratedControlBidiStream) SendMsg(any) error {
	return nil
}

func (stream *fakeGeneratedControlBidiStream) RecvMsg(any) error {
	return nil
}

type fakeGeneratedTunnelBidiStream struct {
	base *fakeTunnelEnvelopeStream
}

func (stream *fakeGeneratedTunnelBidiStream) Send(frame *transportgen.TunnelEnvelope) error {
	return stream.base.Send(frame)
}

func (stream *fakeGeneratedTunnelBidiStream) Recv() (*transportgen.TunnelEnvelope, error) {
	return stream.base.Recv()
}

func (stream *fakeGeneratedTunnelBidiStream) Header() (metadata.MD, error) {
	return metadata.MD{}, nil
}

func (stream *fakeGeneratedTunnelBidiStream) Trailer() metadata.MD {
	return metadata.MD{}
}

func (stream *fakeGeneratedTunnelBidiStream) CloseSend() error {
	return stream.base.CloseSend()
}

func (stream *fakeGeneratedTunnelBidiStream) Context() context.Context {
	return stream.base.Context()
}

func (stream *fakeGeneratedTunnelBidiStream) SendMsg(any) error {
	return nil
}

func (stream *fakeGeneratedTunnelBidiStream) RecvMsg(any) error {
	return nil
}

type fakeTransportServiceClient struct {
	controlStream grpc.BidiStreamingClient[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope]
	tunnelStream  grpc.BidiStreamingClient[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope]

	controlError error
	tunnelError  error

	controlCalled bool
	tunnelCalled  bool
}

func (client *fakeTransportServiceClient) ControlChannel(
	ctx context.Context,
	opts ...grpc.CallOption,
) (grpc.BidiStreamingClient[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope], error) {
	client.controlCalled = true
	if client.controlError != nil {
		return nil, client.controlError
	}
	return client.controlStream, nil
}

func (client *fakeTransportServiceClient) TunnelStream(
	ctx context.Context,
	opts ...grpc.CallOption,
) (grpc.BidiStreamingClient[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope], error) {
	client.tunnelCalled = true
	if client.tunnelError != nil {
		return nil, client.tunnelError
	}
	return client.tunnelStream, nil
}

// TestTransportConfigNormalizeAndValidate 验证配置归一化默认值。
func TestTransportConfigNormalizeAndValidate(testingObject *testing.T) {
	normalizedConfig, err := (TransportConfig{}).NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("normalize config failed: %v", err)
	}
	if normalizedConfig.MaxCallRecvMsgSize <= 0 || normalizedConfig.MaxCallSendMsgSize <= 0 {
		testingObject.Fatalf("unexpected normalized config: %+v", normalizedConfig)
	}
}

// TestTransportOpenChannelRejectsNilClient 验证 nil client 会返回参数错误。
func TestTransportOpenChannelRejectsNilClient(testingObject *testing.T) {
	binding := NewTransport()
	if _, err := binding.OpenControlChannel(context.Background(), nil); err == nil {
		testingObject.Fatalf("expected nil client error for control channel")
	} else if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
	if _, err := binding.OpenTunnelStream(context.Background(), nil); err == nil {
		testingObject.Fatalf("expected nil client error for tunnel stream")
	} else if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestTransportOpenControlAndTunnelStream 验证打开控制流与数据流成功路径。
func TestTransportOpenControlAndTunnelStream(testingObject *testing.T) {
	fakeControlStream := &fakeGeneratedControlBidiStream{
		base: &fakeControlChannelStream{},
	}
	fakeTunnelStream := &fakeGeneratedTunnelBidiStream{
		base: &fakeTunnelEnvelopeStream{},
	}
	client := &fakeTransportServiceClient{
		controlStream: fakeControlStream,
		tunnelStream:  fakeTunnelStream,
	}
	binding := NewTransport()

	controlChannel, err := binding.OpenControlChannel(context.Background(), client)
	if err != nil {
		testingObject.Fatalf("open control channel failed: %v", err)
	}
	if controlChannel == nil || !client.controlCalled {
		testingObject.Fatalf("expected control channel to open successfully")
	}
	if err := controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
		Type:    11,
		Payload: []byte("control-payload"),
	}); err != nil {
		testingObject.Fatalf("write control frame failed: %v", err)
	}
	if len(fakeControlStream.base.sendFrames) != 1 {
		testingObject.Fatalf("expected one sent control frame, got %d", len(fakeControlStream.base.sendFrames))
	}

	tunnelStream, err := binding.OpenTunnelStream(context.Background(), client)
	if err != nil {
		testingObject.Fatalf("open tunnel stream failed: %v", err)
	}
	if tunnelStream == nil || !client.tunnelCalled {
		testingObject.Fatalf("expected tunnel stream to open successfully")
	}
	if err := tunnelStream.WritePayload(context.Background(), []byte("tunnel-payload")); err != nil {
		testingObject.Fatalf("write tunnel payload failed: %v", err)
	}
	if len(fakeTunnelStream.base.sendFrames) != 1 {
		testingObject.Fatalf("expected one sent tunnel envelope, got %d", len(fakeTunnelStream.base.sendFrames))
	}
}
