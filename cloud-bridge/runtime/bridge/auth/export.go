package auth

import (
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/registry"
)

const (
	// AuthErrorInvalidMethod 表示认证方法非法。
	AuthErrorInvalidMethod = connectorAuthErrorInvalidMethod
	// AuthErrorInvalidToken 表示 token 非法。
	AuthErrorInvalidToken = connectorAuthErrorInvalidToken
	// AuthErrorTokenExpired 表示 token 已过期。
	AuthErrorTokenExpired = connectorAuthErrorTokenExpired
	// AuthErrorTokenRevoked 表示 token 已吊销。
	AuthErrorTokenRevoked = connectorAuthErrorTokenRevoked
	// AuthErrorConnectorMismatch 表示 connector 归属不匹配。
	AuthErrorConnectorMismatch = connectorAuthErrorConnectorMismatch
	// AuthErrorSessionSuperseded 表示会话被更高代际抢占。
	AuthErrorSessionSuperseded = connectorAuthErrorSessionSuperseded
	// AuthErrorRateLimited 表示认证触发限流。
	AuthErrorRateLimited = connectorAuthErrorRateLimited
	// AuthErrorInternal 表示认证内部错误。
	AuthErrorInternal = connectorAuthErrorInternal
)

const (
	// TokenHashAlgorithmArgon2ID 标识默认 token 哈希算法。
	TokenHashAlgorithmArgon2ID = connectorTokenHashAlgorithmArgon2ID
	// TokenHashVersionV1 标识当前 token 哈希参数版本。
	TokenHashVersionV1 = connectorTokenHashVersionV1
)

// TokenStatus 定义 token 认证状态。
type TokenStatus = connectorTokenStatus

const (
	// TokenStatusActive 表示 token 可正常使用。
	TokenStatusActive TokenStatus = connectorTokenStatusActive
	// TokenStatusGrace 表示 token 处于平滑切换窗口。
	TokenStatusGrace TokenStatus = connectorTokenStatusGrace
	// TokenStatusRevoked 表示 token 已被吊销。
	TokenStatusRevoked TokenStatus = connectorTokenStatusRevoked
	// TokenStatusExpired 表示 token 已过期。
	TokenStatusExpired TokenStatus = connectorTokenStatusExpired
)

// TokenRecord 对外暴露 token 领域模型。
type TokenRecord = connectorTokenRecord

// TokenStore 定义 token 查询能力。
type TokenStore interface {
	LookupByTokenID(tokenID string) (TokenRecord, bool, error)
}

// NewInMemoryTokenStore 根据 token 记录构建内存索引。
func NewInMemoryTokenStore(records []TokenRecord) TokenStore {
	return newInMemoryConnectorTokenStore(records)
}

// MustHashTokenSecretArgon2ID 计算 token secret 的 argon2id 哈希，失败直接 panic。
func MustHashTokenSecretArgon2ID(tokenSecret string) string {
	return mustHashConnectorTokenSecretArgon2ID(tokenSecret)
}

// VerifyTokenSecret 校验 token secret 与 argon2id 哈希是否匹配。
func VerifyTokenSecret(tokenSecret string, encodedHash string) (bool, error) {
	return verifyConnectorTokenSecret(tokenSecret, encodedHash)
}

// Request 表示一次 ConnectorAuth 请求参数。
type Request struct {
	ConnectorID          string
	AssignedSessionEpoch uint64
	AuthMethod           string
	Token                string
}

// Result 表示一次 ConnectorAuth 认证结果。
type Result struct {
	Success      bool
	SessionID    string
	SessionEpoch uint64
	ErrorCode    string
	ErrorMessage string
}

// CoordinatorOptions 定义认证协调器依赖。
type CoordinatorOptions struct {
	SessionRegistry     *registry.SessionRegistry
	TokenStore          TokenStore
	Metrics             *obs.Metrics
	Now                 func() time.Time
	SessionIDGenerator  func() string
	SupersedeRateWindow time.Duration
	SupersedeRateLimit  int
}

// Coordinator 负责执行固定顺序认证并提交 session。
type Coordinator interface {
	AuthenticateAndCommit(
		request Request,
		commit func(now time.Time, sessionRuntime registry.SessionRuntime) error,
	) Result
}

type defaultCoordinator struct {
	inner *connectorAuthCoordinator
}

// NewCoordinator 创建认证协调器。
func NewCoordinator(options CoordinatorOptions) Coordinator {
	return &defaultCoordinator{
		inner: newConnectorAuthCoordinator(connectorAuthCoordinatorOptions{
			sessionRegistry:     options.SessionRegistry,
			tokenStore:          options.TokenStore,
			metrics:             options.Metrics,
			now:                 options.Now,
			sessionIDGenerator:  options.SessionIDGenerator,
			supersedeRateWindow: options.SupersedeRateWindow,
			supersedeRateLimit:  options.SupersedeRateLimit,
		}),
	}
}

