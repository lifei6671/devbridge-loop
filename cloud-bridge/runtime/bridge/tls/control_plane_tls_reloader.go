package tls

import (
	"context"
	"crypto/tls"
	"errors"
	"sync"
	"time"
)

const (
	// controlPlaneTLSReloadRetryInterval 定义证书刷新失败后的最短重试间隔。
	controlPlaneTLSReloadRetryInterval = 5 * time.Second
	// controlPlaneTLSExternalReloadInterval 定义 external 模式轮询外部证书文件的周期。
	controlPlaneTLSExternalReloadInterval = 15 * time.Second
	// controlPlaneTLSManagedCAReloadInterval 定义 managed_ca 模式检查续签窗口的周期。
	controlPlaneTLSManagedCAReloadInterval = 1 * time.Minute
	// controlPlaneTLSManagedCAMinReloadInterval 定义 managed_ca 短 TTL 场景下的最小刷新间隔。
	controlPlaneTLSManagedCAMinReloadInterval = 200 * time.Millisecond
)

// controlPlaneTLSConfigManager 管理控制面 TLS 配置的加载与热更新。
type controlPlaneTLSConfigManager struct {
	provider   controlPlaneTLSCertificateProvider
	certSource controlPlaneTLSCertSource

	mutex               sync.RWMutex
	serverTLSConfig     *tls.Config
	serverCertNotAfter  time.Time
	lastSuccessfulLoad  time.Time
	lastLoadFailedAt    time.Time
	lastLoadFailureText string
}

// newControlPlaneTLSConfigManager 创建控制面 TLS 配置管理器。
func newControlPlaneTLSConfigManager(
	provider controlPlaneTLSCertificateProvider,
	certSource controlPlaneTLSCertSource,
) (*controlPlaneTLSConfigManager, error) {
	if provider == nil {
		return nil, errors.New("new control plane tls config manager: nil provider")
	}
	return &controlPlaneTLSConfigManager{
		provider:   provider,
		certSource: certSource,
	}, nil
}

// Refresh 触发一次证书加载，并在成功后原子替换当前 TLS 配置。
func (manager *controlPlaneTLSConfigManager) Refresh(ctx context.Context) error {
	if manager == nil || manager.provider == nil {
		return errors.New("refresh control plane tls config: nil manager")
	}
	loadedTLSConfig, err := manager.provider.LoadServerTLSConfig(ctx)
	if err != nil {
		manager.mutex.Lock()
		manager.lastLoadFailedAt = time.Now().UTC()
		manager.lastLoadFailureText = err.Error()
		manager.mutex.Unlock()
		return err
	}
	if loadedTLSConfig == nil {
		return errors.New("refresh control plane tls config: provider returned nil tls config")
	}
	certificateNotAfter, resolveErr := resolveTLSConfigCertificateNotAfter(loadedTLSConfig)
	if resolveErr != nil {
		return resolveErr
	}
	manager.mutex.Lock()
	manager.serverTLSConfig = loadedTLSConfig
	manager.serverCertNotAfter = certificateNotAfter
	manager.lastSuccessfulLoad = time.Now().UTC()
	manager.lastLoadFailedAt = time.Time{}
	manager.lastLoadFailureText = ""
	manager.mutex.Unlock()
	return nil
}

// CurrentServerTLSConfig 返回当前可用的服务端 TLS 配置快照。
func (manager *controlPlaneTLSConfigManager) CurrentServerTLSConfig() *tls.Config {
	if manager == nil {
		return nil
	}
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	return manager.serverTLSConfig
}

// CurrentServerCertNotAfter 返回当前生效服务端证书到期时间，供观测日志使用。
func (manager *controlPlaneTLSConfigManager) CurrentServerCertNotAfter() time.Time {
	if manager == nil {
		return time.Time{}
	}
	manager.mutex.RLock()
	defer manager.mutex.RUnlock()
	return manager.serverCertNotAfter
}

// NextReloadInterval 计算下一次刷新等待时间。
func (manager *controlPlaneTLSConfigManager) NextReloadInterval() time.Duration {
	if manager == nil {
		return controlPlaneTLSReloadRetryInterval
	}
	baseInterval := controlPlaneTLSReloadRetryInterval
	switch manager.certSource {
	case controlPlaneTLSCertSourceExternal:
		baseInterval = controlPlaneTLSExternalReloadInterval
	case controlPlaneTLSCertSourceManagedCA:
		baseInterval = controlPlaneTLSManagedCAReloadInterval
	}
	if manager.certSource != controlPlaneTLSCertSourceManagedCA {
		return baseInterval
	}
	manager.mutex.RLock()
	currentCertNotAfter := manager.serverCertNotAfter
	manager.mutex.RUnlock()
	if currentCertNotAfter.IsZero() {
		return baseInterval
	}
	remainingValidity := time.Until(currentCertNotAfter)
	if remainingValidity <= 0 {
		// 证书已过期时尽快重试刷新，缩短不可用窗口。
		return controlPlaneTLSReloadRetryInterval
	}
	ttlDrivenInterval := remainingValidity / 4
	if ttlDrivenInterval < controlPlaneTLSManagedCAMinReloadInterval {
		ttlDrivenInterval = controlPlaneTLSManagedCAMinReloadInterval
	}
	if ttlDrivenInterval < baseInterval {
		return ttlDrivenInterval
	}
	return baseInterval
}

// resolveTLSConfigCertificateNotAfter 读取 tls.Config 当前 leaf 证书的到期时间。
func resolveTLSConfigCertificateNotAfter(serverTLSConfig *tls.Config) (time.Time, error) {
	if serverTLSConfig == nil {
		return time.Time{}, errors.New("resolve tls config certificate not_after: nil tls config")
	}
	if len(serverTLSConfig.Certificates) == 0 {
		return time.Time{}, errors.New("resolve tls config certificate not_after: empty certificates")
	}
	return resolveTLSCertificateNotAfter(serverTLSConfig.Certificates[0])
}
