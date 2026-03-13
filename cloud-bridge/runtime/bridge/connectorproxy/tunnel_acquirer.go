package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

var (
	// ErrTunnelAcquirerDependencyMissing 表示 TunnelAcquirer 依赖未满足。
	ErrTunnelAcquirerDependencyMissing = errors.New("tunnel acquirer dependency missing")
	// ErrNoIdleTunnel 表示在等待窗口内没有可分配的 idle tunnel。
	ErrNoIdleTunnel = errors.New("no idle tunnel")
)

const (
	// AcquireRefillReason 表示 acquire 触发的补池原因标签。
	AcquireRefillReason = "acquire_timeout"
)

// RefillRequester 定义 no-idle 时的补池触发能力。
type RefillRequester interface {
	RequestRefill(connectorID string, reason string)
}

// TunnelAcquirerOptions 定义 TunnelAcquirer 构造参数。
type TunnelAcquirerOptions struct {
	Registry       *registry.TunnelRegistry
	Refill         RefillRequester
	WaitHint       time.Duration
	PollInterval   time.Duration
	Now            func() time.Time
	RefillReason   string
	EnableNoIdleWT bool
}

// TunnelAcquirer allocates idle tunnels for traffic.
type TunnelAcquirer struct {
	registry       *registry.TunnelRegistry
	refill         RefillRequester
	waitHint       time.Duration
	pollInterval   time.Duration
	now            func() time.Time
	refillReason   string
	enableNoIdleWT bool
}

// NewTunnelAcquirer 创建 tunnel 分配器。
func NewTunnelAcquirer(options TunnelAcquirerOptions) (*TunnelAcquirer, error) {
	if options.Registry == nil {
		return nil, ErrTunnelAcquirerDependencyMissing
	}
	nowFunction := options.Now
	if nowFunction == nil {
		nowFunction = func() time.Time { return time.Now().UTC() }
	}
	pollInterval := options.PollInterval
	if pollInterval <= 0 {
		pollInterval = 10 * time.Millisecond
	}
	refillReason := strings.TrimSpace(options.RefillReason)
	if refillReason == "" {
		refillReason = AcquireRefillReason
	}
	waitHint := options.WaitHint
	if waitHint < 0 {
		waitHint = 0
	}
	enableNoIdleWT := options.EnableNoIdleWT
	if !enableNoIdleWT && waitHint > 0 {
		enableNoIdleWT = true
	}
	return &TunnelAcquirer{
		registry:       options.Registry,
		refill:         options.Refill,
		waitHint:       waitHint,
		pollInterval:   pollInterval,
		now:            nowFunction,
		refillReason:   refillReason,
		enableNoIdleWT: enableNoIdleWT,
	}, nil
}

// AcquireIdleTunnel 为指定 connector 分配一条 idle tunnel。
func (acquirer *TunnelAcquirer) AcquireIdleTunnel(ctx context.Context, connectorID string) (registry.TunnelRuntime, error) {
	if acquirer == nil || acquirer.registry == nil {
		return registry.TunnelRuntime{}, ErrTunnelAcquirerDependencyMissing
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return registry.TunnelRuntime{}, ErrTunnelAcquirerDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}

	acquired, ok := acquirer.registry.AcquireIdle(acquirer.now(), normalizedConnectorID)
	if ok {
		return acquired, nil
	}
	if acquirer.refill != nil {
		acquirer.refill.RequestRefill(normalizedConnectorID, acquirer.refillReason)
	}
	if !acquirer.enableNoIdleWT || acquirer.waitHint == 0 {
		return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w: connector_id=%s", ErrNoIdleTunnel, normalizedConnectorID)
	}

	deadline := acquirer.now().Add(acquirer.waitHint)
	for {
		if normalizedContext.Err() != nil {
			return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w", normalizedContext.Err())
		}
		acquired, ok := acquirer.registry.AcquireIdle(acquirer.now(), normalizedConnectorID)
		if ok {
			return acquired, nil
		}

		remaining := deadline.Sub(acquirer.now())
		if remaining <= 0 {
			return registry.TunnelRuntime{}, fmt.Errorf(
				"acquire idle tunnel: %w: connector_id=%s wait_hint=%s",
				ErrNoIdleTunnel,
				normalizedConnectorID,
				acquirer.waitHint,
			)
		}
		sleepInterval := acquirer.pollInterval
		if remaining < sleepInterval {
			sleepInterval = remaining
		}
		timer := time.NewTimer(sleepInterval)
		select {
		case <-normalizedContext.Done():
			timer.Stop()
			return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w", normalizedContext.Err())
		case <-timer.C:
		}
	}
}
