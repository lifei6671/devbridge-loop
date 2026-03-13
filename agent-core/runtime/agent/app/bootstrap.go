package app

import (
	"context"
	"log"
)

// Runtime wires the agent runtime subsystems together.
type Runtime struct {
	cfg Config
}

// Bootstrap prepares the runtime graph. It is intentionally minimal in the skeleton.
func Bootstrap(ctx context.Context, cfg Config) (*Runtime, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	_ = ctx
	return &Runtime{cfg: cfg}, nil
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
