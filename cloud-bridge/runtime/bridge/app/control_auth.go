package app

import (
	"crypto/subtle"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

const (
	// defaultConnectorSupersedeRateWindow 定义成功抢占限流的默认滑窗。
	defaultConnectorSupersedeRateWindow = 60 * time.Second
	// defaultConnectorSupersedeRateLimit 定义同 connector 默认允许的滑窗内成功抢占次数。
	defaultConnectorSupersedeRateLimit = 3
)

const (
	connectorAuthErrorInvalidMethod     = "auth_invalid_method"
	connectorAuthErrorInvalidToken      = "auth_invalid_token"
	connectorAuthErrorTokenExpired      = "auth_token_expired"
	connectorAuthErrorTokenRevoked      = "auth_token_revoked"
	connectorAuthErrorConnectorMismatch = "auth_connector_mismatch"
	connectorAuthErrorSessionSuperseded = "auth_session_superseded"
	connectorAuthErrorRateLimited       = "auth_rate_limited"
	connectorAuthErrorInternal          = "auth_internal_error"
)

// connectorTokenStatus 定义 token 在认证阶段的状态。
type connectorTokenStatus string

const (
	connectorTokenStatusActive  connectorTokenStatus = "active"
	connectorTokenStatusGrace   connectorTokenStatus = "grace"
	connectorTokenStatusRevoked connectorTokenStatus = "revoked"
	connectorTokenStatusExpired connectorTokenStatus = "expired"
)

// connectorTokenRecord 保存 Connector token 校验所需的最小字段。
type connectorTokenRecord struct {
	TokenID     string
	ConnectorID string
	TokenSecret string
	Status      connectorTokenStatus
	ExpiresAt   time.Time
}

// connectorTokenStore 定义 token 索引查询接口。
type connectorTokenStore interface {
	LookupByTokenID(tokenID string) (connectorTokenRecord, bool, error)
}

// inMemoryConnectorTokenStore 提供开发期可用的内存 token 索引实现。
type inMemoryConnectorTokenStore struct {
	mu        sync.RWMutex
	byTokenID map[string]connectorTokenRecord
}

// newInMemoryConnectorTokenStore 根据 token 记录构建内存索引。
func newInMemoryConnectorTokenStore(records []connectorTokenRecord) *inMemoryConnectorTokenStore {
	store := &inMemoryConnectorTokenStore{
		byTokenID: make(map[string]connectorTokenRecord, len(records)),
	}
	for _, record := range records {
		normalizedTokenID := strings.TrimSpace(record.TokenID)
		if normalizedTokenID == "" {
			// token_id 非法时直接跳过，避免污染索引。
			continue
		}
		normalizedRecord := connectorTokenRecord{
			TokenID:     normalizedTokenID,
			ConnectorID: strings.TrimSpace(record.ConnectorID),
			TokenSecret: strings.TrimSpace(record.TokenSecret),
			Status:      normalizeConnectorTokenStatus(record.Status),
			ExpiresAt:   record.ExpiresAt.UTC(),
		}
		store.byTokenID[normalizedTokenID] = normalizedRecord
	}
	return store
}

// LookupByTokenID 通过 token_id 查询 token 记录。
func (store *inMemoryConnectorTokenStore) LookupByTokenID(tokenID string) (connectorTokenRecord, bool, error) {
	if store == nil {
		return connectorTokenRecord{}, false, fmt.Errorf("connector token store is nil")
	}
	normalizedTokenID := strings.TrimSpace(tokenID)
	if normalizedTokenID == "" {
		return connectorTokenRecord{}, false, nil
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	record, exists := store.byTokenID[normalizedTokenID]
	if !exists {
		return connectorTokenRecord{}, false, nil
	}
	return record, true, nil
}

// connectorScopedLocker 提供 connector 粒度互斥，保证认证提交的原子性。
type connectorScopedLocker struct {
	mu    sync.Mutex
	locks map[string]*connectorScopedLockRef
}

// connectorScopedLockRef 保存 connector 粒度锁及引用计数。
type connectorScopedLockRef struct {
	mu       sync.Mutex
	refCount int
}

// newConnectorScopedLocker 初始化 connector 粒度锁容器。
func newConnectorScopedLocker() *connectorScopedLocker {
	return &connectorScopedLocker{locks: make(map[string]*connectorScopedLockRef)}
}

// lock 获取指定 connector 的互斥锁，并返回释放函数。
func (locker *connectorScopedLocker) lock(connectorID string) func() {
	if locker == nil {
		return func() {}
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return func() {}
	}
	locker.mu.Lock()
	lockRef, exists := locker.locks[normalizedConnectorID]
	if !exists {
		lockRef = &connectorScopedLockRef{}
		locker.locks[normalizedConnectorID] = lockRef
	}
	lockRef.refCount++
	locker.mu.Unlock()

	lockRef.mu.Lock()
	return func() {
		lockRef.mu.Unlock()
		locker.mu.Lock()
		lockRef.refCount--
		if lockRef.refCount <= 0 {
			delete(locker.locks, normalizedConnectorID)
		}
		locker.mu.Unlock()
	}
}

// connectorAuthCoordinatorOptions 定义认证协调器的依赖注入参数。
type connectorAuthCoordinatorOptions struct {
	sessionRegistry     *registry.SessionRegistry
	tokenStore          connectorTokenStore
	now                 func() time.Time
	supersedeRateWindow time.Duration
	supersedeRateLimit  int
}

// connectorAuthCoordinator 负责按固定顺序执行认证校验并提交 session。
type connectorAuthCoordinator struct {
	sessionRegistry     *registry.SessionRegistry
	tokenStore          connectorTokenStore
	now                 func() time.Time
	supersedeRateWindow time.Duration
	supersedeRateLimit  int

	locker *connectorScopedLocker

	historyMu        sync.Mutex
	supersedeHistory map[string][]time.Time
}

// connectorAuthRequest 定义一次 ConnectorAuth 请求的认证参数。
type connectorAuthRequest struct {
	connectorID          string
	assignedSessionEpoch uint64
	authMethod           string
	token                string
}

// connectorAuthResult 表示 ConnectorAuth 的最终判定结果。
type connectorAuthResult struct {
	success      bool
	sessionID    string
	sessionEpoch uint64
	errorCode    string
	errorMessage string
}

// newConnectorAuthCoordinator 创建认证协调器并填充默认值。
func newConnectorAuthCoordinator(options connectorAuthCoordinatorOptions) *connectorAuthCoordinator {
	sessionRegistry := options.sessionRegistry
	if sessionRegistry == nil {
		// 未注入注册表时回退本地实现，避免空指针路径。
		sessionRegistry = registry.NewSessionRegistry()
	}
	tokenStore := options.tokenStore
	if tokenStore == nil {
		// 默认注入一条开发 token，保证本地 agent-local 场景可联调。
		tokenStore = newInMemoryConnectorTokenStore(defaultConnectorTokenRecords())
	}
	nowFunc := options.now
	if nowFunc == nil {
		// 统一使用 UTC 作为认证与限流时间基准。
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	rateWindow := options.supersedeRateWindow
	if rateWindow <= 0 {
		rateWindow = defaultConnectorSupersedeRateWindow
	}
	rateLimit := options.supersedeRateLimit
	if rateLimit <= 0 {
		rateLimit = defaultConnectorSupersedeRateLimit
	}
	return &connectorAuthCoordinator{
		sessionRegistry:     sessionRegistry,
		tokenStore:          tokenStore,
		now:                 nowFunc,
		supersedeRateWindow: rateWindow,
		supersedeRateLimit:  rateLimit,
		locker:              newConnectorScopedLocker(),
		supersedeHistory:    make(map[string][]time.Time),
	}
}

// AuthenticateAndCommit 执行固定顺序认证并在通过后原子提交 session。
func (coordinator *connectorAuthCoordinator) AuthenticateAndCommit(
	request connectorAuthRequest,
	commit func(sessionID string, sessionEpoch uint64) error,
) connectorAuthResult {
	if coordinator == nil {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth coordinator is nil",
		}
	}
	if commit == nil {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit callback is nil",
		}
	}
	normalizedAuthMethod := strings.ToLower(strings.TrimSpace(request.authMethod))
	if normalizedAuthMethod != "token" {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidMethod,
			errorMessage: "unsupported auth_method",
		}
	}
	normalizedConnectorID := strings.TrimSpace(request.connectorID)
	if normalizedConnectorID == "" {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorConnectorMismatch,
			errorMessage: "connector_id is required",
		}
	}
	tokenID, tokenSecret, parsed := parseConnectorToken(request.token)
	if !parsed {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "invalid token format",
		}
	}
	tokenRecord, tokenFound, lookupErr := coordinator.tokenStore.LookupByTokenID(tokenID)
	if lookupErr != nil {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "token lookup failed",
		}
	}
	if !tokenFound {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "token not found",
		}
	}
	if strings.TrimSpace(tokenRecord.ConnectorID) != normalizedConnectorID {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorConnectorMismatch,
			errorMessage: "token connector_id mismatch",
		}
	}
	if subtle.ConstantTimeCompare([]byte(strings.TrimSpace(tokenRecord.TokenSecret)), []byte(tokenSecret)) != 1 {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "token secret mismatch",
		}
	}
	now := coordinator.nowUTC()
	if errorCode, errorMessage, ok := validateConnectorTokenState(tokenRecord, now); !ok {
		return connectorAuthResult{
			errorCode:    errorCode,
			errorMessage: errorMessage,
		}
	}
	if request.assignedSessionEpoch == 0 {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "assigned_session_epoch is required",
		}
	}

	// 对同一 connector 的提交阶段加锁，保证读取与写入的一致性。
	unlock := coordinator.locker.lock(normalizedConnectorID)
	defer unlock()

	currentSession, currentExists := coordinator.sessionRegistry.GetByConnector(normalizedConnectorID)
	if currentExists && currentSession.Epoch >= request.assignedSessionEpoch {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorSessionSuperseded,
			errorMessage: "session epoch has been superseded",
		}
	}
	supersedeAttempt := currentExists &&
		strings.TrimSpace(currentSession.SessionID) != "" &&
		currentSession.Epoch > 0 &&
		(currentSession.State == registry.SessionActive || currentSession.State == registry.SessionDraining)
	if supersedeAttempt && coordinator.isSupersedeRateLimited(normalizedConnectorID, now) {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorRateLimited,
			errorMessage: "session supersede rate limited",
		}
	}

	sessionID := newConnectorSessionID()
	if commitErr := commit(sessionID, request.assignedSessionEpoch); commitErr != nil {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit failed",
		}
	}
	committedSession, committed := coordinator.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !committed || strings.TrimSpace(committedSession.SessionID) != sessionID || committedSession.Epoch != request.assignedSessionEpoch {
		return connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit is not authoritative",
		}
	}
	if supersedeAttempt {
		// 仅在成功抢占生效后计入限流历史。
		coordinator.recordSuccessfulSupersede(normalizedConnectorID, now)
	}
	return connectorAuthResult{
		success:      true,
		sessionID:    sessionID,
		sessionEpoch: request.assignedSessionEpoch,
	}
}

