package obs

import (
	"strings"
	"sync"
	"sync/atomic"

	ltfperrors "github.com/lifei6671/devbridge-loop/ltfp/errors"
)

const (
	// MetricBridgeTunnelAcquireWaitMs 统计 tunnel acquire 等待时延。
	MetricBridgeTunnelAcquireWaitMs = "bridge_tunnel_acquire_wait_ms"
	// MetricBridgeTrafficOpenTimeoutTotal 统计 open_ack timeout 次数。
	MetricBridgeTrafficOpenTimeoutTotal = "bridge_traffic_open_timeout_total"
	// MetricBridgeTrafficOpenRejectTotal 统计 open_ack reject 次数。
	MetricBridgeTrafficOpenRejectTotal = "bridge_traffic_open_reject_total"
	// MetricBridgeTrafficOpenAckLateTotal 统计超时后被丢弃的迟到 open_ack 数量。
	MetricBridgeTrafficOpenAckLateTotal = "bridge_traffic_open_ack_late_total"
	// MetricBridgeHybridFallbackTotal 统计 hybrid fallback 成功次数。
	MetricBridgeHybridFallbackTotal = "bridge_hybrid_fallback_total"
	// MetricBridgeActualEndpointOverrideTotal 统计实际 endpoint 覆盖次数。
	MetricBridgeActualEndpointOverrideTotal = "bridge_actual_endpoint_override_total"
	// MetricBridgeAuthSuccessTotal 统计认证成功次数。
	MetricBridgeAuthSuccessTotal = "bridge_auth_success_total"
	// MetricBridgeAuthFailureTotal 统计认证失败次数。
	MetricBridgeAuthFailureTotal = "bridge_auth_failure_total"
	// MetricBridgeAuthRateLimitTotal 统计认证抢占限流次数。
	MetricBridgeAuthRateLimitTotal = "bridge_auth_rate_limit_total"
	// MetricBridgeAuthSupersedeTotal 统计认证成功接管次数。
	MetricBridgeAuthSupersedeTotal = "bridge_auth_supersede_total"
	// MetricBridgeTLSRejectPlaintextOnRequiredTotal 统计 required 模式拒绝明文的次数。
	MetricBridgeTLSRejectPlaintextOnRequiredTotal = "bridge_tls_reject_plaintext_on_required_total"
	// MetricBridgeTLSRejectTLSOnPlaintextTotal 统计 plaintext 模式拒绝 TLS 的次数。
	MetricBridgeTLSRejectTLSOnPlaintextTotal = "bridge_tls_reject_tls_on_plaintext_total"
	// MetricBridgeTunnelRecycleFailureTotal 统计 tunnel recycle 失败次数。
	MetricBridgeTunnelRecycleFailureTotal = "bridge_tunnel_recycle_failure_total"
)

// Metrics holds metric collectors for the bridge runtime.
type Metrics struct {
	bridgeTunnelAcquireWaitTotalMs atomic.Int64
	bridgeTunnelAcquireWaitCount   atomic.Uint64

	bridgeTrafficOpenTimeoutTotal atomic.Uint64
	bridgeTrafficOpenRejectTotal  atomic.Uint64

	bridgeTrafficOpenAckLateTotal atomic.Uint64

	bridgeHybridFallbackTotal       atomic.Uint64
	bridgeActualEndpointOverrideTot atomic.Uint64

	bridgeAuthSuccessTotal atomic.Uint64
	bridgeAuthFailureTotal atomic.Uint64
	bridgeAuthRateLimitTot atomic.Uint64
	bridgeAuthSupersedeTot atomic.Uint64

	bridgeTLSRejectPlaintextOnRequiredTot atomic.Uint64
	bridgeTLSRejectTLSOnPlaintextTot      atomic.Uint64
	bridgeTunnelRecycleFailureTot         atomic.Uint64

	authErrorCodeMu     sync.Mutex
	authErrorCodeTotals map[string]uint64

	recycleErrorCodeMu     sync.Mutex
	recycleErrorCodeTotals map[string]uint64
}

// NewMetrics 创建桥接运行时指标容器。
func NewMetrics() *Metrics {
	return &Metrics{
		authErrorCodeTotals:    make(map[string]uint64),
		recycleErrorCodeTotals: make(map[string]uint64),
	}
}

// DefaultMetrics 提供运行时默认指标容器。
var DefaultMetrics = NewMetrics()

// IncBridgeTrafficOpenAckLateTotal 增加一次迟到 open_ack 计数。
func (metrics *Metrics) IncBridgeTrafficOpenAckLateTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeTrafficOpenAckLateTotal.Add(1)
}

