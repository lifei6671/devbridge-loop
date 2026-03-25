package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	"github.com/lifei6671/devbridge-loop/agent-core/pkg/events"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/quicbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/validate"
)

const (
	bridgeHeartbeatPingInterval  = 5 * time.Second
	bridgeHeartbeatMissThreshold = 5
	bridgeHeartbeatWriteTimeout  = 2 * time.Second
	bridgeBusinessWriteTimeout   = 3 * time.Second
	bridgeTCPTunnelHandshakeTO   = 2 * time.Second

	bridgeRetryInitialBackoff = time.Second
	bridgeRetryMaxBackoff     = 8 * time.Second
	bridgeRetryJitterRatio    = 0.2

	defaultServiceHealthCheckIntervalSec = 30
	serviceHealthCheckScanInterval       = time.Second
)

type bridgeRetryStage string

const (
	bridgeRetryStageConnect     bridgeRetryStage = "connect"
	bridgeRetryStageHandshake   bridgeRetryStage = "handshake"
	bridgeRetryStageInitialSync bridgeRetryStage = "initial_sync"
	bridgeRetryStageReady       bridgeRetryStage = "ready"
)

type bridgeCommandKind string

const (
	bridgeCommandReconnect bridgeCommandKind = "reconnect"
	bridgeCommandDrain     bridgeCommandKind = "drain"
)

type bridgeCommand struct {
	kind         bridgeCommandKind
	resetBackoff bool
}

type activeExitReason uint8

const (
	activeExitContextDone activeExitReason = iota
	activeExitDrained
	activeExitReconnect
	activeExitLost
)

type activeExitResult struct {
	reason       activeExitReason
	resetBackoff bool
	err          error
}

type runtimeSessionSnapshot struct {
	state             string
	sessionID         *string
	sessionEpoch      *uint64
	lastHeartbeatMS   *uint64
	lastHeartbeatSent *uint64
	reconnectTotal    uint64
	retryFailStreak   uint32
	retryBackoffMS    uint64
	nextRetryAtMS     *uint64
	updatedAtMS       uint64
	lastError         string
	unavailableReason string
}

type runtimeQUICSnapshot struct {
	enabled             bool
	connected           bool
	tunnelProducerReady bool
	localAddr           string
	remoteAddr          string
	streamOpenTimeoutMS uint64
	maxIncomingStreams  uint64
}

type runtimeServiceAddInput struct {
	InstanceID             string
	Scope                  pb.Scope
	ServiceName            string
	Protocol               string
	Host                   string
	Port                   uint32
	SNIName                string
	Exposure               pb.ServiceExposure
	HealthCheckIntervalSec uint32
	HealthCheckMode        string
	HealthCheckPath        string
	RouteHint              pb.RouteHint
}

type runtimeServiceDeleteInput struct {
	LogicalServiceID string
	InstanceID       string
}

// bridgeTunnelOpener 实现数据面 tunnel 建连，按配置选择底层 binding。
type bridgeTunnelOpener struct {
	runtime *Runtime
}

func (opener *bridgeTunnelOpener) Open(ctx context.Context) (tunnel.RuntimeTunnel, error) {
	if opener == nil || opener.runtime == nil {
		return nil, errors.New("bridge tunnel opener is nil")
	}
	sessionID, sessionEpoch, active := opener.runtime.bridgeSessionMeta()
	if !active {
		return nil, errors.New("bridge control channel is not active")
	}
	switch strings.TrimSpace(opener.runtime.cfg.BridgeTransport) {
	case transport.BindingTypeTCPFramed.String():
		if opener.runtime.tcpTransport == nil {
			return nil, errors.New("tcp transport is not initialized")
		}
		tunnelID := opener.runtime.nextTunnelID()
		tunnelMeta := transport.TunnelMeta{
			TunnelID:     tunnelID,
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			CreatedAt:    time.Now().UTC(),
		}
		rawTunnel, err := opener.runtime.openBridgeTCPTunnel(ctx, tunnelMeta)
		if err != nil {
			opener.runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
				Level:        events.EventWarn,
				Module:       events.ModuleAgentRuntimeBridge,
				Code:         events.CodeTCPFramedDialTunnelErr,
				Message:      fmt.Sprintf("tcp framed dial tunnel fail:%v", err),
				SessionID:    sessionID,
				SessionEpoch: sessionEpoch,
				BridgeState:  opener.runtime.bridgeState,
			})
			return nil, err
		}
		dialLocalAddr := ""
		if tunnelLocalAddr := rawTunnel.LocalAddr(); tunnelLocalAddr != nil {
			dialLocalAddr = strings.TrimSpace(tunnelLocalAddr.String())
		}
		if handshakeErr := opener.runtime.writeTCPTunnelHandshake(ctx, rawTunnel, tunnelID, sessionID, sessionEpoch, dialLocalAddr); handshakeErr != nil {
			_ = rawTunnel.Close()
			return nil, fmt.Errorf("write tcp tunnel handshake failed: %w", handshakeErr)
		}
		// 所有新建 tunnel 统一包一层 payload 适配器，供 traffic runtime 直接消费。
		return newRuntimeTrafficTunnelAdapter(rawTunnel), nil
	case transport.BindingTypeGRPCH2.String():
		tunnelID := opener.runtime.nextTunnelID()
		tunnelMeta := transport.TunnelMeta{
			TunnelID:     tunnelID,
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			CreatedAt:    time.Now().UTC(),
		}
		opener.runtime.bridgeMu.RLock()
		grpcTransport := opener.runtime.grpcTransport
		grpcClient := opener.runtime.grpcClient
		opener.runtime.bridgeMu.RUnlock()
		if grpcTransport == nil {
			return nil, errors.New("grpc transport is not initialized")
		}
		if grpcClient == nil {
			return nil, errors.New("grpc transport client is not initialized")
		}
		streamContext := grpcbinding.WithTunnelStreamMetadata(ctx, tunnelID, sessionID, sessionEpoch)
		tunnelStream, err := grpcTransport.OpenTunnelStream(streamContext, grpcClient)
		if err != nil {
			opener.runtime.appendDiagnoseEvent(runtimeDiagnoseEvent{
				Level:        events.EventWarn,
				Module:       events.ModuleAgentRuntimeBridge,
				Code:         events.CodeGRPCTunnelStreamErr,
				Message:      fmt.Sprintf("grpc transport fail:%v", err),
				SessionID:    sessionID,
				SessionEpoch: sessionEpoch,
				BridgeState:  opener.runtime.bridgeState,
			})
			return nil, fmt.Errorf("open grpc tunnel stream failed: %w", err)
		}
		grpcTunnel, err := grpcbinding.NewGRPCH2Tunnel(tunnelStream, tunnelMeta)
		if err != nil {
			_ = tunnelStream.Close(context.Background())
			return nil, fmt.Errorf("create grpc tunnel failed: %w", err)
		}
		// grpc tunnel 同样走统一 payload 适配层，避免 runtime 侧分 binding 分支。
		return newRuntimeTrafficTunnelAdapter(grpcTunnel), nil
	case transport.BindingTypeQUICNative.String():
		opener.runtime.bridgeMu.RLock()
		quicProducer := opener.runtime.quicTunnelProducer
		opener.runtime.bridgeMu.RUnlock()
		if quicProducer == nil {
			return nil, errors.New("quic tunnel producer is not initialized")
		}
		quicTunnel, err := quicProducer.OpenTunnel(ctx)
		if err != nil {
			return nil, fmt.Errorf("open quic tunnel failed: %w", err)
		}
		return newRuntimeTrafficTunnelAdapter(quicTunnel), nil
	default:
		return nil, fmt.Errorf("unsupported bridge transport=%s", opener.runtime.cfg.BridgeTransport)
	}
}

func computeBridgeRetryBackoff(failStreak uint32) time.Duration {
	return computeBridgeRetryBackoffWithJitter(failStreak, randomBridgeRetryJitter())
}

func randomBridgeRetryJitter() float64 {
	var randomBytes [8]byte
	if _, err := rand.Read(randomBytes[:]); err != nil {
		return 0
	}
	randomUnit := float64(binary.BigEndian.Uint64(randomBytes[:])) / float64(math.MaxUint64)
	return randomUnit*2 - 1
}

func computeBridgeRetryBackoffWithJitter(failStreak uint32, jitter float64) time.Duration {
	if failStreak == 0 {
		return 0
	}
	baseBackoff := bridgeRetryInitialBackoff
	for attempt := uint32(1); attempt < failStreak && baseBackoff < bridgeRetryMaxBackoff; attempt++ {
		if baseBackoff >= bridgeRetryMaxBackoff/2 {
			baseBackoff = bridgeRetryMaxBackoff
			break
		}
		baseBackoff *= 2
	}
	if baseBackoff > bridgeRetryMaxBackoff {
		baseBackoff = bridgeRetryMaxBackoff
	}
	if jitter < -1 {
		jitter = -1
	} else if jitter > 1 {
		jitter = 1
	}
	jitterWindow := float64(baseBackoff) * bridgeRetryJitterRatio
	backoff := time.Duration(float64(baseBackoff) + jitterWindow*jitter)
	if backoff < bridgeRetryInitialBackoff {
		backoff = bridgeRetryInitialBackoff
	}
	if backoff > bridgeRetryMaxBackoff {
		return bridgeRetryMaxBackoff
	}
	return backoff
}

// nextBridgeRetryFailStreak 根据阶段结果推进 fail streak。
func nextBridgeRetryFailStreak(
	currentFailStreak uint32,
	stage bridgeRetryStage,
	succeeded bool,
) uint32 {
	if succeeded {
		switch stage {
		case bridgeRetryStageReady:
			// 只有控制连接、握手、首次同步全部成功后才重置指数退避。
			return 0
		default:
			// connect/handshake/sync 中间阶段成功不重置，避免认证拒绝持续回到 1s。
			return currentFailStreak
		}
	}
	// 任一阶段失败都按指数退避累加。
	return currentFailStreak + 1
}

func runtimeNowMillis() uint64 {
	return uint64(time.Now().UTC().UnixMilli())
}

func timeToMillisPtr(at time.Time) *uint64 {
	if at.IsZero() {
		return nil
	}
	return new(uint64(at.UTC().UnixMilli()))
}

func durationToMillis(duration time.Duration) uint64 {
	if duration <= 0 {
		return 0
	}
	return uint64(duration.Milliseconds())
}

func newRuntimeSessionID() string {
	randomBytes := make([]byte, 8)
	if _, err := rand.Read(randomBytes); err != nil {
		// crypto/rand 失败时退化为时间戳，保证 session id 仍可追踪。
		return fmt.Sprintf("session-%d", time.Now().UTC().UnixNano())
	}
	return fmt.Sprintf("session-%s", hex.EncodeToString(randomBytes))
}

func (r *Runtime) nextTunnelID() string {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	r.tunnelIDSequence++
	scope := normalizeTunnelIDScope(strings.TrimSpace(r.bridgeSession))
	if scope == "" {
		scope = normalizeTunnelIDScope(strings.TrimSpace(r.cfg.AgentID))
	}
	if scope == "" {
		return fmt.Sprintf("tun-%d", r.tunnelIDSequence)
	}
	return fmt.Sprintf("tun-%s-%d", scope, r.tunnelIDSequence)
}

func normalizeTunnelIDScope(rawScope string) string {
	normalizedScope := strings.TrimSpace(rawScope)
	if normalizedScope == "" {
		return ""
	}
	if strings.HasPrefix(normalizedScope, "session-") {
		normalizedScope = strings.TrimSpace(strings.TrimPrefix(normalizedScope, "session-"))
	}
	builder := strings.Builder{}
	builder.Grow(len(normalizedScope))
	for _, currentRune := range normalizedScope {
		if (currentRune >= 'a' && currentRune <= 'z') ||
			(currentRune >= 'A' && currentRune <= 'Z') ||
			(currentRune >= '0' && currentRune <= '9') ||
			currentRune == '-' ||
			currentRune == '_' {
			builder.WriteRune(currentRune)
			continue
		}
		builder.WriteByte('_')
	}
	sanitizedScope := strings.Trim(builder.String(), "-_")
	if len(sanitizedScope) > 32 {
		// 保留后缀，通常包含更高区分度的随机片段。
		sanitizedScope = sanitizedScope[len(sanitizedScope)-32:]
	}
	return sanitizedScope
}

func buildQUICTunnelIDPrefix(sessionID string, agentID string) string {
	scope := normalizeTunnelIDScope(strings.TrimSpace(sessionID))
	if scope == "" {
		scope = normalizeTunnelIDScope(strings.TrimSpace(agentID))
	}
	if scope == "" {
		return "tun"
	}
	return fmt.Sprintf("tun-%s", scope)
}

func newQUICTunnelProducer(
	quicConn *quicbinding.Conn,
	sessionID string,
	sessionEpoch uint64,
	agentID string,
) (*quicbinding.TunnelProducer, error) {
	if quicConn == nil {
		return nil, errors.New("quic control connection is nil")
	}
	return quicbinding.NewTunnelProducer(quicConn, quicbinding.TunnelIdentityConfig{
		SessionID:      strings.TrimSpace(sessionID),
		SessionEpoch:   sessionEpoch,
		TunnelIDPrefix: buildQUICTunnelIDPrefix(sessionID, agentID),
	})
}

