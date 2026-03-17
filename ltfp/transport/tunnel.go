package transport

import (
	"context"
	"io"
	"time"
)

// Tunnel 定义数据面最小字节流抽象。
type Tunnel interface {
	io.Reader
	io.Writer
	io.Closer

	ID() string
	Meta() TunnelMeta
	State() TunnelState
	BindingInfo() BindingInfo

	CloseWrite() error
	Reset(cause error) error

	SetDeadline(deadline time.Time) error
	SetReadDeadline(deadline time.Time) error
	SetWriteDeadline(deadline time.Time) error

	// Flush 在回收前尝试清理本地读缓存；若存在脏数据应返回错误。
	Flush() error
	// ReuseCount 返回当前 tunnel 已完成的回收轮次（0-based）。
	ReuseCount() int
	// Recyclable 报告当前 tunnel 是否满足回收前提。
	Recyclable() bool

	Done() <-chan struct{}
	Err() error
}

// TunnelHealthProber 定义 tunnel 可选探活能力。
type TunnelHealthProber interface {
	Probe(ctx context.Context) error
}

// TunnelProducer 定义 Agent 侧主动建 tunnel 能力。
type TunnelProducer interface {
	OpenTunnel(ctx context.Context) (Tunnel, error)
}

// TunnelAcceptor 定义 Server 侧接收 tunnel 能力。
type TunnelAcceptor interface {
	AcceptTunnel(ctx context.Context) (Tunnel, error)
}
