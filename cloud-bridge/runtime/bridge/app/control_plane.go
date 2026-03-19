package app

import (
	"context"
	"crypto/tls"
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
	"time"

	appauth "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/auth"
	bridgecontrol "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/control"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/ingress/hostderiver"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	apptls "github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/tls"
	"github.com/lifei6671/devbridge-loop/ltfp/consistency"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
	"google.golang.org/grpc"
	"google.golang.org/grpc/peer"
)

const (
	defaultHeartbeatReplyTimeout = 2 * time.Second
	// connectorHeartbeatIntervalSec 定义握手返回给 Agent 的建议 heartbeat 间隔。
	connectorHeartbeatIntervalSec = 5
	// defaultWelcomeBinding 作为握手阶段 selected_binding 的默认值。
	defaultWelcomeBinding = "tcp_framed"
	// tcpConnectionClassifierReadTimeout 定义 TCP 入站连接类型判别的首包读取超时。
	tcpConnectionClassifierReadTimeout = 2 * time.Second
	// incomingTunnelProbeInterval 定义入站 tunnel 生命周期探测间隔，兜底处理远端静默断开。
	incomingTunnelProbeInterval = 250 * time.Millisecond
	// incomingTunnelProbeTimeout 定义单次探测超时时间，避免阻塞生命周期协程。
	incomingTunnelProbeTimeout = 120 * time.Millisecond
	// incomingTunnelOwnerResolveWait 定义入站 tunnel 等待 owner 会话对账的窗口。
	incomingTunnelOwnerResolveWait = 5 * time.Second
	// incomingTCPTunnelHandshakeTimeout 定义 TCP 入站读取首帧 tunnel 握手超时。
	incomingTCPTunnelHandshakeTimeout = 2 * time.Second
)

// controlChannelLifecycleState 描述控制连接在 Bridge 侧的运行阶段。
type controlChannelLifecycleState string

const (
	// controlChannelStateConnecting 表示连接刚创建，尚未进入控制面可读写阶段。
	controlChannelStateConnecting controlChannelLifecycleState = "connecting"
	// controlChannelStateConnected 表示底层连接已建立。
	controlChannelStateConnected controlChannelLifecycleState = "connected"
	// controlChannelStateControlReady 表示控制通道已就绪，可处理握手消息。
	controlChannelStateControlReady controlChannelLifecycleState = "control_ready"
	// controlChannelStateAuthenticated 表示连接已完成 ConnectorAuthAck(success=true)。
	controlChannelStateAuthenticated controlChannelLifecycleState = "authenticated"
	// controlChannelStateDraining 表示连接进入排空阶段，不再接受新业务语义。
	controlChannelStateDraining controlChannelLifecycleState = "draining"
	// controlChannelStateClosed 表示连接已关闭并完成收尾。
	controlChannelStateClosed controlChannelLifecycleState = "closed"
	// controlChannelStateFailed 表示连接异常失败，需要触发失败收敛。
	controlChannelStateFailed controlChannelLifecycleState = "failed"
)

var controlChannelLifecycleTransitions = map[controlChannelLifecycleState]map[controlChannelLifecycleState]struct{}{
	controlChannelStateConnecting: {
		controlChannelStateConnected: {},
		controlChannelStateFailed:    {},
		controlChannelStateClosed:    {},
	},
	controlChannelStateConnected: {
		controlChannelStateControlReady: {},
		controlChannelStateFailed:       {},
		controlChannelStateClosed:       {},
	},
	controlChannelStateControlReady: {
		controlChannelStateAuthenticated: {},
		controlChannelStateFailed:        {},
		controlChannelStateClosed:        {},
	},
	controlChannelStateAuthenticated: {
		controlChannelStateDraining: {},
		controlChannelStateFailed:   {},
		controlChannelStateClosed:   {},
	},
	controlChannelStateDraining: {
		controlChannelStateClosed: {},
		controlChannelStateFailed: {},
	},
	controlChannelStateFailed: {
		controlChannelStateClosed: {},
	},
	controlChannelStateClosed: {},
}

// controlMessageDispatcher 负责把控制面业务帧分发给 Bridge 控制处理器。
type controlMessageDispatcher struct {
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	authCoordinator       appauth.Coordinator
	handshakeGuard        appauth.HandshakeGuard
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
	tlsMode               string
	metrics               *obs.Metrics
	publishHandler        *bridgecontrol.PublishHandler
	healthHandler         *bridgecontrol.HealthHandler
	tunnelHandler         *bridgecontrol.TunnelReportHandler
	routeHandler          *bridgecontrol.RouteHandler
	sessionHandler        *bridgecontrol.SessionHandler
}

// controlChannelSessionState 保存单条控制连接最近确认的 session 上下文。
type controlChannelSessionState struct {
	sessionID    string
	sessionEpoch uint64
	connectorID  string
	// lifecycle 显式记录连接生命周期，收敛到 connecting->connected->control_ready->authenticated->draining/closed/failed。
	lifecycle controlChannelLifecycleState
	// sourceIP 保存连接建立时提取出的源地址，供认证审计直接复用。
	sourceIP string
	// assignedSessionEpoch 是 Welcome 阶段预分配给本连接的候选 epoch。
	assignedSessionEpoch uint64
	// helloAccepted 标记当前连接是否已完成 ConnectorHello 阶段。
	helloAccepted bool
	// unauthConnectionReserved 标记当前连接是否已占用未认证连接预算。
	unauthConnectionReserved bool
}

