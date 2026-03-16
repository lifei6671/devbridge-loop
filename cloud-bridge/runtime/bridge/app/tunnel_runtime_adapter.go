package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/codec"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

const (
	// defaultBridgeTunnelIOPollInterval 定义 Bridge 侧 tunnel I/O 轮询窗口，用于响应 context 取消。
	defaultBridgeTunnelIOPollInterval = 100 * time.Millisecond
	// defaultBridgeTunnelMaxPayloadBytes 定义 Bridge 侧单条 StreamPayload 上限。
	defaultBridgeTunnelMaxPayloadBytes = 8 * 1024 * 1024
)

// runtimeBridgeTunnelAdapter 把 transport.Tunnel 适配为 registry.RuntimeTunnel。
type runtimeBridgeTunnelAdapter struct {
	tunnel          transport.Tunnel
	tunnelID        string
	jsonCodec       *codec.JSONCodec
	maxPayloadBytes int
	ioPollInterval  time.Duration
}

var _ registry.RuntimeTunnel = (*runtimeBridgeTunnelAdapter)(nil)
var _ registry.RuntimeTunnelHealthProber = (*runtimeBridgeTunnelAdapter)(nil)

// newRuntimeBridgeTunnelAdapter 创建 Bridge data-plane tunnel payload 适配器。
func newRuntimeBridgeTunnelAdapter(rawTunnel transport.Tunnel, tunnelID string) *runtimeBridgeTunnelAdapter {
	return &runtimeBridgeTunnelAdapter{
		tunnel:          rawTunnel,
		tunnelID:        strings.TrimSpace(tunnelID),
		jsonCodec:       codec.NewJSONCodec(),
		maxPayloadBytes: defaultBridgeTunnelMaxPayloadBytes,
		ioPollInterval:  defaultBridgeTunnelIOPollInterval,
	}
}

// ID 返回 tunnel 唯一标识。
func (adapter *runtimeBridgeTunnelAdapter) ID() string {
	if adapter == nil {
		return ""
	}
	if normalizedTunnelID := strings.TrimSpace(adapter.tunnelID); normalizedTunnelID != "" {
		return normalizedTunnelID
	}
	if adapter.tunnel == nil {
		return ""
	}
	return strings.TrimSpace(adapter.tunnel.ID())
}

// BindingType 返回底层 tunnel 的 binding 类型，供上层做协议特定策略分支。
func (adapter *runtimeBridgeTunnelAdapter) BindingType() transport.BindingType {
	if adapter == nil || adapter.tunnel == nil {
		return ""
	}
	return adapter.tunnel.BindingInfo().Type
}

// Close 关闭底层 tunnel。
func (adapter *runtimeBridgeTunnelAdapter) Close() error {
	if adapter == nil || adapter.tunnel == nil {
		return nil
	}
	return adapter.tunnel.Close()
}

