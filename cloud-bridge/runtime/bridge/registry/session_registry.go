package registry

import (
	"strings"
	"sync"
	"time"
)

// SessionState 定义 Bridge 侧 session 状态。
type SessionState string

const (
	SessionActive   SessionState = "ACTIVE"
	SessionDraining SessionState = "DRAINING"
	SessionStale    SessionState = "STALE"
	SessionClosed   SessionState = "CLOSED"
)

// SessionRuntime 保存 session 的运行态信息。
type SessionRuntime struct {
	SessionID     string
	ConnectorID   string
	Epoch         uint64
	State         SessionState
	LastHeartbeat time.Time
	UpdatedAt     time.Time
}

// AuthoritativeSessionCommitResult 描述一次 connector 权威会话提交的结果。
type AuthoritativeSessionCommitResult struct {
	PreviousSession      SessionRuntime
	PreviousExists       bool
	PreviousStateChanged bool
	CurrentSession       SessionRuntime
}

// SessionRegistry 跟踪 connector 的 session 视图。
type SessionRegistry struct {
	mu          sync.RWMutex
	bySessionID map[string]*SessionRuntime
	byConnector map[string]string
}

// NewSessionRegistry 初始化 session 注册表。
func NewSessionRegistry() *SessionRegistry {
	return &SessionRegistry{
		bySessionID: make(map[string]*SessionRuntime),
		byConnector: make(map[string]string),
	}
}

// Upsert 写入或更新 session 记录。
func (r *SessionRegistry) Upsert(now time.Time, runtime SessionRuntime) {
	r.mu.Lock()
	defer r.mu.Unlock()
	// 使用最新时间戳覆盖更新时间。
	runtime.UpdatedAt = now
	copy := runtime
	r.bySessionID[runtime.SessionID] = &copy
	if runtime.ConnectorID != "" {
		r.byConnector[runtime.ConnectorID] = runtime.SessionID
	}
}

// CommitAuthoritative 原子提交 connector 当前权威会话，并在必要时把旧会话降为 DRAINING。
func (r *SessionRegistry) CommitAuthoritative(
	now time.Time,
	runtime SessionRuntime,
) (AuthoritativeSessionCommitResult, bool) {
	if r == nil {
		return AuthoritativeSessionCommitResult{}, false
	}
	normalizedSessionID := strings.TrimSpace(runtime.SessionID)
	normalizedConnectorID := strings.TrimSpace(runtime.ConnectorID)
	if normalizedSessionID == "" || normalizedConnectorID == "" || runtime.Epoch == 0 {
		return AuthoritativeSessionCommitResult{}, false
	}
	normalizedNow := now.UTC()
	if normalizedNow.IsZero() {
		normalizedNow = time.Now().UTC()
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	result := AuthoritativeSessionCommitResult{}
	if existingSession, exists := r.bySessionID[normalizedSessionID]; exists && existingSession.Epoch > runtime.Epoch {
		// 同 session_id 不允许被更低 epoch 事件回退覆盖。
		return result, false
	}
	if currentSessionID, exists := r.byConnector[normalizedConnectorID]; exists {
		if currentSession, currentExists := r.bySessionID[currentSessionID]; currentExists {
			result.PreviousSession = *currentSession
			result.PreviousExists = true
			if strings.TrimSpace(currentSession.SessionID) != normalizedSessionID {
				if currentSession.Epoch > runtime.Epoch {
					switch currentSession.State {
					case SessionStale, SessionClosed:
						// 旧权威已终态时，允许 Agent 重启后的低 epoch 连接重新接管。
					default:
						return result, false
					}
				}
				if currentSession.Epoch == runtime.Epoch {
					switch currentSession.State {
					case SessionStale, SessionClosed:
						// 同 epoch 但旧权威已终态时允许同代接管。
					default:
						return result, false
					}
				}
				if currentSession.State == SessionActive || currentSession.State == SessionDraining {
					currentSession.State = SessionDraining
					currentSession.UpdatedAt = normalizedNow
					result.PreviousSession = *currentSession
					result.PreviousStateChanged = true
				}
			}
		}
	}

	runtime.SessionID = normalizedSessionID
	runtime.ConnectorID = normalizedConnectorID
	runtime.UpdatedAt = normalizedNow
	if runtime.State == SessionActive && runtime.LastHeartbeat.IsZero() {
		// 认证成功瞬间即视为该 session 已存活，便于后续 heartbeat timeout 计算。
		runtime.LastHeartbeat = normalizedNow
	}
	copy := runtime
	r.bySessionID[normalizedSessionID] = &copy
	r.byConnector[normalizedConnectorID] = normalizedSessionID
	result.CurrentSession = copy
	return result, true
}

// MarkState 更新 session 状态，返回是否更新成功。
func (r *SessionRegistry) MarkState(now time.Time, sessionID string, state SessionState) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.bySessionID[sessionID]
	if !ok {
		// 未找到 session 时直接返回 false。
		return false
	}
	entry.State = state
	entry.UpdatedAt = now
	return true
}

