package session

import "context"

// Authenticator 定义 session 鉴权能力。
type Authenticator interface {
	// Authenticate 执行鉴权握手。
	Authenticate(ctx context.Context) error
}

// NoopAuthenticator 是占位实现，不执行实际鉴权。
type NoopAuthenticator struct{}

// Authenticate 直接返回成功，用于骨架阶段。
func (NoopAuthenticator) Authenticate(ctx context.Context) error {
	_ = ctx // skeleton: 暂不使用 ctx
	return nil
}
