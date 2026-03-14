package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"strings"
	"sync"
	"time"

	bridgecontrol "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/control"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
)

const defaultHeartbeatReplyTimeout = 2 * time.Second

// controlMessageDispatcher 负责把控制面业务帧分发给 Bridge 控制处理器。
type controlMessageDispatcher struct {
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	routeRegistry   *registry.RouteRegistry
	tunnelRegistry  *registry.TunnelRegistry
	publishHandler  *bridgecontrol.PublishHandler
	healthHandler   *bridgecontrol.HealthHandler
	tunnelHandler   *bridgecontrol.TunnelReportHandler
	routeHandler    *bridgecontrol.RouteHandler
	sessionHandler  *bridgecontrol.SessionHandler
}

// controlChannelSessionState 保存单条控制连接最近确认的 session 上下文。
type controlChannelSessionState struct {
	sessionID    string
	sessionEpoch uint64
}

// setSession 更新控制连接会话上下文。
func (state *controlChannelSessionState) setSession(sessionID string, sessionEpoch uint64) {
	if state == nil {
		return
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return
	}
	state.sessionID = normalizedSessionID
	state.sessionEpoch = sessionEpoch
}

// controlMessageDispatcherOptions 定义控制面分发器依赖。
type controlMessageDispatcherOptions struct {
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	routeRegistry   *registry.RouteRegistry
	tunnelRegistry  *registry.TunnelRegistry
}

// newControlMessageDispatcher 创建控制面业务分发器及其共享依赖。
func newControlMessageDispatcher(options controlMessageDispatcherOptions) *controlMessageDispatcher {
	sessionRegistry := options.sessionRegistry
	if sessionRegistry == nil {
		// 未注入时回落到本地会话视图，保持兼容路径可运行。
		sessionRegistry = registry.NewSessionRegistry()
	}
	serviceRegistry := options.serviceRegistry
	if serviceRegistry == nil {
		// 未注入时回落到本地服务视图，避免控制面初始化失败。
		serviceRegistry = registry.NewServiceRegistry()
	}
	routeRegistry := options.routeRegistry
	if routeRegistry == nil {
		// 未注入时回落到本地路由视图。
		routeRegistry = registry.NewRouteRegistry()
	}
	tunnelRegistry := options.tunnelRegistry
	if tunnelRegistry == nil {
		// 未注入时回落到本地 tunnel 视图。
		tunnelRegistry = registry.NewTunnelRegistry()
	}
	eventGuard := consistency.NewResourceEventGuard(4096)
	sessionHandler := bridgecontrol.NewSessionHandler(bridgecontrol.SessionHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		RouteRegistry:   routeRegistry,
		Guard:           eventGuard,
	})
	return &controlMessageDispatcher{
		sessionRegistry: sessionRegistry,
		serviceRegistry: serviceRegistry,
		routeRegistry:   routeRegistry,
		tunnelRegistry:  tunnelRegistry,
		publishHandler: bridgecontrol.NewPublishHandler(bridgecontrol.PublishHandlerOptions{
			Guard:           eventGuard,
			SessionRegistry: sessionRegistry,
			ServiceRegistry: serviceRegistry,
		}),
		healthHandler: bridgecontrol.NewHealthHandler(bridgecontrol.HealthHandlerOptions{
			SessionRegistry: sessionRegistry,
			ServiceRegistry: serviceRegistry,
		}),
		tunnelHandler: bridgecontrol.NewTunnelReportHandler(bridgecontrol.TunnelReportHandlerOptions{
			SessionRegistry: sessionRegistry,
		}),
		routeHandler: bridgecontrol.NewRouteHandler(bridgecontrol.RouteHandlerOptions{
			Guard:           eventGuard,
			SessionRegistry: sessionRegistry,
			RouteRegistry:   routeRegistry,
		}),
		sessionHandler: sessionHandler,
	}
}

