package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/agent-core/internal/config"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/domain"
	"github.com/lifei6671/devbridge-loop/agent-core/internal/store"
)

const defaultEventQueueSize = 512

type outboundEvent struct {
	messageType domain.TunnelMessageType
	eventID     string
	payload     any
}

type reconnectRequest struct {
	result chan error
}

// SyncManager 负责将本地注册事件同步到 cloud-bridge。
type SyncManager struct {
	cfg       config.Config
	state     *store.MemoryStore
	client    *BridgeClient
	tunnelID  string
	queueSize int

	eventCh     chan outboundEvent
	reconnectCh chan reconnectRequest

	mu     sync.RWMutex
	connID string
}

// NewSyncManager 创建同步管理器。
func NewSyncManager(cfg config.Config, state *store.MemoryStore) *SyncManager {
	return &SyncManager{
		cfg:         cfg,
		state:       state,
		client:      NewBridgeClient(cfg.Tunnel.BridgeAddress, cfg.Tunnel.RequestTimeout),
		tunnelID:    buildTunnelID(cfg.RDName),
		queueSize:   defaultEventQueueSize,
		eventCh:     make(chan outboundEvent, defaultEventQueueSize),
		reconnectCh: make(chan reconnectRequest, 8),
	}
}

// Run 启动同步循环：定时心跳、增量事件、手动重连。
func (m *SyncManager) Run(ctx context.Context) {
	if err := m.reconnectWithBackoff(ctx); err != nil {
		m.state.AddError(domain.ErrorTunnelOffline, "initial tunnel reconnect failed", map[string]string{"error": err.Error()})
	}

	heartbeatTicker := time.NewTicker(m.cfg.Tunnel.HeartbeatInterval)
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			m.state.MarkTunnelDisconnected()
			return
		case <-heartbeatTicker.C:
			if err := m.sendHeartbeat(ctx); err != nil {
				m.state.AddError(domain.ErrorTunnelOffline, "send tunnel heartbeat failed", map[string]string{"error": err.Error()})
			}
		case req := <-m.reconnectCh:
			err := m.reconnectWithBackoff(ctx)
			req.result <- err
			close(req.result)
		case event := <-m.eventCh:
			if err := m.sendIncrementalEvent(ctx, event); err != nil {
				m.state.AddError(domain.ErrorTunnelOffline, "sync incremental event failed", map[string]string{
					"eventId": string(event.eventID),
					"type":    string(event.messageType),
					"error":   err.Error(),
				})
			}
		}
	}
}

// RequestReconnect 触发一次重连流程，并等待结果。
func (m *SyncManager) RequestReconnect(ctx context.Context) error {
	request := reconnectRequest{result: make(chan error, 1)}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.reconnectCh <- request:
	}

	select {
	case <-ctx.Done():
		return ctx.Err()
	case err := <-request.result:
		return err
	}
}

