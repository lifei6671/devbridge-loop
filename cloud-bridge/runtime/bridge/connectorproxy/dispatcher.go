package connectorproxy

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrDispatcherDependencyMissing 表示 Dispatcher 关键依赖缺失。
	ErrDispatcherDependencyMissing = errors.New("dispatcher dependency missing")
)

// DispatchRequest 描述 connector path 执行请求。
type DispatchRequest struct {
	ConnectorID string
	TrafficOpen pb.TrafficOpen
}

// DispatchResult 描述 connector path 执行结果。
type DispatchResult struct {
	TunnelID   string
	OpenAck    *pb.TrafficOpenAck
	HTTPStatus int
	ErrorCode  string
}

// DispatcherOptions 定义 Dispatcher 构造参数。
type DispatcherOptions struct {
	TunnelAcquirer *TunnelAcquirer
	OpenHandshake  *OpenHandshake
	Relay          RelayPump
	TunnelRegistry *registry.TunnelRegistry
	FailureMapper  *FailureMapper
	Now            func() time.Time
}

// Dispatcher coordinates connector proxy execution.
type Dispatcher struct {
	tunnelAcquirer *TunnelAcquirer
	openHandshake  *OpenHandshake
	relay          RelayPump
	tunnelRegistry *registry.TunnelRegistry
	failureMapper  *FailureMapper
	now            func() time.Time
}

// NewDispatcher 创建 connector proxy dispatcher。
func NewDispatcher(options DispatcherOptions) (*Dispatcher, error) {
	if options.TunnelAcquirer == nil || options.OpenHandshake == nil || options.TunnelRegistry == nil {
		return nil, ErrDispatcherDependencyMissing
	}
	relay := options.Relay
	if relay == nil {
		relay = NewRelay()
	}
	failureMapper := options.FailureMapper
	if failureMapper == nil {
		failureMapper = NewFailureMapper()
	}
	nowFunction := options.Now
	if nowFunction == nil {
		nowFunction = func() time.Time { return time.Now().UTC() }
	}
	return &Dispatcher{
		tunnelAcquirer: options.TunnelAcquirer,
		openHandshake:  options.OpenHandshake,
		relay:          relay,
		tunnelRegistry: options.TunnelRegistry,
		failureMapper:  failureMapper,
		now:            nowFunction,
	}, nil
}

// Dispatch 执行 connector path 主链路：
// acquire idle tunnel -> mark active -> open handshake -> relay -> close and recycle。
func (dispatcher *Dispatcher) Dispatch(ctx context.Context, request DispatchRequest) (DispatchResult, error) {
	if dispatcher == nil {
		return DispatchResult{}, ErrDispatcherDependencyMissing
	}
	normalizedConnectorID := strings.TrimSpace(request.ConnectorID)
	if normalizedConnectorID == "" {
		return DispatchResult{}, ErrDispatcherDependencyMissing
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}

	acquiredTunnel, err := dispatcher.tunnelAcquirer.AcquireIdleTunnel(normalizedContext, normalizedConnectorID)
	if err != nil {
		mappedFailure := dispatcher.failureMapper.Map(err)
		return DispatchResult{
			HTTPStatus: mappedFailure.HTTPStatus,
			ErrorCode:  mappedFailure.Code,
		}, err
	}

	result := DispatchResult{
		TunnelID:   acquiredTunnel.TunnelID,
		HTTPStatus: 200,
	}
	trafficID := strings.TrimSpace(request.TrafficOpen.TrafficID)
	if err := dispatcher.tunnelRegistry.MarkActive(dispatcher.now(), acquiredTunnel.TunnelID, trafficID); err != nil {
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}

	openAck, err := dispatcher.openHandshake.Execute(normalizedContext, acquiredTunnel.Tunnel, request.TrafficOpen)
	if err != nil {
		var openRejectedError *OpenRejectedError
		if errors.As(err, &openRejectedError) {
			ack := openRejectedError.Ack
			result.OpenAck = &ack
		}
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}
	result.OpenAck = &openAck

	if err := dispatcher.relay.Relay(normalizedContext, acquiredTunnel.Tunnel, request.TrafficOpen.TrafficID); err != nil {
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}
	if err := dispatcher.recycleTunnelClosed(acquiredTunnel); err != nil {
		return dispatcher.failResult(result, err)
	}
	return result, nil
}

func (dispatcher *Dispatcher) failResult(result DispatchResult, dispatchError error) (DispatchResult, error) {
	mappedFailure := dispatcher.failureMapper.Map(dispatchError)
	result.HTTPStatus = mappedFailure.HTTPStatus
	result.ErrorCode = mappedFailure.Code
	return result, dispatchError
}

func (dispatcher *Dispatcher) recycleTunnelClosed(runtime registry.TunnelRuntime) error {
	if runtime.Tunnel == nil {
		return ErrDispatcherDependencyMissing
	}
	closeErr := runtime.Tunnel.Close()
	markClosedErr := dispatcher.tunnelRegistry.MarkClosed(dispatcher.now(), runtime.TunnelID)
	_, removeErr := dispatcher.tunnelRegistry.RemoveTerminal(runtime.TunnelID)
	if closeErr != nil || markClosedErr != nil || removeErr != nil {
		return fmt.Errorf(
			"recycle tunnel closed: %w",
			errors.Join(closeErr, markClosedErr, removeErr),
		)
	}
	return nil
}

func (dispatcher *Dispatcher) recycleTunnelBroken(runtime registry.TunnelRuntime, reason string) error {
	if runtime.Tunnel == nil {
		return ErrDispatcherDependencyMissing
	}
	closeErr := runtime.Tunnel.Close()
	markBrokenErr := dispatcher.tunnelRegistry.MarkBroken(dispatcher.now(), runtime.TunnelID, reason)
	_, removeErr := dispatcher.tunnelRegistry.RemoveTerminal(runtime.TunnelID)
	if closeErr != nil || markBrokenErr != nil || removeErr != nil {
		return fmt.Errorf(
			"recycle tunnel broken: %w",
			errors.Join(closeErr, markBrokenErr, removeErr),
		)
	}
	return nil
}