func (r *Runtime) initTransport() error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	switch strings.TrimSpace(r.cfg.BridgeTransport) {
	case transport.BindingTypeTCPFramed.String():
		transportConfig := tcpbinding.TransportConfig{
			DialTimeout: r.cfg.ControlChannel.DialTimeout,
		}
		tcpTransport, err := tcpbinding.NewTransportWithConfig(transportConfig)
		if err != nil {
			return fmt.Errorf("initialize tcp transport: %w", err)
		}
		r.tcpTransport = tcpTransport
		r.grpcTransport = nil
		r.quicTransport = nil
		return nil
	case transport.BindingTypeGRPCH2.String():
		grpcTransport, err := grpcbinding.NewTransportWithConfig(grpcbinding.TransportConfig{})
		if err != nil {
			return fmt.Errorf("initialize grpc transport: %w", err)
		}
		r.grpcTransport = grpcTransport
		r.tcpTransport = nil
		r.quicTransport = nil
		return nil
	case transport.BindingTypeQUICNative.String():
		quicTransport, err := quicbinding.NewTransportWithConfig(quicbinding.TransportConfig{})
		if err != nil {
			return fmt.Errorf("initialize quic transport: %w", err)
		}
		r.quicTransport = quicTransport
		r.tcpTransport = nil
		r.grpcTransport = nil
		return nil
	case transport.BindingTypeH3Stream.String():
		return fmt.Errorf(
			"bridge transport %s 暂未在 agent-core 中接入，请先使用 %s 或 %s",
			r.cfg.BridgeTransport,
			transport.BindingTypeTCPFramed.String(),
			transport.BindingTypeGRPCH2.String(),
		)
	default:
		return fmt.Errorf("unsupported bridge transport=%s", r.cfg.BridgeTransport)
	}
}

func (r *Runtime) initTunnelManager() error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	registry := tunnel.NewRegistry()
	manager, err := tunnel.NewManager(tunnel.ManagerOptions{
		Config: tunnel.ManagerConfig{
			MinIdle:           r.cfg.TunnelPool.MinIdle,
			MaxIdle:           r.cfg.TunnelPool.MaxIdle,
			IdleTTL:           r.cfg.TunnelPool.TTL,
			MaxReuseCount:     r.cfg.TunnelPool.MaxReuse,
			RecycleTimeout:    r.cfg.TunnelPool.RecycleAckTO,
			MaxInflightOpens:  r.cfg.TunnelPool.MaxInflight,
			TunnelOpenRate:    r.cfg.TunnelPool.OpenRate,
			TunnelOpenBurst:   r.cfg.TunnelPool.OpenBurst,
			ReconcileInterval: r.cfg.TunnelPool.ReconcileGap,
		},
		Registry: registry,
		Opener:   &bridgeTunnelOpener{runtime: r},
		// tunnel manager 事件指标统一写入 runtime 级 metrics 容器。
		Metrics: r.metrics,
	})
	if err != nil {
		return fmt.Errorf("initialize tunnel manager: %w", err)
	}
	refillHandler, err := control.NewRefillHandler(manager, control.RefillHandlerConfig{
		// 以当前 runtime 配置的 max_idle 作为补池请求的硬上限。
		MaxIdle: r.cfg.TunnelPool.MaxIdle,
	})
	if err != nil {
		return fmt.Errorf("initialize refill handler: %w", err)
	}
	tunnelReporter, err := control.NewTunnelReporter(
		manager,
		r,
		control.TunnelReporterConfig{
			// 首版采用事件驱动 + 低频周期纠偏，周期按默认 10s。
			Period:      10 * time.Second,
			EventBuffer: 32,
			// 目标 idle 提示沿用 runtime 配置的 min_idle。
			TargetIdleHint: r.cfg.TunnelPool.MinIdle,
		},
	)
	if err != nil {
		return fmt.Errorf("initialize tunnel reporter: %w", err)
	}
	r.tunnelRegistry = registry
	r.tunnelManager = manager
	r.refillHandler = refillHandler
	r.tunnelReporter = tunnelReporter
	return nil
}

func (r *Runtime) bridgeDesiredUpState() bool {
	r.bridgeMu.RLock()
	defer r.bridgeMu.RUnlock()
	return r.bridgeDesiredUp
}

func (r *Runtime) bridgeSessionMeta() (string, uint64, bool) {
	r.bridgeMu.RLock()
	defer r.bridgeMu.RUnlock()
	if r.bridgeState != events.BridgeStateActive ||
		!r.bridgeSessionReady ||
		r.bridgeSession == "" ||
		r.bridgeEpoch == 0 {
		return "", 0, false
	}
	return r.bridgeSession, r.bridgeEpoch, true
}

// bridgeSessionConnectedMeta 返回控制通道已连通时的会话元信息（不要求 warmup 完成）。
func (r *Runtime) bridgeSessionConnectedMeta() (string, uint64, bool) {
	r.bridgeMu.RLock()
	defer r.bridgeMu.RUnlock()
	if r.bridgeState != events.BridgeStateActive ||
		r.bridgeSession == "" ||
		r.bridgeEpoch == 0 {
		return "", 0, false
	}
	return r.bridgeSession, r.bridgeEpoch, true
}

func (r *Runtime) clearControlChannel() transport.ControlChannel {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	controlChannel := r.controlChannel
	r.controlChannel = nil
	return controlChannel
}

func (r *Runtime) clearGRPCClientConn() *grpc.ClientConn {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	clientConn := r.grpcConn
	r.grpcConn = nil
	r.grpcClient = nil
	return clientConn
}

func (r *Runtime) clearQUICConn() *quicbinding.Conn {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	quicConn := r.quicConn
	r.quicConn = nil
	r.quicTunnelProducer = nil
	return quicConn
}

func (r *Runtime) closeCurrentControlChannel() {
	controlChannel := r.clearControlChannel()
	if controlChannel != nil {
		_ = controlChannel.Close(context.Background())
	}
	clientConn := r.clearGRPCClientConn()
	if clientConn != nil {
		_ = clientConn.Close()
	}
	quicConn := r.clearQUICConn()
	if quicConn != nil {
		_ = quicConn.Close(nil)
	}
}

func (r *Runtime) enqueueBridgeCommand(command bridgeCommand) {
	if r == nil {
		return
	}
	select {
	case r.bridgeCommandChan <- command:
		return
	default:
	}
	select {
	case <-r.bridgeCommandChan:
	default:
	}
	select {
	case r.bridgeCommandChan <- command:
	default:
	}
}

func (r *Runtime) requestBridgeReconnect(resetBackoff bool) {
	if r == nil {
		return
	}
	var sessionID string
	var sessionEpoch uint64
	r.bridgeMu.Lock()
	r.bridgeDesiredUp = true
	r.bridgeState = events.BridgeStateReconnecting
	r.updatedAt = time.Now().UTC()
	sessionID = r.bridgeSession
	sessionEpoch = r.bridgeEpoch
	if resetBackoff {
		r.retryFailStreak = 0
		r.retryBackoff = 0
		r.nextRetryAt = time.Time{}
	}
	r.bridgeMu.Unlock()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventWarn,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeSessionReconnectRequested,
		Message:      fmt.Sprintf("bridge reconnect requested reset_backoff=%t", resetBackoff),
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  events.BridgeStateReconnecting,
	})
	r.enqueueBridgeCommand(bridgeCommand{kind: bridgeCommandReconnect, resetBackoff: resetBackoff})
}

func (r *Runtime) requestBridgeDrain() {
	if r == nil {
		return
	}
	var sessionID string
	var sessionEpoch uint64
	r.bridgeMu.Lock()
	r.bridgeDesiredUp = false
	r.bridgeState = events.BridgeStateDraining
	r.updatedAt = time.Now().UTC()
	sessionID = r.bridgeSession
	sessionEpoch = r.bridgeEpoch
	r.bridgeMu.Unlock()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeStateDraining,
		Message:      "bridge session drain requested",
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  events.BridgeStateDraining,
	})
	// drain 请求到达后先立即回收本地 tunnel，避免等待控制循环导致统计延迟。
	r.notifyTunnelManagerState(tunnel.SessionStateDraining)
	r.enqueueBridgeCommand(bridgeCommand{kind: bridgeCommandDrain})
}

func (r *Runtime) setBridgeConnecting() {
	r.bridgeMu.Lock()
	sessionID := r.bridgeSession
	sessionEpoch := r.bridgeEpoch
	r.bridgeState = events.BridgeStateConnecting
	r.bridgeSessionReady = false
	r.updatedAt = time.Now().UTC()
	r.bridgeMu.Unlock()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeStateConnecting,
		Message:      "bridge control channel is connecting",
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  events.BridgeStateConnecting,
	})
}

func (r *Runtime) setBridgeConnected(sessionID string) {
	var currentSessionID string
	var currentSessionEpoch uint64
	wasReconnect := false
	r.bridgeMu.Lock()
	if r.bridgeSession != "" {
		r.reconnects++
		wasReconnect = true
	}
	r.bridgeSession = sessionID
	r.bridgeEpoch++
	if r.bridgeEpoch == 0 {
		r.bridgeEpoch = 1
	}
	now := time.Now().UTC()
	r.bridgeDesiredUp = true
	r.bridgeState = events.BridgeStateActive
	r.bridgeSessionReady = false
	r.heartbeatAt = now
	r.heartbeatSentAt = now
	r.updatedAt = now
	r.lastErr = ""
	r.retryFailStreak = 0
	r.retryBackoff = 0
	r.nextRetryAt = time.Time{}
	// 在释放锁前拿到最新会话快照，供控制面处理器同步上下文。
	currentSessionID = r.bridgeSession
	currentSessionEpoch = r.bridgeEpoch
	r.bridgeMu.Unlock()
	if wasReconnect {
		r.appendDiagnoseEvent(runtimeDiagnoseEvent{
			Level:        events.EventInfo,
			Module:       events.ModuleAgentRuntimeBridge,
			Code:         events.CodeBridgeReconnectEstablished,
			Message:      "bridge reconnect completed and session switched",
			SessionID:    currentSessionID,
			SessionEpoch: currentSessionEpoch,
			BridgeState:  events.BridgeStateActive,
		})
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeStateActive,
		Message:      "bridge control channel is active",
		SessionID:    currentSessionID,
		SessionEpoch: currentSessionEpoch,
		BridgeState:  events.BridgeStateActive,
	})
	if r.refillHandler != nil {
		// 会话重连后立即刷新补池处理器上下文，避免旧代际请求污染。
		r.refillHandler.SetSession(currentSessionID, currentSessionEpoch)
	}
	if r.controlPublisher != nil {
		// 发布器需要跟随会话切代，保证后续消息幂等字段正确。
		r.controlPublisher.SetSession(currentSessionID, currentSessionEpoch)
	}
	if r.tunnelReporter != nil {
		// tunnel reporter 同步会话字段，避免跨代际上报。
		r.tunnelReporter.SetSession(currentSessionID, currentSessionEpoch)
	}
}

func (r *Runtime) setBridgeSessionReady(ready bool) {
	if r == nil {
		return
	}
	r.bridgeMu.Lock()
	r.bridgeSessionReady = ready
	r.bridgeMu.Unlock()
}

func (r *Runtime) setBridgeRetrying(connectErr error, failStreak uint32, backoff time.Duration) {
	r.bridgeMu.Lock()
	now := time.Now().UTC()
	r.bridgeState = events.BridgeStateReconnecting
	r.bridgeSessionReady = false
	r.updatedAt = now
	r.retryFailStreak = failStreak
	r.retryBackoff = backoff
	sessionID := r.bridgeSession
	sessionEpoch := r.bridgeEpoch
	if backoff > 0 {
		r.nextRetryAt = now.Add(backoff)
	} else {
		r.nextRetryAt = time.Time{}
	}
	lastError := ""
	if connectErr != nil {
		lastError = connectErr.Error()
		r.lastErr = lastError
	}
	r.bridgeMu.Unlock()
	message := fmt.Sprintf(
		"bridge retry scheduled fail_streak=%d backoff_ms=%d",
		failStreak,
		durationToMillis(backoff),
	)
	if strings.TrimSpace(lastError) != "" {
		message = fmt.Sprintf("%s error=%s", message, lastError)
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventWarn,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeRetryScheduled,
		Message:      message,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  events.BridgeStateReconnecting,
	})
}

func (r *Runtime) setBridgeLost(readErr error) {
	r.bridgeMu.Lock()
	sessionID := r.bridgeSession
	sessionEpoch := r.bridgeEpoch
	r.bridgeState = events.BridgeStateStale
	r.bridgeSessionReady = false
	r.updatedAt = time.Now().UTC()
	errorText := ""
	if readErr != nil {
		errorText = readErr.Error()
		r.lastErr = errorText
	}
	r.bridgeMu.Unlock()
	if strings.TrimSpace(errorText) == "" {
		errorText = "bridge control channel became stale"
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventError,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeStateStale,
		Message:      errorText,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  events.BridgeStateStale,
	})
}

func (r *Runtime) setBridgeDrained() {
	r.bridgeMu.Lock()
	closedSessionID := r.bridgeSession
	closedSessionEpoch := r.bridgeEpoch
	r.bridgeDesiredUp = false
	r.bridgeState = events.BridgeStateClosed
	r.bridgeSessionReady = false
	r.bridgeSession = ""
	r.updatedAt = time.Now().UTC()
	r.retryFailStreak = 0
	r.retryBackoff = 0
	r.nextRetryAt = time.Time{}
	r.bridgeMu.Unlock()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeBridge,
		Code:         events.CodeBridgeStateClosed,
		Message:      "bridge session drained and closed",
		SessionID:    closedSessionID,
		SessionEpoch: closedSessionEpoch,
		BridgeState:  events.BridgeStateClosed,
	})
	if r.refillHandler != nil {
		// 会话关闭后清空补池处理器会话上下文，拒绝陈旧请求。
		r.refillHandler.SetSession("", 0)
	}
	if r.controlPublisher != nil {
		// 会话关闭后清空发布器上下文，避免误发旧 session 元信息。
		r.controlPublisher.SetSession("", 0)
	}
	if r.tunnelReporter != nil {
		// 会话关闭后清空 reporter 会话上下文，拒绝陈旧上报。
		r.tunnelReporter.SetSession("", 0)
	}
}

func (r *Runtime) touchBridgeHeartbeat() {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	now := time.Now().UTC()
	r.heartbeatAt = now
	r.updatedAt = now
}

func (r *Runtime) touchBridgeHeartbeatSent() {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	r.heartbeatSentAt = time.Now().UTC()
}

