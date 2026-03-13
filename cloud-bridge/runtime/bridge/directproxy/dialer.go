package directproxy

import (
	"context"
	"errors"
	"fmt"
)

var (
	// ErrDialerDependencyMissing 表示 direct dialer 依赖缺失。
	ErrDialerDependencyMissing = errors.New("direct dialer dependency missing")
	// ErrDirectDialFailed 表示 direct path 所有 endpoint 拨号失败。
	ErrDirectDialFailed = errors.New("direct dial failed")
)

// UpstreamConn 描述直连上游连接句柄。
type UpstreamConn interface {
	Close() error
}

// UpstreamDialer 定义底层拨号能力。
type UpstreamDialer interface {
	Dial(ctx context.Context, endpoint ExternalEndpoint) (UpstreamConn, error)
}

// DialResult 描述 direct dial 成功结果。
type DialResult struct {
	Endpoint   ExternalEndpoint
	Connection UpstreamConn
}

// DialerOptions 定义 Dialer 构造参数。
type DialerOptions struct {
	Upstream UpstreamDialer
}

// Dialer establishes upstream connections for direct proxy.
type Dialer struct {
	upstream UpstreamDialer
}

// NewDialer 创建 direct path 拨号器。
func NewDialer(options DialerOptions) (*Dialer, error) {
	if options.Upstream == nil {
		return nil, ErrDialerDependencyMissing
	}
	return &Dialer{
		upstream: options.Upstream,
	}, nil
}

// Dial 依次尝试 endpoint 列表，直到拨号成功。
func (dialer *Dialer) Dial(ctx context.Context, endpoints []ExternalEndpoint) (DialResult, error) {
	if dialer == nil || dialer.upstream == nil {
		return DialResult{}, ErrDialerDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	if len(endpoints) == 0 {
		return DialResult{}, fmt.Errorf("direct dial: %w: empty endpoint list", ErrDirectDialFailed)
	}
	var dialError error
	for _, endpoint := range endpoints {
		connection, err := dialer.upstream.Dial(normalizedContext, endpoint)
		if err != nil {
			dialError = errors.Join(dialError, err)
			continue
		}
		return DialResult{
			Endpoint:   endpoint,
			Connection: connection,
		}, nil
	}
	if dialError == nil {
		dialError = ErrDirectDialFailed
	}
	return DialResult{}, fmt.Errorf("direct dial: %w", errors.Join(ErrDirectDialFailed, dialError))
}
