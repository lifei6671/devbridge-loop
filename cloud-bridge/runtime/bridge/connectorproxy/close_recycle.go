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

type bridgeCloseAckSentTracker interface {
	TryMarkCloseAckSent(trafficID string) bool
	ClearCloseAckSent(trafficID string)
}

type bridgeUnsafeRecycleTracker interface {
	MarkUnsafeToRecycle(errorCode string, reason string)
	ConsumeUnsafeToRecycle() (string, string, bool)
}

// tryMarkBridgeCloseAckSentOnce 记录 close_ack 是否已发送，保证 simultaneous close 场景至多回 ACK 一次。
func tryMarkBridgeCloseAckSentOnce(tunnel registry.RuntimeTunnel, trafficID string) bool {
	if tunnel == nil {
		return false
	}
	tracker, ok := tunnel.(bridgeCloseAckSentTracker)
	if !ok {
		// 未实现状态跟踪接口时按兼容路径放行一次 ACK。
		return true
	}
	return tracker.TryMarkCloseAckSent(trafficID)
}

// clearBridgeCloseAckSentState 清理当前 traffic 的 close_ack 发送状态。
func clearBridgeCloseAckSentState(tunnel registry.RuntimeTunnel, trafficID string) {
	if tunnel == nil {
		return
	}
	tracker, ok := tunnel.(bridgeCloseAckSentTracker)
	if !ok {
		return
	}
	tracker.ClearCloseAckSent(trafficID)
}

// markBridgeTunnelUnsafeToRecycle 标记本轮 traffic 已不满足安全回收前置条件。
func markBridgeTunnelUnsafeToRecycle(tunnel registry.RuntimeTunnel, errorCode string, reason string) {
	if tunnel == nil {
		return
	}
	tracker, ok := tunnel.(bridgeUnsafeRecycleTracker)
	if !ok {
		return
	}
	tracker.MarkUnsafeToRecycle(errorCode, reason)
}

// consumeBridgeTunnelUnsafeToRecycle 读取并清空 unsafe recycle 标记。
func consumeBridgeTunnelUnsafeToRecycle(tunnel registry.RuntimeTunnel) (string, string, bool) {
	if tunnel == nil {
		return "", "", false
	}
	tracker, ok := tunnel.(bridgeUnsafeRecycleTracker)
	if !ok {
		return "", "", false
	}
	return tracker.ConsumeUnsafeToRecycle()
}

// normalizeTunnelRecycleAckErrorMessage 归一化 peer recycle reject 的错误消息，避免日志出现空文本。
func normalizeTunnelRecycleAckErrorMessage(errorCode string, message string) string {
	normalizedMessage := strings.TrimSpace(message)
	if normalizedMessage != "" {
		return normalizedMessage
	}
	return fmt.Sprintf("peer rejected tunnel recycle with code=%s", strings.TrimSpace(errorCode))
}

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
	failHandshake := func(message string, cause error) error {
		recycleErr := ltfperrors.WrapTunnelRecycleError(
			ltfperrors.CodeTunnelRecycleCloseAckRequired,
			message,
			cause,
		)
		// 无法完成 close_ack 闭环时，后续必须直接关闭 tunnel，而不能继续走 recycle。
		markBridgeTunnelUnsafeToRecycle(
			tunnel,
			ltfperrors.ExtractTunnelRecycleCode(recycleErr),
			ltfperrors.ExtractTunnelRecycleMessage(recycleErr),
		)
		return recycleErr
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
		return failHandshake("write traffic close failed", err)
	}
	for {
		payload, err := tunnel.ReadPayload(normalizedContext)
		if err != nil {
			return failHandshake("wait close ack failed", err)
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == normalizedTrafficID {
			// close 双向并发时只在首次观测到 peer close 时回 ACK，避免双方重复确认。
			if tryMarkBridgeCloseAckSentOnce(tunnel, normalizedTrafficID) {
				if err := tunnel.WritePayload(normalizedContext, pb.StreamPayload{
					CloseAck: &pb.TrafficCloseAck{
						TrafficID: normalizedTrafficID,
						Accepted:  true,
					},
				}); err != nil {
					return failHandshake("write peer close ack failed", err)
				}
			}
			return nil
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == normalizedTrafficID {
			return failHandshake("peer reset before recycle", fmt.Errorf(
				"relay reset code=%s message=%s",
				strings.TrimSpace(payload.Reset.ErrorCode),
				strings.TrimSpace(payload.Reset.ErrorMessage),
			))
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
			return failHandshake("peer rejected close before recycle", fmt.Errorf(
				"close rejected code=%s message=%s",
				strings.TrimSpace(ack.ErrorCode),
				strings.TrimSpace(ack.ErrorMessage),
			))
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
		return pb.TunnelRecycleAck{}, ltfperrors.WrapTunnelRecycleError(
			ltfperrors.CodeTunnelRecycleTunnelUnhealthy,
			"write tunnel recycle request failed",
			err,
		)
	}
	for {
		payload, err := tunnel.ReadPayload(normalizedContext)
		if err != nil {
			return pb.TunnelRecycleAck{}, ltfperrors.WrapTunnelRecycleError(
				ltfperrors.CodeTunnelRecycleTunnelUnhealthy,
				"wait tunnel recycle ack failed",
				err,
			)
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
			return pb.TunnelRecycleAck{}, ltfperrors.NewTunnelRecycleError(
				ltfperrors.CodeTunnelRecycleInvalidSeq,
				fmt.Sprintf(
					"recycle ack seq mismatch: got=%d want=%d",
					ack.RecycleSeq,
					recycleSeq,
				),
			)
		}
		if !ack.Accepted {
			recycleErrorCode := ltfperrors.NormalizeTunnelRecycleCodeOrDefault(
				strings.TrimSpace(ack.ErrorCode),
				ltfperrors.CodeTunnelRecycleTunnelUnhealthy,
			)
			ack.ErrorCode = recycleErrorCode
			return ack, ltfperrors.NewTunnelRecycleError(
				recycleErrorCode,
				normalizeTunnelRecycleAckErrorMessage(
					recycleErrorCode,
					strings.TrimSpace(ack.ErrorMessage),
				),
			)
		}
		return ack, nil
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