// EnqueueRegisterUpsert 推送 REGISTER_UPSERT 事件（按 endpoint 拆分）。
func (m *SyncManager) EnqueueRegisterUpsert(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error {
	if len(reg.Endpoints) == 0 {
		return nil
	}

	for i, endpoint := range reg.Endpoints {
		payload := domain.RegisterUpsertPayload{
			TunnelID:    m.tunnelID,
			Env:         reg.Env,
			ServiceName: reg.ServiceName,
			Protocol:    endpoint.Protocol,
			InstanceID:  reg.InstanceID,
			TargetPort:  endpoint.TargetPort,
			Status:      endpoint.Status,
		}
		event := outboundEvent{
			messageType: domain.MessageRegisterUpsert,
			eventID:     deriveSubEventID(sourceEventID, "upsert", i),
			payload:     payload,
		}
		if err := m.enqueueEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

// EnqueueRegisterDelete 推送 REGISTER_DELETE 事件（按 endpoint 拆分）。
func (m *SyncManager) EnqueueRegisterDelete(ctx context.Context, reg domain.LocalRegistration, sourceEventID string) error {
	if len(reg.Endpoints) == 0 {
		return nil
	}

	for i, endpoint := range reg.Endpoints {
		payload := domain.RegisterDeletePayload{
			TunnelID:    m.tunnelID,
			Env:         reg.Env,
			ServiceName: reg.ServiceName,
			Protocol:    endpoint.Protocol,
			InstanceID:  reg.InstanceID,
			TargetPort:  endpoint.TargetPort,
		}
		event := outboundEvent{
			messageType: domain.MessageRegisterDelete,
			eventID:     deriveSubEventID(sourceEventID, "delete", i),
			payload:     payload,
		}
		if err := m.enqueueEvent(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (m *SyncManager) enqueueEvent(ctx context.Context, event outboundEvent) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case m.eventCh <- event:
		return nil
	}
}

func (m *SyncManager) sendIncrementalEvent(ctx context.Context, event outboundEvent) error {
	if err := m.ensureConnected(ctx); err != nil {
		return err
	}

	if _, err := m.sendMessage(ctx, event.messageType, event.eventID, event.payload, 0); err != nil {
		// 增量发送失败时，先标记断线再重连，随后用同一 eventId 重试一次。
		m.state.MarkTunnelDisconnected()
		if reconnectErr := m.reconnectWithBackoff(ctx); reconnectErr != nil {
			return fmt.Errorf("reconnect after incremental send failure: %w", reconnectErr)
		}
		if _, retryErr := m.sendMessage(ctx, event.messageType, event.eventID, event.payload, 0); retryErr != nil {
			return fmt.Errorf("retry incremental send failed: %w", retryErr)
		}
	}
	return nil
}

func (m *SyncManager) sendHeartbeat(ctx context.Context) error {
	if err := m.ensureConnected(ctx); err != nil {
		return err
	}

	payload := domain.TunnelHeartbeatPayload{
		TunnelID: m.tunnelID,
		ConnID:   m.currentConnID(),
	}
	_, err := m.sendMessage(ctx, domain.MessageTunnelHeartbeat, generateEventID("evt-hb"), payload, 0)
	return err
}

func (m *SyncManager) ensureConnected(ctx context.Context) error {
	state := m.state.TunnelState(m.cfg.Tunnel.BridgeAddress)
	if state.Connected {
		return nil
	}
	return m.reconnectWithBackoff(ctx)
}

func (m *SyncManager) reconnectWithBackoff(ctx context.Context) error {
	// 每次重连尝试都推进 epoch，避免 bridge 保留更高 epoch 会话时陷入永久冲突。
	sessionState := m.state.BeginTunnelReconnect()
	epoch := sessionState.SessionEpoch

	attempt := 0
	for {
		if err := m.performHandshakeAndFullSync(ctx, epoch); err == nil {
			m.state.MarkTunnelConnected()
			m.state.MarkTunnelHeartbeat(time.Now().UTC())
			return nil
		} else {
			backoff := m.backoffForAttempt(attempt)
			attempt++
			diagnosticError := withBridgeConnectivityHint(err, m.cfg.Tunnel.BridgeAddress)

			// 记录下一次重试时间，供 UI 以秒级倒计时展示“自动重连中”。
			nextRetryAt := time.Now().UTC().Add(backoff)
			m.state.MarkTunnelReconnectPending(attempt, nextRetryAt, diagnosticError)
			// 将失败原因写入运行态错误，便于 UI 和日志快速定位“连不上 bridge”的具体原因。
			m.state.AddError(domain.ErrorTunnelOffline, "tunnel reconnect attempt failed", map[string]string{
				"attempt":       fmt.Sprintf("%d", attempt),
				"sessionEpoch":  fmt.Sprintf("%d", epoch),
				"bridgeAddress": m.cfg.Tunnel.BridgeAddress,
				"nextRetryAt":   nextRetryAt.Format(time.RFC3339Nano),
				"error":         diagnosticError,
			})

			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(backoff):
			}

			// 下一次尝试前切换到更新 epoch，防止被 bridge 以 stale-epoch 持续拒绝。
			sessionState = m.state.BeginTunnelReconnect()
			epoch = sessionState.SessionEpoch
		}
	}
}

func (m *SyncManager) performHandshakeAndFullSync(ctx context.Context, epoch int64) error {
	connID := generateEventID("conn")
	m.setConnID(connID)

	hello := domain.HelloPayload{
		TunnelID:        m.tunnelID,
		RDName:          m.cfg.RDName,
		ConnID:          connID,
		BackflowBaseURL: m.cfg.Tunnel.BackflowBaseURL,
	}
	if _, err := m.sendMessage(ctx, domain.MessageHello, generateEventID("evt-hello"), hello, epoch); err != nil {
		return fmt.Errorf("send HELLO: %w", err)
	}

	fullSyncRequest := domain.FullSyncRequestPayload{
		TunnelID: m.tunnelID,
		RDName:   m.cfg.RDName,
	}
	if _, err := m.sendMessage(ctx, domain.MessageFullSyncRequest, generateEventID("evt-fs-req"), fullSyncRequest, epoch); err != nil {
		return fmt.Errorf("send FULL_SYNC_REQUEST: %w", err)
	}

	snapshot := m.buildFullSyncSnapshotPayload()
	if _, err := m.sendMessage(ctx, domain.MessageFullSyncSnapshot, generateEventID("evt-fs-snapshot"), snapshot, epoch); err != nil {
		return fmt.Errorf("send FULL_SYNC_SNAPSHOT: %w", err)
	}

	return nil
}

func (m *SyncManager) buildFullSyncSnapshotPayload() domain.FullSyncSnapshotPayload {
	registrations := m.state.SnapshotRegistrations()
	snapshotRegs := make([]domain.SnapshotRegistration, 0, len(registrations))

	// 全量快照按当前内存态重建，确保重连后与本地注册表收敛一致。
	for _, reg := range registrations {
		endpoints := make([]domain.SnapshotEndpoint, 0, len(reg.Endpoints))
		for _, endpoint := range reg.Endpoints {
			endpoints = append(endpoints, domain.SnapshotEndpoint{
				Protocol:   endpoint.Protocol,
				TargetPort: endpoint.TargetPort,
				Status:     endpoint.Status,
			})
		}
		snapshotRegs = append(snapshotRegs, domain.SnapshotRegistration{
			Env:         reg.Env,
			ServiceName: reg.ServiceName,
			InstanceID:  reg.InstanceID,
			Endpoints:   endpoints,
		})
	}

	return domain.FullSyncSnapshotPayload{
		TunnelID:      m.tunnelID,
		RDName:        m.cfg.RDName,
		Registrations: snapshotRegs,
	}
}

func (m *SyncManager) sendMessage(
	ctx context.Context,
	messageType domain.TunnelMessageType,
	eventID string,
	payload any,
	sessionEpochOverride int64,
) (domain.TunnelReply, error) {
	sessionEpoch, resourceVersion := m.state.CurrentSyncMeta()
	if sessionEpochOverride > 0 {
		sessionEpoch = sessionEpochOverride
	}

	message := domain.TunnelMessage{
		Type: messageType,
		SyncEventMeta: domain.SyncEventMeta{
			SessionEpoch:    sessionEpoch,
			ResourceVersion: resourceVersion,
			EventID:         eventID,
			SentAt:          time.Now().UTC(),
		},
		Payload: payload,
	}

	reply, err := m.client.SendEvent(ctx, message)
	if err != nil {
		return domain.TunnelReply{}, err
	}

	if reply.Type == domain.MessageError {
		return domain.TunnelReply{}, &BridgeReplyError{
			Code:    firstNonEmpty(reply.ErrorCode, string(domain.MessageError)),
			Message: firstNonEmpty(reply.Message, "bridge returned ERROR"),
			Reply:   reply,
		}
	}

	m.state.MarkTunnelHeartbeat(time.Now().UTC())
	return reply, nil
}

func (m *SyncManager) backoffForAttempt(attempt int) time.Duration {
	// 采用指数退避（base * 2^attempt）并受 max 上限约束，避免 bridge 长时间不可达时过载重试。
	base, max := m.reconnectBackoffBounds()
	if attempt <= 0 {
		return base
	}

	backoff := base
	for i := 0; i < attempt; i++ {
		// 到达上限后保持固定，兼容“指数增长 + 最大退避”的设计约束。
		if backoff >= max/2 {
			return max
		}
		backoff *= 2
	}
	if backoff > max {
		return max
	}
	return backoff
}

func (m *SyncManager) reconnectBackoffBounds() (time.Duration, time.Duration) {
	if len(m.cfg.Tunnel.ReconnectBackoff) == 0 {
		return 500 * time.Millisecond, 5 * time.Second
	}

	base := m.cfg.Tunnel.ReconnectBackoff[0]
	max := m.cfg.Tunnel.ReconnectBackoff[len(m.cfg.Tunnel.ReconnectBackoff)-1]
	if base <= 0 {
		base = 500 * time.Millisecond
	}
	if max < base {
		max = base
	}
	return base, max
}

func (m *SyncManager) currentConnID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.connID
}

func (m *SyncManager) setConnID(connID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connID = connID
}

func deriveSubEventID(sourceEventID, action string, index int) string {
	base := strings.TrimSpace(sourceEventID)
	if base == "" {
		base = generateEventID("evt")
	}
	return fmt.Sprintf("%s-%s-%d", base, action, index)
}

func generateEventID(prefix string) string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
	}
	return fmt.Sprintf("%s-%s", prefix, hex.EncodeToString(buf))
}

