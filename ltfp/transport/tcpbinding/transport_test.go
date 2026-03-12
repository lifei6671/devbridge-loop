package tcpbinding

import (
	"context"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTransportConfigNormalizeAndValidate 验证 tcp_framed 配置归一化默认值。
func TestTransportConfigNormalizeAndValidate(testingObject *testing.T) {
	normalizedConfig, err := (TransportConfig{}).NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("normalize config failed: %v", err)
	}
	if normalizedConfig.DialTimeout <= 0 {
		testingObject.Fatalf("expected positive dial timeout, got %s", normalizedConfig.DialTimeout)
	}
	if normalizedConfig.MaxControlFramePayloadSize <= 0 || normalizedConfig.MaxTunnelFramePayloadSize <= 0 {
		testingObject.Fatalf("unexpected frame size defaults: %+v", normalizedConfig)
	}
	if !normalizedConfig.NoDelay || !normalizedConfig.NoDelaySet {
		testingObject.Fatalf("expected default no-delay enabled, got %+v", normalizedConfig)
	}

	explicitConfig, err := (TransportConfig{
		NoDelay:    false,
		NoDelaySet: true,
	}).NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("normalize explicit no-delay config failed: %v", err)
	}
	if explicitConfig.NoDelay {
		testingObject.Fatalf("expected explicit no-delay=false to be preserved, got %+v", explicitConfig)
	}
}

// TestTransportDialAndAcceptControlChannel 验证 tcp transport 可拨号并接受控制连接。
func TestTransportDialAndAcceptControlChannel(testingObject *testing.T) {
	listener, err := newTCPTestListener()
	if err != nil {
		testingObject.Fatalf("create listener failed: %v", err)
	}
	defer listener.Close()

	binding := NewTransport()
	serverResultChannel := make(chan error, 1)
	go func() {
		serverContext, cancelServer := context.WithTimeout(context.Background(), time.Second)
		defer cancelServer()

		controlChannel, err := binding.AcceptControlChannel(serverContext, listener)
		if err != nil {
			serverResultChannel <- err
			return
		}
		defer controlChannel.Close(context.Background())

		frame, err := controlChannel.ReadControlFrame(context.Background())
		if err != nil {
			serverResultChannel <- err
			return
		}
		if frame.Type != 21 || string(frame.Payload) != "hello-from-client" {
			serverResultChannel <- testingErrorf("unexpected server frame: %+v", frame)
			return
		}
		serverResultChannel <- controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
			Type:    22,
			Payload: []byte("hello-from-server"),
		})
	}()

	clientContext, cancelClient := context.WithTimeout(context.Background(), time.Second)
	defer cancelClient()
	controlChannel, err := binding.DialControlChannel(clientContext, listener.Addr().String())
	if err != nil {
		testingObject.Fatalf("dial control channel failed: %v", err)
	}
	defer controlChannel.Close(context.Background())

	if err := controlChannel.WriteControlFrame(context.Background(), transport.ControlFrame{
		Type:    21,
		Payload: []byte("hello-from-client"),
	}); err != nil {
		testingObject.Fatalf("write control frame failed: %v", err)
	}
	replyFrame, err := controlChannel.ReadControlFrame(context.Background())
	if err != nil {
		testingObject.Fatalf("read reply frame failed: %v", err)
	}
	if replyFrame.Type != 22 || string(replyFrame.Payload) != "hello-from-server" {
		testingObject.Fatalf("unexpected reply frame: %+v", replyFrame)
	}

	if serverErr := <-serverResultChannel; serverErr != nil {
		testingObject.Fatalf("server side failed: %v", serverErr)
	}
}
