package tunnel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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
	// 每一轮重连流程只提升一次 epoch，符合“新建连接后 epoch+1”的约束。
	sessionState := m.state.BeginTunnelReconnect()
	epoch := sessionState.SessionEpoch

	attempt := 0
	for {
		if err := m.performHandshakeAndFullSync(ctx, epoch); err == nil {
			m.state.MarkTunnelConnected()
			m.state.MarkTunnelHeartbeat(time.Now().UTC())
			return nil
		}

		m.state.MarkTunnelDisconnected()

		backoff := m.backoffForAttempt(attempt)
		attempt++

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
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
	if len(m.cfg.Tunnel.ReconnectBackoff) == 0 {
		return 1 * time.Second
	}
	if attempt >= len(m.cfg.Tunnel.ReconnectBackoff) {
		return m.cfg.Tunnel.ReconnectBackoff[len(m.cfg.Tunnel.ReconnectBackoff)-1]
	}
	return m.cfg.Tunnel.ReconnectBackoff[attempt]
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