// newControlChannelSessionState 创建控制连接上下文并预先归一化 source_ip。
func newControlChannelSessionState(peerAddr string) *controlChannelSessionState {
	return &controlChannelSessionState{
		lifecycle: controlChannelStateConnecting,
		// 连接建立时就提取 source_ip，避免后续每次认证重复解析地址。
		sourceIP: appauth.NormalizeSourceIP(peerAddr),
	}
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

// setHelloContext 在 ConnectorHello 通过后记录连接上下文。
func (state *controlChannelSessionState) setHelloContext(connectorID string, assignedSessionEpoch uint64) {
	if state == nil {
		return
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" || assignedSessionEpoch == 0 {
		return
	}
	state.connectorID = normalizedConnectorID
	state.assignedSessionEpoch = assignedSessionEpoch
	state.helloAccepted = true
	// 进入 hello 阶段前，连接至少已经准备好处理控制面握手消息。
	state.markControlReady()
}

// transitionLifecycle 按状态机规则推进连接生命周期。
func (state *controlChannelSessionState) transitionLifecycle(nextState controlChannelLifecycleState) bool {
	if state == nil {
		return false
	}
	if state.lifecycle == nextState {
		return true
	}
	allowedTransitions, exists := controlChannelLifecycleTransitions[state.lifecycle]
	if !exists {
		return false
	}
	if _, allowed := allowedTransitions[nextState]; !allowed {
		return false
	}
	state.lifecycle = nextState
	return true
}

// markConnected 把连接状态推进到 connected。
func (state *controlChannelSessionState) markConnected() {
	if state == nil {
		return
	}
	_ = state.transitionLifecycle(controlChannelStateConnected)
}

// markControlReady 把连接状态推进到 control_ready。
func (state *controlChannelSessionState) markControlReady() {
	if state == nil {
		return
	}
	state.markConnected()
	_ = state.transitionLifecycle(controlChannelStateControlReady)
}

// markAuthenticated 把连接状态推进到 authenticated。
func (state *controlChannelSessionState) markAuthenticated() {
	if state == nil {
		return
	}
	state.markControlReady()
	_ = state.transitionLifecycle(controlChannelStateAuthenticated)
}

// markDraining 把连接状态推进到 draining。
func (state *controlChannelSessionState) markDraining() {
	if state == nil {
		return
	}
	if state.lifecycle == controlChannelStateFailed || state.lifecycle == controlChannelStateClosed {
		return
	}
	if state.lifecycle != controlChannelStateAuthenticated {
		// 只有认证后的连接才能进入 draining，未认证连接直接保持原状态。
		return
	}
	_ = state.transitionLifecycle(controlChannelStateDraining)
}

// markFailed 把连接状态推进到 failed。
func (state *controlChannelSessionState) markFailed() {
	if state == nil || state.lifecycle == controlChannelStateClosed {
		return
	}
	_ = state.transitionLifecycle(controlChannelStateFailed)
}

// markClosed 把连接状态推进到 closed。
func (state *controlChannelSessionState) markClosed() {
	if state == nil {
		return
	}
	if state.lifecycle == controlChannelStateAuthenticated {
		_ = state.transitionLifecycle(controlChannelStateDraining)
	}
	_ = state.transitionLifecycle(controlChannelStateClosed)
}

// isAuthenticated 判断连接是否已经完成认证并可处理业务消息。
func (state *controlChannelSessionState) isAuthenticated() bool {
	if state == nil {
		return false
	}
	return state.lifecycle == controlChannelStateAuthenticated
}

// controlMessageDispatcherOptions 定义控制面分发器依赖。
type controlMessageDispatcherOptions struct {
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	authCoordinator       appauth.Coordinator
	handshakeGuard        appauth.HandshakeGuard
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
	hostDerivationDomain  string
	tlsMode               string
	metrics               *obs.Metrics
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
	authCoordinator := options.authCoordinator
	if authCoordinator == nil {
		// 未注入认证协调器时使用默认实现，保证控制面握手可直接落地。
		authCoordinator = appauth.NewCoordinator(appauth.CoordinatorOptions{
			SessionRegistry: sessionRegistry,
			Metrics:         options.metrics,
		})
	}
	metrics := options.metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	handshakeGuard := options.handshakeGuard
	if handshakeGuard == nil {
		handshakeGuard = appauth.NewHandshakeGuard(appauth.HandshakeGuardOptions{})
	}
	var hostDeriver bridgecontrol.HostDeriver
	if strings.TrimSpace(options.hostDerivationDomain) != "" {
		hostDeriver = hostderiver.New(options.hostDerivationDomain, metrics)
	}
	eventGuard := consistency.NewResourceEventGuard(4096)
	sessionHandler := bridgecontrol.NewSessionHandler(bridgecontrol.SessionHandlerOptions{
		SessionRegistry: sessionRegistry,
		ServiceRegistry: serviceRegistry,
		RouteRegistry:   routeRegistry,
		Guard:           eventGuard,
	})
	normalizedTLSMode, err := apptls.NormalizeMode(options.tlsMode)
	if err != nil {
		normalizedTLSMode = apptls.ModePlaintext
	}
	return &controlMessageDispatcher{
		sessionRegistry:       sessionRegistry,
		serviceRegistry:       serviceRegistry,
		routeRegistry:         routeRegistry,
		tunnelRegistry:        tunnelRegistry,
		authCoordinator:       authCoordinator,
		handshakeGuard:        handshakeGuard,
		tunnelPoolReportStore: options.tunnelPoolReportStore,
		tlsMode:               string(normalizedTLSMode),
		metrics:               metrics,
		publishHandler: bridgecontrol.NewPublishHandler(bridgecontrol.PublishHandlerOptions{
			Guard:           eventGuard,
			SessionRegistry: sessionRegistry,
			ServiceRegistry: serviceRegistry,
			Metrics:         metrics,
			HostDeriver:     hostDeriver,
		}),
		healthHandler: bridgecontrol.NewHealthHandler(bridgecontrol.HealthHandlerOptions{
			SessionRegistry: sessionRegistry,
			ServiceRegistry: serviceRegistry,
			Metrics:         metrics,
		}),
		tunnelHandler: bridgecontrol.NewTunnelReportHandler(bridgecontrol.TunnelReportHandlerOptions{
			SessionRegistry: sessionRegistry,
			TunnelRegistry:  tunnelRegistry,
			ReportStore:     options.tunnelPoolReportStore,
		}),
		routeHandler: bridgecontrol.NewRouteHandler(bridgecontrol.RouteHandlerOptions{
			Guard:           eventGuard,
			SessionRegistry: sessionRegistry,
			ServiceRegistry: serviceRegistry,
			RouteRegistry:   routeRegistry,
			Metrics:         metrics,
			HostDeriver:     hostDeriver,
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
	if envelope.MessageType == pb.ControlMessageConnectorHello || envelope.MessageType == pb.ControlMessageConnectorAuth {
		// 握手消息在 frame 层直接处理，避免与资源消息分发逻辑混淆。
		replyEnvelope, handshakeErr := dispatcher.handleHandshakeEnvelope(envelope, sessionState)
		if handshakeErr != nil {
			return nil, transport.ControlMessagePriorityNormal, handshakeErr
		}
		replyFrame, encodeErr := transport.EncodeBusinessControlEnvelopeFrame(*replyEnvelope)
		if encodeErr != nil {
			return nil, transport.ControlMessagePriorityNormal, fmt.Errorf("encode handshake reply failed: %w", encodeErr)
		}
		return &replyFrame, transport.RecommendControlFramePriority(replyFrame.Type), nil
	}
	if sessionState != nil && !sessionState.isAuthenticated() {
		// 认证成功前拒绝所有业务控制消息，避免未认证连接污染运行态。
		// 这里不返回错误，避免 serve 循环直接断链触发无意义重连风暴。
		slog.Warn(
			"reject unauthenticated control message",
			"message_type", envelope.MessageType,
			"connector_id", strings.TrimSpace(envelope.ConnectorID),
			"request_id", strings.TrimSpace(envelope.RequestID),
		)
		return nil, transport.ControlMessagePriorityNormal, nil
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

// handleHandshakeEnvelope 处理 ConnectorHello/ConnectorAuth 握手消息并返回响应。
func (dispatcher *controlMessageDispatcher) handleHandshakeEnvelope(
	envelope pb.ControlEnvelope,
	sessionState *controlChannelSessionState,
) (*pb.ControlEnvelope, error) {
	if dispatcher == nil {
		return nil, errors.New("handshake dispatcher is nil")
	}
	switch envelope.MessageType {
	case pb.ControlMessageConnectorHello:
		return dispatcher.handleConnectorHelloEnvelope(envelope, sessionState)
	case pb.ControlMessageConnectorAuth:
		return dispatcher.handleConnectorAuthEnvelope(envelope, sessionState)
	default:
		return nil, fmt.Errorf("unsupported handshake message type: %s", envelope.MessageType)
	}
}

// handleConnectorHelloEnvelope 校验并应答 ConnectorHello。
func (dispatcher *controlMessageDispatcher) handleConnectorHelloEnvelope(
	envelope pb.ControlEnvelope,
	sessionState *controlChannelSessionState,
) (*pb.ControlEnvelope, error) {
	var helloPayload pb.ConnectorHello
	if err := decodeControlPayload(envelope.Payload, &helloPayload); err != nil {
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"decode connector hello payload failed",
		)
	}
	if err := validate.ValidateConnectorHello(helloPayload); err != nil {
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"invalid connector hello payload",
		)
	}
	normalizedConnectorID := strings.TrimSpace(helloPayload.ConnectorID)
	if normalizedConnectorID == "" {
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"connector_id is required",
		)
	}
	if envelopeConnectorID := strings.TrimSpace(envelope.ConnectorID); envelopeConnectorID != "" && envelopeConnectorID != normalizedConnectorID {
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorConnectorMismatch,
			"connector_id mismatch between envelope and hello payload",
		)
	}
	peerSourceIP := ""
	if sessionState != nil {
		peerSourceIP = strings.TrimSpace(sessionState.sourceIP)
	}
	if allowed, dimension := dispatcher.allowConnectorHello(peerSourceIP, normalizedConnectorID); !allowed {
		slog.Warn(
			"connector hello rate limited",
			"connector_id", normalizedConnectorID,
			"source_ip", peerSourceIP,
			"dimension", dimension,
		)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorRateLimited,
			"authentication rejected",
		)
	}

	assignedSessionEpoch := dispatcher.allocateAssignedSessionEpoch(normalizedConnectorID)
	if sessionState != nil {
		// 记录握手上下文，为后续 ConnectorAuth 提交阶段做约束。
		sessionState.setHelloContext(normalizedConnectorID, assignedSessionEpoch)
	}

	welcomePayload := pb.ConnectorWelcome{
		SelectedBinding:      selectWelcomeBinding(helloPayload.SupportedBindings),
		VersionMajor:         envelope.VersionMajor,
		VersionMinor:         envelope.VersionMinor,
		HeartbeatIntervalSec: connectorHeartbeatIntervalSec,
		AssignedSessionEpoch: assignedSessionEpoch,
		TLSMode:              strings.TrimSpace(dispatcher.tlsMode),
	}
	return buildConnectorWelcomeEnvelope(envelope, normalizedConnectorID, welcomePayload)
}

// handleConnectorAuthEnvelope 校验并应答 ConnectorAuth。
func (dispatcher *controlMessageDispatcher) handleConnectorAuthEnvelope(
	envelope pb.ControlEnvelope,
	sessionState *controlChannelSessionState,
) (*pb.ControlEnvelope, error) {
	auditConnectorID := strings.TrimSpace(envelope.ConnectorID)
	auditSessionEpoch := uint64(0)
	auditSourceIP := ""
	if sessionState != nil {
		if normalizedConnectorID := strings.TrimSpace(sessionState.connectorID); normalizedConnectorID != "" {
			auditConnectorID = normalizedConnectorID
		}
		auditSessionEpoch = sessionState.assignedSessionEpoch
		auditSourceIP = strings.TrimSpace(sessionState.sourceIP)
	}
	var authPayload pb.ConnectorAuth
	emitAuditReject := func(errorCode string, sessionID string, sessionEpoch uint64) {
		appauth.EmitAuthAuditLog(false, appauth.AuditRecord{
			ConnectorID:  auditConnectorID,
			TokenID:      appauth.ExtractTokenIDForAudit(authPayload.Token),
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			SourceIP:     auditSourceIP,
			ErrorCode:    errorCode,
		})
	}
	if sessionState == nil || !sessionState.helloAccepted {
		emitAuditReject(appauth.AuthErrorInternal, "", auditSessionEpoch)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"connector_welcome is required before connector_auth",
		)
	}
	if err := decodeControlPayload(envelope.Payload, &authPayload); err != nil {
		emitAuditReject(appauth.AuthErrorInternal, "", auditSessionEpoch)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"decode connector auth payload failed",
		)
	}
	normalizedConnectorID := strings.TrimSpace(sessionState.connectorID)
	if normalizedConnectorID == "" {
		emitAuditReject(appauth.AuthErrorConnectorMismatch, "", auditSessionEpoch)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorConnectorMismatch,
			"connector_id is missing in handshake context",
		)
	}
	// Hello 阶段已锁定 connector_id，后续审计统一以该权威值输出。
	auditConnectorID = normalizedConnectorID
	if envelopeConnectorID := strings.TrimSpace(envelope.ConnectorID); envelopeConnectorID != "" && envelopeConnectorID != normalizedConnectorID {
		emitAuditReject(appauth.AuthErrorConnectorMismatch, "", auditSessionEpoch)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorConnectorMismatch,
			"connector_id mismatch between hello and auth",
		)
	}
	if dispatcher == nil || dispatcher.authCoordinator == nil {
		emitAuditReject(appauth.AuthErrorInternal, "", auditSessionEpoch)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorInternal,
			"auth coordinator is not initialized",
		)
	}
	if banned, dimension, banUntil := dispatcher.isConnectorAuthBanned(auditSourceIP, normalizedConnectorID); banned {
		emitAuditReject(appauth.AuthErrorRateLimited, "", auditSessionEpoch)
		slog.Warn(
			"connector auth rejected by short-time ban",
			"connector_id", normalizedConnectorID,
			"source_ip", auditSourceIP,
			"dimension", dimension,
			"ban_until", banUntil.Format(time.RFC3339),
		)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorRateLimited,
			"authentication rejected",
		)
	}
	if !dispatcher.acquireAuthConcurrencyBudget() {
		emitAuditReject(appauth.AuthErrorRateLimited, "", auditSessionEpoch)
		slog.Warn(
			"connector auth rejected by concurrency budget",
			"connector_id", normalizedConnectorID,
			"source_ip", auditSourceIP,
		)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			appauth.AuthErrorRateLimited,
			"authentication rejected",
		)
	}
	defer dispatcher.releaseAuthConcurrencyBudget()
	authResult := dispatcher.authCoordinator.AuthenticateAndCommit(
		appauth.Request{
			ConnectorID:          normalizedConnectorID,
			AssignedSessionEpoch: sessionState.assignedSessionEpoch,
			AuthMethod:           authPayload.AuthMethod,
			Token:                authPayload.Token,
		},
		func(now time.Time, sessionRuntime registry.SessionRuntime) error {
			dispatcher.commitAuthenticatedSession(now, sessionRuntime, envelope.ResourceVersion)
			return nil
		},
	)
	if !authResult.Success {
		if appauth.ShouldCountForAuthFailureBan(authResult.ErrorCode) {
			if banned, dimension, banUntil := dispatcher.recordConnectorAuthFailure(auditSourceIP, normalizedConnectorID); banned {
				slog.Warn(
					"connector auth short-time ban activated",
					"connector_id", normalizedConnectorID,
					"source_ip", auditSourceIP,
					"dimension", dimension,
					"ban_until", banUntil.Format(time.RFC3339),
				)
			}
		}
		publicErrorCode, publicErrorMessage := appauth.NormalizePublicAuthReject(authResult.ErrorCode, authResult.ErrorMessage)
		emitAuditReject(authResult.ErrorCode, "", auditSessionEpoch)
		slog.Warn(
			"connector auth rejected",
			"connector_id", normalizedConnectorID,
			"assigned_session_epoch", sessionState.assignedSessionEpoch,
			"error_code", authResult.ErrorCode,
			"error_message", authResult.ErrorMessage,
			"public_error_code", publicErrorCode,
		)
		return buildConnectorAuthAckEnvelope(
			envelope,
			false,
			"",
			0,
			publicErrorCode,
			publicErrorMessage,
		)
	}
	// 认证成功后更新连接上下文，供 heartbeat 与后续资源消息复用。
	sessionState.setSession(authResult.SessionID, authResult.SessionEpoch)
	dispatcher.markSessionAuthenticated(sessionState)
	appauth.EmitAuthAuditLog(true, appauth.AuditRecord{
		ConnectorID:  normalizedConnectorID,
		TokenID:      appauth.ExtractTokenIDForAudit(authPayload.Token),
		SessionID:    authResult.SessionID,
		SessionEpoch: authResult.SessionEpoch,
		SourceIP:     auditSourceIP,
		ErrorCode:    "",
	})
	slog.Info(
		"connector auth committed",
		"connector_id", normalizedConnectorID,
		"session_id", authResult.SessionID,
		"session_epoch", authResult.SessionEpoch,
	)

	return buildConnectorAuthAckEnvelope(
		envelope,
		true,
		authResult.SessionID,
		authResult.SessionEpoch,
		"",
		"",
	)
}

