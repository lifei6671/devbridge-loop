package auth

import (
	"strings"
	"sync"
	"time"
)

const (
	// defaultHelloRateWindow 定义未认证 Hello 限流的默认滑窗。
	defaultHelloRateWindow = 10 * time.Second
	// defaultHelloRateLimitBySourceIP 定义 source_ip 维度默认 Hello 限流阈值。
	defaultHelloRateLimitBySourceIP = 30
	// defaultHelloRateLimitByConnectorID 定义 connector_id 维度默认 Hello 限流阈值。
	defaultHelloRateLimitByConnectorID = 20

	// defaultAuthFailureWindow 定义认证失败限流的默认滑窗。
	defaultAuthFailureWindow = 60 * time.Second
	// defaultAuthFailureLimitBySourceIP 定义 source_ip 维度默认认证失败阈值。
	defaultAuthFailureLimitBySourceIP = 20
	// defaultAuthFailureLimitByConnectorID 定义 connector_id 维度默认认证失败阈值。
	defaultAuthFailureLimitByConnectorID = 10
	// defaultAuthFailureBanDuration 定义认证失败触发封禁后的持续时间。
	defaultAuthFailureBanDuration = 2 * time.Minute

	// defaultUnauthenticatedConnectionBudget 定义未认证控制连接并发预算默认值。
	defaultUnauthenticatedConnectionBudget = 512
	// defaultAuthConcurrencyBudget 定义认证流程并发预算默认值。
	defaultAuthConcurrencyBudget = 128
)

// controlHandshakeGuardOptions 定义握手防护器参数。
type controlHandshakeGuardOptions struct {
	now func() time.Time

	helloRateWindow           time.Duration
	helloRateLimitBySource    int
	helloRateLimitByConnector int

	authFailureWindow           time.Duration
	authFailureLimitBySource    int
	authFailureLimitByConnector int
	authFailureBanDuration      time.Duration

	unauthenticatedConnectionBudget int
	authConcurrencyBudget           int
}

// controlHandshakeGuard 承担未认证入口限流、失败封禁与预算控制。
type controlHandshakeGuard struct {
	now func() time.Time

	helloRateLimiterBySource    *slidingWindowRateLimiter
	helloRateLimiterByConnector *slidingWindowRateLimiter

	authFailureLimiterBySource    *slidingWindowRateLimiter
	authFailureLimiterByConnector *slidingWindowRateLimiter
	authFailureBanDuration        time.Duration

	mutex                      sync.Mutex
	authBanBySourceIPUntil     map[string]time.Time
	authBanByConnectorIDUntil  map[string]time.Time
	activeUnauthenticatedConns int
	activeAuthConcurrency      int
	unauthenticatedConnBudget  int
	authConcurrencyBudget      int
}

// newControlHandshakeGuard 创建握手防护器并注入默认参数。
func newControlHandshakeGuard(options controlHandshakeGuardOptions) *controlHandshakeGuard {
	nowFunc := options.now
	if nowFunc == nil {
		nowFunc = func() time.Time { return time.Now().UTC() }
	}
	helloRateWindow := options.helloRateWindow
	if helloRateWindow <= 0 {
		helloRateWindow = defaultHelloRateWindow
	}
	helloLimitBySource := options.helloRateLimitBySource
	if helloLimitBySource <= 0 {
		helloLimitBySource = defaultHelloRateLimitBySourceIP
	}
	helloLimitByConnector := options.helloRateLimitByConnector
	if helloLimitByConnector <= 0 {
		helloLimitByConnector = defaultHelloRateLimitByConnectorID
	}
	authFailureWindow := options.authFailureWindow
	if authFailureWindow <= 0 {
		authFailureWindow = defaultAuthFailureWindow
	}
	authFailureLimitBySource := options.authFailureLimitBySource
	if authFailureLimitBySource <= 0 {
		authFailureLimitBySource = defaultAuthFailureLimitBySourceIP
	}
	authFailureLimitByConnector := options.authFailureLimitByConnector
	if authFailureLimitByConnector <= 0 {
		authFailureLimitByConnector = defaultAuthFailureLimitByConnectorID
	}
	authFailureBanDuration := options.authFailureBanDuration
	if authFailureBanDuration <= 0 {
		authFailureBanDuration = defaultAuthFailureBanDuration
	}
	unauthenticatedConnectionBudget := options.unauthenticatedConnectionBudget
	if unauthenticatedConnectionBudget <= 0 {
		unauthenticatedConnectionBudget = defaultUnauthenticatedConnectionBudget
	}
	authConcurrencyBudget := options.authConcurrencyBudget
	if authConcurrencyBudget <= 0 {
		authConcurrencyBudget = defaultAuthConcurrencyBudget
	}
	return &controlHandshakeGuard{
		now:                           nowFunc,
		helloRateLimiterBySource:      newSlidingWindowRateLimiter(helloRateWindow, helloLimitBySource),
		helloRateLimiterByConnector:   newSlidingWindowRateLimiter(helloRateWindow, helloLimitByConnector),
		authFailureLimiterBySource:    newSlidingWindowRateLimiter(authFailureWindow, authFailureLimitBySource),
		authFailureLimiterByConnector: newSlidingWindowRateLimiter(authFailureWindow, authFailureLimitByConnector),
		authFailureBanDuration:        authFailureBanDuration,
		authBanBySourceIPUntil:        make(map[string]time.Time),
		authBanByConnectorIDUntil:     make(map[string]time.Time),
		unauthenticatedConnBudget:     unauthenticatedConnectionBudget,
		authConcurrencyBudget:         authConcurrencyBudget,
	}
}