// handleFrame 处理一条控制帧并返回可选响应帧与发送优先级。
func (dispatcher *controlMessageDispatcher) handleFrame(
	frame transport.ControlFrame,
	sessionState *controlChannelSessionState,
) (*transport.ControlFrame, transport.ControlMessagePriority, error) {
	if frame.Type == transport.ControlFrameTypeHeartbeatPing {
		dispatcher.refreshSessionHeartbeatFromState(time.Now().UTC(), sessionState)
		// 保活帧沿用高优先级快速回 pong。
		replyFrame := &transport.ControlFrame{Type: transport.ControlFrameTypeHeartbeatPong}
		return replyFrame, transport.RecommendControlFramePriority(replyFrame.Type), nil
	}
	if frame.Type == transport.ControlFrameTypeHeartbeatPong {
		dispatcher.refreshSessionHeartbeatFromState(time.Now().UTC(), sessionState)
		// 服务端收到 pong 时无需回包。
		return nil, transport.ControlMessagePriorityNormal, nil
	}
	if _, err := transport.ControlMessageTypeForFrameType(frame.Type); err != nil {
		// 未知帧类型先忽略，避免未来扩展字段导致现网中断。
		return nil, transport.ControlMessagePriorityNormal, nil
	}
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frame)
	if err != nil {
		return nil, transport.ControlMessagePriorityNormal, fmt.Errorf("decode business control frame failed: %w", err)
	}
	if sessionID, sessionEpoch, ok := resolveEnvelopeSession(envelope); ok {
		sessionState.setSession(sessionID, sessionEpoch)
	}
	replyEnvelope, err := dispatcher.dispatchEnvelope(envelope)
	if err != nil {
		return nil, transport.ControlMessagePriorityNormal, err
	}
	if replyEnvelope == nil {
		return nil, transport.ControlMessagePriorityNormal, nil
	}
	replyFrame, err := transport.EncodeBusinessControlEnvelopeFrame(*replyEnvelope)
	if err != nil {
		return nil, transport.ControlMessagePriorityNormal, fmt.Errorf("encode business control reply failed: %w", err)
	}
	return &replyFrame, transport.RecommendControlFramePriority(replyFrame.Type), nil
}

// dispatchEnvelope 按消息类型分派业务控制消息，并在需要时返回 ACK。
func (dispatcher *controlMessageDispatcher) dispatchEnvelope(envelope pb.ControlEnvelope) (*pb.ControlEnvelope, error) {
	if dispatcher == nil {
		return nil, nil
	}
	// 资源事件在进入具体处理器前先刷新会话视图，保证 epoch 校验可用。
	dispatcher.upsertSessionFromEnvelope(envelope)
	switch envelope.MessageType {
	case pb.ControlMessageHeartbeat:
		// 业务心跳仅用于刷新会话存活与状态，不返回 ACK。
		dispatcher.handleHeartbeat(envelope)
		return nil, nil
	case pb.ControlMessagePublishService:
		var message pb.PublishService
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		ack := dispatcher.publishHandler.HandlePublish(envelope, message)
		return buildAckEnvelope(envelope, pb.ControlMessagePublishServiceAck, ack, ack.CurrentResourceVersion)
	case pb.ControlMessageUnpublishService:
		var message pb.UnpublishService
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		ack := dispatcher.publishHandler.HandleUnpublish(envelope, message)
		return buildAckEnvelope(envelope, pb.ControlMessageUnpublishServiceAck, ack, ack.CurrentResourceVersion)
	case pb.ControlMessageServiceHealthReport:
		var message pb.ServiceHealthReport
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		if dispatcher.healthHandler != nil {
			dispatcher.healthHandler.HandleReport(envelope, message)
		}
		// 健康上报是单向事件，不返回 ACK。
		return nil, nil
	case pb.ControlMessageTunnelPoolReport:
		var message pb.TunnelPoolReport
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		if dispatcher.tunnelHandler == nil {
			return nil, nil
		}
		refillRequest, shouldSend := dispatcher.tunnelHandler.HandleReport(envelope, message)
		if !shouldSend {
			return nil, nil
		}
		return buildTunnelRefillEnvelope(envelope, refillRequest)
	case pb.ControlMessageRouteAssign:
		var message pb.RouteAssign
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		ack := dispatcher.routeHandler.HandleAssign(envelope, message)
		return buildAckEnvelope(envelope, pb.ControlMessageRouteAssignAck, ack, ack.CurrentResourceVersion)
	case pb.ControlMessageRouteRevoke:
		var message pb.RouteRevoke
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		ack := dispatcher.routeHandler.HandleRevoke(envelope, message)
		return buildAckEnvelope(envelope, pb.ControlMessageRouteRevokeAck, ack, ack.CurrentResourceVersion)
	case pb.ControlMessageConnectorAuthAck:
		// 认证成功后将会话写入注册表，为后续资源消息提供 epoch 基线。
		dispatcher.applyAuthAckSession(envelope)
		return nil, nil
	default:
		// 未接入的消息类型先忽略，避免骨架阶段影响控制链路稳定性。
		return nil, nil
	}
}