func (r *Runtime) sessionSnapshot() runtimeSessionSnapshot {
	r.bridgeMu.RLock()
	defer r.bridgeMu.RUnlock()
	updatedAtMS := runtimeNowMillis()
	if !r.updatedAt.IsZero() {
		updatedAtMS = uint64(r.updatedAt.UnixMilli())
	}
	snapshot := runtimeSessionSnapshot{
		state:           r.bridgeState,
		reconnectTotal:  r.reconnects,
		retryFailStreak: r.retryFailStreak,
		retryBackoffMS:  durationToMillis(r.retryBackoff),
		nextRetryAtMS:   timeToMillisPtr(r.nextRetryAt),
		updatedAtMS:     updatedAtMS,
		lastError:       r.lastErr,
	}
	if r.bridgeSession != "" {
		sessionID := r.bridgeSession
		snapshot.sessionID = &sessionID
	}
	if r.bridgeEpoch > 0 {
		sessionEpoch := r.bridgeEpoch
		snapshot.sessionEpoch = &sessionEpoch
	}
	snapshot.lastHeartbeatMS = timeToMillisPtr(r.heartbeatAt)
	snapshot.lastHeartbeatSent = timeToMillisPtr(r.heartbeatSentAt)
	if snapshot.state == "" {
		snapshot.state = events.BridgeStateUnavailable
	}
	if snapshot.state == events.BridgeStateUnavailable || snapshot.state == events.BridgeStateStale || snapshot.state == events.BridgeStateReconnecting {
		snapshot.unavailableReason = r.lastErr
	}
	if snapshot.updatedAtMS == 0 {
		snapshot.updatedAtMS = runtimeNowMillis()
	}
	return snapshot
}

func (r *Runtime) quicSnapshot() runtimeQUICSnapshot {
	if r == nil {
		return runtimeQUICSnapshot{}
	}
	snapshot := runtimeQUICSnapshot{
		enabled: strings.TrimSpace(r.cfg.BridgeTransport) == transport.BindingTypeQUICNative.String(),
	}
	r.bridgeMu.RLock()
	quicConn := r.quicConn
	snapshot.tunnelProducerReady = r.quicTunnelProducer != nil
	r.bridgeMu.RUnlock()
	if quicConn != nil {
		snapshot.connected = true
		if localAddr := quicConn.LocalAddr(); localAddr != nil {
			snapshot.localAddr = strings.TrimSpace(localAddr.String())
		}
		if remoteAddr := quicConn.RemoteAddr(); remoteAddr != nil {
			snapshot.remoteAddr = strings.TrimSpace(remoteAddr.String())
		}
	}
	if r.quicTransport != nil {
		transportConfig := r.quicTransport.Config()
		snapshot.streamOpenTimeoutMS = durationToMillis(transportConfig.StreamOpenTimeout)
		if transportConfig.MaxIncomingStreams > 0 {
			snapshot.maxIncomingStreams = uint64(transportConfig.MaxIncomingStreams)
		}
	}
	return snapshot
}

func quicSnapshotPayload(snapshot runtimeQUICSnapshot) map[string]any {
	return map[string]any{
		"enabled":                snapshot.enabled,
		"connected":              snapshot.connected,
		"tunnel_producer_ready":  snapshot.tunnelProducerReady,
		"local_addr":             snapshot.localAddr,
		"remote_addr":            snapshot.remoteAddr,
		"stream_open_timeout_ms": snapshot.streamOpenTimeoutMS,
		"max_incoming_streams":   snapshot.maxIncomingStreams,
	}
}

func (r *Runtime) notifyTunnelManagerState(state string) {
	if r == nil || r.tunnelManager == nil {
		return
	}
	_, _ = r.tunnelManager.HandleSessionState(state)
}

func (r *Runtime) connectBridgeControl(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	switch strings.TrimSpace(r.cfg.BridgeTransport) {
	case transport.BindingTypeTCPFramed.String():
		return r.connectBridgeControlTCP(ctx)
	case transport.BindingTypeGRPCH2.String():
		return r.connectBridgeControlGRPC(ctx)
	case transport.BindingTypeQUICNative.String():
		return r.connectBridgeControlQUIC(ctx)
	default:
		return fmt.Errorf("unsupported bridge transport=%s", r.cfg.BridgeTransport)
	}
}

func (r *Runtime) connectBridgeControlTCP(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	if r.tcpTransport == nil {
		return errors.New("tcp transport is not initialized")
	}
	r.closeCurrentControlChannel()
	r.setBridgeConnecting()

	dialTimeout := r.cfg.ControlChannel.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	dialContext, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	rawConn, err := dialBridgeTCPConn(dialContext, r.cfg.BridgeAddr, dialTimeout, r.cfg.BridgeTLS)
	if err != nil {
		return fmt.Errorf("dial bridge control tcp connection failed: %w", err)
	}
	controlChannel, err := r.tcpTransport.OpenControlChannel(rawConn)
	if err != nil {
		_ = rawConn.Close()
		return fmt.Errorf("dial bridge control channel failed: %w", err)
	}

	r.bridgeMu.Lock()
	r.controlChannel = controlChannel
	r.bridgeMu.Unlock()
	r.setBridgeConnected(newRuntimeSessionID())
	return nil
}

func (r *Runtime) connectBridgeControlGRPC(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	if r.grpcTransport == nil {
		return errors.New("grpc transport is not initialized")
	}
	r.closeCurrentControlChannel()
	r.setBridgeConnecting()

	dialTimeout := r.cfg.ControlChannel.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	dialContext, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	dialOptions := append([]grpc.DialOption{}, r.grpcTransport.DialOptions()...)
	transportCredentials, err := buildBridgeGRPCTransportCredentials(r.cfg.BridgeTLS, r.cfg.BridgeAddr)
	if err != nil {
		return fmt.Errorf("build bridge grpc transport credentials failed: %w", err)
	}
	if transportCredentials == nil {
		transportCredentials = insecure.NewCredentials()
	}
	dialOptions = append(
		dialOptions,
		grpc.WithTransportCredentials(transportCredentials),
		grpc.WithBlock(),
	)
	clientConn, err := grpc.DialContext(dialContext, r.cfg.BridgeAddr, dialOptions...)
	if err != nil {
		return fmt.Errorf("dial bridge grpc connection failed: %w", err)
	}
	client := transportgen.NewGRPCH2TransportServiceClient(clientConn)
	controlChannel, err := r.grpcTransport.OpenControlChannel(ctx, client)
	if err != nil {
		_ = clientConn.Close()
		return fmt.Errorf("open bridge grpc control channel failed: %w", err)
	}

	r.bridgeMu.Lock()
	r.controlChannel = controlChannel
	r.grpcConn = clientConn
	r.grpcClient = client
	r.bridgeMu.Unlock()
	r.setBridgeConnected(newRuntimeSessionID())
	return nil
}

func (r *Runtime) connectBridgeControlQUIC(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	if r.quicTransport == nil {
		return errors.New("quic transport is not initialized")
	}
	r.closeCurrentControlChannel()
	r.setBridgeConnecting()

	dialTimeout := r.cfg.ControlChannel.DialTimeout
	if dialTimeout <= 0 {
		dialTimeout = 5 * time.Second
	}
	dialContext, cancelDial := context.WithTimeout(ctx, dialTimeout)
	defer cancelDial()
	tlsConfig, err := buildBridgeQUICClientTLSConfig(r.cfg.BridgeTLS, r.cfg.BridgeAddr)
	if err != nil {
		return fmt.Errorf("build bridge quic tls config failed: %w", err)
	}
	quicConn, err := r.quicTransport.Dial(dialContext, r.cfg.BridgeAddr, tlsConfig)
	if err != nil {
		return fmt.Errorf("dial bridge quic connection failed: %w", err)
	}
	controlChannel, err := quicConn.OpenControlChannel(dialContext)
	if err != nil {
		_ = quicConn.Close(nil)
		return fmt.Errorf("open bridge quic control channel failed: %w", err)
	}

	r.bridgeMu.Lock()
	r.controlChannel = controlChannel
	r.quicConn = quicConn
	r.quicTunnelProducer = nil
	r.bridgeMu.Unlock()
	r.setBridgeConnected(newRuntimeSessionID())
	return nil
}

// openBridgeTCPTunnel 为 tcp_framed binding 建立底层连接，并在需要时启用 TLS。
func (r *Runtime) openBridgeTCPTunnel(ctx context.Context, tunnelMeta transport.TunnelMeta) (*tcpbinding.TCPTunnel, error) {
	if r == nil {
		return nil, errors.New("runtime is nil")
	}
	if r.tcpTransport == nil {
		return nil, errors.New("tcp transport is not initialized")
	}
	rawConn, err := dialBridgeTCPConn(ctx, r.cfg.BridgeAddr, r.bridgeTunnelDialTimeout(), r.cfg.BridgeTLS)
	if err != nil {
		return nil, fmt.Errorf("open bridge tcp tunnel: dial tcp conn: %w", err)
	}
	rawTunnel, err := r.tcpTransport.OpenTunnel(rawConn, tunnelMeta)
	if err != nil {
		_ = rawConn.Close()
		return nil, fmt.Errorf("open bridge tcp tunnel: open tcp tunnel: %w", err)
	}
	return rawTunnel, nil
}

// bridgeTunnelDialTimeout 返回数据面 tunnel 建连应使用的拨号超时。
func (r *Runtime) bridgeTunnelDialTimeout() time.Duration {
	if r == nil || r.tcpTransport == nil {
		return 0
	}
	// tunnel 建连沿用 tcp transport 自身的超时配置，避免被控制面重连预算误伤。
	return r.tcpTransport.Config().DialTimeout
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

func (r *Runtime) sendControlHeartbeatPing(ctx context.Context, controlChannel transport.ControlChannel) error {
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	writeContext, cancel := context.WithTimeout(ctx, bridgeHeartbeatWriteTimeout)
	defer cancel()
	if err := writeControlFrameWithPriority(
		writeContext,
		controlChannel,
		transport.ControlFrame{Type: transport.ControlFrameTypeHeartbeatPing},
		transport.ControlMessagePriorityHigh,
	); err != nil {
		return err
	}
	r.touchBridgeHeartbeatSent()
	return nil
}

func (r *Runtime) sendControlHeartbeatPong(ctx context.Context, controlChannel transport.ControlChannel) error {
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	writeContext, cancel := context.WithTimeout(ctx, bridgeHeartbeatWriteTimeout)
	defer cancel()
	if err := writeControlFrameWithPriority(
		writeContext,
		controlChannel,
		transport.ControlFrame{Type: transport.ControlFrameTypeHeartbeatPong},
		transport.ControlMessagePriorityHigh,
	); err != nil {
		return err
	}
	r.touchBridgeHeartbeatSent()
	return nil
}

func (r *Runtime) readControlFrames(
	ctx context.Context,
	controlChannel transport.ControlChannel,
	frames chan<- transport.ControlFrame,
) error {
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		frame, err := controlChannel.ReadControlFrame(ctx)
		if err != nil {
			return err
		}
		select {
		case frames <- frame:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// handleBridgeBusinessControlFrame 处理来自 Bridge 的业务控制帧。
func (r *Runtime) handleBridgeBusinessControlFrame(ctx context.Context, frame transport.ControlFrame) error {
	envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frame)
	if err != nil {
		return fmt.Errorf("decode bridge business control frame: %w", err)
	}
	return r.handleBridgeControlEnvelope(ctx, envelope)
}

// handleBridgeControlEnvelope 按消息类型分发控制面业务消息到对应处理器。
func (r *Runtime) handleBridgeControlEnvelope(ctx context.Context, envelope pb.ControlEnvelope) error {
	switch envelope.MessageType {
	case pb.ControlMessageTunnelRefillRequest:
		return r.handleTunnelRefillRequestEnvelope(ctx, envelope)
	case pb.ControlMessageControlError:
		return r.handleControlErrorEnvelope(envelope)
	case pb.ControlMessagePublishServiceAck:
		return r.handlePublishServiceAckEnvelope(envelope)
	case pb.ControlMessageRouteAssignAck:
		return r.handleRouteAssignAckEnvelope(envelope)
	case pb.ControlMessageRouteRevokeAck:
		return r.handleRouteRevokeAckEnvelope(envelope)
	default:
		// 其他消息类型在当前阶段先透传忽略，后续按能力分阶段接入。
		return nil
	}
}

// handleTunnelRefillRequestEnvelope 解析并执行 TunnelRefillRequest。
func (r *Runtime) handleTunnelRefillRequestEnvelope(ctx context.Context, envelope pb.ControlEnvelope) error {
	if r == nil || r.refillHandler == nil {
		// runtime 尚未初始化补池处理器时直接忽略，避免 nil 依赖触发崩溃。
		return nil
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	sessionEpoch := envelope.SessionEpoch
	requestID := strings.TrimSpace(envelope.RequestID)
	if len(envelope.Payload) == 0 {
		err := errors.New("tunnel refill payload is empty")
		r.appendDiagnoseEvent(runtimeDiagnoseEvent{
			Level:        events.EventError,
			Module:       events.ModuleAgentRuntimeRefill,
			Code:         events.CodeTunnelRefillPayloadInvalid,
			Message:      err.Error(),
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			RequestID:    requestID,
		})
		return err
	}
	var refillPayload pb.TunnelRefillRequest
	if err := json.Unmarshal(envelope.Payload, &refillPayload); err != nil {
		wrappedErr := fmt.Errorf("unmarshal tunnel refill payload failed: %w", err)
		r.appendDiagnoseEvent(runtimeDiagnoseEvent{
			Level:        events.EventError,
			Module:       events.ModuleAgentRuntimeRefill,
			Code:         events.CodeTunnelRefillPayloadInvalid,
			Message:      wrappedErr.Error(),
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			RequestID:    requestID,
		})
		return wrappedErr
	}

	// 兼容 envelope 与 payload 字段来源：payload 缺失时回落 envelope 元信息。
	sessionID = strings.TrimSpace(refillPayload.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(envelope.SessionID)
	}
	sessionEpoch = refillPayload.SessionEpoch
	if sessionEpoch == 0 {
		sessionEpoch = envelope.SessionEpoch
	}
	requestID = strings.TrimSpace(refillPayload.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(envelope.RequestID)
	}
	requestTimestamp := time.Now().UTC()
	if refillPayload.TimestampUnix > 0 {
		requestTimestamp = time.Unix(refillPayload.TimestampUnix, 0).UTC()
	}
	requestedTargetIdle := parseRefillMetadataCount(refillPayload.Metadata, "target_idle_count")
	bridgeIdleCount := parseRefillMetadataCount(refillPayload.Metadata, "bridge_idle_count")
	bridgeInUseCount := parseRefillMetadataCount(refillPayload.Metadata, "bridge_in_use_count")
	refillRequest := control.TunnelRefillRequest{
		SessionID:           sessionID,
		SessionEpoch:        sessionEpoch,
		RequestID:           requestID,
		RequestedIdleDelta:  refillPayload.RequestedIdleDelta,
		RequestedTargetIdle: requestedTargetIdle,
		Reason:              control.TunnelRefillReason(strings.TrimSpace(refillPayload.Reason)),
		Timestamp:           requestTimestamp,
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeRefill,
		Code:         events.CodeTunnelRefillRequestReceived,
		Message:      fmt.Sprintf("receive tunnel refill request idle_delta=%d target_idle=%d bridge_idle=%d bridge_in_use=%d", refillRequest.RequestedIdleDelta, refillRequest.RequestedTargetIdle, bridgeIdleCount, bridgeInUseCount),
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		RequestID:    requestID,
		Reason:       string(refillRequest.Reason),
	})
	handleResult, err := r.refillHandler.Handle(ctx, refillRequest)
	if err != nil {
		wrappedErr := fmt.Errorf("handle tunnel refill request failed: %w", err)
		r.appendDiagnoseEvent(runtimeDiagnoseEvent{
			Level:        events.EventError,
			Module:       events.ModuleAgentRuntimeRefill,
			Code:         events.CodeTunnelRefillRejected,
			Message:      wrappedErr.Error(),
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			RequestID:    requestID,
			Reason:       string(refillRequest.Reason),
		})
		return wrappedErr
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeRefill,
		Code:         events.CodeTunnelRefillExpansionCheck,
		Message:      fmt.Sprintf("tunnel refill expansion check need_expansion=%t before_idle=%d before_in_use=%d effective_target_idle=%d bridge_idle=%d bridge_in_use=%d", handleResult.NeedExpansion, handleResult.BeforeIdleCount, handleResult.BeforeInUseCount, handleResult.EffectiveTargetIdle, bridgeIdleCount, bridgeInUseCount),
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		RequestID:    requestID,
		Reason:       string(refillRequest.Reason),
	})
	refillEventCode := events.CodeTunnelRefillApplied
	refillEventMessage := fmt.Sprintf(
		"tunnel refill applied idle_delta=%d before_idle=%d before_in_use=%d effective_target_idle=%d bridge_idle=%d bridge_in_use=%d",
		refillRequest.RequestedIdleDelta,
		handleResult.BeforeIdleCount,
		handleResult.BeforeInUseCount,
		handleResult.EffectiveTargetIdle,
		bridgeIdleCount,
		bridgeInUseCount,
	)
	if !handleResult.Accepted {
		refillEventCode = events.CodeTunnelRefillIgnored
		ignoreReason := "not_accepted"
		if handleResult.Deduplicated {
			ignoreReason = "deduplicated"
		} else if !handleResult.NeedExpansion {
			ignoreReason = "already_satisfied"
		}
		refillEventMessage = fmt.Sprintf(
			"tunnel refill ignored reason=%s idle_delta=%d before_idle=%d before_in_use=%d effective_target_idle=%d bridge_idle=%d bridge_in_use=%d",
			ignoreReason,
			refillRequest.RequestedIdleDelta,
			handleResult.BeforeIdleCount,
			handleResult.BeforeInUseCount,
			handleResult.EffectiveTargetIdle,
			bridgeIdleCount,
			bridgeInUseCount,
		)
	}
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeRefill,
		Code:         refillEventCode,
		Message:      refillEventMessage,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		RequestID:    requestID,
		Reason:       string(refillRequest.Reason),
	})
	return nil
}

