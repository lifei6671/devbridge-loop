package grpcbinding

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTunnelProducerOpenTunnel 验证 producer 能主动打开 TunnelStream 并生成 tunnel 元数据。
func TestTunnelProducerOpenTunnel(testingObject *testing.T) {
	fakeTunnelStream := &fakeGeneratedTunnelBidiStream{
		base: &fakeTunnelEnvelopeStream{},
	}
	client := &fakeTransportServiceClient{
		tunnelStream: fakeTunnelStream,
	}
	producer, err := NewTunnelProducer(client, TunnelIdentityConfig{
		SessionID:      "session-producer",
		SessionEpoch:   8,
		TunnelIDPrefix: "agent-tunnel",
	})
	if err != nil {
		testingObject.Fatalf("create tunnel producer failed: %v", err)
	}

	tunnel, err := producer.OpenTunnel(context.Background())
	if err != nil {
		testingObject.Fatalf("open tunnel failed: %v", err)
	}
	if tunnel.BindingInfo().Type != transport.BindingTypeGRPCH2 {
		testingObject.Fatalf("expected grpc_h2 binding type, got %s", tunnel.BindingInfo().Type)
	}
	meta := tunnel.Meta()
	if meta.SessionID != "session-producer" || meta.SessionEpoch != 8 {
		testingObject.Fatalf("unexpected tunnel meta: %+v", meta)
	}
	if !strings.HasPrefix(meta.TunnelID, "agent-tunnel-") {
		testingObject.Fatalf("unexpected tunnel id: %s", meta.TunnelID)
	}
}

// TestTunnelProducerOpenTunnelPropagatesClientError 验证打开流失败会被透传。
func TestTunnelProducerOpenTunnelPropagatesClientError(testingObject *testing.T) {
	client := &fakeTransportServiceClient{
		tunnelError: errors.New("dial failed"),
	}
	producer, err := NewTunnelProducer(client, TunnelIdentityConfig{})
	if err != nil {
		testingObject.Fatalf("create tunnel producer failed: %v", err)
	}
	_, err = producer.OpenTunnel(context.Background())
	if err == nil {
		testingObject.Fatalf("expected open tunnel error")
	}
	if !strings.Contains(err.Error(), "dial failed") {
		testingObject.Fatalf("expected dial failure to be propagated, got %v", err)
	}
}