// nowUTC 返回认证流程统一使用的 UTC 时间戳。
func (coordinator *connectorAuthCoordinator) nowUTC() time.Time {
	if coordinator == nil || coordinator.now == nil {
		return time.Now().UTC()
	}
	return coordinator.now().UTC()
}

// isSupersedeRateLimited 判定当前 connector 是否触发成功抢占限流。
func (coordinator *connectorAuthCoordinator) isSupersedeRateLimited(connectorID string, now time.Time) bool {
	if coordinator == nil {
		return false
	}
	history := coordinator.pruneSupersedeHistory(connectorID, now)
	return len(history) >= coordinator.supersedeRateLimit
}

// recordSuccessfulSupersede 记录一次成功抢占事件。
func (coordinator *connectorAuthCoordinator) recordSuccessfulSupersede(connectorID string, now time.Time) {
	if coordinator == nil {
		return
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return
	}
	history := coordinator.pruneSupersedeHistory(normalizedConnectorID, now)
	history = append(history, now)
	coordinator.historyMu.Lock()
	coordinator.supersedeHistory[normalizedConnectorID] = history
	coordinator.historyMu.Unlock()
}

// pruneSupersedeHistory 清理滑窗外历史并返回当前有效序列。
func (coordinator *connectorAuthCoordinator) pruneSupersedeHistory(connectorID string, now time.Time) []time.Time {
	if coordinator == nil {
		return nil
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID == "" {
		return nil
	}
	cutoff := now.Add(-coordinator.supersedeRateWindow)
	coordinator.historyMu.Lock()
	defer coordinator.historyMu.Unlock()
	history := coordinator.supersedeHistory[normalizedConnectorID]
	if len(history) == 0 {
		return nil
	}
	filtered := make([]time.Time, 0, len(history))
	for _, item := range history {
		// 保留滑窗内事件，超窗事件直接丢弃。
		if item.Before(cutoff) {
			continue
		}
		filtered = append(filtered, item.UTC())
	}
	if len(filtered) == 0 {
		delete(coordinator.supersedeHistory, normalizedConnectorID)
		return nil
	}
	coordinator.supersedeHistory[normalizedConnectorID] = filtered
	return append([]time.Time(nil), filtered...)
}

// validateConnectorTokenState 校验 token 状态机与过期时间。
func validateConnectorTokenState(record connectorTokenRecord, now time.Time) (string, string, bool) {
	normalizedStatus := normalizeConnectorTokenStatus(record.Status)
	switch normalizedStatus {
	case connectorTokenStatusActive, connectorTokenStatusGrace:
		// active / grace 状态允许参与认证，继续检查过期时间。
	case connectorTokenStatusRevoked:
		return connectorAuthErrorTokenRevoked, "token is revoked", false
	case connectorTokenStatusExpired:
		return connectorAuthErrorTokenExpired, "token is expired", false
	default:
		return connectorAuthErrorInvalidToken, "token status is invalid", false
	}
	if !record.ExpiresAt.IsZero() && now.After(record.ExpiresAt.UTC()) {
		return connectorAuthErrorTokenExpired, "token is expired", false
	}
	return "", "", true
}

// normalizeConnectorTokenStatus 归一化 token 状态文本。
func normalizeConnectorTokenStatus(status connectorTokenStatus) connectorTokenStatus {
	switch strings.ToLower(strings.TrimSpace(string(status))) {
	case string(connectorTokenStatusActive):
		return connectorTokenStatusActive
	case string(connectorTokenStatusGrace):
		return connectorTokenStatusGrace
	case string(connectorTokenStatusRevoked):
		return connectorTokenStatusRevoked
	case string(connectorTokenStatusExpired):
		return connectorTokenStatusExpired
	default:
		return ""
	}
}

// parseConnectorToken 解析 dbt_<token_id>.<token_secret> 结构。
func parseConnectorToken(rawToken string) (string, string, bool) {
	normalizedToken := strings.TrimSpace(rawToken)
	if !strings.HasPrefix(normalizedToken, "dbt_") {
		return "", "", false
	}
	withoutPrefix := strings.TrimPrefix(normalizedToken, "dbt_")
	tokenID, tokenSecret, found := strings.Cut(withoutPrefix, ".")
	if !found {
		return "", "", false
	}
	tokenID = strings.TrimSpace(tokenID)
	tokenSecret = strings.TrimSpace(tokenSecret)
	if tokenID == "" || tokenSecret == "" {
		return "", "", false
	}
	if !isValidConnectorTokenID(tokenID) {
		return "", "", false
	}
	return tokenID, tokenSecret, true
}

// isValidConnectorTokenID 校验 token_id 是否满足 [A-Za-z0-9_-]+。
func isValidConnectorTokenID(tokenID string) bool {
	if strings.TrimSpace(tokenID) == "" {
		return false
	}
	for _, char := range tokenID {
		isDigit := char >= '0' && char <= '9'
		isLowerAlpha := char >= 'a' && char <= 'z'
		isUpperAlpha := char >= 'A' && char <= 'Z'
		if isDigit || isLowerAlpha || isUpperAlpha || char == '_' || char == '-' {
			continue
		}
		return false
	}
	return true
}

// defaultConnectorTokenRecords 返回开发环境默认 token 记录。
func defaultConnectorTokenRecords() []connectorTokenRecord {
	return []connectorTokenRecord{
		{
			TokenID:     "agent-local",
			ConnectorID: "agent-local",
			TokenSecret: "agent-dev-secret",
			Status:      connectorTokenStatusActive,
		},
	}
}