func parseRefillMetadataCount(metadata map[string]string, key string) int {
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
	if parseErr != nil || parsedCount < 0 {
		return 0
	}
	return parsedCount
}

// handleControlErrorEnvelope 记录 Bridge 侧上报的控制面错误，便于 UI 与诊断输出。
func (r *Runtime) handleControlErrorEnvelope(envelope pb.ControlEnvelope) error {
	if r == nil {
		return nil
	}
	errorText := "bridge control error"
	if len(envelope.Payload) > 0 {
		var controlError pb.ControlError
		if err := json.Unmarshal(envelope.Payload, &controlError); err != nil {
			return fmt.Errorf("unmarshal control error payload failed: %w", err)
		}
		normalizedCode := strings.TrimSpace(controlError.Code)
		normalizedMessage := strings.TrimSpace(controlError.Message)
		normalizedScope := strings.TrimSpace(controlError.Scope)
		if normalizedMessage != "" {
			errorText = normalizedMessage
		}
		if normalizedCode != "" {
			errorText = normalizedCode + ": " + errorText
		}
		if normalizedScope != "" {
			errorText = normalizedScope + " | " + errorText
		}
	}
	sessionID := strings.TrimSpace(envelope.SessionID)
	sessionEpoch := envelope.SessionEpoch
	bridgeState := ""
	r.bridgeMu.Lock()
	// 将控制面错误写入 runtime 真相源，供 session.snapshot 与诊断视图消费。
	r.lastErr = errorText
	r.updatedAt = time.Now().UTC()
	if sessionID == "" {
		sessionID = r.bridgeSession
	}
	if sessionEpoch == 0 {
		sessionEpoch = r.bridgeEpoch
	}
	bridgeState = r.bridgeState
	r.bridgeMu.Unlock()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventError,
		Module:       events.ModuleAgentRuntimeControl,
		Code:         events.CodeBridgeControlError,
		Message:      errorText,
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  bridgeState,
		RequestID:    strings.TrimSpace(envelope.RequestID),
	})
	return nil
}

// handlePublishServiceAckEnvelope 消费 PublishServiceAck，并回写 catalog 的稳定服务身份。
func (r *Runtime) handlePublishServiceAckEnvelope(envelope pb.ControlEnvelope) error {
	if r == nil || r.serviceCatalog == nil {
		return nil
	}
	if len(envelope.Payload) == 0 {
		return nil
	}
	var publishAck pb.PublishServiceAck
	if err := json.Unmarshal(envelope.Payload, &publishAck); err != nil {
		return fmt.Errorf("unmarshal publish service ack payload failed: %w", err)
	}
	if !publishAck.Accepted {
		// 发布被拒绝时保留本地配置，不做身份回写。
		return nil
	}
	if strings.TrimSpace(publishAck.LogicalServiceID) == "" {
		return nil
	}
	r.serviceCatalog.ApplyPublishIdentity(
		time.Now().UTC(),
		publishAck.ServiceName,
		publishAck.Scope,
		publishAck.LogicalServiceID,
		publishAck.InstanceID,
	)
	return nil
}

// handleRouteAssignAckEnvelope 记录 Bridge 回传的 RouteAssignAck，便于排查 ingress route 不一致问题。
func (r *Runtime) handleRouteAssignAckEnvelope(envelope pb.ControlEnvelope) error {
	if r == nil {
		return nil
	}
	if len(envelope.Payload) == 0 {
		return nil
	}
	var routeAssignAck pb.RouteAssignAck
	if err := json.Unmarshal(envelope.Payload, &routeAssignAck); err != nil {
		return fmt.Errorf("unmarshal route assign ack payload failed: %w", err)
	}
	ackCode := events.CodeRouteAssignAccepted
	ackLevel := events.EventInfo
	if !routeAssignAck.Accepted {
		ackCode = events.CodeRouteAssignRejected
		ackLevel = events.EventWarn
	}
	ackMessage := fmt.Sprintf(
		"route assign ack accepted=%t route_id=%s accepted_version=%d current_version=%d error_code=%s error_message=%s",
		routeAssignAck.Accepted,
		strings.TrimSpace(routeAssignAck.RouteID),
		routeAssignAck.AcceptedResourceVersion,
		routeAssignAck.CurrentResourceVersion,
		strings.TrimSpace(routeAssignAck.ErrorCode),
		strings.TrimSpace(routeAssignAck.ErrorMessage),
	)
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        ackLevel,
		Module:       events.ModuleAgentRuntimeRoute,
		Code:         ackCode,
		Message:      ackMessage,
		SessionID:    strings.TrimSpace(envelope.SessionID),
		SessionEpoch: envelope.SessionEpoch,
		RequestID:    strings.TrimSpace(envelope.RequestID),
	})
	return nil
}

// handleRouteRevokeAckEnvelope 记录 Bridge 回传的 RouteRevokeAck，便于排查路由撤销收敛。
func (r *Runtime) handleRouteRevokeAckEnvelope(envelope pb.ControlEnvelope) error {
	if r == nil {
		return nil
	}
	if len(envelope.Payload) == 0 {
		return nil
	}
	var routeRevokeAck pb.RouteRevokeAck
	if err := json.Unmarshal(envelope.Payload, &routeRevokeAck); err != nil {
		return fmt.Errorf("unmarshal route revoke ack payload failed: %w", err)
	}
	ackCode := events.CodeRouteRevokeAccepted
	ackLevel := events.EventInfo
	if !routeRevokeAck.Accepted {
		ackCode = events.CodeRouteRevokeRejected
		ackLevel = events.EventWarn
	}
	ackMessage := fmt.Sprintf(
		"route revoke ack accepted=%t route_id=%s accepted_version=%d current_version=%d error_code=%s error_message=%s",
		routeRevokeAck.Accepted,
		strings.TrimSpace(routeRevokeAck.RouteID),
		routeRevokeAck.AcceptedResourceVersion,
		routeRevokeAck.CurrentResourceVersion,
		strings.TrimSpace(routeRevokeAck.ErrorCode),
		strings.TrimSpace(routeRevokeAck.ErrorMessage),
	)
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        ackLevel,
		Module:       events.ModuleAgentRuntimeRoute,
		Code:         ackCode,
		Message:      ackMessage,
		SessionID:    strings.TrimSpace(envelope.SessionID),
		SessionEpoch: envelope.SessionEpoch,
		RequestID:    strings.TrimSpace(envelope.RequestID),
	})
	return nil
}

// syncServiceControlState 在会话激活后同步一次服务发布与健康状态。
func (r *Runtime) syncServiceControlState(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if err := r.publishControlHeartbeat(ctx); err != nil {
		return err
	}
	if err := r.publishCatalogServices(ctx); err != nil {
		return err
	}
	if err := r.reportCatalogHealth(ctx); err != nil {
		return err
	}
	return nil
}

// publishControlHeartbeat 在会话激活后优先发送一条业务心跳，用于 Bridge 侧建立 session 上下文。
func (r *Runtime) publishControlHeartbeat(ctx context.Context) error {
	if r == nil || r.controlPublisher == nil {
		return nil
	}
	heartbeatPayload := pb.Heartbeat{
		TimestampUnix: time.Now().UTC().Unix(),
		SessionState:  pb.SessionStateActive,
	}
	envelope, err := r.controlPublisher.Publish(
		ctx,
		pb.ControlMessageHeartbeat,
		"session",
		"heartbeat",
		heartbeatPayload,
	)
	if err != nil {
		return fmt.Errorf("build heartbeat envelope failed: %w", err)
	}
	if err := r.sendBusinessControlEnvelope(ctx, envelope); err != nil {
		return fmt.Errorf("send heartbeat failed: %w", err)
	}
	return nil
}

// publishCatalogServices 遍历本地目录并发送 PublishService。
func (r *Runtime) publishCatalogServices(ctx context.Context) error {
	if r == nil || r.serviceCatalog == nil || r.controlPublisher == nil {
		return nil
	}
	records := r.serviceCatalog.List()
	if len(records) == 0 {
		return nil
	}
	for _, record := range records {
		publishPayload := adapter.ToPublishService(record.Registration)
		resourceID := strings.TrimSpace(publishPayload.InstanceID)
		if resourceID == "" {
			resourceID = buildRuntimeScopeServiceNameKey(publishPayload.ServiceName, publishPayload.Scope)
		}
		envelope, err := r.controlPublisher.Publish(
			ctx,
			pb.ControlMessagePublishService,
			"service",
			resourceID,
			publishPayload,
		)
		if err != nil {
			return fmt.Errorf("build publish service envelope failed: %w", err)
		}
		if err := r.sendBusinessControlEnvelope(ctx, envelope); err != nil {
			return fmt.Errorf("send publish service failed: %w", err)
		}
	}
	return nil
}

// reportCatalogHealth 遍历本地目录并发送 ServiceHealthReport。
func (r *Runtime) reportCatalogHealth(ctx context.Context) error {
	if r == nil || r.serviceCatalog == nil || r.controlPublisher == nil || r.healthReporter == nil {
		return nil
	}
	records := r.serviceCatalog.List()
	if len(records) == 0 {
		return nil
	}
	localServices := make([]adapter.LocalRegistration, 0, len(records))
	for _, record := range records {
		localServices = append(localServices, record.Registration)
	}
	for _, localService := range localServices {
		if err := r.publishServiceHealthReport(ctx, localService); err != nil {
			return err
		}
	}
	return nil
}

