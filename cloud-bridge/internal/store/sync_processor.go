package store

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/internal/domain"
)

// ProcessTunnelEvent 处理来自 agent 的 tunnel 同步事件，并返回 ACK 语义。
func (s *MemoryStore) ProcessTunnelEvent(event domain.TunnelEvent) (domain.TunnelEventReply, error) {
	now := time.Now().UTC()
	normalized, tunnelID, err := normalizeIncomingEvent(event, now)
	if err != nil {
		return domain.TunnelEventReply{}, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	session, hasSession := s.sessions[tunnelID]

	// 旧 epoch 事件必须拒绝，防止重连后旧连接污染最新状态。
	if hasSession && normalized.SessionEpoch < session.SessionEpoch {
		return domain.TunnelEventReply{}, &EventRejectError{
			Code:            domain.SyncErrorStaleEpochEvent,
			Message:         fmt.Sprintf("stale epoch event: incoming=%d current=%d", normalized.SessionEpoch, session.SessionEpoch),
			StatusCode:      http.StatusConflict,
			SessionEpoch:    session.SessionEpoch,
			ResourceVersion: session.ResourceVersion,
		}
	}

	// epoch 升级必须先发送 HELLO，否则拒绝增量事件。
	if hasSession && normalized.SessionEpoch > session.SessionEpoch && normalized.Type != domain.TunnelMessageHELLO {
		return domain.TunnelEventReply{}, &EventRejectError{
			Code:            domain.SyncErrorEpochTransition,
			Message:         "new epoch must start with HELLO",
			StatusCode:      http.StatusConflict,
			SessionEpoch:    session.SessionEpoch,
			ResourceVersion: session.ResourceVersion,
		}
	}

	if !hasSession && normalized.Type != domain.TunnelMessageHELLO {
		return domain.TunnelEventReply{}, &EventRejectError{
			Code:            domain.SyncErrorUnknownSession,
			Message:         "tunnel session does not exist, HELLO required first",
			StatusCode:      http.StatusConflict,
			SessionEpoch:    normalized.SessionEpoch,
			ResourceVersion: 0,
		}
	}

	dedupKey := s.eventDedupKey(tunnelID, normalized.SessionEpoch, normalized.EventID)
	if record, ok := s.findProcessedLocked(dedupKey); ok {
		rv := int64(0)
		if current, exists := s.sessions[tunnelID]; exists {
			rv = current.ResourceVersion
		}
		return domain.TunnelEventReply{
			Type:            domain.TunnelMessageACK,
			Status:          domain.EventStatusDuplicate,
			EventID:         normalized.EventID,
			SessionEpoch:    normalized.SessionEpoch,
			ResourceVersion: rv,
			Deduplicated:    true,
			Message:         fmt.Sprintf("duplicate event ignored (%s)", record.MessageType),
		}, nil
	}

	resourceVersion, err := s.applyEventLocked(tunnelID, normalized, now)
	if err != nil {
		return domain.TunnelEventReply{}, err
	}

	s.appendEventLocked(normalized)
	s.rememberProcessedLocked(processedEventRecord{
		Key:          dedupKey,
		TunnelID:     tunnelID,
		SessionEpoch: normalized.SessionEpoch,
		EventID:      normalized.EventID,
		MessageType:  normalized.Type,
		ProcessedAt:  now,
	})

	return domain.TunnelEventReply{
		Type:            domain.TunnelMessageACK,
		Status:          domain.EventStatusAccepted,
		EventID:         normalized.EventID,
		SessionEpoch:    normalized.SessionEpoch,
		ResourceVersion: resourceVersion,
		Deduplicated:    false,
		Message:         "event applied",
	}, nil
}

func normalizeIncomingEvent(event domain.TunnelEvent, now time.Time) (domain.TunnelEvent, string, error) {
	event.Type = strings.ToUpper(strings.TrimSpace(event.Type))
	event.EventID = strings.TrimSpace(event.EventID)
	if event.SentAt.IsZero() {
		event.SentAt = now
	}
	if event.Payload == nil {
		event.Payload = map[string]any{}
	}

	if event.Type == "" {
		return domain.TunnelEvent{}, "", &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    "event type is required",
			StatusCode: http.StatusBadRequest,
		}
	}
	if event.EventID == "" {
		return domain.TunnelEvent{}, "", &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    "eventId is required",
			StatusCode: http.StatusBadRequest,
		}
	}
	if event.SessionEpoch <= 0 {
		return domain.TunnelEvent{}, "", &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    "sessionEpoch must be positive",
			StatusCode: http.StatusBadRequest,
		}
	}

	tunnelID, err := extractTunnelID(event)
	if err != nil {
		return domain.TunnelEvent{}, "", err
	}
	return event, tunnelID, nil
}

