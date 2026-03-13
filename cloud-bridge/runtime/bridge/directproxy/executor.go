package directproxy

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
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
	Endpoint   ExternalEndpoint
	FromCache  bool
	UsedStale  bool
	HTTPStatus int
	ErrorCode  string
}

// ExecutorOptions 定义 direct executor 构造参数。
type ExecutorOptions struct {
	Discovery     *DiscoveryAdapter
	Cache         *EndpointCache
	Dialer        *Dialer
	Relay         RelayPump
	FailureMapper *FailureMapper
}

// Executor 负责 external_service 路径：
// discovery -> endpoint cache -> direct dial -> relay。
type Executor struct {
	discovery     *DiscoveryAdapter
	cache         *EndpointCache
	dialer        *Dialer
	relay         RelayPump
	failureMapper *FailureMapper
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
	failureMapper := options.FailureMapper
	if failureMapper == nil {
		failureMapper = NewFailureMapper()
	}
	return &Executor{
		discovery:     options.Discovery,
		cache:         cache,
		dialer:        options.Dialer,
		relay:         options.Relay,
		failureMapper: failureMapper,
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
	baseLogFields := obs.LogFields{
		TrafficID: strings.TrimSpace(request.TrafficID),
		ServiceID: strings.TrimSpace(request.Target.ServiceName),
	}

	cacheLookup := executor.cache.Get(request.Target)
	candidates := cacheLookup.Endpoints
	fromCache := cacheLookup.Hit
	usedStale := cacheLookup.Stale && len(cacheLookup.Endpoints) > 0

	if len(candidates) == 0 {
		discovered, discoveryErr := executor.discovery.Discover(normalizedContext, request.Target)
		if discoveryErr != nil {
			mappedFailure := executor.failureMapper.Map(discoveryErr, FailureKindDiscoveryMiss)
			log.Printf(
				"bridge direct execute failed event=discovery_failed http=%d error_code=%s %s err=%v",
				mappedFailure.HTTPStatus,
				mappedFailure.Code,
				obs.FormatLogFields(baseLogFields),
				discoveryErr,
			)
			return ExecuteResult{
				HTTPStatus: mappedFailure.HTTPStatus,
				ErrorCode:  mappedFailure.Code,
			}, fmt.Errorf("direct execute: discovery: %w", discoveryErr)
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
		} else if discoveryErr != nil {
			// stale 刷新失败时保留旧 endpoint，同时输出统一错误码便于诊断。
			mappedFailure := executor.failureMapper.Map(discoveryErr, FailureKindDiscoveryRefresh)
			log.Printf(
				"bridge direct execute degraded event=discovery_refresh_failed http=%d error_code=%s stale_fallback=true %s err=%v",
				mappedFailure.HTTPStatus,
				mappedFailure.Code,
				obs.FormatLogFields(baseLogFields),
				discoveryErr,
			)
		}
	}

	dialResult, err := executor.dialer.Dial(normalizedContext, candidates)
	if err != nil {
		mappedFailure := executor.failureMapper.Map(err, FailureKindDirectDial)
		log.Printf(
			"bridge direct execute failed event=dial_failed http=%d error_code=%s %s err=%v",
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			obs.FormatLogFields(baseLogFields),
			err,
		)
		return ExecuteResult{
			HTTPStatus: mappedFailure.HTTPStatus,
			ErrorCode:  mappedFailure.Code,
		}, fmt.Errorf("direct execute: dial: %w", err)
	}
	if err := executor.relay.Relay(normalizedContext, dialResult.Connection, request.TrafficID); err != nil {
		closeErr := error(nil)
		if dialResult.Connection != nil {
			closeErr = dialResult.Connection.Close()
		}
		joinedErr := errors.Join(err, closeErr)
		mappedFailure := executor.failureMapper.Map(joinedErr, FailureKindRelay)
		log.Printf(
			"bridge direct execute failed event=relay_failed http=%d error_code=%s %s err=%v",
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          strings.TrimSpace(request.TrafficID),
				ServiceID:          strings.TrimSpace(request.Target.ServiceName),
				ActualEndpointID:   strings.TrimSpace(dialResult.Endpoint.EndpointID),
				ActualEndpointAddr: strings.TrimSpace(dialResult.Endpoint.Address),
			}),
			err,
		)
		return ExecuteResult{
			HTTPStatus: mappedFailure.HTTPStatus,
			ErrorCode:  mappedFailure.Code,
		}, fmt.Errorf("direct execute: relay: %w", joinedErr)
	}
	if dialResult.Connection != nil {
		if closeErr := dialResult.Connection.Close(); closeErr != nil {
			mappedFailure := executor.failureMapper.Map(closeErr, FailureKindClose)
			log.Printf(
				"bridge direct execute failed event=close_upstream_failed http=%d error_code=%s %s err=%v",
				mappedFailure.HTTPStatus,
				mappedFailure.Code,
				obs.FormatLogFields(obs.LogFields{
					TrafficID:          strings.TrimSpace(request.TrafficID),
					ServiceID:          strings.TrimSpace(request.Target.ServiceName),
					ActualEndpointID:   strings.TrimSpace(dialResult.Endpoint.EndpointID),
					ActualEndpointAddr: strings.TrimSpace(dialResult.Endpoint.Address),
				}),
				closeErr,
			)
			return ExecuteResult{
				HTTPStatus: mappedFailure.HTTPStatus,
				ErrorCode:  mappedFailure.Code,
			}, fmt.Errorf("direct execute: close upstream: %w", closeErr)
		}
	}
	return ExecuteResult{
		Endpoint:   dialResult.Endpoint,
		FromCache:  fromCache,
		UsedStale:  usedStale,
		HTTPStatus: 200,
	}, nil
}