// reportCatalogHealthByInterval 仅上报到达周期的服务健康状态。
func (r *Runtime) reportCatalogHealthByInterval(ctx context.Context) error {
	if r == nil || r.serviceCatalog == nil || r.controlPublisher == nil || r.healthReporter == nil {
		return nil
	}
	records := r.serviceCatalog.List()
	if len(records) == 0 {
		return nil
	}
	now := time.Now().UTC()
	for _, record := range records {
		interval := serviceHealthCheckInterval(record.Registration)
		if !record.UpdatedAt.IsZero() && now.Before(record.UpdatedAt.Add(interval)) {
			continue
		}
		if err := r.publishServiceHealthReport(ctx, record.Registration); err != nil {
			return err
		}
	}
	return nil
}

func (r *Runtime) publishServiceHealthReport(ctx context.Context, localService adapter.LocalRegistration) error {
	if r == nil || r.controlPublisher == nil || r.healthReporter == nil {
		return nil
	}
	healthReport := r.healthReporter.BuildServiceReport(ctx, localService)
	resourceID := strings.TrimSpace(healthReport.InstanceID)
	if resourceID == "" {
		resourceID = strings.TrimSpace(healthReport.LogicalServiceID)
	}
	envelope, err := r.controlPublisher.Publish(
		ctx,
		pb.ControlMessageServiceHealthReport,
		"service",
		resourceID,
		healthReport,
	)
	if err != nil {
		return fmt.Errorf("build service health envelope failed: %w", err)
	}
	if err := r.sendBusinessControlEnvelope(ctx, envelope); err != nil {
		return fmt.Errorf("send service health failed: %w", err)
	}
	// 上报成功后回写本地健康快照，供 UI 与诊断直接读取。
	r.serviceCatalog.UpdateHealth(
		time.Now().UTC(),
		healthReport.LogicalServiceID,
		healthReport.InstanceID,
		healthReport.ServiceHealthStatus,
		healthReport.EndpointStatuses,
	)
	return nil
}

// SendTunnelPoolReport 把 tunnel 池状态转换为控制面消息并发送到 Bridge。
func (r *Runtime) SendTunnelPoolReport(ctx context.Context, report control.TunnelPoolReport) error {
	if r == nil || r.controlPublisher == nil {
		return nil
	}
	// 仅在控制通道 ACTIVE 时发送，断线阶段静默跳过避免 reporter 退出。
	if _, _, active := r.bridgeSessionMeta(); !active {
		return nil
	}
	checkTimestamp := report.Timestamp.UTC().Unix()
	if report.Timestamp.IsZero() {
		checkTimestamp = time.Now().UTC().Unix()
	}
	payload := pb.TunnelPoolReport{
		SessionID:       strings.TrimSpace(report.SessionID),
		SessionEpoch:    report.SessionEpoch,
		IdleCount:       report.IdleCount,
		InUseCount:      report.InUseCount,
		TargetIdleCount: report.TargetIdleCount,
		Trigger:         strings.TrimSpace(report.Trigger),
		TimestampUnix:   checkTimestamp,
	}
	envelope, err := r.controlPublisher.Publish(
		ctx,
		pb.ControlMessageTunnelPoolReport,
		"tunnel_pool",
		"default",
		payload,
	)
	if err != nil {
		return fmt.Errorf("build tunnel pool report envelope failed: %w", err)
	}
	if err := r.sendBusinessControlEnvelope(ctx, envelope); err != nil {
		// 控制面暂时不可写时静默丢弃，等待会话重连后继续上报。
		return nil
	}
	return nil
}

// writeTCPTunnelHandshake 在 tcp_framed tunnel 建连后写入首帧握手，向 Bridge 对齐 Agent 侧 tunnel_id。
func (r *Runtime) writeTCPTunnelHandshake(
	ctx context.Context,
	rawTunnel transport.Tunnel,
	tunnelID string,
	sessionID string,
	sessionEpoch uint64,
	dialLocalAddr string,
) error {
	if r == nil {
		return errors.New("write tcp tunnel handshake: runtime is nil")
	}
	if rawTunnel == nil {
		return errors.New("write tcp tunnel handshake: nil tunnel")
	}
	normalizedTunnelID := strings.TrimSpace(tunnelID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedTunnelID == "" || normalizedSessionID == "" || sessionEpoch == 0 {
		return errors.New("write tcp tunnel handshake: invalid tunnel/session identity")
	}
	handshakePayload, marshalErr := json.Marshal(pb.TunnelDialAnnounce{
		SessionID:     normalizedSessionID,
		SessionEpoch:  sessionEpoch,
		TunnelID:      normalizedTunnelID,
		DialLocalAddr: strings.TrimSpace(dialLocalAddr),
		TimestampUnix: time.Now().UTC().Unix(),
	})
	if marshalErr != nil {
		return fmt.Errorf("write tcp tunnel handshake: marshal payload: %w", marshalErr)
	}
	writeContext := ctx
	if writeContext == nil {
		writeContext = context.Background()
	}
	if _, hasDeadline := writeContext.Deadline(); !hasDeadline && bridgeTCPTunnelHandshakeTO > 0 {
		var cancel context.CancelFunc
		writeContext, cancel = context.WithTimeout(writeContext, bridgeTCPTunnelHandshakeTO)
		defer cancel()
	}
	if deadline, hasDeadline := writeContext.Deadline(); hasDeadline {
		if err := rawTunnel.SetWriteDeadline(deadline); err != nil {
			return fmt.Errorf("write tcp tunnel handshake: set write deadline: %w", err)
		}
		defer func() {
			_ = rawTunnel.SetWriteDeadline(time.Time{})
		}()
	}
	writtenSize, writeErr := rawTunnel.Write(handshakePayload)
	if writeErr != nil {
		return fmt.Errorf("write tcp tunnel handshake: write payload: %w", writeErr)
	}
	if writtenSize != len(handshakePayload) {
		return fmt.Errorf("write tcp tunnel handshake: %w", io.ErrShortWrite)
	}
	return nil
}

// reportTunnelPoolNow 触发一次立即上报，用于会话激活后的首轮对账。
func (r *Runtime) reportTunnelPoolNow(ctx context.Context, trigger string) {
	if r == nil || r.tunnelReporter == nil {
		return
	}
	// Reporter 失败不应中断主流程，留给周期/事件上报继续纠偏。
	if err := r.tunnelReporter.ReportNow(ctx, trigger); err != nil {
		sessionID, sessionEpoch, bridgeState := r.bridgeRuntimeContext()
		r.appendDiagnoseEvent(runtimeDiagnoseEvent{
			Level:        events.EventWarn,
			Module:       events.ModuleAgentRuntimeRefill,
			Code:         events.CodeTunnelPoolReportFailed,
			Message:      fmt.Sprintf("tunnel pool report failed trigger=%s error=%v", strings.TrimSpace(trigger), err),
			SessionID:    sessionID,
			SessionEpoch: sessionEpoch,
			BridgeState:  bridgeState,
			Trigger:      strings.TrimSpace(trigger),
		})
		return
	}
	sessionID, sessionEpoch, bridgeState := r.bridgeRuntimeContext()
	r.appendDiagnoseEvent(runtimeDiagnoseEvent{
		Level:        events.EventInfo,
		Module:       events.ModuleAgentRuntimeRefill,
		Code:         events.CodeTunnelPoolReportTriggered,
		Message:      fmt.Sprintf("tunnel pool report triggered trigger=%s", strings.TrimSpace(trigger)),
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		BridgeState:  bridgeState,
		Trigger:      strings.TrimSpace(trigger),
	})
}

// sendBusinessControlEnvelope 将业务 envelope 编码为控制帧并发送到 Bridge。
func (r *Runtime) sendBusinessControlEnvelope(ctx context.Context, envelope pb.ControlEnvelope) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	if strings.TrimSpace(envelope.ConnectorID) == "" {
		// 兼容旧调用方未显式填充 connector_id：统一回填本地 agent 标识。
		envelope.ConnectorID = strings.TrimSpace(r.cfg.AgentID)
	}
	r.bridgeMu.RLock()
	controlChannel := r.controlChannel
	r.bridgeMu.RUnlock()
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}
	controlFrame, err := transport.EncodeBusinessControlEnvelopeFrame(envelope)
	if err != nil {
		return fmt.Errorf("encode business control envelope failed: %w", err)
	}
	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	writeContext, cancel := context.WithTimeout(normalizedContext, bridgeBusinessWriteTimeout)
	defer cancel()
	if err := writeControlFrameWithPriority(
		writeContext,
		controlChannel,
		controlFrame,
		transport.RecommendControlFramePriority(controlFrame.Type),
	); err != nil {
		return err
	}
	return nil
}

// buildConnectorHelloEnvelope 构建握手第一步 ConnectorHello 消息。
func (r *Runtime) buildConnectorHelloEnvelope() pb.ControlEnvelope {
	normalizedBinding := strings.TrimSpace(r.cfg.BridgeTransport)
	helloPayload := pb.ConnectorHello{
		ConnectorID:       strings.TrimSpace(r.cfg.AgentID),
		NodeName:          strings.TrimSpace(r.cfg.AgentID),
		Version:           "agent-core",
		SupportedBindings: []string{normalizedBinding},
		Capabilities: []string{
			"control_handshake_v1",
			"tunnel_recycle_v1",
		},
	}
	encodedPayload, _ := json.Marshal(helloPayload)
	// 握手消息不依赖会话元信息；会话权威值由 Bridge 在 AuthAck 返回。
	return pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorHello,
		ConnectorID:  strings.TrimSpace(r.cfg.AgentID),
		Payload:      encodedPayload,
	}
}

// buildConnectorAuthEnvelope 构建握手第二步 ConnectorAuth 消息。
func (r *Runtime) buildConnectorAuthEnvelope() pb.ControlEnvelope {
	authPayload := pb.ConnectorAuth{
		AuthMethod:       strings.TrimSpace(r.cfg.Session.AuthMethod),
		Token:            strings.TrimSpace(r.cfg.Session.AuthToken),
		ClientCapVersion: strings.TrimSpace(r.cfg.Session.ClientCapVersion),
	}
	encodedPayload, _ := json.Marshal(authPayload)
	return pb.ControlEnvelope{
		VersionMajor: 2,
		VersionMinor: 1,
		MessageType:  pb.ControlMessageConnectorAuth,
		ConnectorID:  strings.TrimSpace(r.cfg.AgentID),
		Payload:      encodedPayload,
	}
}

// applyAuthoritativeSession 使用 Bridge 返回的权威 session_id/session_epoch 更新本地上下文。
func (r *Runtime) applyAuthoritativeSession(sessionID string, sessionEpoch uint64) {
	if r == nil {
		return
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return
	}
	r.bridgeMu.Lock()
	// 覆盖 connect 阶段临时会话值，确保后续控制面消息携带权威代际。
	r.bridgeSession = normalizedSessionID
	r.bridgeEpoch = sessionEpoch
	r.updatedAt = time.Now().UTC()
	r.bridgeMu.Unlock()

	// 同步各组件会话上下文，避免跨代际污染。
	if r.refillHandler != nil {
		r.refillHandler.SetSession(normalizedSessionID, sessionEpoch)
	}
	if r.controlPublisher != nil {
		r.controlPublisher.SetSession(normalizedSessionID, sessionEpoch)
	}
	if r.tunnelReporter != nil {
		r.tunnelReporter.SetSession(normalizedSessionID, sessionEpoch)
	}
}

func (r *Runtime) refreshQUICTunnelProducer(sessionID string, sessionEpoch uint64) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	r.bridgeMu.RLock()
	quicConn := r.quicConn
	r.bridgeMu.RUnlock()
	quicProducer, err := newQUICTunnelProducer(quicConn, sessionID, sessionEpoch, r.cfg.AgentID)
	if err != nil {
		return err
	}
	r.bridgeMu.Lock()
	r.quicTunnelProducer = quicProducer
	r.bridgeMu.Unlock()
	return nil
}

// waitHandshakeBusinessEnvelope 等待并解析握手阶段来自 Bridge 的业务控制消息。
func (r *Runtime) waitHandshakeBusinessEnvelope(
	ctx context.Context,
	controlChannel transport.ControlChannel,
) (pb.ControlEnvelope, error) {
	for {
		if ctx.Err() != nil {
			return pb.ControlEnvelope{}, ctx.Err()
		}
		frame, err := controlChannel.ReadControlFrame(ctx)
		if err != nil {
			return pb.ControlEnvelope{}, err
		}
		switch frame.Type {
		case transport.ControlFrameTypeHeartbeatPing:
			// 握手窗口内仍需响应 ping，避免对端将连接误判为超时。
			if err := r.sendControlHeartbeatPong(ctx, controlChannel); err != nil {
				return pb.ControlEnvelope{}, err
			}
			continue
		case transport.ControlFrameTypeHeartbeatPong:
			r.touchBridgeHeartbeat()
			continue
		default:
			// 非业务帧直接忽略，保持对未知扩展帧的兼容性。
			if _, err := transport.ControlMessageTypeForFrameType(frame.Type); err != nil {
				continue
			}
		}
		envelope, err := transport.DecodeBusinessControlEnvelopeFrame(frame)
		if err != nil {
			return pb.ControlEnvelope{}, fmt.Errorf("decode handshake business control frame failed: %w", err)
		}
		return envelope, nil
	}
}