// allocateAssignedSessionEpoch 生成同 connector 的下一候选 epoch。
func (dispatcher *controlMessageDispatcher) allocateAssignedSessionEpoch(connectorID string) uint64 {
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" || dispatcher == nil || dispatcher.sessionRegistry == nil {
		return 1
	}
	if sessionRuntime, exists := dispatcher.sessionRegistry.GetByConnector(normalizedConnectorID); exists {
		return sessionRuntime.Epoch + 1
	}
	return 1
}

// selectWelcomeBinding 从客户端支持列表中选择握手返回 binding。
func selectWelcomeBinding(supportedBindings []string) string {
	if len(supportedBindings) == 0 {
		return defaultWelcomeBinding
	}
	for _, binding := range supportedBindings {
		normalizedBinding := strings.TrimSpace(binding)
		if normalizedBinding != "" {
			return normalizedBinding
		}
	}
	return defaultWelcomeBinding
}

// allowConnectorHello 执行 Hello 阶段 source_ip/connector_id 双维限流判定。
func (dispatcher *controlMessageDispatcher) allowConnectorHello(sourceIP string, connectorID string) (bool, string) {
	if dispatcher == nil || dispatcher.handshakeGuard == nil {
		return true, ""
	}
	allowed, dimension := dispatcher.handshakeGuard.AllowHello(sourceIP, connectorID)
	if allowed {
		return true, ""
	}
	dispatcher.observeAuthRateLimit()
	return false, dimension
}