// Probe 透传底层 tunnel 的可选探活能力，不支持时按无探活能力处理。
func (adapter *runtimeBridgeTunnelAdapter) Probe(ctx context.Context) error {
	if adapter == nil || adapter.tunnel == nil {
		return registry.ErrTunnelRegistryDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	prober, supportsProbe := adapter.tunnel.(transport.TunnelHealthProber)
	if !supportsProbe {
		return nil
	}
	probeErr := prober.Probe(normalizedContext)
	if probeErr == nil || errors.Is(probeErr, transport.ErrUnsupported) {
		return nil
	}
	return probeErr
}

// ReadPayload 从底层 tunnel 读取并解码一条 StreamPayload。
func (adapter *runtimeBridgeTunnelAdapter) ReadPayload(ctx context.Context) (pb.StreamPayload, error) {
	if adapter == nil || adapter.tunnel == nil || adapter.jsonCodec == nil {
		return pb.StreamPayload{}, registry.ErrTunnelRegistryDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	usePollingDeadline := shouldUseBridgeTunnelPollingDeadline(adapter.tunnel)
	readBuffer := make([]byte, adapter.maxPayloadBytes)
	for {
		if err := normalizedContext.Err(); err != nil {
			return pb.StreamPayload{}, err
		}
		if err := adapter.tunnel.SetReadDeadline(
			nextBridgeTunnelReadDeadline(normalizedContext, adapter.ioPollInterval, usePollingDeadline),
		); err != nil {
			return pb.StreamPayload{}, fmt.Errorf("bridge tunnel read payload: set read deadline: %w", err)
		}
		readSize, readErr := adapter.tunnel.Read(readBuffer)
		if readSize > 0 {
			decodedPayload, decodeErr := adapter.jsonCodec.DecodeStreamPayload(readBuffer[:readSize])
			if decodeErr != nil {
				return pb.StreamPayload{}, fmt.Errorf("bridge tunnel read payload: decode stream payload: %w", decodeErr)
			}
			return decodedPayload, nil
		}
		if readErr == nil {
			continue
		}
		if errors.Is(readErr, transport.ErrTimeout) {
			// grpc_h2 不使用短轮询超时；命中 timeout 仅代表上层 deadline 到达。
			if !usePollingDeadline {
				if err := normalizedContext.Err(); err != nil {
					return pb.StreamPayload{}, err
				}
			}
			continue
		}
		return pb.StreamPayload{}, fmt.Errorf("bridge tunnel read payload: read tunnel: %w", readErr)
	}
}

// WritePayload 将 StreamPayload 编码后写入底层 tunnel。
func (adapter *runtimeBridgeTunnelAdapter) WritePayload(ctx context.Context, payload pb.StreamPayload) error {
	if adapter == nil || adapter.tunnel == nil || adapter.jsonCodec == nil {
		return registry.ErrTunnelRegistryDependencyMissing
	}
	encodedPayload, err := adapter.jsonCodec.EncodeStreamPayload(payload)
	if err != nil {
		return fmt.Errorf("bridge tunnel write payload: encode stream payload: %w", err)
	}
	if len(encodedPayload) > adapter.maxPayloadBytes {
		return fmt.Errorf(
			"bridge tunnel write payload: payload exceeds limit size=%d max=%d",
			len(encodedPayload),
			adapter.maxPayloadBytes,
		)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	usePollingDeadline := shouldUseBridgeTunnelPollingDeadline(adapter.tunnel)
	for {
		if err := normalizedContext.Err(); err != nil {
			return err
		}
		if err := adapter.tunnel.SetWriteDeadline(
			nextBridgeTunnelWriteDeadline(normalizedContext, adapter.ioPollInterval, usePollingDeadline),
		); err != nil {
			return fmt.Errorf("bridge tunnel write payload: set write deadline: %w", err)
		}
		writtenSize, writeErr := adapter.tunnel.Write(encodedPayload)
		if writeErr == nil {
			if writtenSize != len(encodedPayload) {
				return io.ErrShortWrite
			}
			return nil
		}
		if errors.Is(writeErr, transport.ErrTimeout) {
			// grpc_h2 不使用短轮询超时；命中 timeout 仅代表上层 deadline 到达。
			if !usePollingDeadline {
				if err := normalizedContext.Err(); err != nil {
					return err
				}
			}
			continue
		}
		return fmt.Errorf("bridge tunnel write payload: write tunnel: %w", writeErr)
	}
}

func shouldUseBridgeTunnelPollingDeadline(rawTunnel transport.Tunnel) bool {
	if rawTunnel == nil {
		return true
	}
	return rawTunnel.BindingInfo().Type != transport.BindingTypeGRPCH2
}

func nextBridgeTunnelReadDeadline(ctx context.Context, pollInterval time.Duration, usePolling bool) time.Time {
	if !usePolling {
		if ctx == nil {
			return time.Time{}
		}
		if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline {
			return contextDeadline
		}
		return time.Time{}
	}
	return nextBridgeTunnelIODeadline(ctx, pollInterval)
}

func nextBridgeTunnelWriteDeadline(ctx context.Context, pollInterval time.Duration, usePolling bool) time.Time {
	if !usePolling {
		if ctx == nil {
			return time.Time{}
		}
		if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline {
			return contextDeadline
		}
		return time.Time{}
	}
	return nextBridgeTunnelIODeadline(ctx, pollInterval)
}

// nextBridgeTunnelIODeadline 计算下一次 tunnel I/O 的短轮询 deadline。
func nextBridgeTunnelIODeadline(ctx context.Context, pollInterval time.Duration) time.Time {
	effectivePollInterval := pollInterval
	if effectivePollInterval <= 0 {
		effectivePollInterval = defaultBridgeTunnelIOPollInterval
	}
	nextDeadline := time.Now().UTC().Add(effectivePollInterval)
	if ctx == nil {
		return nextDeadline
	}
	if contextDeadline, hasDeadline := ctx.Deadline(); hasDeadline && contextDeadline.Before(nextDeadline) {
		return contextDeadline
	}
	return nextDeadline
}