// performControlHandshake 执行 Hello->Welcome->Auth->AuthAck 握手闭环。
func (r *Runtime) performControlHandshake(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	r.bridgeMu.RLock()
	controlChannel := r.controlChannel
	r.bridgeMu.RUnlock()
	if controlChannel == nil {
		return errors.New("control channel is nil")
	}

	normalizedContext := ctx
	if normalizedContext == nil {
		normalizedContext = context.Background()
	}
	handshakeTimeout := r.cfg.Session.AuthTimeout
	if handshakeTimeout <= 0 {
		handshakeTimeout = 5 * time.Second
	}
	handshakeContext, cancelHandshake := context.WithTimeout(normalizedContext, handshakeTimeout)
	defer cancelHandshake()

	helloEnvelope := r.buildConnectorHelloEnvelope()
	if err := r.sendBusinessControlEnvelope(handshakeContext, helloEnvelope); err != nil {
		return fmt.Errorf("send connector hello failed: %w", err)
	}

	welcomeEnvelope, err := r.waitHandshakeBusinessEnvelope(handshakeContext, controlChannel)
	if err != nil {
		return fmt.Errorf("wait connector welcome failed: %w", err)
	}
	if welcomeEnvelope.MessageType != pb.ControlMessageConnectorWelcome {
		return fmt.Errorf("unexpected handshake response before auth: %s", welcomeEnvelope.MessageType)
	}
	var welcomePayload pb.ConnectorWelcome
	if err := json.Unmarshal(welcomeEnvelope.Payload, &welcomePayload); err != nil {
		return fmt.Errorf("decode connector welcome failed: %w", err)
	}
	if welcomePayload.AssignedSessionEpoch == 0 {
		return errors.New("invalid connector welcome: assigned_session_epoch is empty")
	}

	authEnvelope := r.buildConnectorAuthEnvelope()
	if err := r.sendBusinessControlEnvelope(handshakeContext, authEnvelope); err != nil {
		return fmt.Errorf("send connector auth failed: %w", err)
	}

	authAckEnvelope, err := r.waitHandshakeBusinessEnvelope(handshakeContext, controlChannel)
	if err != nil {
		return fmt.Errorf("wait connector auth ack failed: %w", err)
	}
	if authAckEnvelope.MessageType != pb.ControlMessageConnectorAuthAck {
		return fmt.Errorf("unexpected handshake response after auth: %s", authAckEnvelope.MessageType)
	}
	var authAckPayload pb.ConnectorAuthAck
	if err := json.Unmarshal(authAckEnvelope.Payload, &authAckPayload); err != nil {
		return fmt.Errorf("decode connector auth ack failed: %w", err)
	}
	if !authAckPayload.Success {
		// 认证失败错误信息仅透出 code/message，避免输出 token。
		return fmt.Errorf(
			"connector auth rejected: code=%s message=%s",
			strings.TrimSpace(authAckPayload.ErrorCode),
			strings.TrimSpace(authAckPayload.ErrorMessage),
		)
	}
	if strings.TrimSpace(authAckPayload.SessionID) == "" || authAckPayload.SessionEpoch == 0 {
		return errors.New("invalid connector auth ack: empty session authority")
	}
	if authAckPayload.SessionEpoch != welcomePayload.AssignedSessionEpoch {
		return fmt.Errorf(
			"invalid connector auth ack epoch: ack=%d welcome=%d",
			authAckPayload.SessionEpoch,
			welcomePayload.AssignedSessionEpoch,
		)
	}
	r.applyAuthoritativeSession(authAckPayload.SessionID, authAckPayload.SessionEpoch)
	if strings.TrimSpace(r.cfg.BridgeTransport) == transport.BindingTypeQUICNative.String() {
		if err := r.refreshQUICTunnelProducer(authAckPayload.SessionID, authAckPayload.SessionEpoch); err != nil {
			return fmt.Errorf("refresh quic tunnel producer failed: %w", err)
		}
	}
	return nil
}

func (r *Runtime) waitUntilReconnectCommand(ctx context.Context, failStreak *uint32) error {
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case command := <-r.bridgeCommandChan:
			switch command.kind {
			case bridgeCommandReconnect:
				r.bridgeMu.Lock()
				r.bridgeDesiredUp = true
				r.bridgeState = events.BridgeStateReconnecting
				r.updatedAt = time.Now().UTC()
				r.bridgeMu.Unlock()
				if command.resetBackoff && failStreak != nil {
					*failStreak = 0
				}
				return nil
			case bridgeCommandDrain:
				r.setBridgeDrained()
				if failStreak != nil {
					*failStreak = 0
				}
			}
		}
	}
}

func (r *Runtime) waitRetryWindow(ctx context.Context, backoff time.Duration, failStreak *uint32) error {
	retryTimer := time.NewTimer(backoff)
	defer func() {
		if !retryTimer.Stop() {
			select {
			case <-retryTimer.C:
			default:
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-retryTimer.C:
			return nil
		case command := <-r.bridgeCommandChan:
			switch command.kind {
			case bridgeCommandReconnect:
				r.bridgeMu.Lock()
				r.bridgeDesiredUp = true
				r.bridgeMu.Unlock()
				if command.resetBackoff && failStreak != nil {
					*failStreak = 0
				}
				return nil
			case bridgeCommandDrain:
				r.closeCurrentControlChannel()
				r.notifyTunnelManagerState(tunnel.SessionStateDraining)
				r.setBridgeDrained()
				if failStreak != nil {
					*failStreak = 0
				}
				return nil
			}
		}
	}
}

func (r *Runtime) waitForActiveExit(ctx context.Context) activeExitResult {
	controlChannel := func() transport.ControlChannel {
		r.bridgeMu.RLock()
		defer r.bridgeMu.RUnlock()
		return r.controlChannel
	}()
	if controlChannel == nil {
		return activeExitResult{reason: activeExitLost, err: errors.New("control channel is nil")}
	}

	readFrameChan := make(chan transport.ControlFrame, 16)
	readErrChan := make(chan error, 1)
	go func() {
		readErrChan <- r.readControlFrames(ctx, controlChannel, readFrameChan)
	}()
	heartbeatInterval := bridgeHeartbeatPingInterval
	if r != nil && r.cfg.Session.HeartbeatInterval > 0 {
		heartbeatInterval = r.cfg.Session.HeartbeatInterval
	}
	heartbeatTicker := time.NewTicker(heartbeatInterval)
	defer heartbeatTicker.Stop()
	serviceHealthTicker := time.NewTicker(serviceHealthCheckScanInterval)
	defer serviceHealthTicker.Stop()
	serviceHealthScanPermit := make(chan struct{}, 1)
	serviceHealthScanPermit <- struct{}{}

	missedPongCount := uint32(0)
	awaitingPong := false
	if err := r.sendControlHeartbeatPing(ctx, controlChannel); err != nil {
		r.closeCurrentControlChannel()
		r.notifyTunnelManagerState(tunnel.SessionStateStale)
		r.setBridgeLost(err)
		return activeExitResult{reason: activeExitLost, err: err}
	}
	awaitingPong = true

	for {
		select {
		case <-ctx.Done():
			r.closeCurrentControlChannel()
			return activeExitResult{reason: activeExitContextDone, err: ctx.Err()}
		case <-heartbeatTicker.C:
			if awaitingPong {
				missedPongCount++
				if missedPongCount >= bridgeHeartbeatMissThreshold {
					timeoutErr := fmt.Errorf(
						"control heartbeat timeout: missed pong %d consecutive intervals",
						bridgeHeartbeatMissThreshold,
					)
					r.closeCurrentControlChannel()
					r.notifyTunnelManagerState(tunnel.SessionStateStale)
					r.setBridgeLost(timeoutErr)
					return activeExitResult{reason: activeExitLost, err: timeoutErr}
				}
			}
			if err := r.sendControlHeartbeatPing(ctx, controlChannel); err != nil {
				r.closeCurrentControlChannel()
				r.notifyTunnelManagerState(tunnel.SessionStateStale)
				r.setBridgeLost(err)
				return activeExitResult{reason: activeExitLost, err: err}
			}
			awaitingPong = true
		case <-serviceHealthTicker.C:
			select {
			case <-serviceHealthScanPermit:
				// 健康扫描改为异步串行执行，避免阻塞保活帧收发与控制命令处理。
				go func(scanContext context.Context) {
					defer func() {
						serviceHealthScanPermit <- struct{}{}
					}()
					// 周期健康检查采用 best-effort，失败由后续周期继续纠偏。
					_ = r.reportCatalogHealthByInterval(scanContext)
				}(ctx)
			default:
				// 上一轮尚未完成时跳过当前 tick，避免堆积后台扫描。
			}
		case frame := <-readFrameChan:
			r.touchBridgeHeartbeat()
			switch frame.Type {
			case transport.ControlFrameTypeHeartbeatPing:
				if err := r.sendControlHeartbeatPong(ctx, controlChannel); err != nil {
					r.closeCurrentControlChannel()
					r.notifyTunnelManagerState(tunnel.SessionStateStale)
					r.setBridgeLost(err)
					return activeExitResult{reason: activeExitLost, err: err}
				}
			case transport.ControlFrameTypeHeartbeatPong:
				awaitingPong = false
				missedPongCount = 0
			default:
				// 非保活帧尝试按业务控制消息解码；未知帧类型保持兼容并忽略。
				if _, err := transport.ControlMessageTypeForFrameType(frame.Type); err != nil {
					continue
				}
				if err := r.handleBridgeBusinessControlFrame(ctx, frame); err != nil {
					r.closeCurrentControlChannel()
					r.notifyTunnelManagerState(tunnel.SessionStateStale)
					r.setBridgeLost(err)
					return activeExitResult{reason: activeExitLost, err: err}
				}
			}
		case command := <-r.bridgeCommandChan:
			switch command.kind {
			case bridgeCommandDrain:
				r.closeCurrentControlChannel()
				r.notifyTunnelManagerState(tunnel.SessionStateDraining)
				r.setBridgeDrained()
				return activeExitResult{reason: activeExitDrained, resetBackoff: true}
			case bridgeCommandReconnect:
				r.closeCurrentControlChannel()
				return activeExitResult{reason: activeExitReconnect, resetBackoff: command.resetBackoff}
			}
		case readErr := <-readErrChan:
			r.closeCurrentControlChannel()
			if readErr == nil {
				readErr = errors.New("control channel closed")
			}
			r.notifyTunnelManagerState(tunnel.SessionStateStale)
			r.setBridgeLost(readErr)
			return activeExitResult{reason: activeExitLost, err: readErr}
		}
	}
}

func (r *Runtime) runBridgeControlLoop(ctx context.Context) error {
	if r == nil {
		return errors.New("runtime is nil")
	}
	var failStreak uint32 = 0
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !r.bridgeDesiredUpState() {
			if err := r.waitUntilReconnectCommand(ctx, &failStreak); err != nil {
				return err
			}
		}

		if err := r.connectBridgeControl(ctx); err != nil {
			failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageConnect, false)
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		}
		failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageConnect, true)
		if err := r.performControlHandshake(ctx); err != nil {
			// 握手失败视为连接不可用，按失活链路进入重连退避。
			r.closeCurrentControlChannel()
			r.notifyTunnelManagerState(tunnel.SessionStateStale)
			r.setBridgeLost(err)
			failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageHandshake, false)
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		}
		failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageHandshake, true)
		if err := r.syncServiceControlState(ctx); err != nil {
			// 资源同步失败视为会话不可用，按失活链路进入重连退避。
			r.closeCurrentControlChannel()
			r.notifyTunnelManagerState(tunnel.SessionStateStale)
			r.setBridgeLost(err)
			failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageInitialSync, false)
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		}
		failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageInitialSync, true)
		// 会话进入 ready 阶段后重置 fail streak，后续新一轮故障重新从 1s 开始退避。
		failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageReady, true)
		r.setBridgeSessionReady(true)
		r.notifyTunnelManagerState(tunnel.SessionStateActive)
		// 会话建连成功后立即上报一次 tunnel 池快照，触发 Bridge 侧补池判定。
		r.reportTunnelPoolNow(ctx, "session_active")
		activeExit := r.waitForActiveExit(ctx)
		switch activeExit.reason {
		case activeExitContextDone:
			if activeExit.err == nil {
				return ctx.Err()
			}
			return activeExit.err
		case activeExitDrained:
			failStreak = 0
			continue
		case activeExitReconnect:
			if activeExit.resetBackoff {
				failStreak = 0
			}
			continue
		case activeExitLost:
			failStreak = nextBridgeRetryFailStreak(failStreak, bridgeRetryStageReady, false)
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(activeExit.err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		default:
			continue
		}
	}
}

// 组装 agent.snapshot 返回体。
func (r *Runtime) agentSnapshotPayload() map[string]any {
	sessionSnapshot := r.sessionSnapshot()
	quicSnapshot := r.quicSnapshot()
	tunnelPoolSnapshot := tunnel.Snapshot{}
	if r.tunnelRegistry != nil {
		tunnelPoolSnapshot = r.tunnelRegistry.Snapshot()
	}
	return map[string]any{
		"agent_id":           r.cfg.AgentID,
		"bridge_addr":        r.cfg.BridgeAddr,
		"bridge_transport":   r.cfg.BridgeTransport,
		"state":              sessionSnapshot.state,
		"session_id":         sessionSnapshot.sessionID,
		"session_epoch":      sessionSnapshot.sessionEpoch,
		"started_at_ms":      uint64(r.startedAt.UnixMilli()),
		"updated_at_ms":      sessionSnapshot.updatedAtMS,
		"last_error":         sessionSnapshot.lastError,
		"bridge_unavailable": sessionSnapshot.unavailableReason,
		"quic":               quicSnapshotPayload(quicSnapshot),
		"tunnel_pool": map[string]any{
			"opening":  tunnelPoolSnapshot.OpeningCount,
			"idle":     tunnelPoolSnapshot.IdleCount,
			"reserved": tunnelPoolSnapshot.ReservedCount,
			"active":   tunnelPoolSnapshot.ActiveCount,
			"closing":  tunnelPoolSnapshot.ClosingCount,
			"closed":   tunnelPoolSnapshot.ClosedCount,
			"broken":   tunnelPoolSnapshot.BrokenCount,
			"total":    tunnelPoolSnapshot.TotalCount,
		},
	}
}