// BridgeTrafficOpenAckLateTotal 返回迟到 open_ack 当前累计值。
func (metrics *Metrics) BridgeTrafficOpenAckLateTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTrafficOpenAckLateTotal.Load()
}

// ObserveBridgeTunnelAcquireWait 记录一次 acquire idle tunnel 的等待时延。
func (metrics *Metrics) ObserveBridgeTunnelAcquireWait(waitMs int64) {
	if metrics == nil {
		return
	}
	if waitMs < 0 {
		waitMs = 0
	}
	metrics.bridgeTunnelAcquireWaitTotalMs.Add(waitMs)
	metrics.bridgeTunnelAcquireWaitCount.Add(1)
}

// BridgeTunnelAcquireWaitTotalMs 返回 acquire 等待总毫秒数。
func (metrics *Metrics) BridgeTunnelAcquireWaitTotalMs() int64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTunnelAcquireWaitTotalMs.Load()
}

// BridgeTunnelAcquireWaitCount 返回 acquire 等待样本数。
func (metrics *Metrics) BridgeTunnelAcquireWaitCount() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTunnelAcquireWaitCount.Load()
}

// IncBridgeTrafficOpenTimeoutTotal 增加一次 open_ack timeout 计数。
func (metrics *Metrics) IncBridgeTrafficOpenTimeoutTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeTrafficOpenTimeoutTotal.Add(1)
}

// BridgeTrafficOpenTimeoutTotal 返回 open_ack timeout 总次数。
func (metrics *Metrics) BridgeTrafficOpenTimeoutTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTrafficOpenTimeoutTotal.Load()
}

// IncBridgeTrafficOpenRejectTotal 增加一次 open_ack reject 计数。
func (metrics *Metrics) IncBridgeTrafficOpenRejectTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeTrafficOpenRejectTotal.Add(1)
}

// BridgeTrafficOpenRejectTotal 返回 open_ack reject 总次数。
func (metrics *Metrics) BridgeTrafficOpenRejectTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTrafficOpenRejectTotal.Load()
}

// IncBridgeHybridFallbackTotal 增加一次 hybrid fallback 成功计数。
func (metrics *Metrics) IncBridgeHybridFallbackTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeHybridFallbackTotal.Add(1)
}

// BridgeHybridFallbackTotal 返回 hybrid fallback 成功总次数。
func (metrics *Metrics) BridgeHybridFallbackTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeHybridFallbackTotal.Load()
}

// IncBridgeActualEndpointOverrideTotal 增加一次 endpoint 覆盖计数。
func (metrics *Metrics) IncBridgeActualEndpointOverrideTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeActualEndpointOverrideTot.Add(1)
}

// BridgeActualEndpointOverrideTotal 返回 endpoint 覆盖总次数。
func (metrics *Metrics) BridgeActualEndpointOverrideTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeActualEndpointOverrideTot.Load()
}

// IncBridgeAuthSuccessTotal 增加一次认证成功计数。
func (metrics *Metrics) IncBridgeAuthSuccessTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeAuthSuccessTotal.Add(1)
}

// BridgeAuthSuccessTotal 返回认证成功累计值。
func (metrics *Metrics) BridgeAuthSuccessTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeAuthSuccessTotal.Load()
}

// ObserveBridgeAuthFailure 记录一次认证失败及错误码分布。
func (metrics *Metrics) ObserveBridgeAuthFailure(errorCode string) {
	if metrics == nil {
		return
	}
	metrics.bridgeAuthFailureTotal.Add(1)
	normalizedErrorCode := strings.TrimSpace(errorCode)
	if normalizedErrorCode == "" {
		return
	}
	metrics.authErrorCodeMu.Lock()
	defer metrics.authErrorCodeMu.Unlock()
	metrics.authErrorCodeTotals[normalizedErrorCode]++
}

// BridgeAuthFailureTotal 返回认证失败累计值。
func (metrics *Metrics) BridgeAuthFailureTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeAuthFailureTotal.Load()
}

// IncBridgeAuthRateLimitTotal 增加一次认证限流计数。
func (metrics *Metrics) IncBridgeAuthRateLimitTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeAuthRateLimitTot.Add(1)
}

// BridgeAuthRateLimitTotal 返回认证限流累计值。
func (metrics *Metrics) BridgeAuthRateLimitTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeAuthRateLimitTot.Load()
}

// IncBridgeAuthSupersedeTotal 增加一次认证成功接管计数。
func (metrics *Metrics) IncBridgeAuthSupersedeTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeAuthSupersedeTot.Add(1)
}

