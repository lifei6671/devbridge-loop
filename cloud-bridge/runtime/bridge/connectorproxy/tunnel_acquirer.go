package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
)

var (
	// ErrTunnelAcquirerDependencyMissing 表示 TunnelAcquirer 依赖未满足。
	ErrTunnelAcquirerDependencyMissing = errors.New("tunnel acquirer dependency missing")
	// ErrNoTunnel 表示当前没有可分配 tunnel。
	ErrNoTunnel = errors.New("no available tunnel")
	// ErrNoIdleTunnel 表示在等待窗口内没有可分配的 idle tunnel。
	ErrNoIdleTunnel = errors.New("no idle tunnel")
	// ErrTunnelStale 表示分配阶段探活失败，拿到的是僵尸 tunnel。
	ErrTunnelStale = errors.New("tunnel stale")
)

const (
	// AcquireRefillReason 表示 acquire 触发的补池原因标签。
	AcquireRefillReason = "acquire_timeout"
	// defaultMaxAcquireRetry 表示分配阶段默认的 stale tunnel 重试次数。
	defaultMaxAcquireRetry = 3
)

// RefillRequester 定义 no-idle 时的补池触发能力。
type RefillRequester interface {
	RequestRefill(connectorID string, reason string)
}

// TunnelAcquirerOptions 定义 TunnelAcquirer 构造参数。
type TunnelAcquirerOptions struct {
	Registry        *registry.TunnelRegistry
	Refill          RefillRequester
	WaitHint        time.Duration
	PollInterval    time.Duration
	MaxAcquireRetry int
	Now             func() time.Time
	Metrics         *obs.Metrics
	RefillReason    string
	EnableNoIdleWT  bool
}

// TunnelAcquirer allocates idle tunnels for traffic.
type TunnelAcquirer struct {
	registry        *registry.TunnelRegistry
	refill          RefillRequester
	waitHint        time.Duration
	pollInterval    time.Duration
	maxAcquireRetry int
	now             func() time.Time
	metrics         *obs.Metrics
	refillReason    string
	enableNoIdleWT  bool
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
	maxAcquireRetry := options.MaxAcquireRetry
	if maxAcquireRetry <= 0 {
		maxAcquireRetry = defaultMaxAcquireRetry
	}
	enableNoIdleWT := options.EnableNoIdleWT
	if !enableNoIdleWT && waitHint > 0 {
		enableNoIdleWT = true
	}
	return &TunnelAcquirer{
		registry:        options.Registry,
		refill:          options.Refill,
		waitHint:        waitHint,
		pollInterval:    pollInterval,
		maxAcquireRetry: maxAcquireRetry,
		now:             nowFunction,
		metrics:         normalizeBridgeMetrics(options.Metrics),
		refillReason:    refillReason,
		enableNoIdleWT:  enableNoIdleWT,
	}, nil
}

// AcquireIdleTunnel 为指定 connector 分配一条 idle tunnel。
func (acquirer *TunnelAcquirer) AcquireIdleTunnel(ctx context.Context, connectorID string) (registry.TunnelRuntime, error) {
	acquireStartedAt := time.Now()
	defer acquirer.observeAcquireWait(acquireStartedAt)
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

	var staleFailure error
	for attempt := 0; attempt < acquirer.maxAcquireRetry; attempt++ {
		acquired, acquireErr := acquirer.acquireIdleOrWait(normalizedContext, normalizedConnectorID)
		if acquireErr != nil {
			if staleFailure != nil {
				return registry.TunnelRuntime{}, fmt.Errorf(
					"acquire idle tunnel: %w: connector_id=%s",
					errors.Join(acquireErr, staleFailure),
					normalizedConnectorID,
				)
			}
			return registry.TunnelRuntime{}, acquireErr
		}
		probeErr := acquirer.probeAcquiredTunnel(normalizedContext, acquired)
		if probeErr == nil {
			return acquired, nil
		}
		if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
			return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w", probeErr)
		}
		staleFailure = fmt.Errorf(
			"%w: connector_id=%s tunnel_id=%s cause=%v",
			ErrTunnelStale,
			normalizedConnectorID,
			acquired.TunnelID,
			probeErr,
		)
		if acquirer.refill != nil {
			acquirer.refill.RequestRefill(normalizedConnectorID, acquirer.refillReason)
		}
		if recycleErr := acquirer.recycleStaleTunnel(acquired, probeErr); recycleErr != nil {
			return registry.TunnelRuntime{}, fmt.Errorf(
				"acquire idle tunnel: recycle stale tunnel: %w",
				errors.Join(staleFailure, recycleErr),
			)
		}
	}
	return registry.TunnelRuntime{}, fmt.Errorf(
		"acquire idle tunnel: %w: connector_id=%s",
		errors.Join(ErrNoTunnel, ErrNoIdleTunnel, staleFailure),
		normalizedConnectorID,
	)
}