// 组装 session.snapshot 返回体。
func (r *Runtime) sessionSnapshotPayload() map[string]any {
	sessionSnapshot := r.sessionSnapshot()
	quicSnapshot := r.quicSnapshot()
	return map[string]any{
		"bridge_transport":          r.cfg.BridgeTransport,
		"state":                     sessionSnapshot.state,
		"session_id":                sessionSnapshot.sessionID,
		"session_epoch":             sessionSnapshot.sessionEpoch,
		"last_heartbeat_at_ms":      sessionSnapshot.lastHeartbeatMS,
		"last_heartbeat_sent_at_ms": sessionSnapshot.lastHeartbeatSent,
		"reconnect_total":           sessionSnapshot.reconnectTotal,
		"retry_fail_streak":         sessionSnapshot.retryFailStreak,
		"retry_backoff_ms":          sessionSnapshot.retryBackoffMS,
		"next_retry_at_ms":          sessionSnapshot.nextRetryAtMS,
		"updated_at_ms":             sessionSnapshot.updatedAtMS,
		"last_error":                sessionSnapshot.lastError,
		"unavailable_reason":        sessionSnapshot.unavailableReason,
		"quic":                      quicSnapshotPayload(quicSnapshot),
		"source":                    "agent.runtime",
	}
}

func resolveDefaultHealthCheckModeByProtocol(protocol string) string {
	normalizedProtocol := strings.ToLower(strings.TrimSpace(protocol))
	switch normalizedProtocol {
	case "http", "https":
		return normalizedProtocol
	default:
		return "tcp"
	}
}

func normalizeServiceHealthCheckPath(path string) string {
	normalizedPath := strings.TrimSpace(path)
	if normalizedPath == "" {
		return "/"
	}
	if !strings.HasPrefix(normalizedPath, "/") {
		return "/" + normalizedPath
	}
	return normalizedPath
}

func normalizeServiceHealthCheckConfig(
	serviceProtocol string,
	intervalSec uint32,
	mode string,
	path string,
) (pb.HealthCheckConfig, error) {
	normalizedMode := strings.ToLower(strings.TrimSpace(mode))
	if normalizedMode == "" {
		normalizedMode = resolveDefaultHealthCheckModeByProtocol(serviceProtocol)
	}
	switch normalizedMode {
	case "tcp", "http", "https":
	default:
		return pb.HealthCheckConfig{}, fmt.Errorf("invalid health_check_mode=%s", strings.TrimSpace(mode))
	}

	normalizedIntervalSec := intervalSec
	if normalizedIntervalSec == 0 {
		normalizedIntervalSec = defaultServiceHealthCheckIntervalSec
	}

	normalizedPath := ""
	if normalizedMode == "http" || normalizedMode == "https" {
		normalizedPath = normalizeServiceHealthCheckPath(path)
	}

	return pb.HealthCheckConfig{
		Type:        normalizedMode,
		Endpoint:    normalizedPath,
		IntervalSec: normalizedIntervalSec,
	}, nil
}

func isL7CapableServiceProtocol(serviceProtocol string) bool {
	switch strings.ToLower(strings.TrimSpace(serviceProtocol)) {
	case "http", "https", "grpc", "grpc_h2", "grpc-h2":
		return true
	default:
		return false
	}
}

func hasMeaningfulServiceExposure(exposure pb.ServiceExposure) bool {
	return strings.TrimSpace(string(exposure.IngressMode)) != "" ||
		strings.TrimSpace(exposure.Host) != "" ||
		exposure.ListenPort > 0 ||
		strings.TrimSpace(exposure.SNIName) != "" ||
		strings.TrimSpace(exposure.PathPrefix) != "" ||
		exposure.AllowExport
}

func normalizeServiceExposure(serviceProtocol string, exposure pb.ServiceExposure) (pb.ServiceExposure, error) {
	normalizedExposure := pb.ServiceExposure{
		IngressMode: pb.IngressMode(strings.TrimSpace(string(exposure.IngressMode))),
		Host:        strings.ToLower(strings.TrimSpace(exposure.Host)),
		ListenPort:  exposure.ListenPort,
		SNIName:     strings.TrimSpace(exposure.SNIName),
		PathPrefix:  strings.TrimSpace(exposure.PathPrefix),
		AllowExport: exposure.AllowExport,
	}
	if normalizedExposure.IngressMode == "" && !hasMeaningfulServiceExposure(normalizedExposure) {
		return pb.ServiceExposure{}, nil
	}
	if normalizedExposure.IngressMode == "" {
		if isL7CapableServiceProtocol(serviceProtocol) {
			normalizedExposure.IngressMode = pb.IngressModeL7Shared
		} else {
			normalizedExposure.IngressMode = pb.IngressModeL4DedicatedPort
		}
	}
	switch normalizedExposure.IngressMode {
	case pb.IngressModeL7Shared:
		if !isL7CapableServiceProtocol(serviceProtocol) {
			return pb.ServiceExposure{}, fmt.Errorf("exposure.ingress_mode=%s requires an http/https/grpc upstream", normalizedExposure.IngressMode)
		}
		normalizedExposure.SNIName = ""
	case pb.IngressModeTLSSNIShared:
		if strings.ToLower(strings.TrimSpace(serviceProtocol)) != "https" {
			return pb.ServiceExposure{}, fmt.Errorf("exposure.ingress_mode=%s requires protocol=https", normalizedExposure.IngressMode)
		}
		if normalizedExposure.SNIName == "" {
			return pb.ServiceExposure{}, errors.New("exposure.sni_name is required for tls_sni_shared")
		}
		normalizedExposure.Host = ""
		normalizedExposure.PathPrefix = ""
	case pb.IngressModeL4DedicatedPort:
		normalizedExposure.Host = ""
		normalizedExposure.PathPrefix = ""
		normalizedExposure.SNIName = ""
	default:
		return pb.ServiceExposure{}, fmt.Errorf("unsupported exposure.ingress_mode=%s", normalizedExposure.IngressMode)
	}
	return normalizedExposure, nil
}

