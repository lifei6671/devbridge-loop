package auth

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

const (
	// defaultConnectorSupersedeRateWindow 定义成功抢占限流的默认滑窗。
	defaultConnectorSupersedeRateWindow = 60 * time.Second
	// defaultConnectorSupersedeRateLimit 定义同 connector 默认允许的滑窗内成功抢占次数。
	defaultConnectorSupersedeRateLimit = 3
)

const (
	connectorAuthErrorInvalidMethod     = ltfperrors.CodeAuthInvalidMethod
	connectorAuthErrorInvalidToken      = ltfperrors.CodeAuthInvalidToken
	connectorAuthErrorTokenExpired      = ltfperrors.CodeAuthTokenExpired
	connectorAuthErrorTokenRevoked      = ltfperrors.CodeAuthTokenRevoked
	connectorAuthErrorConnectorMismatch = ltfperrors.CodeAuthConnectorMismatch
	connectorAuthErrorSessionSuperseded = ltfperrors.CodeAuthSessionSuperseded
	connectorAuthErrorRateLimited       = ltfperrors.CodeAuthRateLimited
	connectorAuthErrorInternal          = ltfperrors.CodeAuthInternalError
)

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
	metrics             *obs.Metrics
	now                 func() time.Time
	sessionIDGenerator  func() string
	supersedeRateWindow time.Duration
	supersedeRateLimit  int
}

// connectorAuthCoordinator 负责按固定顺序执行认证校验并提交 session。
type connectorAuthCoordinator struct {
	sessionRegistry     *registry.SessionRegistry
	tokenStore          connectorTokenStore
	metrics             *obs.Metrics
	now                 func() time.Time
	sessionIDGenerator  func() string
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
	sessionIDGenerator := options.sessionIDGenerator
	if sessionIDGenerator == nil {
		// 未注入时使用默认 session_id 生成器，保持认证链路自洽。
		sessionIDGenerator = defaultConnectorSessionIDGenerator
	}
	rateWindow := options.supersedeRateWindow
	if rateWindow <= 0 {
		rateWindow = defaultConnectorSupersedeRateWindow
	}
	rateLimit := options.supersedeRateLimit
	if rateLimit <= 0 {
		rateLimit = defaultConnectorSupersedeRateLimit
	}
	metrics := options.metrics
	if metrics == nil {
		metrics = obs.DefaultMetrics
	}
	return &connectorAuthCoordinator{
		sessionRegistry:     sessionRegistry,
		tokenStore:          tokenStore,
		metrics:             metrics,
		now:                 nowFunc,
		sessionIDGenerator:  sessionIDGenerator,
		supersedeRateWindow: rateWindow,
		supersedeRateLimit:  rateLimit,
		locker:              newConnectorScopedLocker(),
		supersedeHistory:    make(map[string][]time.Time),
	}
}

