package directproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrExecutorDependencyMissing 表示 direct executor 依赖缺失。
	ErrExecutorDependencyMissing = errors.New("direct executor dependency missing")
)

// ExecuteRequest 描述 external_service 执行请求。
type ExecuteRequest struct {
	TrafficID string
	Target    pb.ExternalServiceTarget
}

// ExecuteResult 描述 external_service 执行结果。
type ExecuteResult struct {
	Endpoint  ExternalEndpoint
	FromCache bool
	UsedStale bool
}

// ExecutorOptions 定义 direct executor 构造参数。
type ExecutorOptions struct {
	Discovery *DiscoveryAdapter
	Cache     *EndpointCache
	Dialer    *Dialer
	Relay     RelayPump
}

// Executor 负责 external_service 路径：
// discovery -> endpoint cache -> direct dial -> relay。
type Executor struct {
	discovery *DiscoveryAdapter
	cache     *EndpointCache
	dialer    *Dialer
	relay     RelayPump
}

// NewExecutor 创建 direct proxy executor。
func NewExecutor(options ExecutorOptions) (*Executor, error) {
	if options.Discovery == nil || options.Dialer == nil || options.Relay == nil {
		return nil, ErrExecutorDependencyMissing
	}
	cache := options.Cache
	if cache == nil {
		cache = NewEndpointCache(EndpointCacheOptions{})
	}
	return &Executor{
		discovery: options.Discovery,
		cache:     cache,
		dialer:    options.Dialer,
		relay:     options.Relay,
	}, nil
}

// Execute 执行 external_service 主链路。
func (executor *Executor) Execute(ctx context.Context, request ExecuteRequest) (ExecuteResult, error) {
	if executor == nil || executor.discovery == nil || executor.dialer == nil || executor.relay == nil {
		return ExecuteResult{}, ErrExecutorDependencyMissing
	}
	if strings.TrimSpace(request.TrafficID) == "" {
		return ExecuteResult{}, ltfperrors.New(
			ltfperrors.CodeMissingRequiredField,
			"traffic_id is required for direct executor",
		)
	}
	if strings.TrimSpace(request.Target.ServiceName) == "" {
		return ExecuteResult{}, ltfperrors.New(
			ltfperrors.CodeMissingRequiredField,
			"external_service.service_name is required",
		)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}

	cacheLookup := executor.cache.Get(request.Target)
	candidates := cacheLookup.Endpoints
	fromCache := cacheLookup.Hit
	usedStale := cacheLookup.Stale && len(cacheLookup.Endpoints) > 0

	if len(candidates) == 0 {
		discovered, discoveryErr := executor.discovery.Discover(normalizedContext, request.Target)
		if discoveryErr != nil {
			return ExecuteResult{}, fmt.Errorf("direct execute: discovery: %w", discoveryErr)
		}
		executor.cache.Put(request.Target, discovered)
		candidates = discovered
		fromCache = false
		usedStale = false
	} else if cacheLookup.Stale {
		discovered, discoveryErr := executor.discovery.Discover(normalizedContext, request.Target)
		if discoveryErr == nil && len(discovered) > 0 {
			executor.cache.Put(request.Target, discovered)
			candidates = discovered
			fromCache = false
			usedStale = false
		}
	}

	dialResult, err := executor.dialer.Dial(normalizedContext, candidates)
	if err != nil {
		return ExecuteResult{}, fmt.Errorf("direct execute: dial: %w", err)
	}
	if err := executor.relay.Relay(normalizedContext, dialResult.Connection, request.TrafficID); err != nil {
		closeErr := error(nil)
		if dialResult.Connection != nil {
			closeErr = dialResult.Connection.Close()
		}
		return ExecuteResult{}, fmt.Errorf("direct execute: relay: %w", errors.Join(err, closeErr))
	}
	if dialResult.Connection != nil {
		if closeErr := dialResult.Connection.Close(); closeErr != nil {
			return ExecuteResult{}, fmt.Errorf("direct execute: close upstream: %w", closeErr)
		}
	}
	return ExecuteResult{
		Endpoint:  dialResult.Endpoint,
		FromCache: fromCache,
		UsedStale: usedStale,
	}, nil
}