func buildLocalRPCHeaderMatcherPayloads(matchers []pb.HeaderMatcher) []map[string]any {
	if len(matchers) == 0 {
		return nil
	}
	payloads := make([]map[string]any, 0, len(matchers))
	for _, matcher := range matchers {
		payload := map[string]any{
			"name": strings.TrimSpace(matcher.Name),
		}
		if strings.TrimSpace(matcher.Exact) != "" {
			payload["exact"] = strings.TrimSpace(matcher.Exact)
		}
		if strings.TrimSpace(matcher.Prefix) != "" {
			payload["prefix"] = strings.TrimSpace(matcher.Prefix)
		}
		if strings.TrimSpace(matcher.Regex) != "" {
			payload["regex"] = strings.TrimSpace(matcher.Regex)
		}
		if matcher.Present != nil {
			payload["present"] = *matcher.Present
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func buildLocalRPCQueryMatcherPayloads(matchers []pb.QueryMatcher) []map[string]any {
	if len(matchers) == 0 {
		return nil
	}
	payloads := make([]map[string]any, 0, len(matchers))
	for _, matcher := range matchers {
		payload := map[string]any{
			"name": strings.TrimSpace(matcher.Name),
		}
		if strings.TrimSpace(matcher.Exact) != "" {
			payload["exact"] = strings.TrimSpace(matcher.Exact)
		}
		if strings.TrimSpace(matcher.Prefix) != "" {
			payload["prefix"] = strings.TrimSpace(matcher.Prefix)
		}
		if strings.TrimSpace(matcher.Regex) != "" {
			payload["regex"] = strings.TrimSpace(matcher.Regex)
		}
		if matcher.Present != nil {
			payload["present"] = *matcher.Present
		}
		payloads = append(payloads, payload)
	}
	return payloads
}

func buildLocalRPCRouteHintPayload(routeHint pb.RouteHint) map[string]any {
	if len(routeHint.MatchHeaders) == 0 && len(routeHint.MatchQueries) == 0 && routeHint.Priority == 0 {
		return nil
	}
	payload := map[string]any{
		"priority": routeHint.Priority,
	}
	if matchHeaders := buildLocalRPCHeaderMatcherPayloads(routeHint.MatchHeaders); len(matchHeaders) > 0 {
		payload["match_headers"] = matchHeaders
	}
	if matchQueries := buildLocalRPCQueryMatcherPayloads(routeHint.MatchQueries); len(matchQueries) > 0 {
		payload["match_queries"] = matchQueries
	}
	return payload
}

func buildLocalRPCServiceExposurePayload(exposure pb.ServiceExposure) map[string]any {
	if !hasMeaningfulServiceExposure(exposure) {
		return nil
	}
	payload := map[string]any{
		"ingress_mode": string(exposure.IngressMode),
		"allow_export": exposure.AllowExport,
	}
	if strings.TrimSpace(exposure.Host) != "" {
		payload["host"] = strings.ToLower(strings.TrimSpace(exposure.Host))
	}
	if exposure.ListenPort > 0 {
		payload["listen_port"] = exposure.ListenPort
	}
	if strings.TrimSpace(exposure.SNIName) != "" {
		payload["sni_name"] = strings.TrimSpace(exposure.SNIName)
	}
	if strings.TrimSpace(exposure.PathPrefix) != "" {
		payload["path_prefix"] = strings.TrimSpace(exposure.PathPrefix)
	}
	return payload
}

func serviceHealthCheckInterval(registration adapter.LocalRegistration) time.Duration {
	intervalSec := registration.HealthCheck.IntervalSec
	if intervalSec == 0 {
		intervalSec = defaultServiceHealthCheckIntervalSec
	}
	return time.Duration(intervalSec) * time.Second
}

// 新增或更新本地服务目录项，并在会话 ACTIVE 时尽力同步控制面。
func (r *Runtime) addOrUpdateService(input runtimeServiceAddInput) (map[string]any, error) {
	if r == nil || r.serviceCatalog == nil {
		return nil, errors.New("service catalog is not initialized")
	}
	normalizedServiceName := strings.TrimSpace(input.ServiceName)
	if normalizedServiceName == "" {
		return nil, errors.New("service_name is required")
	}
	normalizedProtocol := strings.ToLower(strings.TrimSpace(input.Protocol))
	if normalizedProtocol == "" {
		normalizedProtocol = "tcp"
	}
	normalizedHost := strings.TrimSpace(input.Host)
	if normalizedHost == "" {
		normalizedHost = "127.0.0.1"
	}
	normalizedSNIName := strings.TrimSpace(input.SNIName)
	if input.Port == 0 {
		return nil, errors.New("port must be greater than 0")
	}
	normalizedNamespace := strings.TrimSpace(input.Scope.Namespace)
	normalizedEnvironment := strings.TrimSpace(input.Scope.Environment)
	if normalizedNamespace == "" {
		return nil, errors.New("scope.namespace is required")
	}
	if normalizedEnvironment == "" {
		return nil, errors.New("scope.environment is required")
	}
	if normalizedProtocol != "https" {
		normalizedSNIName = ""
	}
	healthCheckConfig, err := normalizeServiceHealthCheckConfig(
		normalizedProtocol,
		input.HealthCheckIntervalSec,
		input.HealthCheckMode,
		input.HealthCheckPath,
	)
	if err != nil {
		return nil, err
	}
	normalizedExposure, err := normalizeServiceExposure(normalizedProtocol, input.Exposure)
	if err != nil {
		return nil, err
	}
	if err := validate.ValidateRouteHint(input.RouteHint); err != nil {
		return nil, err
	}

	record := r.serviceCatalog.Upsert(time.Now().UTC(), adapter.LocalRegistration{
		LogicalServiceID: "",
		InstanceID:       strings.TrimSpace(input.InstanceID),
		Scope: pb.Scope{
			Namespace:   normalizedNamespace,
			Environment: normalizedEnvironment,
		},
		ServiceName: normalizedServiceName,
		ServiceType: normalizedProtocol,
		Exposure:    normalizedExposure,
		HealthCheck: healthCheckConfig,
		RouteHint:   input.RouteHint,
		Endpoints: []pb.ServiceEndpoint{
			{
				EndpointID: fmt.Sprintf("%s-%s-%d", normalizedServiceName, normalizedHost, input.Port),
				Protocol:   normalizedProtocol,
				Host:       normalizedHost,
				Port:       input.Port,
				ServerName: normalizedSNIName,
			},
		},
	})
	if strings.TrimSpace(record.Registration.InstanceID) == "" {
		return nil, errors.New("add service failed: empty service identity")
	}
	if _, _, active := r.bridgeSessionConnectedMeta(); active {
		// 会话可用时尽力触发一次全量同步；失败仅记录诊断，不回滚本地目录。
		if err := r.syncServiceControlState(context.Background()); err != nil {
			r.appendDiagnoseEvent(runtimeDiagnoseEvent{
				Level:   events.EventWarn,
				Module:  events.ModuleAgentRuntimeService,
				Code:    events.CodeServiceSyncFailed,
				Message: fmt.Sprintf("sync service to bridge failed: %v", err),
			})
		}
	}

	updatedAtMS := runtimeNowMillis()
	if !record.UpdatedAt.IsZero() {
		updatedAtMS = uint64(record.UpdatedAt.UTC().UnixMilli())
	}
	return map[string]any{
		"accepted":                  true,
		"logical_service_id":        record.Registration.LogicalServiceID,
		"instance_id":               record.Registration.InstanceID,
		"scope":                     record.Registration.Scope,
		"service_name":              record.Registration.ServiceName,
		"protocol":                  normalizedProtocol,
		"host":                      normalizedHost,
		"port":                      input.Port,
		"sni_name":                  normalizedSNIName,
		"exposure":                  buildLocalRPCServiceExposurePayload(record.Registration.Exposure),
		"health_check_mode":         record.Registration.HealthCheck.Type,
		"health_check_interval_sec": record.Registration.HealthCheck.IntervalSec,
		"health_check_path":         record.Registration.HealthCheck.Endpoint,
		"route_hint":                buildLocalRPCRouteHintPayload(record.Registration.RouteHint),
		"endpoint_count":            len(record.Registration.Endpoints),
		"updated_at_ms":             updatedAtMS,
		"source":                    "agent.runtime",
	}, nil
}

// 删除本地服务目录项，并在会话 ACTIVE 时尽力同步一次 UnpublishService。
func (r *Runtime) removeService(input runtimeServiceDeleteInput) (map[string]any, error) {
	if r == nil || r.serviceCatalog == nil {
		return nil, errors.New("service catalog is not initialized")
	}
	normalizedLogicalServiceID := strings.TrimSpace(input.LogicalServiceID)
	normalizedInstanceID := strings.TrimSpace(input.InstanceID)
	if normalizedLogicalServiceID == "" && normalizedInstanceID == "" {
		return nil, errors.New("logical_service_id or instance_id is required")
	}

	var targetRegistration adapter.LocalRegistration
	for _, record := range r.serviceCatalog.List() {
		recordLogicalServiceID := strings.TrimSpace(record.Registration.LogicalServiceID)
		recordInstanceID := strings.TrimSpace(record.Registration.InstanceID)
		if normalizedInstanceID != "" {
			if recordInstanceID != normalizedInstanceID {
				continue
			}
		} else if recordLogicalServiceID != normalizedLogicalServiceID {
			continue
		}
		normalizedLogicalServiceID = recordLogicalServiceID
		normalizedInstanceID = recordInstanceID
		targetRegistration = record.Registration
		break
	}
	if normalizedLogicalServiceID == "" && normalizedInstanceID == "" {
		return map[string]any{
			"accepted":           true,
			"deleted":            false,
			"logical_service_id": strings.TrimSpace(input.LogicalServiceID),
			"instance_id":        strings.TrimSpace(input.InstanceID),
			"updated_at_ms":      runtimeNowMillis(),
			"source":             "agent.runtime",
		}, nil
	}

	deleted := r.serviceCatalog.RemoveByInstanceID(normalizedInstanceID)
	if deleted {
		if _, _, active := r.bridgeSessionConnectedMeta(); active && r.controlPublisher != nil {
			unpublishPayload := adapter.ToUnpublishService(
				targetRegistration,
				"service removed by agent localrpc",
			)
			resourceID := strings.TrimSpace(unpublishPayload.InstanceID)
			if resourceID == "" {
				resourceID = strings.TrimSpace(unpublishPayload.LogicalServiceID)
			}
			envelope, err := r.controlPublisher.Publish(
				context.Background(),
				pb.ControlMessageUnpublishService,
				"service",
				resourceID,
				unpublishPayload,
			)
			if err != nil {
				r.appendDiagnoseEvent(runtimeDiagnoseEvent{
					Level:   events.EventWarn,
					Module:  events.ModuleAgentRuntimeService,
					Code:    events.CodeServiceUnpublishBuildFailed,
					Message: fmt.Sprintf("build unpublish service envelope failed: %v", err),
				})
			} else if err := r.sendBusinessControlEnvelope(context.Background(), envelope); err != nil {
				r.appendDiagnoseEvent(runtimeDiagnoseEvent{
					Level:   events.EventWarn,
					Module:  events.ModuleAgentRuntimeService,
					Code:    events.CodeServiceUnpublishSendFailed,
					Message: fmt.Sprintf("send unpublish service failed: %v", err),
				})
			}
		}
	}

	return map[string]any{
		"accepted":           true,
		"deleted":            deleted,
		"logical_service_id": normalizedLogicalServiceID,
		"instance_id":        normalizedInstanceID,
		"updated_at_ms":      runtimeNowMillis(),
		"source":             "agent.runtime",
	}, nil
}

func buildRuntimeScopeServiceNameKey(serviceName string, scope pb.Scope) string {
	normalizedServiceName := strings.TrimSpace(serviceName)
	normalizedNamespace := strings.TrimSpace(scope.Namespace)
	normalizedEnvironment := strings.TrimSpace(scope.Environment)
	if normalizedServiceName == "" || normalizedNamespace == "" || normalizedEnvironment == "" {
		return ""
	}
	return normalizedNamespace + "|" + normalizedEnvironment + "|" + normalizedServiceName
}

// 组装 service.list 返回体。
func (r *Runtime) serviceListPayload() map[string]any {
	if r == nil || r.serviceCatalog == nil {
		return map[string]any{
			"services":      []map[string]any{},
			"updated_at_ms": runtimeNowMillis(),
			"source":        "agent.runtime",
		}
	}
	records := r.serviceCatalog.List()
	items := make([]map[string]any, 0, len(records))
	for _, record := range records {
		updatedAtMS := uint64(runtimeNowMillis())
		if !record.UpdatedAt.IsZero() {
			updatedAtMS = uint64(record.UpdatedAt.UTC().UnixMilli())
		}
		endpointsPayload := make([]map[string]any, 0, len(record.Registration.Endpoints))
		primarySNIName := strings.TrimSpace(record.Registration.Exposure.SNIName)
		for _, endpoint := range record.Registration.Endpoints {
			endpointSNIName := strings.TrimSpace(endpoint.ServerName)
			if primarySNIName == "" && endpointSNIName != "" {
				primarySNIName = endpointSNIName
			}
			endpointsPayload = append(endpointsPayload, map[string]any{
				"endpoint_id": endpoint.EndpointID,
				"protocol":    endpoint.Protocol,
				"host":        endpoint.Host,
				"port":        endpoint.Port,
				"server_name": endpoint.ServerName,
				"sni_name":    endpointSNIName,
			})
		}
		items = append(items, map[string]any{
			"logical_service_id":        record.Registration.LogicalServiceID,
			"instance_id":               record.Registration.InstanceID,
			"scope":                     record.Registration.Scope,
			"service_name":              record.Registration.ServiceName,
			"service_type":              record.Registration.ServiceType,
			"protocol":                  record.Registration.ServiceType,
			"exposure":                  buildLocalRPCServiceExposurePayload(record.Registration.Exposure),
			"health_check_mode":         record.Registration.HealthCheck.Type,
			"health_check_interval_sec": record.Registration.HealthCheck.IntervalSec,
			"health_check_path":         record.Registration.HealthCheck.Endpoint,
			"route_hint":                buildLocalRPCRouteHintPayload(record.Registration.RouteHint),
			"status":                    string(pb.ServiceStatusActive),
			"health_status":             string(record.HealthStatus),
			"endpoints":                 endpointsPayload,
			"sni_name":                  primarySNIName,
			"endpoint_count":            len(record.Registration.Endpoints),
			"resource_version":          uint64(0),
			"updated_at_ms":             updatedAtMS,
		})
	}
	return map[string]any{
		"services":      items,
		"updated_at_ms": runtimeNowMillis(),
		"source":        "agent.runtime",
	}
}

// 组装 tunnel.list 返回体。
func (r *Runtime) tunnelListPayload() map[string]any {
	tunnelRecords := []tunnel.Record{}
	if r.tunnelRegistry != nil {
		tunnelRecords = r.tunnelRegistry.List(256)
	}
	sessionSnapshot := r.sessionSnapshot()
	connectionProtocol := strings.TrimSpace(r.cfg.BridgeTransport)
	if connectionProtocol == "" {
		connectionProtocol = "unknown"
	}
	items := make([]map[string]any, 0, len(tunnelRecords))
	for _, record := range tunnelRecords {
		association, _ := r.tunnelAssociationByID(record.TunnelID)
		updatedAtMS := uint64(runtimeNowMillis())
		if !record.UpdatedAt.IsZero() {
			updatedAtMS = uint64(record.UpdatedAt.UnixMilli())
		}
		if !association.UpdatedAt.IsZero() {
			// 关联信息更新时间比 tunnel 状态更新更晚时，优先返回最新运行态时间戳。
			associationUpdatedAtMS := uint64(association.UpdatedAt.UTC().UnixMilli())
			if associationUpdatedAtMS > updatedAtMS {
				updatedAtMS = associationUpdatedAtMS
			}
		}
		remoteAddr := strings.TrimSpace(r.cfg.BridgeAddr)
		items = append(items, map[string]any{
			"tunnel_id":                 record.TunnelID,
			"traffic_id":                association.TrafficID,
			"logical_service_id":        association.LogicalServiceID,
			"instance_id":               association.InstanceID,
			"state":                     string(record.State),
			"protocol":                  connectionProtocol,
			"local_addr":                association.LocalAddr,
			"remote_addr":               remoteAddr,
			"latency_ms":                association.OpenAckLatencyMS,
			"upstream_dial_latency_ms":  association.UpstreamDialLatencyMS,
			"last_heartbeat_at_ms":      sessionSnapshot.lastHeartbeatMS,
			"last_heartbeat_sent_at_ms": sessionSnapshot.lastHeartbeatSent,
			"last_error":                record.LastError,
			"updated_at_ms":             updatedAtMS,
		})
	}
	return map[string]any{
		"tunnels":       items,
		"updated_at_ms": runtimeNowMillis(),
		"source":        "agent.runtime",
	}
}

// 组装 traffic.stats.snapshot 返回体（runtime 真实链路指标）。
func (r *Runtime) trafficStatsSnapshotPayload() map[string]any {
	now := time.Now().UTC()
	uploadTotalBytes := uint64(0)
	downloadTotalBytes := uint64(0)
	metrics := obs.DefaultMetrics
	if r != nil && r.metrics != nil {
		metrics = r.metrics
	}
	if metrics != nil {
		uploadTotalBytes = metrics.AgentTrafficUploadTotalBytes()
		downloadTotalBytes = metrics.AgentTrafficDownloadTotalBytes()
	}

	uploadBytesPerSec := float64(0)
	downloadBytesPerSec := float64(0)
	sampleWindowMS := uint64(0)

	if r != nil {
		r.trafficStatsMutex.Lock()
		if !r.trafficStatsLastAt.IsZero() {
			elapsed := now.Sub(r.trafficStatsLastAt)
			if elapsed < 0 {
				elapsed = 0
			}
			sampleWindowMS = uint64(elapsed.Milliseconds())
			if sampleWindowMS > 0 {
				if uploadTotalBytes >= r.trafficUploadLast {
					uploadBytesPerSec = float64(uploadTotalBytes-r.trafficUploadLast) * 1000 / float64(sampleWindowMS)
				}
				if downloadTotalBytes >= r.trafficDownloadLast {
					downloadBytesPerSec = float64(downloadTotalBytes-r.trafficDownloadLast) * 1000 / float64(sampleWindowMS)
				}
			}
		}
		// 每次快照后更新采样基线，供下一次计算实时速率。
		r.trafficStatsLastAt = now
		r.trafficUploadLast = uploadTotalBytes
		r.trafficDownloadLast = downloadTotalBytes
		r.trafficStatsMutex.Unlock()
	}

	return map[string]any{
		"upload_bytes_per_sec":   uploadBytesPerSec,
		"download_bytes_per_sec": downloadBytesPerSec,
		"upload_total_bytes":     uploadTotalBytes,
		"download_total_bytes":   downloadTotalBytes,
		"sample_window_ms":       sampleWindowMS,
		// runtime 统计是链路视角，不对应具体宿主网卡数量。
		"interface_count": uint64(0),
		"updated_at_ms":   uint64(now.UnixMilli()),
		"source":          "agent.runtime.traffic",
	}
}

// 组装 config.snapshot 返回体。
func (r *Runtime) configSnapshotPayload(ipcTransport string, ipcEndpoint string) map[string]any {
	return map[string]any{
		"agent_id":                     r.cfg.AgentID,
		"bridge_addr":                  r.cfg.BridgeAddr,
		"bridge_transport":             r.cfg.BridgeTransport,
		"tunnel_pool_min_idle":         r.cfg.TunnelPool.MinIdle,
		"tunnel_pool_max_idle":         r.cfg.TunnelPool.MaxIdle,
		"tunnel_pool_max_inflight":     r.cfg.TunnelPool.MaxInflight,
		"tunnel_pool_ttl_ms":           durationToMillis(r.cfg.TunnelPool.TTL),
		"tunnel_pool_max_reuse":        r.cfg.TunnelPool.MaxReuse,
		"tunnel_pool_recycle_ack_ms":   durationToMillis(r.cfg.TunnelPool.RecycleAckTO),
		"tunnel_pool_open_rate":        r.cfg.TunnelPool.OpenRate,
		"tunnel_pool_open_burst":       r.cfg.TunnelPool.OpenBurst,
		"tunnel_pool_reconcile_gap_ms": durationToMillis(r.cfg.TunnelPool.ReconcileGap),
		"ipc_transport":                ipcTransport,
		"ipc_endpoint":                 ipcEndpoint,
		"updated_at_ms":                runtimeNowMillis(),
		"source":                       "agent.runtime",
	}
}

func (r *Runtime) diagnoseSnapshotPayload() map[string]any {
	tunnelPoolSnapshot := tunnel.Snapshot{}
	if r.tunnelRegistry != nil {
		tunnelPoolSnapshot = r.tunnelRegistry.Snapshot()
	}
	sessionSnapshot := r.sessionSnapshot()
	quicSnapshot := r.quicSnapshot()
	events := r.snapshotDiagnoseEvents(runtimeDiagnoseDefaultLogLimit)
	summary := summarizeDiagnoseEvents(events)
	return map[string]any{
		"bridge_transport":    r.cfg.BridgeTransport,
		"state":               sessionSnapshot.state,
		"last_error":          sessionSnapshot.lastError,
		"retry_fail_streak":   sessionSnapshot.retryFailStreak,
		"retry_backoff_ms":    sessionSnapshot.retryBackoffMS,
		"next_retry_at_ms":    sessionSnapshot.nextRetryAtMS,
		"tunnel_idle_count":   tunnelPoolSnapshot.IdleCount,
		"tunnel_active_count": tunnelPoolSnapshot.ActiveCount,
		"event_total":         summary.EventTotal,
		"event_error_count":   summary.ErrorCount,
		"event_state_changes": summary.StateChangeCount,
		"event_reconnects":    summary.ReconnectCount,
		"event_refill_total":  summary.RefillEventCount,
		"last_event_at_ms":    summary.LastEventAtMS,
		"last_event_code":     summary.LastEventCode,
		"last_event_message":  summary.LastEventMessage,
		"quic":                quicSnapshotPayload(quicSnapshot),
		"updated_at_ms":       runtimeNowMillis(),
		"source":              "agent.runtime.diagnose",
	}
}
