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
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

var (
	// ErrDispatcherDependencyMissing 表示 Dispatcher 关键依赖缺失。
	ErrDispatcherDependencyMissing = errors.New("dispatcher dependency missing")
)

const (
	trafficOpenHintEndpointIDKey   = "endpoint_id"
	trafficOpenHintEndpointAddrKey = "endpoint_addr"
	openAckActualEndpointIDKey     = "actual_endpoint_id"
	openAckActualEndpointAddrKey   = "actual_endpoint_addr"
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
	Metrics        *obs.Metrics
	Now            func() time.Time
}

// Dispatcher coordinates connector proxy execution.
type Dispatcher struct {
	tunnelAcquirer *TunnelAcquirer
	openHandshake  *OpenHandshake
	relay          RelayPump
	tunnelRegistry *registry.TunnelRegistry
	failureMapper  *FailureMapper
	metrics        *obs.Metrics
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
		metrics:        normalizeBridgeMetrics(options.Metrics),
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
	baseLogFields := obs.LogFields{
		TrafficID:   strings.TrimSpace(request.TrafficOpen.TrafficID),
		ServiceID:   strings.TrimSpace(request.TrafficOpen.ServiceID),
		ConnectorID: normalizedConnectorID,
	}

	acquiredTunnel, err := dispatcher.tunnelAcquirer.AcquireIdleTunnel(normalizedContext, normalizedConnectorID)
	if err != nil {
		mappedFailure := dispatcher.failureMapper.Map(err)
		log.Printf(
			"bridge connector dispatch failed event=acquire_idle_failed %s err=%v",
			obs.FormatLogFields(baseLogFields),
			err,
		)
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
		log.Printf(
			"bridge connector dispatch failed event=mark_tunnel_active_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:   trafficID,
				ServiceID:   strings.TrimSpace(request.TrafficOpen.ServiceID),
				ConnectorID: normalizedConnectorID,
				TunnelID:    strings.TrimSpace(acquiredTunnel.TunnelID),
			}),
			err,
		)
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}

	openAck, err := dispatcher.openHandshake.Execute(normalizedContext, acquiredTunnel.Tunnel, request.TrafficOpen)
	if err != nil {
		dispatcher.observeOpenHandshakeFailure(err)
		var openRejectedError *OpenRejectedError
		if errors.As(err, &openRejectedError) {
			ack := openRejectedError.Ack
			result.OpenAck = &ack
		}
		log.Printf(
			"bridge connector dispatch failed event=open_handshake_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:   trafficID,
				ServiceID:   strings.TrimSpace(request.TrafficOpen.ServiceID),
				ConnectorID: normalizedConnectorID,
				TunnelID:    strings.TrimSpace(acquiredTunnel.TunnelID),
			}),
			err,
		)
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}
	result.OpenAck = &openAck
	if dispatcher.isActualEndpointOverride(request.TrafficOpen, openAck) {
		dispatcher.metrics.IncBridgeActualEndpointOverrideTotal()
	}

	if err := dispatcher.relay.Relay(normalizedContext, acquiredTunnel.Tunnel, request.TrafficOpen.TrafficID); err != nil {
		relayEvent := "relay_failed"
		resetCode := ""
		resetMessage := ""
		var relayResetError *RelayResetError
		if errors.As(err, &relayResetError) {
			// 收到 reset 时单独打标，便于与普通 relay I/O 失败区分。
			relayEvent = "relay_reset_received"
			resetCode = strings.TrimSpace(relayResetError.ResetCode)
			resetMessage = strings.TrimSpace(relayResetError.ResetMessage)
		}
		mappedFailure := dispatcher.failureMapper.Map(err)
		log.Printf(
			"bridge connector dispatch failed event=%s http=%d error_code=%s reset_code=%s reset_message=%s %s err=%v",
			relayEvent,
			mappedFailure.HTTPStatus,
			mappedFailure.Code,
			resetCode,
			resetMessage,
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          trafficID,
				ServiceID:          strings.TrimSpace(request.TrafficOpen.ServiceID),
				ActualEndpointID:   strings.TrimSpace(openAck.Metadata[openAckActualEndpointIDKey]),
				ActualEndpointAddr: strings.TrimSpace(openAck.Metadata[openAckActualEndpointAddrKey]),
				ConnectorID:        normalizedConnectorID,
				TunnelID:           strings.TrimSpace(acquiredTunnel.TunnelID),
			}),
			err,
		)
		recycleErr := dispatcher.recycleTunnelBroken(acquiredTunnel, err.Error())
		return dispatcher.failResult(result, errors.Join(err, recycleErr))
	}
	if err := dispatcher.recycleTunnelClosed(acquiredTunnel); err != nil {
		log.Printf(
			"bridge connector dispatch failed event=recycle_tunnel_closed_failed %s err=%v",
			obs.FormatLogFields(obs.LogFields{
				TrafficID:          trafficID,
				ServiceID:          strings.TrimSpace(request.TrafficOpen.ServiceID),
				ActualEndpointID:   strings.TrimSpace(openAck.Metadata[openAckActualEndpointIDKey]),
				ActualEndpointAddr: strings.TrimSpace(openAck.Metadata[openAckActualEndpointAddrKey]),
				ConnectorID:        normalizedConnectorID,
				TunnelID:           strings.TrimSpace(acquiredTunnel.TunnelID),
			}),
			err,
		)
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

// observeOpenHandshakeFailure 根据握手失败类型更新 Bridge 侧错误指标。
func (dispatcher *Dispatcher) observeOpenHandshakeFailure(dispatchErr error) {
	if dispatcher == nil || dispatcher.metrics == nil {
		return
	}
	if errors.Is(dispatchErr, ErrOpenAckTimeout) {
		// open_ack 超时用于跟踪 pre-open 阶段容量与可用性问题。
		dispatcher.metrics.IncBridgeTrafficOpenTimeoutTotal()
	}
	if errors.Is(dispatchErr, ErrTrafficOpenRejected) && !isConnectorDialFailed(dispatchErr) {
		// open_ack reject 用于统计 agent 显式拒绝比例。
		dispatcher.metrics.IncBridgeTrafficOpenRejectTotal()
	}
}

// isActualEndpointOverride 判断 open_ack 回传 endpoint 是否覆盖了请求 hint。
func (dispatcher *Dispatcher) isActualEndpointOverride(open pb.TrafficOpen, ack pb.TrafficOpenAck) bool {
	if dispatcher == nil {
		return false
	}
	actualEndpointID := strings.TrimSpace(ack.Metadata[openAckActualEndpointIDKey])
	actualEndpointAddr := strings.TrimSpace(ack.Metadata[openAckActualEndpointAddrKey])
	if actualEndpointID == "" && actualEndpointAddr == "" {
		return false
	}
	requestedEndpointID := strings.TrimSpace(open.EndpointSelectionHint[trafficOpenHintEndpointIDKey])
	requestedEndpointAddr := strings.TrimSpace(open.EndpointSelectionHint[trafficOpenHintEndpointAddrKey])
	if requestedEndpointID != "" && actualEndpointID != "" && requestedEndpointID != actualEndpointID {
		return true
	}
	if requestedEndpointAddr != "" && actualEndpointAddr != "" && requestedEndpointAddr != actualEndpointAddr {
		return true
	}
	return false
}