// isConnectorAuthBanned 检查认证失败封禁是否命中。
func (dispatcher *controlMessageDispatcher) isConnectorAuthBanned(
	sourceIP string,
	connectorID string,
) (bool, string, time.Time) {
	if dispatcher == nil || dispatcher.handshakeGuard == nil {
		return false, "", time.Time{}
	}
	banned, dimension, banUntil := dispatcher.handshakeGuard.IsAuthBanned(sourceIP, connectorID)
	if banned {
		dispatcher.observeAuthRateLimit()
	}
	return banned, dimension, banUntil
}

// recordConnectorAuthFailure 记录认证失败并在达到阈值时激活短时封禁。
func (dispatcher *controlMessageDispatcher) recordConnectorAuthFailure(
	sourceIP string,
	connectorID string,
) (bool, string, time.Time) {
	if dispatcher == nil || dispatcher.handshakeGuard == nil {
		return false, "", time.Time{}
	}
	return dispatcher.handshakeGuard.RecordAuthFailure(sourceIP, connectorID)
}

// acquireAuthConcurrencyBudget 尝试占用认证并发预算。
func (dispatcher *controlMessageDispatcher) acquireAuthConcurrencyBudget() bool {
	if dispatcher == nil || dispatcher.handshakeGuard == nil {
		return true
	}
	allowed := dispatcher.handshakeGuard.TryAcquireAuthConcurrency()
	if !allowed {
		dispatcher.observeAuthRateLimit()
	}
	return allowed
}

// releaseAuthConcurrencyBudget 释放认证并发预算。
func (dispatcher *controlMessageDispatcher) releaseAuthConcurrencyBudget() {
	if dispatcher == nil || dispatcher.handshakeGuard == nil {
		return
	}
	dispatcher.handshakeGuard.ReleaseAuthConcurrency()
}

// reserveUnauthenticatedConnectionBudget 尝试占用未认证连接预算。
func (dispatcher *controlMessageDispatcher) reserveUnauthenticatedConnectionBudget(sessionState *controlChannelSessionState) bool {
	if dispatcher == nil || dispatcher.handshakeGuard == nil || sessionState == nil {
		return true
	}
	if sessionState.unauthConnectionReserved || sessionState.isAuthenticated() {
		return true
	}
	allowed := dispatcher.handshakeGuard.TryAcquireUnauthenticatedConnection()
	if !allowed {
		dispatcher.observeAuthRateLimit()
		return false
	}
	sessionState.unauthConnectionReserved = true
	return true
}

// releaseUnauthenticatedConnectionBudget 释放未认证连接预算。
func (dispatcher *controlMessageDispatcher) releaseUnauthenticatedConnectionBudget(sessionState *controlChannelSessionState) {
	if dispatcher == nil || dispatcher.handshakeGuard == nil || sessionState == nil {
		return
	}
	if !sessionState.unauthConnectionReserved {
		return
	}
	dispatcher.handshakeGuard.ReleaseUnauthenticatedConnection()
	sessionState.unauthConnectionReserved = false
}

// markSessionAuthenticated 在认证成功时更新连接状态并释放未认证预算占用。
func (dispatcher *controlMessageDispatcher) markSessionAuthenticated(sessionState *controlChannelSessionState) {
	if sessionState == nil {
		return
	}
	sessionState.markAuthenticated()
	dispatcher.releaseUnauthenticatedConnectionBudget(sessionState)
}

// markSessionFailedFromState 在控制连接异常退出时把会话标记为 FAILED 并触发清理。
func (dispatcher *controlMessageDispatcher) markSessionFailedFromState(
	now time.Time,
	sessionState *controlChannelSessionState,
	reason string,
) {
	if dispatcher == nil || sessionState == nil || !sessionState.isAuthenticated() {
		return
	}
	sessionState.markFailed()
	dispatcher.transitionSessionState(
		now,
		sessionState.sessionID,
		sessionState.sessionEpoch,
		registry.SessionFailed,
		reason,
	)
}

// closeSessionFromState 在连接正常收尾时把会话标记为 CLOSED 并触发清理。
func (dispatcher *controlMessageDispatcher) closeSessionFromState(
	now time.Time,
	sessionState *controlChannelSessionState,
	reason string,
) {
	if dispatcher == nil || sessionState == nil || !sessionState.isAuthenticated() {
		return
	}
	sessionState.markDraining()
	sessionState.markClosed()
	dispatcher.transitionSessionState(
		now,
		sessionState.sessionID,
		sessionState.sessionEpoch,
		registry.SessionClosed,
		reason,
	)
}