// AuthenticateAndCommit 执行固定顺序认证并在通过后原子提交 session。
func (coordinator *connectorAuthCoordinator) AuthenticateAndCommit(
	request connectorAuthRequest,
	commit func(now time.Time, sessionRuntime registry.SessionRuntime) error,
) (result connectorAuthResult) {
	supersedeSucceeded := false
	defer func() {
		// 认证结果需要在函数真正返回时再采样，避免遗漏成功接管后的 supersede 计数。
		coordinator.observeAuthResult(&result, supersedeSucceeded)
	}()
	if coordinator == nil {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth coordinator is nil",
		}
		return result
	}
	if commit == nil {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit callback is nil",
		}
		return result
	}
	normalizedAuthMethod := strings.ToLower(strings.TrimSpace(request.authMethod))
	if normalizedAuthMethod != "token" {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidMethod,
			errorMessage: "unsupported auth_method",
		}
		return result
	}
	normalizedConnectorID := strings.TrimSpace(request.connectorID)
	if normalizedConnectorID == "" {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorConnectorMismatch,
			errorMessage: "connector_id is required",
		}
		return result
	}
	tokenID, tokenSecret, parsed := parseConnectorToken(request.token)
	if !parsed {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "invalid token format",
		}
		return result
	}
	tokenRecord, tokenFound, lookupErr := coordinator.tokenStore.LookupByTokenID(tokenID)
	if lookupErr != nil {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "token lookup failed",
		}
		return result
	}
	if !tokenFound {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "token not found",
		}
		return result
	}
	if strings.TrimSpace(tokenRecord.ConnectorID) != normalizedConnectorID {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorConnectorMismatch,
			errorMessage: "token connector_id mismatch",
		}
		return result
	}
	secretMatched, verifyErr := verifyConnectorTokenSecret(tokenSecret, tokenRecord.TokenSecretHash)
	if verifyErr != nil {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "token hash verify failed",
		}
		return result
	}
	if !secretMatched {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInvalidToken,
			errorMessage: "token secret mismatch",
		}
		return result
	}
	now := coordinator.nowUTC()
	if errorCode, errorMessage, ok := validateConnectorTokenState(tokenRecord, now); !ok {
		result = connectorAuthResult{
			errorCode:    errorCode,
			errorMessage: errorMessage,
		}
		return result
	}
	if request.assignedSessionEpoch == 0 {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "assigned_session_epoch is required",
		}
		return result
	}

	// 对同一 connector 的提交阶段加锁，保证读取与写入的一致性。
	unlock := coordinator.locker.lock(normalizedConnectorID)
	defer unlock()

	currentSession, currentExists := coordinator.sessionRegistry.GetByConnector(normalizedConnectorID)
	if currentExists && currentSession.Epoch >= request.assignedSessionEpoch {
		switch currentSession.State {
		case registry.SessionStale, registry.SessionFailed, registry.SessionClosed:
			// 旧权威已终态时允许新握手重新接管 connector。
		default:
			result = connectorAuthResult{
				errorCode:    connectorAuthErrorSessionSuperseded,
				errorMessage: "session epoch has been superseded",
			}
			return result
		}
	}
	supersedeAttempt := currentExists &&
		strings.TrimSpace(currentSession.SessionID) != "" &&
		currentSession.Epoch > 0 &&
		(currentSession.State == registry.SessionActive || currentSession.State == registry.SessionDraining)
	if supersedeAttempt && coordinator.isSupersedeRateLimited(normalizedConnectorID, now) {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorRateLimited,
			errorMessage: "session supersede rate limited",
		}
		return result
	}

	sessionID := coordinator.sessionIDGenerator()
	if commitErr := commit(now, registry.SessionRuntime{
		SessionID:     sessionID,
		ConnectorID:   normalizedConnectorID,
		Epoch:         request.assignedSessionEpoch,
		State:         registry.SessionActive,
		LastHeartbeat: now,
		UpdatedAt:     now,
	}); commitErr != nil {
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit failed",
		}
		return result
	}
	committedSession, committed := coordinator.sessionRegistry.GetByConnector(normalizedConnectorID)
	if !committed || strings.TrimSpace(committedSession.SessionID) != sessionID || committedSession.Epoch != request.assignedSessionEpoch {
		// commit 回调必须让当前 connector 视图与刚签发的 session 完全一致，否则视为非权威提交。
		result = connectorAuthResult{
			errorCode:    connectorAuthErrorInternal,
			errorMessage: "auth commit is not authoritative",
		}
		return result
	}
	if supersedeAttempt {
		// 仅在成功抢占生效后计入限流历史。
		coordinator.recordSuccessfulSupersede(normalizedConnectorID, now)
		supersedeSucceeded = true
	}
	result = connectorAuthResult{
		success:      true,
		sessionID:    sessionID,
		sessionEpoch: request.assignedSessionEpoch,
	}
	return result
}

// defaultConnectorSessionIDGenerator 生成握手成功后的 session_id。
func defaultConnectorSessionIDGenerator() string {
	return fmt.Sprintf("session-%d", time.Now().UTC().UnixNano())
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

// observeAuthResult 统一记录认证成功率、错误码分布与接管/限流指标。
func (coordinator *connectorAuthCoordinator) observeAuthResult(result *connectorAuthResult, supersedeSucceeded bool) {
	if coordinator == nil || coordinator.metrics == nil || result == nil {
		return
	}
	if result.success {
		coordinator.metrics.IncBridgeAuthSuccessTotal()
		if supersedeSucceeded {
			coordinator.metrics.IncBridgeAuthSupersedeTotal()
		}
		return
	}
	coordinator.metrics.ObserveBridgeAuthFailure(result.errorCode)
	if result.errorCode == connectorAuthErrorRateLimited {
		coordinator.metrics.IncBridgeAuthRateLimitTotal()
	}
}
