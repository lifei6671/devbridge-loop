package app

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

const (
	defaultHeartbeatReplyTimeout = 2 * time.Second
	// tcpConnectionClassifierReadTimeout 定义 TCP 入站连接类型判别的首包读取超时。
	tcpConnectionClassifierReadTimeout = 2 * time.Second
	// defaultIncomingTunnelIDPrefixTCP 定义 Bridge 侧 TCP 入站 tunnel_id 前缀。
	defaultIncomingTunnelIDPrefixTCP = "tcp-bridge-tunnel"
	// incomingTunnelProbeInterval 定义入站 tunnel 生命周期探测间隔，兜底处理远端静默断开。
	incomingTunnelProbeInterval = 250 * time.Millisecond
	// incomingTunnelProbeTimeout 定义单次探测超时时间，避免阻塞生命周期协程。
	incomingTunnelProbeTimeout = 120 * time.Millisecond
	// incomingTunnelDialAnnounceWait 定义入站 tunnel 等待 Agent 宣告 tunnel_id 的窗口。
	incomingTunnelDialAnnounceWait = 180 * time.Millisecond
	// incomingTunnelDialAnnounceTTL 定义宣告 tunnel_id 在队列中的最大保留时长。
	incomingTunnelDialAnnounceTTL = 3 * time.Second
)

type announcedTunnelDialRuntime struct {
	tunnelID      string
	dialLocalAddr string
	announcedAt   time.Time
}

// controlMessageDispatcher 负责把控制面业务帧分发给 Bridge 控制处理器。
type controlMessageDispatcher struct {
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
	publishHandler        *bridgecontrol.PublishHandler
	healthHandler         *bridgecontrol.HealthHandler
	tunnelHandler         *bridgecontrol.TunnelReportHandler
	routeHandler          *bridgecontrol.RouteHandler
	sessionHandler        *bridgecontrol.SessionHandler

	tunnelDialAnnounceMutex  sync.Mutex
	tunnelDialAnnounceQueues map[string][]announcedTunnelDialRuntime
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
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
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
		sessionRegistry:       sessionRegistry,
		serviceRegistry:       serviceRegistry,
		routeRegistry:         routeRegistry,
		tunnelRegistry:        tunnelRegistry,
		tunnelPoolReportStore: options.tunnelPoolReportStore,
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
			TunnelRegistry:  tunnelRegistry,
			ReportStore:     options.tunnelPoolReportStore,
		}),
		routeHandler: bridgecontrol.NewRouteHandler(bridgecontrol.RouteHandlerOptions{
			Guard:           eventGuard,
			SessionRegistry: sessionRegistry,
			RouteRegistry:   routeRegistry,
		}),
		sessionHandler:           sessionHandler,
		tunnelDialAnnounceQueues: make(map[string][]announcedTunnelDialRuntime),
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
		reportSessionID := strings.TrimSpace(message.SessionID)
		if reportSessionID == "" {
			reportSessionID = strings.TrimSpace(envelope.SessionID)
		}
		reportSessionEpoch := message.SessionEpoch
		if reportSessionEpoch == 0 {
			reportSessionEpoch = envelope.SessionEpoch
		}
		reportConnectorID := strings.TrimSpace(envelope.ConnectorID)
		if reportConnectorID == "" && dispatcher.sessionRegistry != nil {
			if sessionRuntime, exists := dispatcher.sessionRegistry.GetBySession(reportSessionID); exists {
				reportConnectorID = strings.TrimSpace(sessionRuntime.ConnectorID)
			}
		}
		slog.Info(
			"bridge receive tunnel pool report",
			"connector_id", reportConnectorID,
			"session_id", reportSessionID,
			"session_epoch", reportSessionEpoch,
			"idle_count", message.IdleCount,
			"in_use_count", message.InUseCount,
			"target_idle_count", message.TargetIdleCount,
			"trigger", strings.TrimSpace(message.Trigger),
		)
		if dispatcher.tunnelHandler == nil {
			return nil, nil
		}
		refillRequest, shouldSend := dispatcher.tunnelHandler.HandleReport(envelope, message)
		if !shouldSend {
			slog.Info(
				"bridge skip tunnel refill request",
				"connector_id", reportConnectorID,
				"session_id", reportSessionID,
				"session_epoch", reportSessionEpoch,
				"idle_count", message.IdleCount,
				"in_use_count", message.InUseCount,
				"target_idle_count", message.TargetIdleCount,
				"trigger", strings.TrimSpace(message.Trigger),
			)
			return nil, nil
		}
		slog.Info(
			"bridge send tunnel refill request",
			"connector_id", reportConnectorID,
			"session_id", refillRequest.SessionID,
			"session_epoch", refillRequest.SessionEpoch,
			"request_id", refillRequest.RequestID,
			"requested_idle_delta", refillRequest.RequestedIdleDelta,
			"reason", strings.TrimSpace(refillRequest.Reason),
			"bridge_idle_count", parseRefillMetadataInt(refillRequest.Metadata, "bridge_idle_count"),
			"bridge_in_use_count", parseRefillMetadataInt(refillRequest.Metadata, "bridge_in_use_count"),
			"bridge_idle_recycled_count", parseRefillMetadataInt(refillRequest.Metadata, "bridge_idle_recycled_count"),
			"agent_idle_count", parseRefillMetadataInt(refillRequest.Metadata, "idle_count"),
			"agent_target_idle_count", parseRefillMetadataInt(refillRequest.Metadata, "target_idle_count"),
		)
		return buildTunnelRefillEnvelope(envelope, refillRequest)
	case pb.ControlMessageTunnelDialAnnounce:
		var message pb.TunnelDialAnnounce
		if err := decodeControlPayload(envelope.Payload, &message); err != nil {
			return nil, err
		}
		if strings.TrimSpace(message.SessionID) == "" {
			message.SessionID = strings.TrimSpace(envelope.SessionID)
		}
		if message.SessionEpoch == 0 {
			message.SessionEpoch = envelope.SessionEpoch
		}
		slog.Info(
			"bridge receive tunnel dial announce",
			"connector_id", strings.TrimSpace(envelope.ConnectorID),
			"session_id", strings.TrimSpace(message.SessionID),
			"session_epoch", message.SessionEpoch,
			"tunnel_id", strings.TrimSpace(message.TunnelID),
			"dial_local_addr", strings.TrimSpace(message.DialLocalAddr),
		)
		dispatcher.enqueueTunnelDialAnnounce(message)
		return nil, nil
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

