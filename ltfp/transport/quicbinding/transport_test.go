package quicbinding

import (
	"errors"
	"testing"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

// TestTransportConfigNormalizeAndValidate 验证 QUIC transport 配置默认值会被正确回填。
func TestTransportConfigNormalizeAndValidate(testingObject *testing.T) {
	normalizedConfig, err := (TransportConfig{}).NormalizeAndValidate()
	if err != nil {
		testingObject.Fatalf("normalize config failed: %v", err)
	}
	if normalizedConfig.HandshakeIdleTimeout <= 0 {
		testingObject.Fatalf("expected handshake idle timeout default, got %+v", normalizedConfig)
	}
	if normalizedConfig.MaxIdleTimeout <= 0 {
		testingObject.Fatalf("expected max idle timeout default, got %+v", normalizedConfig)
	}
	if normalizedConfig.KeepAlivePeriod <= 0 {
		testingObject.Fatalf("expected keepalive period default, got %+v", normalizedConfig)
	}
	if normalizedConfig.StreamOpenTimeout <= 0 {
		testingObject.Fatalf("expected stream open timeout default, got %+v", normalizedConfig)
	}
	if normalizedConfig.MaxIncomingStreams <= 0 {
		testingObject.Fatalf("expected positive max incoming streams, got %+v", normalizedConfig)
	}
	if normalizedConfig.MaxControlFramePayloadSize <= 0 || normalizedConfig.MaxTunnelFramePayloadSize <= 0 {
		testingObject.Fatalf("expected positive frame payload limits, got %+v", normalizedConfig)
	}
}

// TestTransportConfigNormalizeRejectsNegativeDuration 验证负数 duration 会被拒绝。
func TestTransportConfigNormalizeRejectsNegativeDuration(testingObject *testing.T) {
	_, err := (TransportConfig{
		KeepAlivePeriod: -time.Second,
	}).NormalizeAndValidate()
	if err == nil {
		testingObject.Fatalf("expected invalid duration error")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestTransportConfigNormalizeRejectsInvalidStreamLimit 验证非正数 stream 配额会被拒绝。
func TestTransportConfigNormalizeRejectsNegativeStreamLimit(testingObject *testing.T) {
	_, err := (TransportConfig{MaxIncomingStreams: -1}).NormalizeAndValidate()
	if err == nil {
		testingObject.Fatalf("expected negative stream limit error")
	}
	if !errors.Is(err, transport.ErrInvalidArgument) {
		testingObject.Fatalf("expected ErrInvalidArgument, got %v", err)
	}
}

// TestNewTransportWithConfigUsesNormalizedConfig 验证构造函数会保留归一化后的配置。
func TestNewTransportWithConfigUsesNormalizedConfig(testingObject *testing.T) {
	binding, err := NewTransportWithConfig(TransportConfig{MaxIncomingStreams: 32})
	if err != nil {
		testingObject.Fatalf("new transport with config failed: %v", err)
	}
	if binding.Config().MaxIncomingStreams != 32 {
		testingObject.Fatalf("expected max incoming streams to be preserved, got %+v", binding.Config())
	}
	if binding.Config().KeepAlivePeriod <= 0 {
		testingObject.Fatalf("expected keepalive period default to be backfilled, got %+v", binding.Config())
	}
}