func extractTunnelID(event domain.TunnelEvent) (string, error) {
	// HELLO 消息用强类型 payload 解析，避免字段拼写差异导致会话漂移。
	if event.Type == domain.TunnelMessageHELLO {
		var payload domain.HelloPayload
		if err := decodePayload(event.Payload, &payload); err != nil {
			return "", &EventRejectError{
				Code:       domain.SyncErrorInvalidPayload,
				Message:    fmt.Sprintf("decode HELLO payload failed: %v", err),
				StatusCode: http.StatusBadRequest,
			}
		}
		if strings.TrimSpace(payload.TunnelID) == "" {
			return "", &EventRejectError{
				Code:       domain.SyncErrorMissingTunnelID,
				Message:    "HELLO payload.tunnelId is required",
				StatusCode: http.StatusBadRequest,
			}
		}
		return strings.TrimSpace(payload.TunnelID), nil
	}

	// 其他消息统一从 payload.tunnelId 读取，便于事件去重和状态隔离。
	value, ok := event.Payload["tunnelId"]
	if !ok {
		return "", &EventRejectError{
			Code:       domain.SyncErrorMissingTunnelID,
			Message:    "payload.tunnelId is required",
			StatusCode: http.StatusBadRequest,
		}
	}
	tunnelID := strings.TrimSpace(fmt.Sprintf("%v", value))
	if tunnelID == "" {
		return "", &EventRejectError{
			Code:       domain.SyncErrorMissingTunnelID,
			Message:    "payload.tunnelId is empty",
			StatusCode: http.StatusBadRequest,
		}
	}
	return tunnelID, nil
}

func (s *MemoryStore) applyEventLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	switch event.Type {
	case domain.TunnelMessageHELLO:
		return s.applyHELLOLocked(tunnelID, event, now)
	case domain.TunnelMessageFullSyncRequest:
		return s.applyFullSyncRequestLocked(tunnelID, event, now)
	case domain.TunnelMessageFullSyncSnapshot:
		return s.applyFullSyncSnapshotLocked(tunnelID, event, now)
	case domain.TunnelMessageRegisterUpsert:
		return s.applyRegisterUpsertLocked(tunnelID, event, now)
	case domain.TunnelMessageRegisterDelete:
		return s.applyRegisterDeleteLocked(tunnelID, event, now)
	case domain.TunnelMessageTunnelHeartbeat:
		return s.applyTunnelHeartbeatLocked(tunnelID, event, now)
	default:
		return 0, &EventRejectError{
			Code:       domain.SyncErrorUnsupportedType,
			Message:    fmt.Sprintf("unsupported message type: %s", event.Type),
			StatusCode: http.StatusBadRequest,
		}
	}
}

func (s *MemoryStore) applyHELLOLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	var payload domain.HelloPayload
	if err := decodePayload(event.Payload, &payload); err != nil {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    fmt.Sprintf("decode HELLO payload failed: %v", err),
			StatusCode: http.StatusBadRequest,
		}
	}

	existing, exists := s.sessions[tunnelID]

	// 新 epoch 握手成功后必须清理旧拦截，避免旧路由继续暴露。
	if exists && event.SessionEpoch > existing.SessionEpoch {
		s.clearTunnelStateLocked(tunnelID)
	}

	connectedAt := existing.ConnectedAt
	if connectedAt.IsZero() || event.SessionEpoch > existing.SessionEpoch {
		connectedAt = now
	}

	s.sessions[tunnelID] = domain.TunnelSession{
		TunnelID:        tunnelID,
		RDName:          firstNonEmpty(strings.TrimSpace(payload.RDName), existing.RDName),
		ConnID:          firstNonEmpty(strings.TrimSpace(payload.ConnID), event.EventID),
		SessionEpoch:    event.SessionEpoch,
		ResourceVersion: maxInt64(existing.ResourceVersion, event.ResourceVersion),
		Status:          "connected",
		LastHeartbeatAt: now,
		ConnectedAt:     connectedAt,
	}
	return s.sessions[tunnelID].ResourceVersion, nil
}

