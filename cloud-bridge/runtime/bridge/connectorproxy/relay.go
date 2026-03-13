package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

var (
	// ErrRelayDependencyMissing 表示 relay 关键依赖缺失。
	ErrRelayDependencyMissing = errors.New("relay dependency missing")
	// ErrRelayReset 表示 relay 过程收到 reset。
	ErrRelayReset = errors.New("relay reset")
)

// RelayResetError 描述 relay 收到 reset 时的结构化详情。
type RelayResetError struct {
	TrafficID    string
	ResetCode    string
	ResetMessage string
}

// Error 返回 reset 错误的结构化文本。
func (relayResetError *RelayResetError) Error() string {
	if relayResetError == nil {
		return ErrRelayReset.Error()
	}
	return fmt.Sprintf(
		"relay reset: traffic_id=%s error_code=%s message=%s",
		strings.TrimSpace(relayResetError.TrafficID),
		strings.TrimSpace(relayResetError.ResetCode),
		strings.TrimSpace(relayResetError.ResetMessage),
	)
}

// Unwrap 暴露基础 reset 错误类型，保持 errors.Is 行为一致。
func (relayResetError *RelayResetError) Unwrap() error {
	return ErrRelayReset
}

// RelayPump 定义 connector path relay 抽象。
type RelayPump interface {
	Relay(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) error
}

// RelayFunc 允许函数式实现 RelayPump。
type RelayFunc func(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) error

// Relay 调用函数本体执行 relay。
func (relay RelayFunc) Relay(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) error {
	return relay(ctx, tunnel, trafficID)
}

// Relay pumps framed data between client and tunnel.
type Relay struct{}

// NewRelay 创建默认 relay 执行器。
func NewRelay() *Relay {
	return &Relay{}
}

// Relay 等待 traffic close 或 reset 结束当前 connector path。
func (relay *Relay) Relay(ctx context.Context, tunnel registry.RuntimeTunnel, trafficID string) error {
	_ = relay
	if tunnel == nil {
		return ErrRelayDependencyMissing
	}
	normalizedTrafficID := strings.TrimSpace(trafficID)
	if normalizedTrafficID == "" {
		return ErrRelayDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}

	for {
		payload, err := tunnel.ReadPayload(normalizedContext)
		if err != nil {
			return fmt.Errorf("relay: read payload: %w", err)
		}
		if payload.Close != nil && strings.TrimSpace(payload.Close.TrafficID) == normalizedTrafficID {
			return nil
		}
		if payload.Reset != nil && strings.TrimSpace(payload.Reset.TrafficID) == normalizedTrafficID {
			return &RelayResetError{
				TrafficID:    normalizedTrafficID,
				ResetCode:    strings.TrimSpace(payload.Reset.ErrorCode),
				ResetMessage: strings.TrimSpace(payload.Reset.ErrorMessage),
			}
		}
	}
}
