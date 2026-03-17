package connectorproxy

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

const (
	// DefaultCloseAckTimeout 定义 close -> close_ack 等待超时。
	DefaultCloseAckTimeout = 3 * time.Second
	// DefaultRecycleHandshakeTimeout 定义 recycle -> recycle_ack 等待超时。
	DefaultRecycleHandshakeTimeout = 3 * time.Second
)

// WriteTrafficCloseAndAwaitAck 发送 TrafficClose 并等待对端 TrafficCloseAck。
func WriteTrafficCloseAndAwaitAck(
	ctx context.Context,
	tunnel registry.RuntimeTunnel,
	trafficID string,
	reason string,
	timeout time.Duration,
) error {
	if tunnel == nil {
		return ErrDispatcherDependencyMissing
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return fmt.Errorf("write traffic close and await ack: empty traffic id")
	}
	normalizedContext, cancel := withHandshakeTimeout(ctx, timeout, DefaultCloseAckTimeout)
	defer cancel()
	closePayload := pb.StreamPayload{
		Close: &pb.TrafficClose{
			TrafficID: normalizedTrafficID,
			Reason:    strings.TrimSpace(reason),
		},
	}
	if err := tunnel.WritePayload(normalizedContext, closePayload); err != nil {
		return fmt.Errorf("write traffic close and await ack: write close: %w", err)
	}
	for {
		payload, err := tunnel.ReadPayload(normalizedContext)
		if err != nil {
			return fmt.Errorf("write traffic close and await ack: read close ack: %w", err)
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == normalizedTrafficID {
			// close 双向并发时优先 ACK 对端 close，避免双方都在等待 close_ack 导致僵持。
			if err := tunnel.WritePayload(normalizedContext, pb.StreamPayload{
				CloseAck: &pb.TrafficCloseAck{
					TrafficID: normalizedTrafficID,
					Accepted:  true,
				},
			}); err != nil {
				return fmt.Errorf("write traffic close and await ack: write peer close ack: %w", err)
			}
			return nil
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == normalizedTrafficID {
			return fmt.Errorf(
				"write traffic close and await ack: relay reset code=%s message=%s",
				strings.TrimSpace(payload.Reset.ErrorCode),
				strings.TrimSpace(payload.Reset.ErrorMessage),
			)
		}
		if payload.CloseAck == nil {
			// 忽略非 close_ack 帧，继续等待目标确认帧。
			continue
		}
		ack := payload.CloseAck
		if strings.TrimSpace(ack.TrafficID) != normalizedTrafficID {
			// 仅接受当前 traffic 的 close_ack。
			continue
		}
		if !ack.Accepted {
			return fmt.Errorf(
				"write traffic close and await ack: close rejected code=%s message=%s",
				strings.TrimSpace(ack.ErrorCode),
				strings.TrimSpace(ack.ErrorMessage),
			)
		}
		return nil
	}
}

// ExecuteTunnelRecycleHandshake 执行 TunnelRecycle -> TunnelRecycleAck 握手。
func ExecuteTunnelRecycleHandshake(
	ctx context.Context,
	tunnel registry.RuntimeTunnel,
	tunnelID string,
	recycleSeq uint64,
	isFinal bool,
	timeout time.Duration,
) (pb.TunnelRecycleAck, error) {
	if tunnel == nil {
		return pb.TunnelRecycleAck{}, ErrDispatcherDependencyMissing
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return pb.TunnelRecycleAck{}, fmt.Errorf("execute tunnel recycle handshake: empty tunnel id")
	}
	if recycleSeq == 0 {
		return pb.TunnelRecycleAck{}, fmt.Errorf("execute tunnel recycle handshake: recycle seq must be greater than 0")
	}
	normalizedContext, cancel := withHandshakeTimeout(ctx, timeout, DefaultRecycleHandshakeTimeout)
	defer cancel()
	recyclePayload := pb.StreamPayload{
		Recycle: &pb.TunnelRecycle{
			TunnelID:   normalizedTunnelID,
			RecycleSeq: recycleSeq,
			IsFinal:    isFinal,
		},
	}
	if err := tunnel.WritePayload(normalizedContext, recyclePayload); err != nil {
		return pb.TunnelRecycleAck{}, fmt.Errorf("execute tunnel recycle handshake: write recycle: %w", err)
	}
	for {
		payload, err := tunnel.ReadPayload(normalizedContext)
		if err != nil {
			return pb.TunnelRecycleAck{}, fmt.Errorf("execute tunnel recycle handshake: read recycle ack: %w", err)
		}
		if payload.RecycleAck == nil {
			// 忽略非 recycle_ack 帧，继续等待确认。
			continue
		}
		ack := *payload.RecycleAck
		if strings.TrimSpace(ack.TunnelID) != normalizedTunnelID {
			continue
		}
		if ack.RecycleSeq != recycleSeq {
			return pb.TunnelRecycleAck{}, fmt.Errorf(
				"execute tunnel recycle handshake: recycle seq mismatch got=%d want=%d",
				ack.RecycleSeq,
				recycleSeq,
			)
		}
		if !ack.Accepted {
			recycleErrorCode := normalizeRecycleRejectCode(ack.ErrorCode)
			return ack, fmt.Errorf(
				"execute tunnel recycle handshake: recycle rejected code=%s message=%s",
				recycleErrorCode,
				strings.TrimSpace(ack.ErrorMessage),
			)
		}
		return ack, nil
	}
}

func normalizeRecycleRejectCode(errorCode string) string {
	normalizedCode := strings.TrimSpace(errorCode)
	switch normalizedCode {
	case ltfperrors.CodeTunnelRecycleInvalidSeq:
		return ltfperrors.CodeTunnelRecycleInvalidSeq
	case ltfperrors.CodeTunnelRecycleCloseAckRequired:
		return ltfperrors.CodeTunnelRecycleCloseAckRequired
	case ltfperrors.CodeTunnelRecycleTunnelUnhealthy:
		return ltfperrors.CodeTunnelRecycleTunnelUnhealthy
	case ltfperrors.CodeTunnelRecycleBufferDirty:
		return ltfperrors.CodeTunnelRecycleBufferDirty
	case ltfperrors.CodeTunnelRecycleTunnelMismatch:
		return ltfperrors.CodeTunnelRecycleTunnelMismatch
	default:
		return normalizedCode
	}
}

func withHandshakeTimeout(
	ctx context.Context,
	timeout time.Duration,
	defaultTimeout time.Duration,
) (context.Context, context.CancelFunc) {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	effectiveTimeout := timeout
	if effectiveTimeout <= 0 {
		effectiveTimeout = defaultTimeout
	}
	if effectiveTimeout <= 0 {
		return normalizedContext, func() {}
	}
	if _, hasDeadline := normalizedContext.Deadline(); hasDeadline {
		return normalizedContext, func() {}
	}
	timeoutContext, cancel := context.WithTimeout(normalizedContext, effectiveTimeout)
	return timeoutContext, cancel
}