// applyAuthAckSession 从 ConnectorAuthAck 载荷提取会话信息并写入注册表。
func (dispatcher *controlMessageDispatcher) applyAuthAckSession(envelope pb.ControlEnvelope) {
	if dispatcher == nil || dispatcher.sessionHandler == nil {
		return
	}
	var authAck pb.ConnectorAuthAck
	if len(envelope.Payload) == 0 {
		return
	}
	if json.Unmarshal(envelope.Payload, &authAck) != nil || !authAck.Success {
		return
	}
	sessionID := strings.TrimSpace(authAck.SessionID)
	sessionEpoch := authAck.SessionEpoch
	if sessionID == "" {
		sessionID = strings.TrimSpace(envelope.SessionID)
	}
	if sessionEpoch == 0 {
		sessionEpoch = envelope.SessionEpoch
	}
	if sessionID == "" || sessionEpoch == 0 {
		return
	}
	now := time.Now().UTC()
	dispatcher.upsertActiveSession(
		now,
		sessionID,
		sessionEpoch,
		strings.TrimSpace(envelope.ConnectorID),
		envelope.ResourceVersion,
	)
}

// upsertSessionFromEnvelope 在处理资源事件前对会话运行态做最小更新。
func (dispatcher *controlMessageDispatcher) upsertSessionFromEnvelope(envelope pb.ControlEnvelope) {
	if dispatcher == nil || dispatcher.sessionHandler == nil {
		return
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	if sessionID == "" || envelope.SessionEpoch == 0 {
		return
	}
	now := time.Now().UTC()
	sessionRuntime, exists := dispatcher.sessionRegistry.GetBySession(sessionID)
	if exists && sessionRuntime.Epoch > envelope.SessionEpoch {
		// 已存在更高 epoch 时不允许回退覆盖。
		return
	}
	if exists && sessionRuntime.Epoch == envelope.SessionEpoch {
		switch sessionRuntime.State {
		case registry.SessionDraining, registry.SessionStale, registry.SessionClosed:
			// 非 heartbeat 资源事件不允许把非 ACTIVE 会话重新提升为 ACTIVE。
			return
		}
	}
	connectorID := strings.TrimSpace(envelope.ConnectorID)
	if connectorID == "" && exists {
		connectorID = sessionRuntime.ConnectorID
	}
	dispatcher.upsertActiveSession(
		now,
		sessionID,
		envelope.SessionEpoch,
		connectorID,
		envelope.ResourceVersion,
	)
}

// handleHeartbeat 处理业务心跳并同步 session 生命周期。
func (dispatcher *controlMessageDispatcher) handleHeartbeat(envelope pb.ControlEnvelope) {
	if dispatcher == nil || dispatcher.sessionRegistry == nil {
		return
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	sessionEpoch := envelope.SessionEpoch
	if sessionID == "" || sessionEpoch == 0 {
		return
	}
	now := time.Now().UTC()
	// 先把会话刷新为 ACTIVE，确保 heartbeat 本身可更新 last_heartbeat。
	dispatcher.upsertActiveSession(
		now,
		sessionID,
		sessionEpoch,
		strings.TrimSpace(envelope.ConnectorID),
		envelope.ResourceVersion,
	)

	if len(envelope.Payload) == 0 {
		return
	}
	var heartbeat pb.Heartbeat
	if json.Unmarshal(envelope.Payload, &heartbeat) != nil {
		// 心跳载荷解析失败时只保留存活刷新语义，不中断主流程。
		return
	}
	targetState := normalizeRegistrySessionState(heartbeat.SessionState)
	if targetState == "" || targetState == registry.SessionActive {
		return
	}
	dispatcher.transitionSessionState(
		now,
		sessionID,
		sessionEpoch,
		targetState,
		"heartbeat_state_report",
	)
}

// upsertActiveSession 写入 ACTIVE 会话，并处理同 connector 切代下的旧会话收敛。
func (dispatcher *controlMessageDispatcher) upsertActiveSession(
	now time.Time,
	sessionID string,
	sessionEpoch uint64,
	connectorID string,
	resourceVersion uint64,
) {
	if dispatcher == nil || dispatcher.sessionHandler == nil || dispatcher.sessionRegistry == nil {
		return
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	existingSession, exists := dispatcher.sessionRegistry.GetBySession(normalizedSessionID)
	if exists && existingSession.Epoch > sessionEpoch {
		// 旧连接的低 epoch 消息不允许覆盖。
		return
	}
	if normalizedConnectorID == "" && exists {
		normalizedConnectorID = strings.TrimSpace(existingSession.ConnectorID)
	}

	if normalizedConnectorID != "" {
		if connectorSession, connectorExists := dispatcher.sessionRegistry.GetByConnector(normalizedConnectorID); connectorExists &&
			strings.TrimSpace(connectorSession.SessionID) != normalizedSessionID {
			if connectorSession.Epoch > sessionEpoch {
				// connector 已绑定到更高 epoch 的会话，当前消息视为旧连接噪声。
				return
			}
			if connectorSession.Epoch < sessionEpoch {
				// 同 connector 切到新会话时，把旧会话降级为 DRAINING 并立即收敛相关运行态。
				dispatcher.transitionSessionState(
					now,
					connectorSession.SessionID,
					connectorSession.Epoch,
					registry.SessionDraining,
					"session_epoch_takeover",
				)
			}
		}
	}

	dispatcher.sessionHandler.UpsertSession(registry.SessionRuntime{
		SessionID:     normalizedSessionID,
		ConnectorID:   normalizedConnectorID,
		Epoch:         sessionEpoch,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	})
	dispatcher.sessionHandler.MarkReconnectBaseline(normalizedSessionID, sessionEpoch, resourceVersion)
}

// transitionSessionState 执行 session 状态迁移，并触发生命周期副作用。
func (dispatcher *controlMessageDispatcher) transitionSessionState(
	now time.Time,
	sessionID string,
	expectedEpoch uint64,
	targetState registry.SessionState,
	reason string,
) {
	if dispatcher == nil || dispatcher.sessionRegistry == nil {
		return
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" {
		return
	}
	currentSession, exists := dispatcher.sessionRegistry.GetBySession(normalizedSessionID)
	if !exists {
		return
	}
	if expectedEpoch > 0 && currentSession.Epoch != expectedEpoch {
		// 仅允许当前 epoch 会话执行迁移，防止跨代误操作。
		return
	}
	if currentSession.State == targetState {
		return
	}
	if !dispatcher.sessionRegistry.MarkState(now, normalizedSessionID, targetState) {
		return
	}
	currentSession.State = targetState
	currentSession.UpdatedAt = now
	dispatcher.applySessionLifecycleEffects(now, currentSession, reason)
}

// applySessionLifecycleEffects 在 session 进入非 ACTIVE 时收敛 service/tunnel 运行态。
func (dispatcher *controlMessageDispatcher) applySessionLifecycleEffects(
	now time.Time,
	sessionRuntime registry.SessionRuntime,
	reason string,
) {
	if dispatcher == nil {
		return
	}
	normalizedReason := strings.TrimSpace(reason)
	switch sessionRuntime.State {
	case registry.SessionDraining:
		if dispatcher.serviceRegistry != nil && dispatcher.isCurrentConnectorSession(sessionRuntime) {
			// DRAINING 后立即摘流：服务标记 INACTIVE，避免被 resolver 继续命中。
			dispatcher.serviceRegistry.MarkLifecycleByConnector(
				now,
				sessionRuntime.ConnectorID,
				pb.ServiceStatusInactive,
				pb.HealthStatusUnknown,
			)
		}
		if dispatcher.tunnelRegistry != nil {
			dispatcher.tunnelRegistry.PurgeBySession(now, sessionRuntime.SessionID, "session_draining:"+normalizedReason)
		}
	case registry.SessionStale, registry.SessionClosed:
		if dispatcher.serviceRegistry != nil && dispatcher.isCurrentConnectorSession(sessionRuntime) {
			// STALE/CLOSED 服务仅保留审计价值，不再参与路由解析。
			dispatcher.serviceRegistry.MarkLifecycleByConnector(
				now,
				sessionRuntime.ConnectorID,
				pb.ServiceStatusStale,
				pb.HealthStatusUnknown,
			)
		}
		if dispatcher.tunnelRegistry != nil {
			dispatcher.tunnelRegistry.PurgeBySession(now, sessionRuntime.SessionID, "session_terminal:"+normalizedReason)
		}
	}
}

// isCurrentConnectorSession 判断给定 session 是否仍是 connector 当前会话。
func (dispatcher *controlMessageDispatcher) isCurrentConnectorSession(sessionRuntime registry.SessionRuntime) bool {
	if dispatcher == nil || dispatcher.sessionRegistry == nil {
		return false
	}
	normalizedConnectorID := strings.TrimSpace(sessionRuntime.ConnectorID)
	if normalizedConnectorID == "" {
		return false
	}
	currentSession, exists := dispatcher.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !exists {
		return false
	}
	return strings.TrimSpace(currentSession.SessionID) == strings.TrimSpace(sessionRuntime.SessionID) &&
		currentSession.Epoch == sessionRuntime.Epoch
}

// refreshSessionHeartbeatFromState 根据连接上下文刷新会话心跳。
func (dispatcher *controlMessageDispatcher) refreshSessionHeartbeatFromState(
	now time.Time,
	sessionState *controlChannelSessionState,
) {
	if dispatcher == nil || dispatcher.sessionRegistry == nil || sessionState == nil {
		return
	}
	sessionID := strings.TrimSpace(sessionState.sessionID)
	if sessionID == "" {
		return
	}
	sessionRuntime, exists := dispatcher.sessionRegistry.GetBySession(sessionID)
	if !exists {
		return
	}
	if sessionState.sessionEpoch > 0 && sessionRuntime.Epoch != sessionState.sessionEpoch {
		// 连接上下文与当前会话代际不一致时不刷新，避免跨代污染。
		return
	}
	_ = dispatcher.sessionRegistry.RecordHeartbeat(now, sessionID)
}

// resolveEnvelopeSession 从 envelope 解析可用于连接上下文的会话字段。
func resolveEnvelopeSession(envelope pb.ControlEnvelope) (string, uint64, bool) {
	sessionID := strings.TrimSpace(envelope.SessionID)
	sessionEpoch := envelope.SessionEpoch
	if sessionID != "" && sessionEpoch > 0 {
		return sessionID, sessionEpoch, true
	}
	if envelope.MessageType != pb.ControlMessageConnectorAuthAck || len(envelope.Payload) == 0 {
		return "", 0, false
	}
	var authAck pb.ConnectorAuthAck
	if err := json.Unmarshal(envelope.Payload, &authAck); err != nil || !authAck.Success {
		return "", 0, false
	}
	sessionID = strings.TrimSpace(authAck.SessionID)
	sessionEpoch = authAck.SessionEpoch
	if sessionID == "" || sessionEpoch == 0 {
		return "", 0, false
	}
	return sessionID, sessionEpoch, true
}

// sweepSessionLifecycle 按超时规则推进 session 从 ACTIVE->STALE->CLOSED。
func (dispatcher *controlMessageDispatcher) sweepSessionLifecycle(
	now time.Time,
	heartbeatTimeout time.Duration,
	staleTTL time.Duration,
) {
	if dispatcher == nil || dispatcher.sessionRegistry == nil {
		return
	}
	normalizedNow := now
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}
	for _, staleSession := range dispatcher.sessionRegistry.SweepHeartbeatTimeout(normalizedNow, heartbeatTimeout) {
		dispatcher.applySessionLifecycleEffects(normalizedNow, staleSession, "heartbeat_timeout")
	}
	for _, closedSession := range dispatcher.sessionRegistry.SweepStaleToClosed(normalizedNow, staleTTL) {
		dispatcher.applySessionLifecycleEffects(normalizedNow, closedSession, "stale_ttl_expired")
	}
}

// normalizeRegistrySessionState 把协议 session_state 映射为 registry 状态。
func normalizeRegistrySessionState(state pb.SessionState) registry.SessionState {
	switch state {
	case pb.SessionStateActive:
		return registry.SessionActive
	case pb.SessionStateDraining:
		return registry.SessionDraining
	case pb.SessionStateStale:
		return registry.SessionStale
	case pb.SessionStateClosed:
		return registry.SessionClosed
	default:
		return ""
	}
}

// decodeControlPayload 解析控制消息 payload 到指定结构体。
func decodeControlPayload(rawPayload []byte, out any) error {
	if len(rawPayload) == 0 {
		return errors.New("control payload is empty")
	}
	if err := json.Unmarshal(rawPayload, out); err != nil {
		return fmt.Errorf("decode control payload failed: %w", err)
	}
	return nil
}

// buildAckEnvelope 以请求 envelope 为模板构造 ACK 控制面封装。
func buildAckEnvelope(
	requestEnvelope pb.ControlEnvelope,
	ackType pb.ControlMessageType,
	ackPayload any,
	ackResourceVersion uint64,
) (*pb.ControlEnvelope, error) {
	encodedPayload, err := json.Marshal(ackPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal ack payload failed: %w", err)
	}
	versionMajor := requestEnvelope.VersionMajor
	if versionMajor == 0 {
		// 兼容缺失版本字段的请求，默认回写当前协议主版本。
		versionMajor = 2
	}
	versionMinor := requestEnvelope.VersionMinor
	if versionMinor == 0 {
		// 兼容缺失版本字段的请求，默认回写当前协议次版本。
		versionMinor = 1
	}
	if ackResourceVersion == 0 {
		ackResourceVersion = requestEnvelope.ResourceVersion
	}
	return &pb.ControlEnvelope{
		VersionMajor:    versionMajor,
		VersionMinor:    versionMinor,
		MessageType:     ackType,
		RequestID:       requestEnvelope.RequestID,
		SessionID:       requestEnvelope.SessionID,
		SessionEpoch:    requestEnvelope.SessionEpoch,
		ConnectorID:     requestEnvelope.ConnectorID,
		ResourceType:    requestEnvelope.ResourceType,
		ResourceID:      requestEnvelope.ResourceID,
		EventID:         requestEnvelope.EventID,
		ResourceVersion: ackResourceVersion,
		Payload:         encodedPayload,
	}, nil
}

// buildTunnelRefillEnvelope 以 TunnelPoolReport 请求为模板构造 TunnelRefillRequest。
func buildTunnelRefillEnvelope(
	requestEnvelope pb.ControlEnvelope,
	refillRequest pb.TunnelRefillRequest,
) (*pb.ControlEnvelope, error) {
	encodedPayload, err := json.Marshal(refillRequest)
	if err != nil {
		return nil, fmt.Errorf("marshal refill payload failed: %w", err)
	}
	versionMajor := requestEnvelope.VersionMajor
	if versionMajor == 0 {
		versionMajor = 2
	}
	versionMinor := requestEnvelope.VersionMinor
	if versionMinor == 0 {
		versionMinor = 1
	}
	// 补池请求属于控制面容量事件，沿用请求会话字段并重新生成 event_id。
	return &pb.ControlEnvelope{
		VersionMajor:    versionMajor,
		VersionMinor:    versionMinor,
		MessageType:     pb.ControlMessageTunnelRefillRequest,
		RequestID:       refillRequest.RequestID,
		SessionID:       refillRequest.SessionID,
		SessionEpoch:    refillRequest.SessionEpoch,
		ConnectorID:     requestEnvelope.ConnectorID,
		ResourceType:    "tunnel_pool",
		ResourceID:      "default",
		EventID:         fmt.Sprintf("evt-refill-%d", time.Now().UTC().UnixNano()),
		ResourceVersion: requestEnvelope.ResourceVersion,
		Payload:         encodedPayload,
	}, nil
}

type controlPlaneServer struct {
	tcpListenAddr  string
	grpcListenAddr string
	heartbeatTTL   time.Duration

	tcpTransport  *tcpbinding.Transport
	grpcTransport *grpcbinding.Transport
	dispatcher    *controlMessageDispatcher

	mu           sync.Mutex
	tcpListener  net.Listener
	grpcListener net.Listener
	grpcServer   *grpc.Server
}

// controlPlaneDependencies 定义控制面运行时共享依赖。
type controlPlaneDependencies struct {
	sessionRegistry *registry.SessionRegistry
	serviceRegistry *registry.ServiceRegistry
	routeRegistry   *registry.RouteRegistry
	tunnelRegistry  *registry.TunnelRegistry
}

// newControlPlaneServer 创建控制面服务，并绑定业务分发器依赖。
func newControlPlaneServer(
	config ControlPlaneConfig,
	dependencies controlPlaneDependencies,
) (*controlPlaneServer, error) {
	tcpTransport, err := tcpbinding.NewTransportWithConfig(tcpbinding.TransportConfig{})
	if err != nil {
		return nil, fmt.Errorf("new control plane tcp transport: %w", err)
	}
	grpcTransport, err := grpcbinding.NewTransportWithConfig(grpcbinding.TransportConfig{})
	if err != nil {
		return nil, fmt.Errorf("new control plane grpc transport: %w", err)
	}
	return &controlPlaneServer{
		tcpListenAddr:  strings.TrimSpace(config.ListenAddr),
		grpcListenAddr: strings.TrimSpace(config.GRPCH2ListenAddr),
		heartbeatTTL:   config.HeartbeatTimeout,
		tcpTransport:   tcpTransport,
		grpcTransport:  grpcTransport,
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry: dependencies.sessionRegistry,
			serviceRegistry: dependencies.serviceRegistry,
			routeRegistry:   dependencies.routeRegistry,
			tunnelRegistry:  dependencies.tunnelRegistry,
		}),
	}, nil
}

