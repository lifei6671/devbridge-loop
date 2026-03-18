package tls

import (
	"context"
	stdtls "crypto/tls"
	"net"
	"time"

	"github.com/lifei6671/devbridge-loop/cloud-bridge/runtime/bridge/obs"
)

const (
	// ReloadRetryInterval 定义证书刷新失败后的最短重试间隔。
	ReloadRetryInterval = controlPlaneTLSReloadRetryInterval
)

var (
	// ErrTLSRejected 表示连接被 tls_mode 主动拒绝。
	ErrTLSRejected = errControlPlaneTLSRejected
	// ErrTLSRejectPlaintextOnRequired 表示 required 模式拒绝明文连接。
	ErrTLSRejectPlaintextOnRequired = errControlPlaneTLSRejectPlaintextOnRequired
	// ErrTLSRejectTLSOnPlaintext 表示 plaintext 模式拒绝 TLS 连接。
	ErrTLSRejectTLSOnPlaintext = errControlPlaneTLSRejectTLSOnPlaintext
)

// Mode 表示控制面 TLS 接入模式。
type Mode = controlPlaneTLSMode

const (
	// ModeRequired 表示仅允许 TLS。
	ModeRequired Mode = controlPlaneTLSModeRequired
	// ModeOptional 表示同时允许 TLS/明文。
	ModeOptional Mode = controlPlaneTLSModeOptional
	// ModePlaintext 表示仅允许明文。
	ModePlaintext Mode = controlPlaneTLSModePlaintext
)

// NormalizeMode 归一化并校验 TLS 模式。
func NormalizeMode(rawMode string) (Mode, error) {
	return normalizeControlPlaneTLSMode(rawMode)
}

// CertSource 表示控制面证书来源模式。
type CertSource = controlPlaneTLSCertSource

const (
	// CertSourceExternal 表示外部证书模式。
	CertSourceExternal CertSource = controlPlaneTLSCertSourceExternal
	// CertSourceManagedCA 表示自建 CA 模式。
	CertSourceManagedCA CertSource = controlPlaneTLSCertSourceManagedCA
)

// NormalizeCertSource 归一化并校验证书来源模式。
func NormalizeCertSource(rawSource string) (CertSource, error) {
	return normalizeControlPlaneTLSCertSource(rawSource)
}

// CertificateProvider 抽象控制面证书加载来源。
type CertificateProvider interface {
	LoadServerTLSConfig(ctx context.Context) (*stdtls.Config, error)
}

// CertificateProviderConfig 定义证书 provider 的配置输入。
type CertificateProviderConfig = controlPlaneTLSCertificateProviderConfig

// ManagedCAServerCertificateRequest 定义 managed_ca 签发请求。
type ManagedCAServerCertificateRequest = managedCAServerCertificateRequest

// ManagedCACertificateIssuer 抽象 managed_ca 证书签发能力。
type ManagedCACertificateIssuer interface {
	IssueServerCertificate(ctx context.Context, request ManagedCAServerCertificateRequest) (stdtls.Certificate, error)
}

// CertificateProviderOptions 定义证书 provider 可选依赖。
type CertificateProviderOptions struct {
	ManagedCAIssuer ManagedCACertificateIssuer
}

// NewCertificateProvider 根据配置创建证书 provider。
func NewCertificateProvider(
	config CertificateProviderConfig,
	options CertificateProviderOptions,
) (CertificateProvider, error) {
	return newControlPlaneTLSCertificateProvider(
		config,
		controlPlaneTLSCertificateProviderOptions{managedCAIssuer: options.ManagedCAIssuer},
	)
}

// NewLocalManagedCACertificateIssuer 创建本地自建 CA 签发器。
func NewLocalManagedCACertificateIssuer() ManagedCACertificateIssuer {
	return newLocalManagedCACertificateIssuer()
}

// ConfigManager 管理控制面 TLS 配置加载与热更新。
type ConfigManager interface {
	Refresh(ctx context.Context) error
	CurrentServerTLSConfig() *stdtls.Config
	CurrentServerCertNotAfter() time.Time
	NextReloadInterval() time.Duration
}

// NewConfigManager 创建 TLS 配置管理器。
func NewConfigManager(provider CertificateProvider, certSource CertSource) (ConfigManager, error) {
	return newControlPlaneTLSConfigManager(provider, certSource)
}

// AcceptConnWithTLS 按 tls_mode 判定并接入入站连接。
func AcceptConnWithTLS(
	rawConn net.Conn,
	tlsMode Mode,
	serverTLSConfig *stdtls.Config,
	metrics *obs.Metrics,
) (net.Conn, bool, error) {
	return acceptControlPlaneConnWithTLS(rawConn, tlsMode, serverTLSConfig, metrics)
}

// NewTLSAwareListener 创建具备 tls_mode 判定能力的监听器包装。
func NewTLSAwareListener(
	listener net.Listener,
	tlsMode Mode,
	serverTLSConfigGetter func() *stdtls.Config,
	metrics *obs.Metrics,
) net.Listener {
	return newControlPlaneTLSAwareListener(listener, tlsMode, serverTLSConfigGetter, metrics)
}

// RemoteAddrString 返回连接远端地址字符串。
func RemoteAddrString(rawConn net.Conn) string {
	return remoteAddrString(rawConn)
}

// RefreshOnce 提供与 context 结合的一次刷新入口，便于外部编排调用。
func RefreshOnce(ctx context.Context, manager ConfigManager) error {
	if manager == nil {
		return nil
	}
	return manager.Refresh(ctx)
}

// NextReloadInterval 返回证书下一次刷新间隔。
func NextReloadInterval(manager ConfigManager) time.Duration {
	if manager == nil {
		return ReloadRetryInterval
	}
	return manager.NextReloadInterval()
}