// AuthenticateAndCommit 执行认证并在通过后提交会话。
func (coordinator *defaultCoordinator) AuthenticateAndCommit(
	request Request,
	commit func(now time.Time, sessionRuntime registry.SessionRuntime) error,
) Result {
	if coordinator == nil {
		return Result{
			Success:      false,
			SessionID:    "",
			SessionEpoch: 0,
			ErrorCode:    AuthErrorInternal,
			ErrorMessage: "auth coordinator is nil",
		}
	}
	result := coordinator.inner.AuthenticateAndCommit(
		connectorAuthRequest{
			connectorID:          request.ConnectorID,
			assignedSessionEpoch: request.AssignedSessionEpoch,
			authMethod:           request.AuthMethod,
			token:                request.Token,
		},
		commit,
	)
	return Result{
		Success:      result.success,
		SessionID:    result.sessionID,
		SessionEpoch: result.sessionEpoch,
		ErrorCode:    result.errorCode,
		ErrorMessage: result.errorMessage,
	}
}

// HandshakeGuardOptions 定义握手防护器参数。
type HandshakeGuardOptions struct {
	Now func() time.Time

	HelloRateWindow           time.Duration
	HelloRateLimitBySource    int
	HelloRateLimitByConnector int

	AuthFailureWindow           time.Duration
	AuthFailureLimitBySource    int
	AuthFailureLimitByConnector int
	AuthFailureBanDuration      time.Duration

	UnauthenticatedConnectionBudget int
	AuthConcurrencyBudget           int
}

// HandshakeGuard 承担未认证入口限流、失败封禁和预算控制。
type HandshakeGuard interface {
	AllowHello(sourceIP string, connectorID string) (bool, string)
	IsAuthBanned(sourceIP string, connectorID string) (bool, string, time.Time)
	RecordAuthFailure(sourceIP string, connectorID string) (bool, string, time.Time)
	TryAcquireUnauthenticatedConnection() bool
	ReleaseUnauthenticatedConnection()
	TryAcquireAuthConcurrency() bool
	ReleaseAuthConcurrency()
}

// NewHandshakeGuard 创建握手防护器。
func NewHandshakeGuard(options HandshakeGuardOptions) HandshakeGuard {
	return newControlHandshakeGuard(controlHandshakeGuardOptions{
		now:                             options.Now,
		helloRateWindow:                 options.HelloRateWindow,
		helloRateLimitBySource:          options.HelloRateLimitBySource,
		helloRateLimitByConnector:       options.HelloRateLimitByConnector,
		authFailureWindow:               options.AuthFailureWindow,
		authFailureLimitBySource:        options.AuthFailureLimitBySource,
		authFailureLimitByConnector:     options.AuthFailureLimitByConnector,
		authFailureBanDuration:          options.AuthFailureBanDuration,
		unauthenticatedConnectionBudget: options.UnauthenticatedConnectionBudget,
		authConcurrencyBudget:           options.AuthConcurrencyBudget,
	})
}

// ShouldCountForAuthFailureBan 判断错误码是否计入失败封禁统计。
func ShouldCountForAuthFailureBan(errorCode string) bool {
	return shouldCountForAuthFailureBan(errorCode)
}

// NormalizePublicAuthReject 统一外显错误口径，降低枚举区分度。
func NormalizePublicAuthReject(errorCode string, errorMessage string) (string, string) {
	return normalizePublicAuthReject(errorCode, errorMessage)
}

// AuditRecord 表示认证审计日志字段。
type AuditRecord = connectorAuthAuditRecord

// AuditLogger 抽象认证审计输出，便于替换为文件、消息队列或第三方审计平台实现。
type AuditLogger interface {
	EmitAuthAuditLog(success bool, record AuditRecord)
}

type slogAuditLogger struct{}

func (logger slogAuditLogger) EmitAuthAuditLog(success bool, record AuditRecord) {
	emitConnectorAuthAuditLog(success, record)
}

// NewSlogAuditLogger 创建基于 slog 的认证审计输出实现。
func NewSlogAuditLogger() AuditLogger {
	return slogAuditLogger{}
}

// EmitAuthAuditLog 输出认证审计日志。
func EmitAuthAuditLog(success bool, record AuditRecord) {
	NewSlogAuditLogger().EmitAuthAuditLog(success, record)
}

// ExtractTokenIDForAudit 从 token 中提取 token_id（用于脱敏审计）。
func ExtractTokenIDForAudit(rawToken string) string {
	return extractConnectorTokenIDForAudit(rawToken)
}

// NormalizeSourceIP 从远端地址提取稳定 source_ip。
func NormalizeSourceIP(rawPeerAddr string) string {
	return normalizeConnectorAuthSourceIP(rawPeerAddr)
}