// nowUTC 返回统一 UTC 时间戳，便于测试可控与跨节点行为一致。
func (guard *controlHandshakeGuard) nowUTC() time.Time {
	if guard == nil || guard.now == nil {
		return time.Now().UTC()
	}
	return guard.now().UTC()
}

// AllowHello 尝试放行一次 Hello，并返回触发限流的维度标签。
func (guard *controlHandshakeGuard) AllowHello(sourceIP string, connectorID string) (bool, string) {
	if guard == nil {
		return true, ""
	}
	now := guard.nowUTC()
	normalizedSourceIP := strings.TrimSpace(sourceIP)
	if normalizedSourceIP != "" && !guard.helloRateLimiterBySource.Allow(normalizedSourceIP, now) {
		return false, "source_ip"
	}
	normalizedConnectorID := strings.TrimSpace(connectorID)
	if normalizedConnectorID != "" && !guard.helloRateLimiterByConnector.Allow(normalizedConnectorID, now) {
		return false, "connector_id"
	}
	return true, ""
}

// IsAuthBanned 判断当前 source_ip 或 connector_id 是否处于失败封禁期。
func (guard *controlHandshakeGuard) IsAuthBanned(sourceIP string, connectorID string) (bool, string, time.Time) {
	if guard == nil {
		return false, "", time.Time{}
	}
	now := guard.nowUTC()
	normalizedSourceIP := strings.TrimSpace(sourceIP)
	normalizedConnectorID := strings.TrimSpace(connectorID)

	guard.mutex.Lock()
	defer guard.mutex.Unlock()

	if normalizedSourceIP != "" {
		if banUntil, ok := guard.authBanBySourceIPUntil[normalizedSourceIP]; ok {
			if banUntil.After(now) {
				return true, "source_ip", banUntil
			}
			delete(guard.authBanBySourceIPUntil, normalizedSourceIP)
		}
	}
	if normalizedConnectorID != "" {
		if banUntil, ok := guard.authBanByConnectorIDUntil[normalizedConnectorID]; ok {
			if banUntil.After(now) {
				return true, "connector_id", banUntil
			}
			delete(guard.authBanByConnectorIDUntil, normalizedConnectorID)
		}
	}
	return false, "", time.Time{}
}

// RecordAuthFailure 记录一次认证失败，并在超阈值时激活封禁。
func (guard *controlHandshakeGuard) RecordAuthFailure(sourceIP string, connectorID string) (bool, string, time.Time) {
	if guard == nil {
		return false, "", time.Time{}
	}
	now := guard.nowUTC()
	normalizedSourceIP := strings.TrimSpace(sourceIP)
	normalizedConnectorID := strings.TrimSpace(connectorID)
	banUntil := now.Add(guard.authFailureBanDuration)

	if normalizedSourceIP != "" && !guard.authFailureLimiterBySource.Allow(normalizedSourceIP, now) {
		guard.mutex.Lock()
		guard.authBanBySourceIPUntil[normalizedSourceIP] = banUntil
		guard.mutex.Unlock()
		return true, "source_ip", banUntil
	}
	if normalizedConnectorID != "" && !guard.authFailureLimiterByConnector.Allow(normalizedConnectorID, now) {
		guard.mutex.Lock()
		guard.authBanByConnectorIDUntil[normalizedConnectorID] = banUntil
		guard.mutex.Unlock()
		return true, "connector_id", banUntil
	}
	return false, "", time.Time{}
}

// TryAcquireUnauthenticatedConnection 尝试占用一个未认证连接预算。
func (guard *controlHandshakeGuard) TryAcquireUnauthenticatedConnection() bool {
	if guard == nil {
		return true
	}
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	if guard.activeUnauthenticatedConns >= guard.unauthenticatedConnBudget {
		return false
	}
	guard.activeUnauthenticatedConns++
	return true
}