func tunnelDialAnnounceQueueKey(sessionID string, sessionEpoch uint64) string {
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return ""
	}
	return fmt.Sprintf("%s#%d", normalizedSessionID, sessionEpoch)
}

func (dispatcher *controlMessageDispatcher) enqueueTunnelDialAnnounce(message pb.TunnelDialAnnounce) {
	if dispatcher == nil {
		return
	}
	queueKey := tunnelDialAnnounceQueueKey(message.SessionID, message.SessionEpoch)
	normalizedTunnelID := strings.TrimSpace(message.TunnelID)
	normalizedDialLocalAddr := strings.TrimSpace(message.DialLocalAddr)
	if queueKey == "" || normalizedTunnelID == "" {
		return
	}
	normalizedNow := time.Now().UTC()
	dispatcher.tunnelDialAnnounceMutex.Lock()
	defer dispatcher.tunnelDialAnnounceMutex.Unlock()
	queue := dispatcher.tunnelDialAnnounceQueues[queueKey]
	cleanQueue := make([]announcedTunnelDialRuntime, 0, len(queue)+1)
	for _, item := range queue {
		if normalizedNow.Sub(item.announcedAt) <= incomingTunnelDialAnnounceTTL {
			cleanQueue = append(cleanQueue, item)
		}
	}
	cleanQueue = append(cleanQueue, announcedTunnelDialRuntime{
		tunnelID:      normalizedTunnelID,
		dialLocalAddr: normalizedDialLocalAddr,
		announcedAt:   normalizedNow,
	})
	dispatcher.tunnelDialAnnounceQueues[queueKey] = cleanQueue
}