func (server *controlPlaneServer) run(ctx context.Context) error {
	if server == nil {
		return errors.New("control plane server is nil")
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	runContext, cancel := context.WithCancel(normalizedContext)
	defer cancel()

	runners := []func(context.Context) error{
		server.runTCP,
		server.runSessionLifecycleLoop,
	}
	if server.grpcListenAddr != "" {
		runners = append(runners, server.runGRPC)
	}
	serverErrChan := make(chan error, len(runners))
	var serverWaitGroup sync.WaitGroup
	for _, run := range runners {
		serverWaitGroup.Add(1)
		go func(runFn func(context.Context) error) {
			defer serverWaitGroup.Done()
			serverErrChan <- runFn(runContext)
		}(run)
	}
	defer serverWaitGroup.Wait()

	var firstErr error
	for range runners {
		runErr := <-serverErrChan
		if runErr != nil && firstErr == nil {
			firstErr = runErr
			cancel()
			_ = server.shutdown()
		}
	}
	return firstErr
}

// runSessionLifecycleLoop 周期推进 session 超时收敛，并联动 service/tunnel 生命周期。
func (server *controlPlaneServer) runSessionLifecycleLoop(ctx context.Context) error {
	if server == nil || server.dispatcher == nil {
		return nil
	}
	heartbeatTTL := server.heartbeatTTL
	if heartbeatTTL <= 0 {
		// 未显式配置时使用保守默认值，避免误判。
		heartbeatTTL = 30 * time.Second
	}
	staleTTL := heartbeatTTL
	sweepInterval := heartbeatTTL / 3
	if sweepInterval < time.Second {
		// 极小 heartbeat 配置下也至少每秒 sweep 一次，降低调度开销。
		sweepInterval = time.Second
	}
	ticker := time.NewTicker(sweepInterval)
	defer ticker.Stop()

	for {
		server.dispatcher.sweepSessionLifecycle(time.Now().UTC(), heartbeatTTL, staleTTL)
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
	}
}

func (server *controlPlaneServer) runTCP(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.tcpListenAddr)
	if err != nil {
		return fmt.Errorf("listen tcp control plane: %w", err)
	}
	server.mu.Lock()
	server.tcpListener = listener
	server.mu.Unlock()
	defer func() {
		_ = listener.Close()
		server.mu.Lock()
		if server.tcpListener == listener {
			server.tcpListener = nil
		}
		server.mu.Unlock()
	}()

	var channelWaitGroup sync.WaitGroup
	defer channelWaitGroup.Wait()

	for {
		controlChannel, acceptErr := server.tcpTransport.AcceptControlChannel(ctx, listener)
		if acceptErr != nil {
			if isControlPlaneStopError(acceptErr) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept tcp control channel: %w", acceptErr)
		}
		channelWaitGroup.Add(1)
		go func(channel transport.ControlChannel) {
			defer channelWaitGroup.Done()
			if err := serveControlChannelWithDispatcher(ctx, channel, server.dispatcher); err != nil && !isControlChannelClosedError(err) {
				_ = channel.Close(context.Background())
			}
		}(controlChannel)
	}
}