// ReleaseUnauthenticatedConnection 释放一个未认证连接预算。
func (guard *controlHandshakeGuard) ReleaseUnauthenticatedConnection() {
	if guard == nil {
		return
	}
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	if guard.activeUnauthenticatedConns <= 0 {
		guard.activeUnauthenticatedConns = 0
		return
	}
	guard.activeUnauthenticatedConns--
}

// TryAcquireAuthConcurrency 尝试占用一个认证并发预算。
func (guard *controlHandshakeGuard) TryAcquireAuthConcurrency() bool {
	if guard == nil {
		return true
	}
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	if guard.activeAuthConcurrency >= guard.authConcurrencyBudget {
		return false
	}
	guard.activeAuthConcurrency++
	return true
}

// ReleaseAuthConcurrency 释放一个认证并发预算。
func (guard *controlHandshakeGuard) ReleaseAuthConcurrency() {
	if guard == nil {
		return
	}
	guard.mutex.Lock()
	defer guard.mutex.Unlock()
	if guard.activeAuthConcurrency <= 0 {
		guard.activeAuthConcurrency = 0
		return
	}
	guard.activeAuthConcurrency--
}

// slidingWindowRateLimiter 提供轻量级滑动窗口限流。
type slidingWindowRateLimiter struct {
	window time.Duration
	limit  int

	mutex         sync.Mutex
	history       map[string][]time.Time
	lastCleanupAt time.Time
}

// newSlidingWindowRateLimiter 创建滑窗限流器。
func newSlidingWindowRateLimiter(window time.Duration, limit int) *slidingWindowRateLimiter {
	if window <= 0 {
		window = time.Second
	}
	if limit <= 0 {
		limit = 1
	}
	return &slidingWindowRateLimiter{
		window:  window,
		limit:   limit,
		history: make(map[string][]time.Time),
	}
}

// Allow 判断并记录一次请求，返回是否允许通过。
func (limiter *slidingWindowRateLimiter) Allow(key string, now time.Time) bool {
	if limiter == nil {
		return true
	}
	normalizedKey := strings.TrimSpace(key)
	if normalizedKey == "" {
		return true
	}
	cutoff := now.Add(-limiter.window)
	limiter.mutex.Lock()
	defer limiter.mutex.Unlock()
	if limiter.lastCleanupAt.IsZero() || !now.Before(limiter.lastCleanupAt.Add(limiter.window)) {
		limiter.cleanupExpiredHistoryLocked(cutoff)
		limiter.lastCleanupAt = now.UTC()
	}
	rawHistory := limiter.history[normalizedKey]
	filteredHistory := make([]time.Time, 0, len(rawHistory)+1)
	for _, historyTime := range rawHistory {
		if historyTime.Before(cutoff) {
			continue
		}
		filteredHistory = append(filteredHistory, historyTime.UTC())
	}
	if len(filteredHistory) >= limiter.limit {
		limiter.history[normalizedKey] = filteredHistory
		return false
	}
	filteredHistory = append(filteredHistory, now.UTC())
	limiter.history[normalizedKey] = filteredHistory
	return true
}

// cleanupExpiredHistoryLocked 清理滑窗外键，防止高基数键长期驻留。
func (limiter *slidingWindowRateLimiter) cleanupExpiredHistoryLocked(cutoff time.Time) {
	if limiter == nil {
		return
	}
	for key, rawHistory := range limiter.history {
		filteredHistory := make([]time.Time, 0, len(rawHistory))
		for _, historyTime := range rawHistory {
			if historyTime.Before(cutoff) {
				continue
			}
			filteredHistory = append(filteredHistory, historyTime.UTC())
		}
		if len(filteredHistory) == 0 {
			delete(limiter.history, key)
			continue
		}
		limiter.history[key] = filteredHistory
	}
}

// shouldCountForAuthFailureBan 判断某类错误是否应计入“认证失败封禁”。
func shouldCountForAuthFailureBan(errorCode string) bool {
	switch strings.TrimSpace(errorCode) {
	case connectorAuthErrorInvalidMethod,
		connectorAuthErrorInvalidToken,
		connectorAuthErrorTokenExpired,
		connectorAuthErrorTokenRevoked,
		connectorAuthErrorConnectorMismatch:
		return true
	default:
		return false
	}
}

// normalizePublicAuthReject 统一外显错误口径，降低 connector/token 枚举区分度。
func normalizePublicAuthReject(errorCode string, errorMessage string) (string, string) {
	normalizedErrorCode := strings.TrimSpace(errorCode)
	switch normalizedErrorCode {
	case connectorAuthErrorInvalidToken, connectorAuthErrorTokenRevoked, connectorAuthErrorConnectorMismatch:
		return connectorAuthErrorInvalidToken, "authentication rejected"
	case connectorAuthErrorRateLimited:
		return connectorAuthErrorRateLimited, "authentication rejected"
	default:
		return normalizedErrorCode, strings.TrimSpace(errorMessage)
	}
}