// RecordHeartbeat 记录 session 的心跳时间。
func (r *SessionRegistry) RecordHeartbeat(now time.Time, sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.bySessionID[sessionID]
	if !ok {
		// 未找到 session，无法记录心跳。
		return false
	}
	entry.LastHeartbeat = now
	entry.UpdatedAt = now
	return true
}

// GetBySession 获取 session 运行态快照。
func (r *SessionRegistry) GetBySession(sessionID string) (SessionRuntime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	entry, ok := r.bySessionID[sessionID]
	if !ok {
		// 未找到则返回零值。
		return SessionRuntime{}, false
	}
	return *entry, true
}

// GetByConnector 通过 connector_id 查找 session。
func (r *SessionRegistry) GetByConnector(connectorID string) (SessionRuntime, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	sessionID, ok := r.byConnector[connectorID]
	if !ok {
		// 未建立索引时直接返回。
		return SessionRuntime{}, false
	}
	entry, ok := r.bySessionID[sessionID]
	if !ok {
		return SessionRuntime{}, false
	}
	return *entry, true
}

// Remove 删除 session 记录并清理索引。
func (r *SessionRegistry) Remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	entry, ok := r.bySessionID[sessionID]
	if ok && entry.ConnectorID != "" {
		// 仅在索引仍指向当前 session 时删除，避免误删新会话映射。
		if indexedSessionID, exists := r.byConnector[entry.ConnectorID]; exists && indexedSessionID == sessionID {
			delete(r.byConnector, entry.ConnectorID)
		}
	}
	delete(r.bySessionID, sessionID)
}

// List 返回所有 session 的快照列表。
func (r *SessionRegistry) List() []SessionRuntime {
	r.mu.RLock()
	defer r.mu.RUnlock()
	result := make([]SessionRuntime, 0, len(r.bySessionID))
	for _, entry := range r.bySessionID {
		// 复制值，避免外部修改内部状态。
		result = append(result, *entry)
	}
	return result
}

// SweepHeartbeatTimeout 根据心跳超时将 session 标记为 STALE。
func (r *SessionRegistry) SweepHeartbeatTimeout(now time.Time, timeout time.Duration) []SessionRuntime {
	if timeout <= 0 {
		// 超时时间非法时不做处理。
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var stale []SessionRuntime
	for _, entry := range r.bySessionID {
		if entry.State != SessionActive && entry.State != SessionDraining {
			// 仅 ACTIVE/DRAINING 会话参与 heartbeat 判死，避免重复触发 STALE 副作用。
			continue
		}
		if entry.LastHeartbeat.IsZero() {
			// 未记录心跳时跳过，由上层决定策略。
			continue
		}
		if now.Sub(entry.LastHeartbeat) > timeout {
			// 超时则进入 STALE，等待收敛。
			entry.State = SessionStale
			entry.UpdatedAt = now
			stale = append(stale, *entry)
		}
	}
	return stale
}

// SweepStaleToClosed 将超过阈值的 STALE session 收敛为 CLOSED。
func (r *SessionRegistry) SweepStaleToClosed(now time.Time, staleTTL time.Duration) []SessionRuntime {
	if staleTTL <= 0 {
		// 未配置阈值则不执行收敛。
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	var closed []SessionRuntime
	for _, entry := range r.bySessionID {
		if entry.State != SessionStale {
			// 仅处理 STALE 状态。
			continue
		}
		if now.Sub(entry.UpdatedAt) > staleTTL {
			// 超过阈值则收敛为 CLOSED。
			entry.State = SessionClosed
			entry.UpdatedAt = now
			closed = append(closed, *entry)
		}
	}
	return closed
}
