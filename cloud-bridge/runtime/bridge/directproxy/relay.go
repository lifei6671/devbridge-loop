package directproxy

import (
	"context"
	"errors"
)

var (
	// ErrDirectRelayDependencyMissing 表示 direct relay 依赖缺失。
	ErrDirectRelayDependencyMissing = errors.New("direct relay dependency missing")
)

// RelayPump 定义 direct path relay 行为。
type RelayPump interface {
	Relay(ctx context.Context, connection UpstreamConn, trafficID string) error
}

// RelayFunc 允许直接用函数注入 relay 行为。
type RelayFunc func(ctx context.Context, connection UpstreamConn, trafficID string) error

// Relay 执行函数式 relay。
func (relayFunc RelayFunc) Relay(ctx context.Context, connection UpstreamConn, trafficID string) error {
	if relayFunc == nil {
		return ErrDirectRelayDependencyMissing
	}
	return relayFunc(ctx, connection, trafficID)
}

// Relay pumps framed data between client and external upstream.
type Relay struct{}

// NewRelay 创建 direct path 默认 relay。
func NewRelay() *Relay {
	return &Relay{}
}

// Relay 执行默认 relay；骨架阶段保持可替换实现。
func (relay *Relay) Relay(ctx context.Context, connection UpstreamConn, trafficID string) error {
	_ = relay
	_ = trafficID
	if connection == nil {
		return ErrDirectRelayDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	select {
	case <-normalizedContext.Done():
		return normalizedContext.Err()
	default:
		return nil
	}
}