func buildTunnelID(rdName string) string {
	hostname, err := os.Hostname()
	if err != nil || strings.TrimSpace(hostname) == "" {
		hostname = "unknown-host"
	}
	base := strings.TrimSpace(rdName)
	if base == "" {
		base = "unknown-rd"
	}
	return fmt.Sprintf("tunnel-%s-%s", sanitizeID(base), sanitizeID(hostname))
}

func sanitizeID(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	replacer := strings.NewReplacer(" ", "-", "/", "-", "\\", "-", ":", "-", "_", "-")
	value = replacer.Replace(value)
	if value == "" {
		return "unknown"
	}
	return value
}

func withBridgeConnectivityHint(err error, bridgeAddress string) string {
	if err == nil {
		return ""
	}

	message := strings.TrimSpace(err.Error())
	if message == "" {
		return ""
	}
	if runtime.GOOS != "windows" {
		return message
	}

	// Windows 下若 bridge 实际跑在 WSL，127.0.0.1 往往不可直连，需要改成 WSL IP。
	lowerMessage := strings.ToLower(message)
	lowerAddress := strings.ToLower(strings.TrimSpace(bridgeAddress))
	if strings.Contains(lowerMessage, "connection refused") &&
		(strings.Contains(lowerAddress, "127.0.0.1") || strings.Contains(lowerAddress, "localhost")) {
		return message + " | hint: 若 cloud-bridge 运行在 WSL，请将 tunnelBridgeAddress 改为 WSL 的实际 IP:38080，或直接在 Windows 启动 cloud-bridge.exe。"
	}
	return message
}