func (dispatcher *controlMessageDispatcher) consumeTunnelDialAnnounce(
	sessionID string,
	sessionEpoch uint64,
	peerAddr string,
	wait time.Duration,
) string {
	if dispatcher == nil {
		return ""
	}
	queueKey := tunnelDialAnnounceQueueKey(sessionID, sessionEpoch)
	if queueKey == "" {
		return ""
	}
	normalizedWait := wait
	if normalizedWait < 0 {
		normalizedWait = 0
	}
	deadline := time.Now().UTC().Add(normalizedWait)
	normalizedPeerAddr := strings.TrimSpace(peerAddr)
	for {
		now := time.Now().UTC()
		dispatcher.tunnelDialAnnounceMutex.Lock()
		queue := dispatcher.tunnelDialAnnounceQueues[queueKey]
		cleanQueue := make([]announcedTunnelDialRuntime, 0, len(queue))
		nextTunnelID := ""
		for _, item := range queue {
			if now.Sub(item.announcedAt) > incomingTunnelDialAnnounceTTL {
				continue
			}
			if nextTunnelID == "" {
				if normalizedPeerAddr != "" && strings.TrimSpace(item.dialLocalAddr) != normalizedPeerAddr {
					cleanQueue = append(cleanQueue, item)
					continue
				}
				nextTunnelID = item.tunnelID
				continue
			}
			cleanQueue = append(cleanQueue, item)
		}
		if nextTunnelID == "" {
			if len(cleanQueue) == 0 {
				delete(dispatcher.tunnelDialAnnounceQueues, queueKey)
			} else {
				dispatcher.tunnelDialAnnounceQueues[queueKey] = cleanQueue
			}
		} else if len(cleanQueue) == 0 {
			delete(dispatcher.tunnelDialAnnounceQueues, queueKey)
		} else {
			dispatcher.tunnelDialAnnounceQueues[queueKey] = cleanQueue
		}
		dispatcher.tunnelDialAnnounceMutex.Unlock()

		if nextTunnelID != "" || normalizedWait == 0 || !now.Before(deadline) {
			return nextTunnelID
		}
		time.Sleep(10 * time.Millisecond)
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
		if dispatcher.tunnelPoolReportStore != nil {
			dispatcher.tunnelPoolReportStore.RemoveBySession(sessionRuntime.SessionID, sessionRuntime.Epoch)
		}
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
		if dispatcher.tunnelPoolReportStore != nil {
			dispatcher.tunnelPoolReportStore.RemoveBySession(sessionRuntime.SessionID, sessionRuntime.Epoch)
		}
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

func parseRefillMetadataInt(metadata map[string]string, key string) int {
	if len(metadata) == 0 {
		return 0
	}
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return 0
	}
	countText := strings.TrimSpace(metadata[normalizedKey])
	if countText == "" {
		return 0
	}
	parsedCount, parseErr := strconv.Atoi(countText)
	if parseErr != nil {
		return 0
	}
	return parsedCount
}

type controlPlaneServer struct {
	tcpListenAddr  string
	grpcListenAddr string
	heartbeatTTL   time.Duration

	tcpTransport  *tcpbinding.Transport
	grpcTransport *grpcbinding.Transport
	dispatcher    *controlMessageDispatcher
	grpcAcceptor  *grpcbinding.TunnelAcceptor

	mu           sync.Mutex
	tcpListener  net.Listener
	grpcListener net.Listener
	grpcServer   *grpc.Server

	tcpTunnelSequence atomic.Uint64
}

// controlPlaneDependencies 定义控制面运行时共享依赖。
type controlPlaneDependencies struct {
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
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
		grpcAcceptor:   grpcbinding.NewTunnelAcceptor(grpcbinding.TunnelAcceptorConfig{}),
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry:       dependencies.sessionRegistry,
			serviceRegistry:       dependencies.serviceRegistry,
			routeRegistry:         dependencies.routeRegistry,
			tunnelRegistry:        dependencies.tunnelRegistry,
			tunnelPoolReportStore: dependencies.tunnelPoolReportStore,
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
		runners = append(runners, server.runGRPCTunnelAcceptLoop)
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

	acceptPollInterval := tcpbinding.DefaultTransportConfig().AcceptPollInterval
	if server.tcpTransport != nil {
		acceptPollInterval = server.tcpTransport.Config().AcceptPollInterval
	}
	for {
		connection, acceptErr := acceptTCPConnectionWithContext(ctx, listener, acceptPollInterval)
		if acceptErr != nil {
			if isControlPlaneStopError(acceptErr) || ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("accept tcp inbound connection: %w", acceptErr)
		}
		channelWaitGroup.Add(1)
		go func(rawConn net.Conn) {
			defer channelWaitGroup.Done()
			_ = server.serveTCPInboundConnection(ctx, rawConn)
		}(connection)
	}
}

func (server *controlPlaneServer) runGRPC(ctx context.Context) error {
	listener, err := net.Listen("tcp", server.grpcListenAddr)
	if err != nil {
		return fmt.Errorf("listen grpc_h2 control plane: %w", err)
	}
	grpcServer := grpc.NewServer(server.grpcTransport.ServerOptions()...)
	transportgen.RegisterGRPCH2TransportServiceServer(grpcServer, &grpcControlPlaneService{
		dispatcher:     server.dispatcher,
		tunnelAcceptor: server.grpcAcceptor,
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

// runGRPCTunnelAcceptLoop 从 gRPC TunnelAcceptor 消费 tunnel 并登记到共享 registry。
func (server *controlPlaneServer) runGRPCTunnelAcceptLoop(ctx context.Context) error {
	if server == nil || server.grpcAcceptor == nil {
		return nil
	}
	for {
		acceptedTunnel, err := server.grpcAcceptor.AcceptTunnel(ctx)
		if err != nil {
			if ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, transport.ErrClosed) {
				return nil
			}
			return fmt.Errorf("accept grpc tunnel: %w", err)
		}
		if acceptedTunnel == nil {
			continue
		}
		if registerErr := server.registerAcceptedTunnel(acceptedTunnel, transport.BindingTypeGRPCH2); registerErr != nil {
			// 单条 tunnel 入库失败不应影响整个接入循环。
			_ = acceptedTunnel.Close()
			continue
		}
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
	if server.grpcAcceptor != nil {
		server.grpcAcceptor.Close(transport.ErrClosed)
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
	dispatcher     *controlMessageDispatcher
	tunnelAcceptor *grpcbinding.TunnelAcceptor
}

func (service *grpcControlPlaneService) ControlChannel(
	stream grpc.BidiStreamingServer[transportgen.ControlFrameEnvelope, transportgen.ControlFrameEnvelope],
) error {
	return serveGRPCControlChannelWithDispatcher(stream, service.dispatcher)
}

func (service *grpcControlPlaneService) TunnelStream(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
) error {
	return serveGRPCTunnelStreamWithAcceptor(stream, service.tunnelAcceptor)
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
	return serveGRPCTunnelStreamWithAcceptor(stream, nil)
}

// serveGRPCTunnelStreamWithAcceptor 处理 gRPC TunnelStream 并在可用时交由 acceptor 入队。
func serveGRPCTunnelStreamWithAcceptor(
	stream grpc.BidiStreamingServer[transportgen.TunnelEnvelope, transportgen.TunnelEnvelope],
	tunnelAcceptor *grpcbinding.TunnelAcceptor,
) error {
	if tunnelAcceptor != nil {
		return tunnelAcceptor.HandleTunnelStream(stream)
	}
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

// serveTCPInboundConnection 对单条 TCP 入站连接做类型判别并分派到 control/tunnel 处理链路。
func (server *controlPlaneServer) serveTCPInboundConnection(ctx context.Context, rawConn net.Conn) error {
	if server == nil || rawConn == nil {
		return errors.New("serve tcp inbound: invalid argument")
	}
	classifiedConn, isControlChannel, err := classifyTCPInboundConnection(rawConn)
	if err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("serve tcp inbound: classify connection: %w", err)
	}
	if isControlChannel {
		controlChannel, openErr := server.tcpTransport.OpenControlChannel(classifiedConn)
		if openErr != nil {
			_ = classifiedConn.Close()
			return fmt.Errorf("serve tcp inbound: open control channel: %w", openErr)
		}
		if serveErr := serveControlChannelWithDispatcher(ctx, controlChannel, server.dispatcher); serveErr != nil && !isControlChannelClosedError(serveErr) {
			_ = controlChannel.Close(context.Background())
			return fmt.Errorf("serve tcp inbound: serve control channel: %w", serveErr)
		}
		return nil
	}
	if registerErr := server.handleAcceptedTCPTunnel(classifiedConn); registerErr != nil {
		_ = classifiedConn.Close()
		return registerErr
	}
	return nil
}

// handleAcceptedTCPTunnel 将一条已判别为数据面 tunnel 的 TCP 连接登记到共享 tunnel registry。
func (server *controlPlaneServer) handleAcceptedTCPTunnel(rawConn net.Conn) error {
	if server == nil || server.tcpTransport == nil {
		return errors.New("handle accepted tcp tunnel: tcp transport is nil")
	}
	connectorID, sessionID, sessionEpoch, ownerResolved := server.resolveSingleActiveSessionOwner()
	if !ownerResolved {
		// owner 不明确时直接回收 tunnel，避免错误归属影响后续流量调度。
		_ = rawConn.Close()
		return nil
	}
	normalizedNow := time.Now().UTC()
	tunnelID := ""
	peerAddr := ""
	if rawConn != nil && rawConn.RemoteAddr() != nil {
		peerAddr = strings.TrimSpace(rawConn.RemoteAddr().String())
	}
	if server.dispatcher != nil {
		tunnelID = server.dispatcher.consumeTunnelDialAnnounce(
			sessionID,
			sessionEpoch,
			peerAddr,
			incomingTunnelDialAnnounceWait,
		)
	}
	if strings.TrimSpace(tunnelID) == "" {
		tunnelID = fmt.Sprintf("%s-%d", defaultIncomingTunnelIDPrefixTCP, server.tcpTunnelSequence.Add(1))
		slog.Info(
			"bridge accept tunnel id fallback",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
			"tunnel_id", tunnelID,
		)
	} else {
		slog.Info(
			"bridge accept tunnel id matched",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
			"tunnel_id", tunnelID,
		)
	}
	rawTunnel, err := server.tcpTransport.OpenTunnel(rawConn, transport.TunnelMeta{
		TunnelID:  tunnelID,
		CreatedAt: normalizedNow,
	})
	if err != nil {
		return fmt.Errorf("handle accepted tcp tunnel: open tunnel: %w", err)
	}
	return server.registerAcceptedTunnelWithOwner(
		rawTunnel,
		transport.BindingTypeTCPFramed,
		connectorID,
		sessionID,
	)
}

// registerAcceptedTunnel 将 transport tunnel 适配并注册到 Bridge tunnel registry。
func (server *controlPlaneServer) registerAcceptedTunnel(
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
) error {
	if server == nil || server.dispatcher == nil || server.dispatcher.tunnelRegistry == nil {
		if rawTunnel != nil {
			_ = rawTunnel.Close()
		}
		return errors.New("register accepted tunnel: tunnel registry dependency missing")
	}
	if rawTunnel == nil {
		return errors.New("register accepted tunnel: nil tunnel")
	}
	connectorID, sessionID, _, ok := server.resolveSingleActiveSessionOwner()
	if !ok {
		// owner 不明确时直接回收 tunnel，避免错误归属影响后续流量调度。
		_ = rawTunnel.Close()
		return nil
	}
	return server.registerAcceptedTunnelWithOwner(rawTunnel, bindingType, connectorID, sessionID)
}

func (server *controlPlaneServer) registerAcceptedTunnelWithOwner(
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
	connectorID string,
	sessionID string,
) error {
	if server == nil || server.dispatcher == nil || server.dispatcher.tunnelRegistry == nil {
		if rawTunnel != nil {
			_ = rawTunnel.Close()
		}
		return errors.New("register accepted tunnel: tunnel registry dependency missing")
	}
	if rawTunnel == nil {
		return errors.New("register accepted tunnel: nil tunnel")
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedConnectorID == "" || normalizedSessionID == "" {
		_ = rawTunnel.Close()
		return nil
	}
	normalizedNow := time.Now().UTC()
	adapter := newRuntimeBridgeTunnelAdapter(rawTunnel)
	if adapter == nil {
		_ = rawTunnel.Close()
		return errors.New("register accepted tunnel: build runtime adapter failed")
	}
	registeredRuntime, err := server.dispatcher.tunnelRegistry.UpsertIdle(
		normalizedNow,
		normalizedConnectorID,
		normalizedSessionID,
		adapter,
	)
	if err != nil {
		_ = rawTunnel.Close()
		return fmt.Errorf("register accepted tunnel: upsert idle: %w", err)
	}
	go server.watchAcceptedTunnelLifecycle(registeredRuntime.TunnelID, rawTunnel, bindingType)
	return nil
}

// resolveSingleActiveSessionOwner 在当前会话视图中解析 tunnel 归属。
//
// 规则：
//  1. 仅允许单 connector 活跃；多 connector 同时 ACTIVE 仍视为歧义。
//  2. 同 connector 若存在重复 ACTIVE session（如 Agent 快速重启导致 epoch 相同并存），
//     优先使用 sessionRegistry 的 connector 当前映射，避免误判 owner 缺失导致入站 tunnel 被立即关闭。
func (server *controlPlaneServer) resolveSingleActiveSessionOwner() (string, string, uint64, bool) {
	if server == nil || server.dispatcher == nil || server.dispatcher.sessionRegistry == nil {
		return "", "", 0, false
	}
	sessionItems := server.dispatcher.sessionRegistry.List()
	candidates := make([]registry.SessionRuntime, 0, len(sessionItems))
	connectorSet := make(map[string]struct{}, len(sessionItems))
	for _, sessionRuntime := range sessionItems {
		if sessionRuntime.State != registry.SessionActive {
			continue
		}
		sessionRuntime.ConnectorID = strings.TrimSpace(sessionRuntime.ConnectorID)
		sessionRuntime.SessionID = strings.TrimSpace(sessionRuntime.SessionID)
		if sessionRuntime.ConnectorID == "" || sessionRuntime.SessionID == "" {
			continue
		}
		candidates = append(candidates, sessionRuntime)
		connectorSet[sessionRuntime.ConnectorID] = struct{}{}
	}
	if len(candidates) == 0 {
		return "", "", 0, false
	}
	if len(connectorSet) != 1 {
		// 多 connector 同时 ACTIVE 时 owner 不可判定。
		return "", "", 0, false
	}

	// 只有一个 connector 时，优先取 connector 当前映射会话（可收敛同 epoch 重连并发场景）。
	var connectorID string
	for key := range connectorSet {
		connectorID = key
		break
	}
	if connectorID != "" {
		if currentSession, exists := server.dispatcher.sessionRegistry.GetByConnector(connectorID); exists {
			normalizedCurrentSessionID := strings.TrimSpace(currentSession.SessionID)
			normalizedCurrentConnectorID := strings.TrimSpace(currentSession.ConnectorID)
			if currentSession.State == registry.SessionActive &&
				normalizedCurrentSessionID != "" &&
				normalizedCurrentConnectorID == connectorID {
				return connectorID, normalizedCurrentSessionID, currentSession.Epoch, true
			}
		}
	}

	// 兜底从 ACTIVE 候选中选择“更高 epoch，其次更晚更新时间”的会话。
	best := candidates[0]
	for _, candidate := range candidates[1:] {
		if candidate.Epoch > best.Epoch {
			best = candidate
			continue
		}
		if candidate.Epoch < best.Epoch {
			continue
		}
		if candidate.UpdatedAt.After(best.UpdatedAt) {
			best = candidate
			continue
		}
		if candidate.UpdatedAt.Equal(best.UpdatedAt) && candidate.LastHeartbeat.After(best.LastHeartbeat) {
			best = candidate
		}
	}
	return strings.TrimSpace(best.ConnectorID), strings.TrimSpace(best.SessionID), best.Epoch, true
}

// watchAcceptedTunnelLifecycle 监听 tunnel 终止并在 idle/reserved 阶段做兜底回收。
func (server *controlPlaneServer) watchAcceptedTunnelLifecycle(
	tunnelID string,
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
) {
	if server == nil || rawTunnel == nil || server.dispatcher == nil || server.dispatcher.tunnelRegistry == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return
	}
	probeTicker := time.NewTicker(incomingTunnelProbeInterval)
	defer probeTicker.Stop()
	for {
		select {
		case <-rawTunnel.Done():
			server.recycleAcceptedIdleTunnel(normalizedTunnelID, rawTunnel, bindingType, rawTunnel.Err())
			return
		case <-probeTicker.C:
			runtimeSnapshot, exists := server.dispatcher.tunnelRegistry.Get(normalizedTunnelID)
			if !exists {
				return
			}
			// active tunnel 的生命周期由 connector dispatcher 收敛，避免与 dispatch 回收路径竞争。
			if runtimeSnapshot.State != registry.TunnelStateIdle && runtimeSnapshot.State != registry.TunnelStateReserved {
				continue
			}
			prober, supportsProbe := rawTunnel.(transport.TunnelHealthProber)
			if !supportsProbe {
				continue
			}
			probeContext, cancelProbe := context.WithTimeout(context.Background(), incomingTunnelProbeTimeout)
			probeErr := prober.Probe(probeContext)
			cancelProbe()
			if probeErr == nil ||
				errors.Is(probeErr, transport.ErrTimeout) ||
				errors.Is(probeErr, transport.ErrUnsupported) ||
				errors.Is(probeErr, context.Canceled) ||
				errors.Is(probeErr, context.DeadlineExceeded) {
				continue
			}
			server.recycleAcceptedIdleTunnel(normalizedTunnelID, rawTunnel, bindingType, probeErr)
			return
		}
	}
}

func (server *controlPlaneServer) recycleAcceptedIdleTunnel(
	tunnelID string,
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
	cause error,
) {
	if server == nil || rawTunnel == nil || server.dispatcher == nil || server.dispatcher.tunnelRegistry == nil {
		return
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	if normalizedTunnelID == "" {
		return
	}

	runtimeSnapshot, exists := server.dispatcher.tunnelRegistry.Get(normalizedTunnelID)
	if !exists {
		return
	}
	if runtimeSnapshot.State != registry.TunnelStateIdle && runtimeSnapshot.State != registry.TunnelStateReserved {
		return
	}

	lastError := "incoming_tunnel_closed"
	effectiveCause := cause
	if effectiveCause == nil {
		effectiveCause = rawTunnel.Err()
	}
	if effectiveCause != nil && !errors.Is(effectiveCause, transport.ErrClosed) {
		lastError = strings.TrimSpace(effectiveCause.Error())
		if lastError == "" {
			lastError = "incoming_tunnel_closed"
		}
	}
	if bindingType != "" {
		lastError = fmt.Sprintf("%s binding=%s", lastError, bindingType)
	}
	normalizedNow := time.Now().UTC()
	markErr := server.dispatcher.tunnelRegistry.MarkBroken(normalizedNow, normalizedTunnelID, lastError)
	if markErr != nil && !errors.Is(markErr, registry.ErrTunnelNotFound) && !errors.Is(markErr, registry.ErrInvalidTunnelStateTransition) {
		return
	}
	_, _ = server.dispatcher.tunnelRegistry.RemoveTerminal(normalizedTunnelID)
}

// classifyTCPInboundConnection 基于首 2 字节识别 TCP 连接是 control channel 还是 data tunnel。
func classifyTCPInboundConnection(rawConn net.Conn) (net.Conn, bool, error) {
	if rawConn == nil {
		return nil, false, errors.New("classify tcp inbound: nil connection")
	}
	peekBuffer := make([]byte, 2)
	if err := rawConn.SetReadDeadline(time.Now().UTC().Add(tcpConnectionClassifierReadTimeout)); err != nil {
		return nil, false, fmt.Errorf("classify tcp inbound: set read deadline: %w", err)
	}
	readSize, readErr := io.ReadFull(rawConn, peekBuffer)
	_ = rawConn.SetReadDeadline(time.Time{})
	if readErr != nil {
		if netErr, ok := readErr.(net.Error); ok && netErr.Timeout() && readSize == 0 {
			// tunnel 首帧通常由 Bridge 在分配后主动发送；若无首包可先按 tunnel 接入处理。
			return rawConn, false, nil
		}
		return nil, false, fmt.Errorf("classify tcp inbound: read prefix: %w", readErr)
	}
	frameType := binary.BigEndian.Uint16(peekBuffer)
	classifiedConn := &prefixedNetConn{
		Conn:   rawConn,
		prefix: append([]byte(nil), peekBuffer...),
	}
	return classifiedConn, isKnownControlFrameType(frameType), nil
}

// isKnownControlFrameType 判断帧类型是否属于控制面帧。
func isKnownControlFrameType(frameType uint16) bool {
	if frameType == transport.ControlFrameTypeHeartbeatPing || frameType == transport.ControlFrameTypeHeartbeatPong {
		return true
	}
	_, err := transport.ControlMessageTypeForFrameType(frameType)
	return err == nil
}

// acceptTCPConnectionWithContext 在支持 deadline 的 listener 上轮询 Accept，以响应 ctx 取消。
func acceptTCPConnectionWithContext(ctx context.Context, listener net.Listener, pollInterval time.Duration) (net.Conn, error) {
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	if listener == nil {
		return nil, errors.New("accept tcp connection: nil listener")
	}
	effectivePollInterval := pollInterval
	if effectivePollInterval <= 0 {
		effectivePollInterval = 200 * time.Millisecond
	}
	deadlineCapableListener, supportsDeadline := listener.(interface{ SetDeadline(time.Time) error })
	if !supportsDeadline {
		return listener.Accept()
	}
	for {
		acceptDeadline := time.Now().UTC().Add(effectivePollInterval)
		if contextDeadline, hasDeadline := normalizedContext.Deadline(); hasDeadline && contextDeadline.Before(acceptDeadline) {
			acceptDeadline = contextDeadline
		}
		if err := deadlineCapableListener.SetDeadline(acceptDeadline); err != nil {
			return nil, fmt.Errorf("accept tcp connection: set deadline: %w", err)
		}
		connection, acceptErr := listener.Accept()
		if acceptErr == nil {
			_ = deadlineCapableListener.SetDeadline(time.Time{})
			return connection, nil
		}
		if normalizedContext.Err() != nil {
			_ = deadlineCapableListener.SetDeadline(time.Time{})
			return nil, normalizedContext.Err()
		}
		if netErr, ok := acceptErr.(net.Error); ok && netErr.Timeout() {
			// 超时只用于轮询 ctx 和 listener 关闭信号，不视为真正错误。
			continue
		}
		return nil, acceptErr
	}
}

// prefixedNetConn 在底层连接前拼接一段已读前缀，便于连接类型判别后回放首包。
type prefixedNetConn struct {
	net.Conn
	prefix []byte
}

// Read 优先消费前缀缓存，再回退到底层连接读取。
func (conn *prefixedNetConn) Read(payload []byte) (int, error) {
	if conn == nil {
		return 0, net.ErrClosed
	}
	if len(conn.prefix) == 0 {
		return conn.Conn.Read(payload)
	}
	writtenSize := copy(payload, conn.prefix)
	conn.prefix = conn.prefix[writtenSize:]
	return writtenSize, nil
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
