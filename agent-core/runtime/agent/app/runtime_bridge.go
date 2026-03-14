package app

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/runtime/agent/tunnel"
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
		return opener.runtime.tcpTransport.DialTunnel(
			ctx,
			opener.runtime.cfg.BridgeAddr,
			tunnelMeta,
		)
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
		return grpcTunnel, nil
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
	})
	if err != nil {
		return fmt.Errorf("initialize tunnel manager: %w", err)
	}
	r.tunnelRegistry = registry
	r.tunnelManager = manager
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
	r.bridgeMu.Lock()
	defer r.bridgeMu.Unlock()
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
	defer r.bridgeMu.Unlock()
	r.bridgeDesiredUp = false
	r.bridgeState = "CLOSED"
	r.bridgeSession = ""
	r.updatedAt = time.Now().UTC()
	r.retryFailStreak = 0
	r.retryBackoff = 0
	r.nextRetryAt = time.Time{}
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
	return map[string]any{
		"services":      []map[string]any{},
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
		items = append(items, map[string]any{
			"tunnel_id":     record.TunnelID,
			"service_id":    "--",
			"state":         string(record.State),
			"local_addr":    "--",
			"remote_addr":   r.cfg.BridgeAddr,
			"latency_ms":    uint64(0),
			"last_error":    record.LastError,
			"updated_at_ms": uint64(record.UpdatedAt.UnixMilli()),
		})
	}
	return map[string]any{
		"tunnels":       items,
		"updated_at_ms": runtimeNowMillis(),
		"source":        "agent.runtime",
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