func (acquirer *TunnelAcquirer) acquireIdleOrWait(
	ctx context.Context,
	connectorID string,
) (registry.TunnelRuntime, error) {
	acquired, ok := acquirer.registry.AcquireIdle(acquirer.now(), connectorID)
	if ok {
		return acquired, nil
	}
	if acquirer.refill != nil {
		acquirer.refill.RequestRefill(connectorID, acquirer.refillReason)
	}
	if !acquirer.enableNoIdleWT || acquirer.waitHint == 0 {
		log.Printf(
			"bridge acquire idle failed event=no_idle_tunnel %s",
			obs.FormatLogFields(obs.LogFields{ConnectorID: connectorID}),
		)
		return registry.TunnelRuntime{}, fmt.Errorf(
			"acquire idle tunnel: %w: connector_id=%s",
			errors.Join(ErrNoIdleTunnel, ErrNoTunnel),
			connectorID,
		)
	}

	deadline := acquirer.now().Add(acquirer.waitHint)
	for {
		if ctx.Err() != nil {
			return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w", ctx.Err())
		}
		acquired, ok := acquirer.registry.AcquireIdle(acquirer.now(), connectorID)
		if ok {
			return acquired, nil
		}

		remaining := deadline.Sub(acquirer.now())
		if remaining <= 0 {
			log.Printf(
				"bridge acquire idle timeout event=acquire_wait_timeout %s",
				obs.FormatLogFields(obs.LogFields{ConnectorID: connectorID}),
			)
			return registry.TunnelRuntime{}, fmt.Errorf(
				"acquire idle tunnel: %w: connector_id=%s wait_hint=%s",
				errors.Join(ErrNoIdleTunnel, ErrNoTunnel),
				connectorID,
				acquirer.waitHint,
			)
		}
		sleepInterval := acquirer.pollInterval
		if remaining < sleepInterval {
			sleepInterval = remaining
		}
		timer := time.NewTimer(sleepInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return registry.TunnelRuntime{}, fmt.Errorf("acquire idle tunnel: %w", ctx.Err())
		case <-timer.C:
		}
	}
}

func (acquirer *TunnelAcquirer) probeAcquiredTunnel(ctx context.Context, runtime registry.TunnelRuntime) error {
	if runtime.Tunnel == nil {
		return ErrTunnelAcquirerDependencyMissing
	}
	prober, supportsProbe := runtime.Tunnel.(registry.RuntimeTunnelHealthProber)
	if !supportsProbe {
		return nil
	}
	probeErr := prober.Probe(ctx)
	if probeErr == nil {
		return nil
	}
	if errors.Is(probeErr, transport.ErrUnsupported) {
		return nil
	}
	if errors.Is(probeErr, context.Canceled) || errors.Is(probeErr, context.DeadlineExceeded) {
		return probeErr
	}
	return probeErr
}

func (acquirer *TunnelAcquirer) recycleStaleTunnel(runtime registry.TunnelRuntime, probeErr error) error {
	if runtime.Tunnel == nil {
		return ErrTunnelAcquirerDependencyMissing
	}
	closeErr := runtime.Tunnel.Close()
	reason := strings.TrimSpace(fmt.Sprintf("%v", probeErr))
	if reason == "" {
		reason = ErrTunnelStale.Error()
	}
	markBrokenErr := acquirer.registry.MarkBroken(acquirer.now(), runtime.TunnelID, reason)
	_, removeErr := acquirer.registry.RemoveTerminal(runtime.TunnelID)
	if closeErr != nil || markBrokenErr != nil || removeErr != nil {
		return errors.Join(closeErr, markBrokenErr, removeErr)
	}
	return nil
}

// observeAcquireWait 记录一次 acquire idle tunnel 的总耗时。
func (acquirer *TunnelAcquirer) observeAcquireWait(startedAt time.Time) {
	if acquirer == nil || acquirer.metrics == nil {
		return
	}
	// acquire 等待时长统一按调用入口到返回时刻计算。
	acquirer.metrics.ObserveBridgeTunnelAcquireWait(time.Since(startedAt).Milliseconds())
}

// normalizeBridgeMetrics 归一化 Bridge 指标依赖，未注入时回落默认指标容器。
func normalizeBridgeMetrics(metrics *obs.Metrics) *obs.Metrics {
	if metrics == nil {
		return obs.DefaultMetrics
	}
	return metrics
}
