package app

import (
	"context"
	"log"
)

// BootstrapOptions 定义 runtime 初始化时的可选覆盖项。
type BootstrapOptions struct {
	// TunnelPoolOverride 允许外部按字段覆盖 tunnelPool 参数。
	// 首版仅在 Bootstrap 时生效，不支持运行时热更新。
	TunnelPoolOverride *TunnelPoolOverride
}

// Runtime wires the agent runtime subsystems together.
type Runtime struct {
	cfg Config
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	return BootstrapWithOptions(ctx, cfg, BootstrapOptions{})
}

// BootstrapWithOptions 在基础配置之上应用初始化覆盖参数并完成校验。
func BootstrapWithOptions(ctx context.Context, cfg Config, options BootstrapOptions) (*Runtime, error) {
	resolvedConfig := cfg
	if options.TunnelPoolOverride != nil {
		// 仅覆盖显式传入字段，未传字段保持原配置（通常来自默认值）。
		resolvedConfig = resolvedConfig.ApplyTunnelPoolOverride(*options.TunnelPoolOverride)
	}
	if err := resolvedConfig.Validate(); err != nil {
		return nil, err
	}
	_ = ctx
	return &Runtime{cfg: resolvedConfig}, nil
}

// Run starts the runtime. In the skeleton it blocks on context cancellation.
func (r *Runtime) Run(ctx context.Context) error {
	log.Printf("agent runtime starting agent_id=%s bridge_addr=%s", r.cfg.AgentID, r.cfg.BridgeAddr)
	<-ctx.Done()
	return ctx.Err()
}

// Shutdown allows graceful teardown.
func (r *Runtime) Shutdown(ctx context.Context) error {
	_ = ctx
	return nil
}