func (server *controlPlaneServer) runGRPC(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.grpcListenAddr)
	if err != nil {
		return fmt.Errorf("listen grpc_h2 control plane: %w", err)
	}
	grpcServer := grpc.NewServer(server.grpcTransport.ServerOptions()...)
	transportgen.RegisterGRPCH2TransportServiceServer(grpcServer, &grpcControlPlaneService{
		dispatcher: server.dispatcher,
	})

	server.mu.Lock()
	server.grpcListener = listener
	server.grpcServer = grpcServer
	server.mu.Unlock()
	defer func() {
		_ = listener.Close()
		server.mu.Lock()
		if server.grpcListener == listener {
			server.grpcListener = nil
		}
		if server.grpcServer == grpcServer {
			server.grpcServer = nil
		}
		server.mu.Unlock()
	}()

	serveErrChan := make(chan error, 1)
	go func() {
		serveErrChan <- grpcServer.Serve(listener)
	}()

	select {
	case <-ctx.Done():
		grpcServer.GracefulStop()
		serveErr := <-serveErrChan
		if serveErr != nil && !isGRPCServerStopError(serveErr) {
			return fmt.Errorf("serve grpc_h2 control plane: %w", serveErr)
		}
		return nil
	case serveErr := <-serveErrChan:
		if isGRPCServerStopError(serveErr) || ctx.Err() != nil {
			return nil
		}
		return fmt.Errorf("serve grpc_h2 control plane: %w", serveErr)
	}
}

