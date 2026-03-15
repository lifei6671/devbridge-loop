package control

import (
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// TunnelPoolReportRuntime 表示 Bridge 保存的一条 Agent tunnel 池上报快照。
type TunnelPoolReportRuntime struct {
	ConnectorID     string
	SessionID       string
	SessionEpoch    uint64
	IdleCount       int
	InUseCount      int
	TargetIdleCount int
	Trigger         string
	ReportedAt      time.Time
	UpdatedAt       time.Time
}

// TunnelPoolReportStore 维护“每个 connector 最新一条”的 tunnel 池上报视图。
type TunnelPoolReportStore struct {
	mutex        sync.RWMutex
	byConnector  map[string]TunnelPoolReportRuntime
	bySessionKey map[string]string
}

// NewTunnelPoolReportStore 创建 tunnel 池上报内存存储。
func NewTunnelPoolReportStore() *TunnelPoolReportStore {
	return &TunnelPoolReportStore{
		byConnector:  make(map[string]TunnelPoolReportRuntime),
		bySessionKey: make(map[string]string),
	}
}

// Upsert 写入或更新一条 tunnel 池上报快照。
func (store *TunnelPoolReportStore) Upsert(
	now time.Time,
	connectorID string,
	sessionID string,
	sessionEpoch uint64,
	report pb.TunnelPoolReport,
) {
	if store == nil {
		return
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedConnectorID == "" || normalizedSessionID == "" || sessionEpoch == 0 {
		return
	}
	updatedAt := now.UTC()
	if updatedAt.IsZero() {
		updatedAt = time.Now().UTC()
	}
	reportedAt := updatedAt
	if report.TimestampUnix > 0 {
		reportedAt = time.Unix(report.TimestampUnix, 0).UTC()
	}
	nextRuntime := TunnelPoolReportRuntime{
		ConnectorID:     normalizedConnectorID,
		SessionID:       normalizedSessionID,
		SessionEpoch:    sessionEpoch,
		IdleCount:       maxInt(report.IdleCount, 0),
		InUseCount:      maxInt(report.InUseCount, 0),
		TargetIdleCount: maxInt(report.TargetIdleCount, 0),
		Trigger:         strings.TrimSpace(report.Trigger),
		ReportedAt:      reportedAt,
		UpdatedAt:       updatedAt,
	}
	store.mutex.Lock()
	defer store.mutex.Unlock()
	sessionKey := buildTunnelPoolReportSessionKey(normalizedSessionID, sessionEpoch)

	// 若同 connector 已绑定更高 epoch，会拒绝旧代际上报覆盖。
	if existingRuntime, exists := store.byConnector[normalizedConnectorID]; exists {
		if existingRuntime.SessionEpoch > sessionEpoch {
			return
		}
		existingSessionKey := buildTunnelPoolReportSessionKey(existingRuntime.SessionID, existingRuntime.SessionEpoch)
		if existingSessionKey != sessionKey {
			delete(store.bySessionKey, existingSessionKey)
		}
	}
	// 若旧 connector 仍引用同 session_key，需要先解除反向索引。
	if existingConnectorID, exists := store.bySessionKey[sessionKey]; exists && existingConnectorID != normalizedConnectorID {
		delete(store.byConnector, existingConnectorID)
	}
	store.byConnector[normalizedConnectorID] = nextRuntime
	store.bySessionKey[sessionKey] = normalizedConnectorID
}

// RemoveBySession 删除指定 session 的 tunnel 池上报快照（会话切代/回收时调用）。
func (store *TunnelPoolReportStore) RemoveBySession(sessionID string, sessionEpoch uint64) {
	if store == nil {
		return
	}
	normalizedSessionID := strings.TrimSpace(sessionID)
	if normalizedSessionID == "" || sessionEpoch == 0 {
		return
	}
	sessionKey := buildTunnelPoolReportSessionKey(normalizedSessionID, sessionEpoch)
	store.mutex.Lock()
	defer store.mutex.Unlock()
	connectorID, exists := store.bySessionKey[sessionKey]
	if !exists {
		return
	}
	delete(store.bySessionKey, sessionKey)
	delete(store.byConnector, connectorID)
}

// List 返回 tunnel 池上报快照列表（按 updated_at 倒序）。
func (store *TunnelPoolReportStore) List() []TunnelPoolReportRuntime {
	if store == nil {
		return []TunnelPoolReportRuntime{}
	}
	store.mutex.RLock()
	defer store.mutex.RUnlock()
	result := make([]TunnelPoolReportRuntime, 0, len(store.byConnector))
	for _, runtime := range store.byConnector {
		result = append(result, runtime)
	}
	sort.Slice(result, func(left, right int) bool {
		if result[left].UpdatedAt.Equal(result[right].UpdatedAt) {
			return result[left].ConnectorID < result[right].ConnectorID
		}
		return result[left].UpdatedAt.After(result[right].UpdatedAt)
	})
	return result
}

func buildTunnelPoolReportSessionKey(sessionID string, sessionEpoch uint64) string {
	return strings.TrimSpace(sessionID) + "#" + strconv.FormatUint(sessionEpoch, 10)
}

func maxInt(value int, fallback int) int {
	if value < fallback {
		return fallback
	}
	return value
}