func (s *MemoryStore) applyFullSyncRequestLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	session := s.sessions[tunnelID]
	session.LastHeartbeatAt = now
	session.Status = "connected"
	session.ResourceVersion = maxInt64(session.ResourceVersion, event.ResourceVersion)
	s.sessions[tunnelID] = session
	return session.ResourceVersion, nil
}

func (s *MemoryStore) applyFullSyncSnapshotLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	var payload domain.FullSyncSnapshotPayload
	if err := decodePayload(event.Payload, &payload); err != nil {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    fmt.Sprintf("decode FULL_SYNC_SNAPSHOT payload failed: %v", err),
			StatusCode: http.StatusBadRequest,
		}
	}

	// 全量快照先清空该 tunnel 的旧状态，再按当前内存态重建。
	s.clearTunnelStateLocked(tunnelID)
	for _, reg := range payload.Registrations {
		for _, endpoint := range reg.Endpoints {
			upsert := domain.RegisterUpsertPayload{
				TunnelID:    tunnelID,
				Env:         reg.Env,
				ServiceName: reg.ServiceName,
				Protocol:    endpoint.Protocol,
				InstanceID:  reg.InstanceID,
				TargetPort:  endpoint.TargetPort,
				Status:      endpoint.Status,
				BridgeHost:  payload.BridgeHost,
				BridgePort:  payload.BridgePort,
			}
			s.upsertInterceptAndRouteLocked(tunnelID, upsert, now)
		}
	}

	session := s.sessions[tunnelID]
	session.RDName = firstNonEmpty(strings.TrimSpace(payload.RDName), session.RDName)
	session.ResourceVersion = maxInt64(session.ResourceVersion, event.ResourceVersion)
	session.LastHeartbeatAt = now
	session.Status = "connected"
	s.sessions[tunnelID] = session
	return session.ResourceVersion, nil
}

func (s *MemoryStore) applyRegisterUpsertLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	var payload domain.RegisterUpsertPayload
	if err := decodePayload(event.Payload, &payload); err != nil {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    fmt.Sprintf("decode REGISTER_UPSERT payload failed: %v", err),
			StatusCode: http.StatusBadRequest,
		}
	}
	payload.TunnelID = firstNonEmpty(strings.TrimSpace(payload.TunnelID), tunnelID)

	if strings.TrimSpace(payload.Env) == "" || strings.TrimSpace(payload.ServiceName) == "" || strings.TrimSpace(payload.Protocol) == "" || payload.TargetPort <= 0 {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    "REGISTER_UPSERT requires env/serviceName/protocol/targetPort",
			StatusCode: http.StatusBadRequest,
		}
	}

	s.upsertInterceptAndRouteLocked(tunnelID, payload, now)

	session := s.sessions[tunnelID]
	session.ResourceVersion = maxInt64(session.ResourceVersion, event.ResourceVersion)
	session.LastHeartbeatAt = now
	session.Status = "connected"
	s.sessions[tunnelID] = session
	return session.ResourceVersion, nil
}