// BridgeAuthSupersedeTotal 返回认证成功接管累计值。
func (metrics *Metrics) BridgeAuthSupersedeTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeAuthSupersedeTot.Load()
}

// IncBridgeTLSRejectPlaintextOnRequiredTotal 增加一次 required 模式拒绝明文计数。
func (metrics *Metrics) IncBridgeTLSRejectPlaintextOnRequiredTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeTLSRejectPlaintextOnRequiredTot.Add(1)
}

// BridgeTLSRejectPlaintextOnRequiredTotal 返回 required 模式拒绝明文累计值。
func (metrics *Metrics) BridgeTLSRejectPlaintextOnRequiredTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTLSRejectPlaintextOnRequiredTot.Load()
}

// IncBridgeTLSRejectTLSOnPlaintextTotal 增加一次 plaintext 模式拒绝 TLS 计数。
func (metrics *Metrics) IncBridgeTLSRejectTLSOnPlaintextTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeTLSRejectTLSOnPlaintextTot.Add(1)
}

// BridgeTLSRejectTLSOnPlaintextTotal 返回 plaintext 模式拒绝 TLS 累计值。
func (metrics *Metrics) BridgeTLSRejectTLSOnPlaintextTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTLSRejectTLSOnPlaintextTot.Load()
}

// ObserveBridgeTunnelRecycleFailure 记录一次 recycle 失败及错误码分布。
func (metrics *Metrics) ObserveBridgeTunnelRecycleFailure(errorCode string) {
	if metrics == nil {
		return
	}
	metrics.bridgeTunnelRecycleFailureTot.Add(1)
	normalizedErrorCode := ltfperrors.NormalizeTunnelRecycleCodeOrDefault(
		errorCode,
		ltfperrors.CodeTunnelRecycleTunnelUnhealthy,
	)
	metrics.recycleErrorCodeMu.Lock()
	defer metrics.recycleErrorCodeMu.Unlock()
	metrics.recycleErrorCodeTotals[normalizedErrorCode]++
}

// BridgeTunnelRecycleFailureTotal 返回 tunnel recycle 失败累计值。
func (metrics *Metrics) BridgeTunnelRecycleFailureTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeTunnelRecycleFailureTot.Load()
}

// BridgeTunnelRecycleErrorCodeTotal 返回指定 recycle 错误码的累计值。
func (metrics *Metrics) BridgeTunnelRecycleErrorCodeTotal(errorCode string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedErrorCode := ltfperrors.NormalizeTunnelRecycleCodeOrDefault(
		errorCode,
		ltfperrors.CodeTunnelRecycleTunnelUnhealthy,
	)
	metrics.recycleErrorCodeMu.Lock()
	defer metrics.recycleErrorCodeMu.Unlock()
	return metrics.recycleErrorCodeTotals[normalizedErrorCode]
}

// BridgeTunnelRecycleErrorCodeTotals 返回 recycle 错误码累计分布快照。
func (metrics *Metrics) BridgeTunnelRecycleErrorCodeTotals() map[string]uint64 {
	if metrics == nil {
		return map[string]uint64{}
	}
	metrics.recycleErrorCodeMu.Lock()
	defer metrics.recycleErrorCodeMu.Unlock()
	cloned := make(map[string]uint64, len(metrics.recycleErrorCodeTotals))
	for errorCode, total := range metrics.recycleErrorCodeTotals {
		cloned[errorCode] = total
	}
	return cloned
}

// BridgeAuthErrorCodeTotal 返回指定认证错误码的累计值。
func (metrics *Metrics) BridgeAuthErrorCodeTotal(errorCode string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedErrorCode := strings.TrimSpace(errorCode)
	if normalizedErrorCode == "" {
		return 0
	}
	metrics.authErrorCodeMu.Lock()
	defer metrics.authErrorCodeMu.Unlock()
	return metrics.authErrorCodeTotals[normalizedErrorCode]
}

// BridgeAuthErrorCodeTotals 返回认证错误码累计分布快照。
func (metrics *Metrics) BridgeAuthErrorCodeTotals() map[string]uint64 {
	if metrics == nil {
		return map[string]uint64{}
	}
	metrics.authErrorCodeMu.Lock()
	defer metrics.authErrorCodeMu.Unlock()
	cloned := make(map[string]uint64, len(metrics.authErrorCodeTotals))
	for errorCode, total := range metrics.authErrorCodeTotals {
		cloned[errorCode] = total
	}
	return cloned
}