// observeAuthRateLimit 统一记录限流/预算拒绝指标，便于告警规则复用。
func (dispatcher *controlMessageDispatcher) observeAuthRateLimit() {
	if dispatcher == nil || dispatcher.metrics == nil {
		return
	}
	dispatcher.metrics.ObserveBridgeAuthFailure(appauth.AuthErrorRateLimited)
	dispatcher.metrics.IncBridgeAuthRateLimitTotal()
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
		reportTrigger := strings.TrimSpace(message.Trigger)
		if reportTrigger == "event:tunnel_active" {
			agentConnectedCount := message.IdleCount + message.InUseCount
			if agentConnectedCount < 0 {
				agentConnectedCount = 0
			}
			slog.Info(
				"bridge observe tunnel_active report",
				"connector_id", reportConnectorID,
				"session_id", reportSessionID,
				"session_epoch", reportSessionEpoch,
				"agent_idle_count", message.IdleCount,
				"agent_in_use_count", message.InUseCount,
				"agent_connected_count", agentConnectedCount,
				"agent_target_idle_count", message.TargetIdleCount,
			)
		}
		slog.Info(
			"bridge receive tunnel pool report",
			"connector_id", reportConnectorID,
			"session_id", reportSessionID,
			"session_epoch", reportSessionEpoch,
			"idle_count", message.IdleCount,
			"in_use_count", message.InUseCount,
			"target_idle_count", message.TargetIdleCount,
			"trigger", reportTrigger,
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
				"trigger", reportTrigger,
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
		case registry.SessionDraining, registry.SessionStale, registry.SessionFailed, registry.SessionClosed:
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
	dispatcher.commitAuthenticatedSession(now, registry.SessionRuntime{
		SessionID:     normalizedSessionID,
		ConnectorID:   strings.TrimSpace(connectorID),
		Epoch:         sessionEpoch,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	}, resourceVersion)
}

// commitAuthenticatedSession 原子提交当前 connector 的权威会话，并收敛旧会话副作用。
func (dispatcher *controlMessageDispatcher) commitAuthenticatedSession(
	now time.Time,
	sessionRuntime registry.SessionRuntime,
	resourceVersion uint64,
) {
	if dispatcher == nil || dispatcher.sessionHandler == nil || dispatcher.sessionRegistry == nil {
		return
	}
	normalizedConnectorID := strings.TrimSpace(sessionRuntime.ConnectorID)
	sessionRuntime.SessionID = strings.TrimSpace(sessionRuntime.SessionID)
	if sessionRuntime.SessionID == "" || sessionRuntime.Epoch == 0 {
		return
	}
	if normalizedConnectorID == "" {
		if existingSession, exists := dispatcher.sessionRegistry.GetBySession(sessionRuntime.SessionID); exists {
			normalizedConnectorID = strings.TrimSpace(existingSession.ConnectorID)
		}
	}
	sessionRuntime.ConnectorID = normalizedConnectorID
	sessionRuntime.State = registry.SessionActive
	if sessionRuntime.LastHeartbeat.IsZero() {
		sessionRuntime.LastHeartbeat = now
	}
	commitResult, committed := dispatcher.sessionRegistry.CommitAuthoritative(now, sessionRuntime)
	if !committed {
		return
	}
	if commitResult.PreviousStateChanged {
		dispatcher.applySessionLifecycleEffects(now, commitResult.PreviousSession, "session_epoch_takeover")
	}
	dispatcher.sessionHandler.MarkReconnectBaseline(
		commitResult.CurrentSession.SessionID,
		commitResult.CurrentSession.Epoch,
		resourceVersion,
	)
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
		shouldMarkConnectorInactive := normalizedReason == "session_epoch_takeover" ||
			dispatcher.isCurrentConnectorSession(sessionRuntime)
		if dispatcher.serviceRegistry != nil && shouldMarkConnectorInactive {
			affectedServiceIDs := dispatcher.serviceRegistry.ListLogicalServiceIDsByRuntime(
				sessionRuntime.ConnectorID,
				"",
			)
			// takeover 场景下旧 session 已不再是当前权威，但旧实例仍需立即摘流。
			// 优先按 connector+session 收敛，避免同 connector 新会话实例被误摘流。
			dispatcher.serviceRegistry.MarkLifecycleByConnectorAndSession(
				now,
				sessionRuntime.ConnectorID,
				sessionRuntime.SessionID,
				pb.ServiceStatusInactive,
				pb.HealthStatusUnknown,
			)
			// 生命周期收敛后回刷可用实例快照，保证服务池可用数实时更新。
			bridgecontrol.RefreshServiceAvailabilityMetricsByServiceIDs(
				dispatcher.metrics,
				dispatcher.serviceRegistry,
				affectedServiceIDs,
			)
		}
		if dispatcher.tunnelRegistry != nil {
			dispatcher.tunnelRegistry.PurgeBySession(now, sessionRuntime.SessionID, "session_draining:"+normalizedReason)
		}
	case registry.SessionStale, registry.SessionFailed, registry.SessionClosed:
		if dispatcher.tunnelPoolReportStore != nil {
			dispatcher.tunnelPoolReportStore.RemoveBySession(sessionRuntime.SessionID, sessionRuntime.Epoch)
		}
		if dispatcher.serviceRegistry != nil && dispatcher.isCurrentConnectorSession(sessionRuntime) {
			affectedServiceIDs := dispatcher.serviceRegistry.ListLogicalServiceIDsByRuntime(
				sessionRuntime.ConnectorID,
				"",
			)
			// STALE/FAILED/CLOSED 服务仅保留审计价值，不再参与路由解析。
			dispatcher.serviceRegistry.MarkLifecycleByConnectorAndSession(
				now,
				sessionRuntime.ConnectorID,
				sessionRuntime.SessionID,
				pb.ServiceStatusStale,
				pb.HealthStatusUnknown,
			)
			// 终态会话收敛后同步刷新可用实例快照，避免保留过期可用状态。
			bridgecontrol.RefreshServiceAvailabilityMetricsByServiceIDs(
				dispatcher.metrics,
				dispatcher.serviceRegistry,
				affectedServiceIDs,
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

// buildConnectorWelcomeEnvelope 构造 ConnectorWelcome 响应。
func buildConnectorWelcomeEnvelope(
	requestEnvelope pb.ControlEnvelope,
	connectorID string,
	welcomePayload pb.ConnectorWelcome,
) (*pb.ControlEnvelope, error) {
	encodedPayload, err := json.Marshal(welcomePayload)
	if err != nil {
		return nil, fmt.Errorf("marshal connector welcome payload failed: %w", err)
	}
	versionMajor := requestEnvelope.VersionMajor
	if versionMajor == 0 {
		versionMajor = 2
	}
	versionMinor := requestEnvelope.VersionMinor
	if versionMinor == 0 {
		versionMinor = 1
	}
	return &pb.ControlEnvelope{
		VersionMajor: versionMajor,
		VersionMinor: versionMinor,
		MessageType:  pb.ControlMessageConnectorWelcome,
		RequestID:    requestEnvelope.RequestID,
		ConnectorID:  strings.TrimSpace(connectorID),
		Payload:      encodedPayload,
	}, nil
}

// buildConnectorAuthAckEnvelope 构造 ConnectorAuthAck 响应。
func buildConnectorAuthAckEnvelope(
	requestEnvelope pb.ControlEnvelope,
	success bool,
	sessionID string,
	sessionEpoch uint64,
	errorCode string,
	errorMessage string,
) (*pb.ControlEnvelope, error) {
	authAckPayload := pb.ConnectorAuthAck{
		Success:      success,
		SessionID:    strings.TrimSpace(sessionID),
		SessionEpoch: sessionEpoch,
		ErrorCode:    strings.TrimSpace(errorCode),
		ErrorMessage: strings.TrimSpace(errorMessage),
	}
	encodedPayload, err := json.Marshal(authAckPayload)
	if err != nil {
		return nil, fmt.Errorf("marshal connector auth ack payload failed: %w", err)
	}
	versionMajor := requestEnvelope.VersionMajor
	if versionMajor == 0 {
		versionMajor = 2
	}
	versionMinor := requestEnvelope.VersionMinor
	if versionMinor == 0 {
		versionMinor = 1
	}
	return &pb.ControlEnvelope{
		VersionMajor: versionMajor,
		VersionMinor: versionMinor,
		MessageType:  pb.ControlMessageConnectorAuthAck,
		RequestID:    requestEnvelope.RequestID,
		SessionID:    strings.TrimSpace(sessionID),
		SessionEpoch: sessionEpoch,
		ConnectorID:  strings.TrimSpace(requestEnvelope.ConnectorID),
		Payload:      encodedPayload,
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
	tcpListenAddr    string
	grpcListenAddr   string
	heartbeatTTL     time.Duration
	tlsMode          apptls.Mode
	tlsCertSource    apptls.CertSource
	tlsConfigManager apptls.ConfigManager
	metrics          *obs.Metrics

	tcpTransport  *tcpbinding.Transport
	grpcTransport *grpcbinding.Transport
	dispatcher    *controlMessageDispatcher
	grpcAcceptor  *grpcbinding.TunnelAcceptor

	mu           sync.Mutex
	tcpListener  net.Listener
	grpcListener net.Listener
	grpcServer   *grpc.Server
}

// controlPlaneDependencies 定义控制面运行时共享依赖。
type controlPlaneDependencies struct {
	sessionRegistry       *registry.SessionRegistry
	serviceRegistry       *registry.ServiceRegistry
	routeRegistry         *registry.RouteRegistry
	tunnelRegistry        *registry.TunnelRegistry
	tunnelPoolReportStore *bridgecontrol.TunnelPoolReportStore
	metrics               *obs.Metrics
	hostDerivationDomain  string
	managedCAIssuer       apptls.ManagedCACertificateIssuer
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
	normalizedTLSMode, err := apptls.NormalizeMode(config.TLSMode)
	if err != nil {
		return nil, err
	}
	normalizedTLSCertSource, err := apptls.NormalizeCertSource(config.TLSCertSource)
	if err != nil {
		return nil, err
	}
	var tlsConfigManager apptls.ConfigManager
	if normalizedTLSMode != apptls.ModePlaintext {
		certificateProvider, providerErr := apptls.NewCertificateProvider(
			apptls.CertificateProviderConfig{
				TLSCertSource:            config.TLSCertSource,
				TLSCertFile:              config.TLSCertFile,
				TLSKeyFile:               config.TLSKeyFile,
				TLSCACertFile:            config.TLSCACertFile,
				TLSCAKeyFile:             config.TLSCAKeyFile,
				TLSServerCommonName:      config.TLSServerCommonName,
				TLSServerSANDNS:          append([]string(nil), config.TLSServerSANDNS...),
				TLSServerSANIPs:          append([]string(nil), config.TLSServerSANIPs...),
				TLSServerCertTTL:         config.TLSServerCertTTL,
				TLSServerCertRenewBefore: config.TLSServerCertRenewBefore,
			},
			apptls.CertificateProviderOptions{
				ManagedCAIssuer: dependencies.managedCAIssuer,
			},
		)
		if providerErr != nil {
			return nil, providerErr
		}
		tlsConfigManager, err = apptls.NewConfigManager(certificateProvider, normalizedTLSCertSource)
		if err != nil {
			return nil, err
		}
		// 启动阶段立即加载证书，失败时直接终止启动，避免服务处于半可用状态。
		if err := tlsConfigManager.Refresh(context.Background()); err != nil {
			return nil, err
		}
	}
	metrics := dependencies.metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	return &controlPlaneServer{
		tcpListenAddr:    strings.TrimSpace(config.ListenAddr),
		grpcListenAddr:   strings.TrimSpace(config.GRPCH2ListenAddr),
		heartbeatTTL:     config.HeartbeatTimeout,
		tlsMode:          normalizedTLSMode,
		tlsCertSource:    normalizedTLSCertSource,
		tlsConfigManager: tlsConfigManager,
		metrics:          metrics,
		tcpTransport:     tcpTransport,
		grpcTransport:    grpcTransport,
		grpcAcceptor:     grpcbinding.NewTunnelAcceptor(grpcbinding.TunnelAcceptorConfig{}),
		dispatcher: newControlMessageDispatcher(controlMessageDispatcherOptions{
			sessionRegistry:       dependencies.sessionRegistry,
			serviceRegistry:       dependencies.serviceRegistry,
			routeRegistry:         dependencies.routeRegistry,
			tunnelRegistry:        dependencies.tunnelRegistry,
			tunnelPoolReportStore: dependencies.tunnelPoolReportStore,
			hostDerivationDomain:  dependencies.hostDerivationDomain,
			tlsMode:               string(normalizedTLSMode),
			metrics:               metrics,
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
	if server.tlsConfigManager != nil {
		// TLS 启用时后台周期刷新证书，实现续签和替换热加载。
		runners = append(runners, server.runTLSCertificateReloadLoop)
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

// runTLSCertificateReloadLoop 周期刷新控制面服务端证书，实现续签与热加载。
func (server *controlPlaneServer) runTLSCertificateReloadLoop(ctx context.Context) error {
	if server == nil || server.tlsConfigManager == nil {
		return nil
	}
	for {
		reloadInterval := server.tlsConfigManager.NextReloadInterval()
		if reloadInterval <= 0 {
			reloadInterval = apptls.ReloadRetryInterval
		}
		reloadTimer := time.NewTimer(reloadInterval)
		select {
		case <-ctx.Done():
			if !reloadTimer.Stop() {
				select {
				case <-reloadTimer.C:
				default:
				}
			}
			return nil
		case <-reloadTimer.C:
		}
		if err := server.tlsConfigManager.Refresh(ctx); err != nil {
			slog.Warn(
				"reload control plane tls certificate failed",
				"tls_mode", string(server.tlsMode),
				"tls_cert_source", string(server.tlsCertSource),
				"error", err.Error(),
			)
			continue
		}
		slog.Info(
			"reload control plane tls certificate success",
			"tls_mode", string(server.tlsMode),
			"tls_cert_source", string(server.tlsCertSource),
			"cert_not_after", server.tlsConfigManager.CurrentServerCertNotAfter().Format(time.RFC3339),
		)
	}
}

// currentServerTLSConfig 返回当前生效的服务端 TLS 配置。
func (server *controlPlaneServer) currentServerTLSConfig() *tls.Config {
	if server == nil || server.tlsConfigManager == nil {
		return nil
	}
	return server.tlsConfigManager.CurrentServerTLSConfig()
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
		serveErrChan <- grpcServer.Serve(
			apptls.NewTLSAwareListener(listener, server.tlsMode, server.currentServerTLSConfig, server.metrics),
		)
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
) (serveErr error) {
	effectiveDispatcher := dispatcher
	if effectiveDispatcher == nil {
		// 测试或兼容路径未注入 dispatcher 时按默认实现创建一份。
		effectiveDispatcher = newControlMessageDispatcher(controlMessageDispatcherOptions{})
	}
	sessionState := newControlChannelSessionState(grpcPeerAddrString(stream.Context()))
	sessionState.markConnected()
	sessionState.markControlReady()
	defer func() {
		effectiveDispatcher.releaseUnauthenticatedConnectionBudget(sessionState)
		// 认证成功后，连接退出必须显式推进会话终态，避免遗留隧道资源。
		if serveErr != nil &&
			!errors.Is(serveErr, context.Canceled) &&
			!errors.Is(serveErr, context.DeadlineExceeded) {
			effectiveDispatcher.markSessionFailedFromState(
				time.Now().UTC(),
				sessionState,
				"grpc_control_channel_failed",
			)
			return
		}
		effectiveDispatcher.closeSessionFromState(
			time.Now().UTC(),
			sessionState,
			"grpc_control_channel_closed",
		)
	}()
	if !effectiveDispatcher.reserveUnauthenticatedConnectionBudget(sessionState) {
		slog.Warn(
			"reject grpc control channel by unauthenticated connection budget",
			"peer_addr", grpcPeerAddrString(stream.Context()),
		)
		return nil
	}
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
	acceptedConn, tlsEnabled, acceptErr := apptls.AcceptConnWithTLS(
		rawConn,
		server.tlsMode,
		server.currentServerTLSConfig(),
		server.metrics,
	)
	if acceptErr != nil {
		slog.Warn(
			"reject tcp inbound connection by tls mode",
			"tls_mode", string(server.tlsMode),
			"peer_addr", apptls.RemoteAddrString(rawConn),
			"error", acceptErr.Error(),
		)
		_ = rawConn.Close()
		return nil
	}
	classifiedConn, isControlChannel, err := classifyTCPInboundConnection(acceptedConn)
	if err != nil {
		_ = acceptedConn.Close()
		return fmt.Errorf("serve tcp inbound: classify connection: %w", err)
	}
	if isControlChannel {
		slog.Debug(
			"accept tcp control connection",
			"tls_mode", string(server.tlsMode),
			"tls_enabled", tlsEnabled,
			"peer_addr", apptls.RemoteAddrString(rawConn),
		)
		controlChannel, openErr := server.tcpTransport.OpenControlChannel(classifiedConn)
		if openErr != nil {
			_ = classifiedConn.Close()
			return fmt.Errorf("serve tcp inbound: open control channel: %w", openErr)
		}
		if serveErr := serveControlChannelWithDispatcherAndPeerAddr(
			ctx,
			controlChannel,
			server.dispatcher,
			apptls.RemoteAddrString(rawConn),
		); serveErr != nil && !isControlChannelClosedError(serveErr) {
			_ = controlChannel.Close(context.Background())
			return fmt.Errorf("serve tcp inbound: serve control channel: %w", serveErr)
		}
		return nil
	}
	slog.Debug(
		"accept tcp tunnel connection",
		"tls_mode", string(server.tlsMode),
		"tls_enabled", tlsEnabled,
		"peer_addr", apptls.RemoteAddrString(rawConn),
	)
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
	peerAddr := ""
	if rawConn != nil && rawConn.RemoteAddr() != nil {
		peerAddr = strings.TrimSpace(rawConn.RemoteAddr().String())
	}
	handshake, handshakeErr := readIncomingTCPTunnelHandshake(
		rawConn,
		incomingTCPTunnelHandshakeTimeout,
		server.tcpTransport.Config().MaxTunnelFramePayloadSize,
	)
	if handshakeErr != nil {
		slog.Info(
			"bridge accept tunnel dropped: read tcp handshake failed",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
			"error", handshakeErr.Error(),
		)
		_ = rawConn.Close()
		return nil
	}
	tunnelID := strings.TrimSpace(handshake.TunnelID)
	if tunnelID == "" {
		slog.Info(
			"bridge accept tunnel dropped: missing handshake tunnel id",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
		)
		_ = rawConn.Close()
		return nil
	}
	if announcedSessionID := strings.TrimSpace(handshake.SessionID); announcedSessionID != "" && announcedSessionID != sessionID {
		slog.Info(
			"bridge accept tunnel dropped: handshake session mismatch",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
			"announced_session_id", announcedSessionID,
			"tunnel_id", tunnelID,
		)
		_ = rawConn.Close()
		return nil
	}
	if handshake.SessionEpoch != 0 && handshake.SessionEpoch != sessionEpoch {
		slog.Info(
			"bridge accept tunnel dropped: handshake session epoch mismatch",
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"peer_addr", peerAddr,
			"announced_session_epoch", handshake.SessionEpoch,
			"tunnel_id", tunnelID,
		)
		_ = rawConn.Close()
		return nil
	}
	slog.Info(
		"bridge accept tunnel id from tcp handshake",
		"session_id", sessionID,
		"session_epoch", sessionEpoch,
		"peer_addr", peerAddr,
		"tunnel_id", tunnelID,
		"dial_local_addr", strings.TrimSpace(handshake.DialLocalAddr),
	)
	openConn := connForTransportOpen(rawConn)
	rawTunnel, err := server.tcpTransport.OpenTunnel(openConn, transport.TunnelMeta{
		TunnelID:     tunnelID,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		CreatedAt:    normalizedNow,
	})
	if err != nil {
		return fmt.Errorf("handle accepted tcp tunnel: open tunnel: %w", err)
	}
	return server.registerAcceptedTunnelWithOwner(
		rawTunnel,
		transport.BindingTypeTCPFramed,
		connectorID,
		sessionID,
		tunnelID,
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
	connectorID, sessionID, sessionEpoch, ok := server.resolveAcceptedTunnelOwner(rawTunnel, bindingType, incomingTunnelOwnerResolveWait)
	if !ok {
		if bindingType == transport.BindingTypeGRPCH2 {
			rawMeta := rawTunnel.Meta()
			slog.Info(
				"bridge register grpc tunnel dropped: owner unresolved",
				"raw_tunnel_id", strings.TrimSpace(rawTunnel.ID()),
				"meta_session_id", strings.TrimSpace(rawMeta.SessionID),
				"meta_session_epoch", rawMeta.SessionEpoch,
				"tunnel_id_source", grpcTunnelIDSource(rawTunnel),
			)
		}
		// owner 不明确时直接回收 tunnel，避免错误归属影响后续流量调度。
		_ = rawTunnel.Close()
		return nil
	}
	registeredTunnelID := strings.TrimSpace(rawTunnel.ID())
	if bindingType == transport.BindingTypeGRPCH2 {
		tunnelIDSource := grpcTunnelIDSource(rawTunnel)
		if tunnelIDSource != grpcbinding.TunnelIDSourceStreamMetadata {
			slog.Info(
				"bridge register grpc tunnel dropped: missing stream metadata tunnel id",
				"connector_id", connectorID,
				"session_id", sessionID,
				"session_epoch", sessionEpoch,
				"raw_tunnel_id", strings.TrimSpace(rawTunnel.ID()),
			)
			_ = rawTunnel.Close()
			return nil
		}
		slog.Info(
			"bridge register grpc tunnel",
			"connector_id", connectorID,
			"session_id", sessionID,
			"session_epoch", sessionEpoch,
			"raw_tunnel_id", strings.TrimSpace(rawTunnel.ID()),
			"registered_tunnel_id", registeredTunnelID,
			"tunnel_id_source", tunnelIDSource,
		)
	}
	return server.registerAcceptedTunnelWithOwner(rawTunnel, bindingType, connectorID, sessionID, registeredTunnelID)
}

func grpcTunnelIDSource(rawTunnel transport.Tunnel) string {
	if rawTunnel == nil {
		return ""
	}
	meta := rawTunnel.Meta()
	if len(meta.Labels) == 0 {
		return ""
	}
	return strings.TrimSpace(meta.Labels[grpcbinding.TunnelMetaLabelTunnelIDSource])
}

func readIncomingTCPTunnelHandshake(
	rawConn net.Conn,
	timeout time.Duration,
	maxPayloadBytes int,
) (pb.TunnelDialAnnounce, error) {
	if rawConn == nil {
		return pb.TunnelDialAnnounce{}, errors.New("read incoming tcp tunnel handshake: nil conn")
	}
	handshakeTimeout := timeout
	if handshakeTimeout > 0 {
		if err := rawConn.SetReadDeadline(time.Now().UTC().Add(handshakeTimeout)); err != nil {
			return pb.TunnelDialAnnounce{}, fmt.Errorf("read incoming tcp tunnel handshake: set read deadline: %w", err)
		}
		defer func() {
			_ = rawConn.SetReadDeadline(time.Time{})
		}()
	}
	frameHeader := make([]byte, 4)
	if _, err := io.ReadFull(rawConn, frameHeader); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read incoming tcp tunnel handshake: read frame header: %w", err)
	}
	payloadSize := int(binary.BigEndian.Uint32(frameHeader))
	if payloadSize <= 0 {
		return pb.TunnelDialAnnounce{}, errors.New("read incoming tcp tunnel handshake: empty payload")
	}
	maxAllowedPayload := maxPayloadBytes
	if maxAllowedPayload <= 0 {
		maxAllowedPayload = 64 * 1024
	}
	if payloadSize > maxAllowedPayload {
		return pb.TunnelDialAnnounce{}, fmt.Errorf(
			"read incoming tcp tunnel handshake: payload too large size=%d max=%d",
			payloadSize,
			maxAllowedPayload,
		)
	}
	payload := make([]byte, payloadSize)
	if _, err := io.ReadFull(rawConn, payload); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read incoming tcp tunnel handshake: read payload: %w", err)
	}
	var handshake pb.TunnelDialAnnounce
	if err := json.Unmarshal(payload, &handshake); err != nil {
		return pb.TunnelDialAnnounce{}, fmt.Errorf("read incoming tcp tunnel handshake: decode payload: %w", err)
	}
	return handshake, nil
}

func (server *controlPlaneServer) resolveAcceptedTunnelOwner(
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
	wait time.Duration,
) (string, string, uint64, bool) {
	if bindingType != transport.BindingTypeGRPCH2 {
		return server.resolveSingleActiveSessionOwner()
	}
	if connectorID, sessionID, sessionEpoch, ok := server.resolveGRPCTunnelOwnerBySessionMeta(rawTunnel, wait); ok {
		return connectorID, sessionID, sessionEpoch, true
	}
	// metadata 已提供但无法解析 owner 时直接失败，避免重复等待与错误回退归属。
	if grpcTunnelHasSessionMeta(rawTunnel) {
		return "", "", 0, false
	}
	return server.waitSingleActiveSessionOwner(wait)
}

func grpcTunnelHasSessionMeta(rawTunnel transport.Tunnel) bool {
	if rawTunnel == nil {
		return false
	}
	rawMeta := rawTunnel.Meta()
	return strings.TrimSpace(rawMeta.SessionID) != "" && rawMeta.SessionEpoch > 0
}

func (server *controlPlaneServer) resolveGRPCTunnelOwnerBySessionMeta(
	rawTunnel transport.Tunnel,
	wait time.Duration,
) (string, string, uint64, bool) {
	if server == nil || rawTunnel == nil {
		return "", "", 0, false
	}
	rawMeta := rawTunnel.Meta()
	sessionID := strings.TrimSpace(rawMeta.SessionID)
	sessionEpoch := rawMeta.SessionEpoch
	if sessionID == "" || sessionEpoch == 0 {
		return "", "", 0, false
	}
	connectorID, ok := server.waitSessionOwnerBySessionID(sessionID, sessionEpoch, wait)
	if !ok {
		return "", "", 0, false
	}
	return connectorID, sessionID, sessionEpoch, true
}

func (server *controlPlaneServer) waitSingleActiveSessionOwner(
	wait time.Duration,
) (string, string, uint64, bool) {
	normalizedWait := wait
	if normalizedWait < 0 {
		normalizedWait = 0
	}
	deadline := time.Now().UTC().Add(normalizedWait)
	for {
		connectorID, sessionID, sessionEpoch, ok := server.resolveSingleActiveSessionOwner()
		if ok {
			return connectorID, sessionID, sessionEpoch, true
		}
		now := time.Now().UTC()
		if normalizedWait == 0 || !now.Before(deadline) {
			return "", "", 0, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (server *controlPlaneServer) waitSessionOwnerBySessionID(
	sessionID string,
	sessionEpoch uint64,
	wait time.Duration,
) (string, bool) {
	if server == nil || server.dispatcher == nil || server.dispatcher.sessionRegistry == nil {
		return "", false
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return "", false
	}
	normalizedWait := wait
	if normalizedWait < 0 {
		normalizedWait = 0
	}
	deadline := time.Now().UTC().Add(normalizedWait)
	for {
		sessionRuntime, exists := server.dispatcher.sessionRegistry.GetBySession(normalizedSessionID)
		if exists &&
			sessionRuntime.State == registry.SessionActive &&
			sessionRuntime.Epoch == sessionEpoch &&
			strings.TrimSpace(sessionRuntime.ConnectorID) != "" {
			return strings.TrimSpace(sessionRuntime.ConnectorID), true
		}
		now := time.Now().UTC()
		if normalizedWait == 0 || !now.Before(deadline) {
			return "", false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (server *controlPlaneServer) registerAcceptedTunnelWithOwner(
	rawTunnel transport.Tunnel,
	bindingType transport.BindingType,
	connectorID string,
	sessionID string,
	tunnelID string,
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
	adapter := newRuntimeBridgeTunnelAdapter(rawTunnel, tunnelID)
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
	if !isKnownControlFrameType(frameType) {
		if isLikelyHTTPPrefix(peekBuffer) {
			return nil, false, errors.New("classify tcp inbound: non-ltfp protocol on control port (possible http/grpc)")
		}
		// 非控制帧前缀按数据面 tunnel 接入；首包由后续 tunnel 握手解析。
		classifiedConn := &prefixedNetConn{
			Conn:   rawConn,
			prefix: append([]byte(nil), peekBuffer...),
		}
		return classifiedConn, false, nil
	}
	classifiedConn := &prefixedNetConn{
		Conn:   rawConn,
		prefix: append([]byte(nil), peekBuffer...),
	}
	return classifiedConn, true, nil
}

// isKnownControlFrameType 判断帧类型是否属于控制面帧。
func isKnownControlFrameType(frameType uint16) bool {
	if frameType == transport.ControlFrameTypeHeartbeatPing || frameType == transport.ControlFrameTypeHeartbeatPong {
		return true
	}
	_, err := transport.ControlMessageTypeForFrameType(frameType)
	return err == nil
}

func isLikelyHTTPPrefix(prefix []byte) bool {
	if len(prefix) < 2 {
		return false
	}
	switch strings.ToUpper(string(prefix)) {
	case "GE", "HE", "PO", "PU", "DE", "OP", "PA", "TR", "CO", "PR":
		return true
	default:
		return false
	}
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

// connForTransportOpen 返回用于 transport.OpenTunnel/OpenControlChannel 的连接。
// 当前仅在 prefixed 前缀已消费完时解包到底层连接，避免丢失尚未回放的首包字节。
func connForTransportOpen(rawConn net.Conn) net.Conn {
	if rawConn == nil {
		return nil
	}
	prefixedConn, ok := rawConn.(*prefixedNetConn)
	if !ok || prefixedConn == nil {
		return rawConn
	}
	if len(prefixedConn.prefix) != 0 || prefixedConn.Conn == nil {
		return rawConn
	}
	return prefixedConn.Conn
}

func serveControlChannel(ctx context.Context, controlChannel transport.ControlChannel) error {
	return serveControlChannelWithDispatcher(ctx, controlChannel, nil)
}

func serveControlChannelWithDispatcher(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	dispatcher *controlMessageDispatcher,
) error {
	return serveControlChannelWithDispatcherAndPeerAddr(ctx, controlChannel, dispatcher, "")
}

// serveControlChannelWithDispatcherAndPeerAddr 处理控制流并把接入源地址注入认证审计上下文。
func serveControlChannelWithDispatcherAndPeerAddr(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	dispatcher *controlMessageDispatcher,
	peerAddr string,
) (serveErr error) {
	effectiveDispatcher := dispatcher
	if effectiveDispatcher == nil {
		// 测试或兼容路径未注入 dispatcher 时按默认实现创建一份。
		effectiveDispatcher = newControlMessageDispatcher(controlMessageDispatcherOptions{})
	}
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	sessionState := newControlChannelSessionState(peerAddr)
	sessionState.markConnected()
	sessionState.markControlReady()
	defer func() {
		effectiveDispatcher.releaseUnauthenticatedConnectionBudget(sessionState)
		// 认证成功后，连接退出必须显式推进会话终态，避免遗留隧道资源。
		if serveErr != nil &&
			!errors.Is(serveErr, context.Canceled) &&
			!errors.Is(serveErr, context.DeadlineExceeded) {
			effectiveDispatcher.markSessionFailedFromState(
				time.Now().UTC(),
				sessionState,
				"tcp_control_channel_failed",
			)
		} else {
			effectiveDispatcher.closeSessionFromState(
				time.Now().UTC(),
				sessionState,
				"tcp_control_channel_closed",
			)
		}
		_ = controlChannel.Close(context.Background())
	}()
	if !effectiveDispatcher.reserveUnauthenticatedConnectionBudget(sessionState) {
		slog.Warn(
			"reject control channel by unauthenticated connection budget",
			"peer_addr", strings.TrimSpace(peerAddr),
		)
		return nil
	}

	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := controlChannel.ReadControlFrame(ctx)
		if err != nil {
			if isControlChannelClosedError(err) {
				return nil
			}
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
			if isControlChannelClosedError(replyErr) {
				return nil
			}
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

// grpcPeerAddrString 从 gRPC context 中提取对端地址字符串。
func grpcPeerAddrString(ctx context.Context) string {
	peerInfo, ok := peer.FromContext(ctx)
	if !ok || peerInfo.Addr == nil {
		return ""
	}
	return strings.TrimSpace(peerInfo.Addr.String())
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