func (server *controlPlaneServer) shutdown() error {
	if server == nil {
		return nil
	}
	server.mu.Lock()
	defer server.mu.Unlock()

	var firstErr error
	if server.tcpListener != nil {
		if err := server.tcpListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
		server.tcpListener = nil
	}
	if server.grpcServer != nil {
		server.grpcServer.Stop()
		server.grpcServer = nil
	}
	if server.grpcListener != nil {
		if err := server.grpcListener.Close(); err != nil && !errors.Is(err, net.ErrClosed) && firstErr == nil {
			firstErr = err
		}
		server.grpcListener = nil
	}
	return firstErr
}

type grpcControlPlaneService struct {
	transportgen.UnimplementedGRPCH2TransportServiceServer
	dispatcher *controlMessageDispatcher
}

func (service *grpcControlPlaneService) ControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	return serveGRPCControlChannelWithDispatcher(stream, service.dispatcher)
}

func (service *grpcControlPlaneService) TunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	return serveGRPCTunnelStream(stream)
}

func serveGRPCControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	return serveGRPCControlChannelWithDispatcher(stream, nil)
}

func serveGRPCControlChannelWithDispatcher(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
	dispatcher *controlMessageDispatcher,
) error {
	effectiveDispatcher := dispatcher
	if effectiveDispatcher == nil {
		// 测试或兼容路径未注入 dispatcher 时按默认实现创建一份。
		effectiveDispatcher = newControlMessageDispatcher(controlMessageDispatcherOptions{})
	}
	sessionState := &controlChannelSessionState{}
	for {
		frameEnvelope, err := stream.Recv()
		if err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return err
		}
		if frameEnvelope == nil {
			continue
		}
		if frameEnvelope.FrameType > math.MaxUint16 {
			return fmt.Errorf("control frame_type overflow: %d", frameEnvelope.FrameType)
		}
		replyFrame, _, err := effectiveDispatcher.handleFrame(transport.ControlFrame{
			Type:    uint16(frameEnvelope.FrameType),
			Payload: append([]byte(nil), frameEnvelope.Payload...),
		}, sessionState)
		if err != nil {
			return err
		}
		if replyFrame == nil {
			continue
		}
		replyEnvelope := &transportgen.ControlFrameEnvelope{
			FrameType: uint32(replyFrame.Type),
			Payload:   append([]byte(nil), replyFrame.Payload...),
		}
		if err := stream.Send(replyEnvelope); err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return fmt.Errorf("write grpc control reply frame: %w", err)
		}
	}
}

func serveGRPCTunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	for {
		_, err := stream.Recv()
		if err != nil {
			if isGRPCStreamClosedError(err) || stream.Context().Err() != nil {
				return nil
			}
			return err
		}
	}
}

func serveControlChannel(ctx context.Context, controlChannel transport.ControlChannel) error {
	return serveControlChannelWithDispatcher(ctx, controlChannel, nil)
}

func serveControlChannelWithDispatcher(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	dispatcher *controlMessageDispatcher,
) error {
	effectiveDispatcher := dispatcher
	if effectiveDispatcher == nil {
		// 测试或兼容路径未注入 dispatcher 时按默认实现创建一份。
		effectiveDispatcher = newControlMessageDispatcher(controlMessageDispatcherOptions{})
	}
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	sessionState := &controlChannelSessionState{}
	defer func() {
		_ = controlChannel.Close(context.Background())
	}()

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := controlChannel.ReadControlFrame(ctx)
		if err != nil {
			return err
		}
		replyFrame, priority, err := effectiveDispatcher.handleFrame(frame, sessionState)
		if err != nil {
			return err
		}
		if replyFrame == nil {
			continue
		}
		replyContext, cancel := context.WithTimeout(ctx, defaultHeartbeatReplyTimeout)
		replyErr := writeControlFrameWithPriority(
			replyContext,
			controlChannel,
			*replyFrame,
			priority,
		)
		cancel()
		if replyErr != nil {
			return fmt.Errorf("write control reply frame: %w", replyErr)
		}
	}
}

func writeControlFrameWithPriority(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	frame transport.ControlFrame,
	priority transport.ControlMessagePriority,
) error {
	if prioritizedControlChannel, ok := controlChannel.(transport.PrioritizedControlChannel); ok {
		return prioritizedControlChannel.WritePrioritizedControlFrame(ctx, transport.PrioritizedControlFrame{
			Priority: priority,
			Frame:    frame,
		})
	}
	return controlChannel.WriteControlFrame(ctx, frame)
}

func isControlPlaneStopError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "use of closed network connection")
}

func isControlChannelClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) || errors.Is(err, transport.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed")
}

func isGRPCServerStopError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, grpc.ErrServerStopped) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	return strings.Contains(strings.ToLower(err.Error()), "closed")
}

func isGRPCStreamClosedError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, io.EOF) || errors.Is(err, context.Canceled) || errors.Is(err, net.ErrClosed) {
		return true
	}
	lowerMessage := strings.ToLower(err.Error())
	return strings.Contains(lowerMessage, "closed") || strings.Contains(lowerMessage, "canceled")
}
