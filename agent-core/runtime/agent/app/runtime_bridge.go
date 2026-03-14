package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/control"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/obs"
	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
	"github.com/lifei6671/devbridge-loop/ltfp/adapter"
	"github.com/lifei6671/devbridge-loop/ltfp/pb"
	transportgen "github.com/lifei6671/devbridge-loop/ltfp/pb/gen/devbridge/loop/v2/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/grpcbinding"
	"github.com/lifei6671/devbridge-loop/ltfp/transport/tcpbinding"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

const (
	bridgeHeartbeatPingInterval  = 5 * time.Second
	bridgeHeartbeatMissThreshold = 5
	bridgeHeartbeatWriteTimeout  = 2 * time.Second
	bridgeBusinessWriteTimeout   = 3 * time.Second

	bridgeRetryInitialBackoff = time.Second
	bridgeRetryMaxBackoff     = 8 * time.Second
	bridgeRetryJitterRatio    = 0.2
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
	tunnelMeta := transport.TunnelMeta{
		TunnelID:     opener.runtime.nextTunnelID(),
		SessionID:    sessionID,
		SessionEpoch: sessionEpoch,
		CreatedAt:    time.Now().UTC(),
	}
	switch strings.TrimSpace(opener.runtime.cfg.BridgeTransport) {
	case transport.BindingTypeTCPFramed.String():
		if opener.runtime.tcpTransport == nil {
			return nil, errors.New("tcp transport is not initialized")
		}
		rawTunnel, err := opener.runtime.tcpTransport.DialTunnel(
			ctx,
			opener.runtime.cfg.BridgeAddr,
			tunnelMeta,
		)
		if err != nil {
			return nil, err
		}
		// 所有新建 tunnel 统一包一层 payload 适配器，供 traffic runtime 直接消费。
		return newRuntimeTrafficTunnelAdapter(rawTunnel), nil
	case transport.BindingTypeGRPCH2.String():
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
		tunnelStream, err := grpcTransport.OpenTunnelStream(ctx, grpcClient)
		if err != nil {
			return nil, fmt.Errorf("open grpc tunnel stream failed: %w", err)
		}
		grpcTunnel, err := grpcbinding.NewGRPCH2Tunnel(tunnelStream, tunnelMeta)
		if err != nil {
			_ = tunnelStream.Close(context.Background())
			return nil, fmt.Errorf("create grpc tunnel failed: %w", err)
		}
		// grpc tunnel 同样走统一 payload 适配层，避免 runtime 侧分 binding 分支。
		return newRuntimeTrafficTunnelAdapter(grpcTunnel), nil
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

func runtimeNowMillis() uint64 {
	return uint64(time.Now().UTC().UnixMilli())
}

func timeToMillisPtr(at time.Time) *uint64 {
	if at.IsZero() {
		return nil
	}
	value := uint64(at.UTC().UnixMilli())
	return &value
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
	return fmt.Sprintf("tun-%d", r.tunnelIDSequence)
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
		return nil
	case transport.BindingTypeGRPCH2.String():
		grpcTransport, err := grpcbinding.NewTransportWithConfig(grpcbinding.TransportConfig{})
		if err != nil {
			return fmt.Errorf("initialize grpc transport: %w", err)
		}
		r.grpcTransport = grpcTransport
		r.tcpTransport = nil
		return nil
	case transport.BindingTypeQUICNative.String(),
		transport.BindingTypeH3Stream.String():
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
	if r.bridgeState != "ACTIVE" || r.bridgeSession == "" || r.bridgeEpoch == 0 {
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

func (r *Runtime) closeCurrentControlChannel() {
	controlChannel := r.clearControlChannel()
	if controlChannel != nil {
		_ = controlChannel.Close(context.Background())
	}
	clientConn := r.clearGRPCClientConn()
	if clientConn != nil {
		_ = clientConn.Close()
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
	r.bridgeMu.Lock()
	r.bridgeDesiredUp = true
	r.bridgeState = "RECONNECTING"
	r.updatedAt = time.Now().UTC()
	if resetBackoff {
		r.retryFailStreak = 0
		r.retryBackoff = 0
		r.nextRetryAt = time.Time{}
	}
	r.bridgeMu.Unlock()
	r.enqueueBridgeCommand(bridgeCommand{kind: bridgeCommandReconnect, resetBackoff: resetBackoff})
}

func (r *Runtime) requestBridgeDrain() {
	if r == nil {
		return
	}
	r.bridgeMu.Lock()
	r.bridgeDesiredUp = false
	r.bridgeState = "DRAINING"
	r.updatedAt = time.Now().UTC()
	r.bridgeMu.Unlock()
	r.enqueueBridgeCommand(bridgeCommand{kind: bridgeCommandDrain})
}

func (r *Runtime) setBridgeConnecting() {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	r.bridgeState = "CONNECTING"
	r.updatedAt = time.Now().UTC()
}

func (r *Runtime) setBridgeConnected(sessionID string) {
	var currentSessionID string
	var currentSessionEpoch uint64
	r.bridgeMu.Lock()
	if r.bridgeSession != "" {
		r.reconnects++
	}
	r.bridgeSession = sessionID
	r.bridgeEpoch++
	if r.bridgeEpoch == 0 {
		r.bridgeEpoch = 1
	}
	now := time.Now().UTC()
	r.bridgeDesiredUp = true
	r.bridgeState = "ACTIVE"
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

func (r *Runtime) setBridgeRetrying(connectErr error, failStreak uint32, backoff time.Duration) {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	now := time.Now().UTC()
	r.bridgeState = "RECONNECTING"
	r.updatedAt = now
	r.retryFailStreak = failStreak
	r.retryBackoff = backoff
	if backoff > 0 {
		r.nextRetryAt = now.Add(backoff)
	} else {
		r.nextRetryAt = time.Time{}
	}
	if connectErr != nil {
		r.lastErr = connectErr.Error()
	}
}

func (r *Runtime) setBridgeLost(readErr error) {
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
	r.bridgeState = "STALE"
	r.updatedAt = time.Now().UTC()
	if readErr != nil {
		r.lastErr = readErr.Error()
	}
}

func (r *Runtime) setBridgeDrained() {
	r.bridgeMu.Lock()
	r.bridgeDesiredUp = false
	r.bridgeState = "CLOSED"
	r.bridgeSession = ""
	r.updatedAt = time.Now().UTC()
	r.retryFailStreak = 0
	r.retryBackoff = 0
	r.nextRetryAt = time.Time{}
	r.bridgeMu.Unlock()
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
		snapshot.state = "UNAVAILABLE"
	}
	if snapshot.state == "UNAVAILABLE" || snapshot.state == "STALE" || snapshot.state == "RECONNECTING" {
		snapshot.unavailableReason = r.lastErr
	}
	if snapshot.updatedAtMS == 0 {
		snapshot.updatedAtMS = runtimeNowMillis()
	}
	return snapshot
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
	controlChannel, err := r.tcpTransport.DialControlChannel(dialContext, r.cfg.BridgeAddr)
	if err != nil {
		return fmt.Errorf("dial bridge control channel failed: %w", err)
	}

	r.bridgeMu.Lock()
	r.controlChannel = controlChannel
	r.bridgeMu.Unlock()
	r.setBridgeConnected(newRuntimeSessionID())
	r.notifyTunnelManagerState(tunnel.SessionStateActive)
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
	dialOptions = append(
		dialOptions,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
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
	r.notifyTunnelManagerState(tunnel.SessionStateActive)
	return nil
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
	if len(envelope.Payload) == 0 {
		return errors.New("tunnel refill payload is empty")
	}
	var refillPayload pb.TunnelRefillRequest
	if err := json.Unmarshal(envelope.Payload, &refillPayload); err != nil {
		return fmt.Errorf("unmarshal tunnel refill payload failed: %w", err)
	}

	// 兼容 envelope 与 payload 字段来源：payload 缺失时回落 envelope 元信息。
	sessionID := strings.TrimSpace(refillPayload.SessionID)
	if sessionID == "" {
		sessionID = strings.TrimSpace(envelope.SessionID)
	}
	sessionEpoch := refillPayload.SessionEpoch
	if sessionEpoch == 0 {
		sessionEpoch = envelope.SessionEpoch
	}
	requestID := strings.TrimSpace(refillPayload.RequestID)
	if requestID == "" {
		requestID = strings.TrimSpace(envelope.RequestID)
	}
	requestTimestamp := time.Now().UTC()
	if refillPayload.TimestampUnix > 0 {
		requestTimestamp = time.Unix(refillPayload.TimestampUnix, 0).UTC()
	}
	refillRequest := control.TunnelRefillRequest{
		SessionID:          sessionID,
		SessionEpoch:       sessionEpoch,
		RequestID:          requestID,
		RequestedIdleDelta: refillPayload.RequestedIdleDelta,
		Reason:             control.TunnelRefillReason(strings.TrimSpace(refillPayload.Reason)),
		Timestamp:          requestTimestamp,
	}
	if _, err := r.refillHandler.Handle(ctx, refillRequest); err != nil {
		return fmt.Errorf("handle tunnel refill request failed: %w", err)
	}
	return nil
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
	r.bridgeMu.Lock()
	// 将控制面错误写入 runtime 真相源，供 session.snapshot 与诊断视图消费。
	r.lastErr = errorText
	r.updatedAt = time.Now().UTC()
	r.bridgeMu.Unlock()
	return nil
}

// handlePublishServiceAckEnvelope 消费 PublishServiceAck，并回写 catalog 的稳定 service_id。
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
		// 发布被拒绝时保留本地配置，不做 service_id 回写。
		return nil
	}
	serviceID := strings.TrimSpace(publishAck.ServiceID)
	serviceKey := strings.TrimSpace(publishAck.ServiceKey)
	if serviceID == "" || serviceKey == "" {
		return nil
	}
	r.serviceCatalog.SetServiceIDByKey(time.Now().UTC(), serviceKey, serviceID)
	return nil
}

// syncServiceControlState 在会话激活后同步一次服务发布与健康状态。
func (r *Runtime) syncServiceControlState(ctx context.Context) error {
	if r == nil {
		return nil
	}
	if err := r.publishCatalogServices(ctx); err != nil {
		return err
	}
	if err := r.reportCatalogHealth(ctx); err != nil {
		return err
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
		resourceID := strings.TrimSpace(publishPayload.ServiceID)
		if resourceID == "" {
			resourceID = strings.TrimSpace(publishPayload.ServiceKey)
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
	healthReports := r.healthReporter.BuildReports(ctx, localServices)
	now := time.Now().UTC()
	for _, healthReport := range healthReports {
		resourceID := strings.TrimSpace(healthReport.ServiceID)
		if resourceID == "" {
			resourceID = strings.TrimSpace(healthReport.ServiceKey)
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
			now,
			healthReport.ServiceID,
			healthReport.ServiceKey,
			healthReport.ServiceHealthStatus,
			healthReport.EndpointStatuses,
		)
	}
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

// reportTunnelPoolNow 触发一次立即上报，用于会话激活后的首轮对账。
func (r *Runtime) reportTunnelPoolNow(ctx context.Context, trigger string) {
	if r == nil || r.tunnelReporter == nil {
		return
	}
	// Reporter 失败不应中断主流程，留给周期/事件上报继续纠偏。
	_ = r.tunnelReporter.ReportNow(ctx, trigger)
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
				r.bridgeState = "RECONNECTING"
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
			failStreak++
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		}
		failStreak = 0
		if err := r.syncServiceControlState(ctx); err != nil {
			// 资源同步失败视为会话不可用，按失活链路进入重连退避。
			r.closeCurrentControlChannel()
			r.notifyTunnelManagerState(tunnel.SessionStateStale)
			r.setBridgeLost(err)
			failStreak++
			backoff := computeBridgeRetryBackoff(failStreak)
			r.setBridgeRetrying(err, failStreak, backoff)
			if waitErr := r.waitRetryWindow(ctx, backoff, &failStreak); waitErr != nil {
				return waitErr
			}
			continue
		}
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
			failStreak++
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
	return map[string]any{
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
		"source":                    "agent.runtime",
	}
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
		items = append(items, map[string]any{
			"service_id":       record.Registration.ServiceID,
			"service_key":      record.Registration.ServiceKey,
			"namespace":        record.Registration.Namespace,
			"environment":      record.Registration.Environment,
			"service_name":     record.Registration.ServiceName,
			"service_type":     record.Registration.ServiceType,
			"status":           string(pb.ServiceStatusActive),
			"health_status":    string(record.HealthStatus),
			"endpoint_count":   len(record.Registration.Endpoints),
			"resource_version": uint64(0),
			"updated_at_ms":    updatedAtMS,
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
			"tunnel_id":                record.TunnelID,
			"traffic_id":               association.TrafficID,
			"service_id":               association.ServiceID,
			"state":                    string(record.State),
			"local_addr":               association.LocalAddr,
			"remote_addr":              remoteAddr,
			"latency_ms":               association.OpenAckLatencyMS,
			"upstream_dial_latency_ms": association.UpstreamDialLatencyMS,
			"last_error":               record.LastError,
			"updated_at_ms":            updatedAtMS,
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
	return map[string]any{
		"state":               sessionSnapshot.state,
		"last_error":          sessionSnapshot.lastError,
		"retry_fail_streak":   sessionSnapshot.retryFailStreak,
		"retry_backoff_ms":    sessionSnapshot.retryBackoffMS,
		"next_retry_at_ms":    sessionSnapshot.nextRetryAtMS,
		"tunnel_idle_count":   tunnelPoolSnapshot.IdleCount,
		"tunnel_active_count": tunnelPoolSnapshot.ActiveCount,
		"updated_at_ms":       runtimeNowMillis(),
	}
}
