package traffic

import (
	"context"
	"io"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// PayloadWriter 定义数据面消息写出能力。
type PayloadWriter interface {
	// WritePayload 写出一条 StreamPayload。
	WritePayload(ctx context.Context, payload pb.StreamPayload) error
}

// TunnelIO 统一描述 tunnel 的消息读写抽象。
type TunnelIO interface {
	PayloadReader
	PayloadWriter
}

// RelayPump 负责在 tunnel 与 upstream 之间执行 relay。
type RelayPump interface {
	// Relay 启动双向 relay，返回时表示该 traffic 已终止。
	Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error
}

// RelayFunc 允许使用函数形式实现 RelayPump。
type RelayFunc func(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error

// Relay 以函数方式执行 relay。
func (relay RelayFunc) Relay(ctx context.Context, tunnel TunnelIO, upstream io.ReadWriteCloser, trafficID string) error {
	return relay(ctx, tunnel, upstream, trafficID)
}