func (s *MemoryStore) applyRegisterDeleteLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	var payload domain.RegisterDeletePayload
	if err := decodePayload(event.Payload, &payload); err != nil {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    fmt.Sprintf("decode REGISTER_DELETE payload failed: %v", err),
			StatusCode: http.StatusBadRequest,
		}
	}

	if strings.TrimSpace(payload.Env) == "" || strings.TrimSpace(payload.ServiceName) == "" || strings.TrimSpace(payload.Protocol) == "" {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    "REGISTER_DELETE requires env/serviceName/protocol",
			StatusCode: http.StatusBadRequest,
		}
	}

	// 删除语义支持按 instanceId / targetPort 精确命中，也支持按三元组批量删除。
	for key, intercept := range s.intercepts {
		if intercept.TunnelID != tunnelID {
			continue
		}
		if !strings.EqualFold(intercept.Env, payload.Env) || !strings.EqualFold(intercept.ServiceName, payload.ServiceName) || !strings.EqualFold(intercept.Protocol, payload.Protocol) {
			continue
		}
		if strings.TrimSpace(payload.InstanceID) != "" && intercept.InstanceID != strings.TrimSpace(payload.InstanceID) {
			continue
		}
		if payload.TargetPort > 0 && intercept.TargetPort != payload.TargetPort {
			continue
		}
		delete(s.intercepts, key)
	}

	for key, route := range s.routes {
		if route.TunnelID != tunnelID {
			continue
		}
		if !strings.EqualFold(route.Env, payload.Env) || !strings.EqualFold(route.ServiceName, payload.ServiceName) || !strings.EqualFold(route.Protocol, payload.Protocol) {
			continue
		}
		if payload.TargetPort > 0 && route.TargetPort != payload.TargetPort {
			continue
		}
		delete(s.routes, key)
	}

	session := s.sessions[tunnelID]
	session.ResourceVersion = maxInt64(session.ResourceVersion, event.ResourceVersion)
	session.LastHeartbeatAt = now
	session.Status = "connected"
	s.sessions[tunnelID] = session
	return session.ResourceVersion, nil
}

func (s *MemoryStore) applyTunnelHeartbeatLocked(tunnelID string, event domain.TunnelEvent, now time.Time) (int64, error) {
	var payload domain.TunnelHeartbeatPayload
	if err := decodePayload(event.Payload, &payload); err != nil {
		return 0, &EventRejectError{
			Code:       domain.SyncErrorInvalidPayload,
			Message:    fmt.Sprintf("decode TUNNEL_HEARTBEAT payload failed: %v", err),
			StatusCode: http.StatusBadRequest,
		}
	}

	session := s.sessions[tunnelID]
	if connID := strings.TrimSpace(payload.ConnID); connID != "" {
		session.ConnID = connID
	}
	session.LastHeartbeatAt = now
	session.Status = "connected"
	session.ResourceVersion = maxInt64(session.ResourceVersion, event.ResourceVersion)
	s.sessions[tunnelID] = session
	return session.ResourceVersion, nil
}

func (s *MemoryStore) upsertInterceptAndRouteLocked(tunnelID string, payload domain.RegisterUpsertPayload, now time.Time) {
	env := strings.TrimSpace(payload.Env)
	serviceName := strings.TrimSpace(payload.ServiceName)
	protocol := sanitizeProtocol(payload.Protocol)
	instanceID := strings.TrimSpace(payload.InstanceID)
	targetPort := payload.TargetPort
	status := sanitizeStatus(payload.Status)

	interceptKey := activeInterceptKey(tunnelID, env, serviceName, protocol, instanceID, targetPort)
	s.intercepts[interceptKey] = domain.ActiveIntercept{
		Env:         env,
		ServiceName: serviceName,
		Protocol:    protocol,
		TunnelID:    tunnelID,
		InstanceID:  instanceID,
		TargetPort:  targetPort,
		Status:      status,
		UpdatedAt:   now,
	}

	bridgeHost := firstNonEmpty(strings.TrimSpace(payload.BridgeHost), s.bridgeHost)
	bridgePort := payload.BridgePort
	if bridgePort <= 0 {
		bridgePort = s.bridgePort
	}

	routeKey := bridgeRouteKey(tunnelID, env, serviceName, protocol, instanceID, targetPort)
	s.routes[routeKey] = domain.BridgeRoute{
		Env:         env,
		ServiceName: serviceName,
		Protocol:    protocol,
		BridgeHost:  bridgeHost,
		BridgePort:  bridgePort,
		TunnelID:    tunnelID,
		TargetPort:  targetPort,
	}
}

func decodePayload(payload map[string]any, target any) error {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes, target)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}
