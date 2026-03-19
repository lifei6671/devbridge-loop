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
	// MetricBridgeScopeFallbackTotal 统计 scope fallback 成功次数。
	MetricBridgeScopeFallbackTotal = "bridge_scope_fallback_total"
	// MetricBridgeHostDeriveTotal 统计 Host 自动派生次数。
	MetricBridgeHostDeriveTotal = "bridge_host_derive_total"
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
	// MetricBridgeServicePublishTotal 统计服务池维度发布次数。
	MetricBridgeServicePublishTotal = "bridge_service_publish_total"
	// MetricBridgeServiceInstancePublishTotal 统计实例维度发布次数。
	MetricBridgeServiceInstancePublishTotal = "bridge_service_instance_publish_total"
	// MetricBridgeServiceAvailableInstances 统计服务池当前可用实例数。
	MetricBridgeServiceAvailableInstances = "bridge_service_available_instances"
	// MetricBridgeServiceInstanceAvailable 统计实例当前是否可用（0/1）。
	MetricBridgeServiceInstanceAvailable = "bridge_service_instance_available"
	// MetricBridgeRouteHitTotal 统计服务池维度路由命中次数。
	MetricBridgeRouteHitTotal = "bridge_route_hit_total"
	// MetricBridgeServiceInstanceRouteHitTotal 统计实例维度路由命中次数。
	MetricBridgeServiceInstanceRouteHitTotal = "bridge_service_instance_route_hit_total"
	// MetricBridgeRouteFailureReasonTotal 统计服务池维度路由失败原因次数。
	MetricBridgeRouteFailureReasonTotal = "bridge_route_failure_reason_total"
	// MetricBridgeServiceInstanceRouteFailureReasonTotal 统计实例维度路由失败原因次数。
	MetricBridgeServiceInstanceRouteFailureReasonTotal = "bridge_service_instance_route_failure_reason_total"
	// MetricBridgeInstanceSelectorPickTotal 统计实例选择次数。
	MetricBridgeInstanceSelectorPickTotal = "bridge_instance_selector_pick_total"
	// MetricBridgeRouteConflictRejectionTotal 统计路由冲突拒绝次数。
	MetricBridgeRouteConflictRejectionTotal = "bridge_route_conflict_rejection_total"
)

// Metrics holds metric collectors for the bridge runtime.
type Metrics struct {
	bridgeTunnelAcquireWaitTotalMs atomic.Int64
	bridgeTunnelAcquireWaitCount   atomic.Uint64

	bridgeTrafficOpenTimeoutTotal atomic.Uint64
	bridgeTrafficOpenRejectTotal  atomic.Uint64

	bridgeTrafficOpenAckLateTotal atomic.Uint64

	bridgeScopeFallbackTotal        atomic.Uint64
	bridgeHostDeriveSuccessTotal    atomic.Uint64
	bridgeHostDeriveFailureTotal    atomic.Uint64
	bridgeActualEndpointOverrideTot atomic.Uint64
	bridgeRouteConflictRejectTotal  atomic.Uint64

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

	serviceDimensionMu                      sync.Mutex
	servicePublishTotals                    map[string]uint64
	serviceInstancePublishTotals            map[string]map[string]uint64
	serviceAvailableInstances               map[string]map[string]struct{}
	serviceRouteHitTotals                   map[string]uint64
	serviceInstanceRouteHitTotals           map[string]map[string]uint64
	serviceRouteFailureReasonTotals         map[string]map[string]uint64
	serviceInstanceRouteFailureReasonTotals map[string]map[string]map[string]uint64
	instanceSelectorPickTotals              map[string]map[string]uint64
}

// NewMetrics 创建桥接运行时指标容器。
func NewMetrics() *Metrics {
	return &Metrics{
		authErrorCodeTotals:                     make(map[string]uint64),
		recycleErrorCodeTotals:                  make(map[string]uint64),
		servicePublishTotals:                    make(map[string]uint64),
		serviceInstancePublishTotals:            make(map[string]map[string]uint64),
		serviceAvailableInstances:               make(map[string]map[string]struct{}),
		serviceRouteHitTotals:                   make(map[string]uint64),
		serviceInstanceRouteHitTotals:           make(map[string]map[string]uint64),
		serviceRouteFailureReasonTotals:         make(map[string]map[string]uint64),
		serviceInstanceRouteFailureReasonTotals: make(map[string]map[string]map[string]uint64),
		instanceSelectorPickTotals:              make(map[string]map[string]uint64),
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

// IncBridgeScopeFallbackTotal 增加一次 scope fallback 成功计数。
func (metrics *Metrics) IncBridgeScopeFallbackTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeScopeFallbackTotal.Add(1)
}

// BridgeScopeFallbackTotal 返回 scope fallback 成功总次数。
func (metrics *Metrics) BridgeScopeFallbackTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeScopeFallbackTotal.Load()
}

// ObserveBridgeHostDerive 记录一次 Host 自动派生结果。
func (metrics *Metrics) ObserveBridgeHostDerive(success bool) {
	if metrics == nil {
		return
	}
	if success {
		metrics.bridgeHostDeriveSuccessTotal.Add(1)
		return
	}
	metrics.bridgeHostDeriveFailureTotal.Add(1)
}

// BridgeHostDeriveTotal 返回 Host 自动派生累计值。
func (metrics *Metrics) BridgeHostDeriveTotal(success bool) uint64 {
	if metrics == nil {
		return 0
	}
	if success {
		return metrics.bridgeHostDeriveSuccessTotal.Load()
	}
	return metrics.bridgeHostDeriveFailureTotal.Load()
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

// IncBridgeRouteConflictRejectionTotal 增加一次路由冲突拒绝计数。
func (metrics *Metrics) IncBridgeRouteConflictRejectionTotal() {
	if metrics == nil {
		return
	}
	metrics.bridgeRouteConflictRejectTotal.Add(1)
}

// BridgeRouteConflictRejectionTotal 返回路由冲突拒绝总次数。
func (metrics *Metrics) BridgeRouteConflictRejectionTotal() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.bridgeRouteConflictRejectTotal.Load()
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

const (
	// defaultBridgeRouteFailureReason 作为空失败原因的统一兜底值。
	defaultBridgeRouteFailureReason = "unknown"
)

// ObserveBridgeServicePublish 记录一次发布事件的服务池/实例维度计数。
func (metrics *Metrics) ObserveBridgeServicePublish(serviceID string, serviceInstanceID string) {
	if metrics == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	metrics.servicePublishTotals[normalizedServiceID]++
	if normalizedServiceInstanceID == "" {
		return
	}
	if metrics.serviceInstancePublishTotals[normalizedServiceID] == nil {
		metrics.serviceInstancePublishTotals[normalizedServiceID] = make(map[string]uint64)
	}
	metrics.serviceInstancePublishTotals[normalizedServiceID][normalizedServiceInstanceID]++
}

// BridgeServicePublishTotal 返回指定服务池发布累计值。
func (metrics *Metrics) BridgeServicePublishTotal(serviceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	return metrics.servicePublishTotals[normalizedServiceID]
}

// BridgeServiceInstancePublishTotal 返回指定实例发布累计值。
func (metrics *Metrics) BridgeServiceInstancePublishTotal(serviceID string, serviceInstanceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if normalizedServiceID == "" || normalizedServiceInstanceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	instanceTotals, exists := metrics.serviceInstancePublishTotals[normalizedServiceID]
	if !exists {
		return 0
	}
	return instanceTotals[normalizedServiceInstanceID]
}

// SetBridgeServiceAvailableInstances 用服务池当前可用实例快照覆盖可用性指标。
func (metrics *Metrics) SetBridgeServiceAvailableInstances(serviceID string, serviceInstanceIDs []string) {
	if metrics == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	normalizedAvailableInstances := make(map[string]struct{}, len(serviceInstanceIDs))
	for _, serviceInstanceID := range serviceInstanceIDs {
		normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
		if normalizedServiceInstanceID == "" {
			continue
		}
		// 去重后再写入，避免重复实例导致可用数膨胀。
		normalizedAvailableInstances[normalizedServiceInstanceID] = struct{}{}
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	if len(normalizedAvailableInstances) == 0 {
		// 当前服务池无可用实例时直接清空快照。
		delete(metrics.serviceAvailableInstances, normalizedServiceID)
		return
	}
	metrics.serviceAvailableInstances[normalizedServiceID] = normalizedAvailableInstances
}

// BridgeServiceAvailableInstanceTotal 返回服务池当前可用实例数。
func (metrics *Metrics) BridgeServiceAvailableInstanceTotal(serviceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	return uint64(len(metrics.serviceAvailableInstances[normalizedServiceID]))
}

// BridgeServiceInstanceAvailableTotal 返回实例当前可用状态（可用=1，不可用=0）。
func (metrics *Metrics) BridgeServiceInstanceAvailableTotal(serviceID string, serviceInstanceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if normalizedServiceID == "" || normalizedServiceInstanceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	instances := metrics.serviceAvailableInstances[normalizedServiceID]
	if len(instances) == 0 {
		return 0
	}
	if _, exists := instances[normalizedServiceInstanceID]; exists {
		return 1
	}
	return 0
}

// ObserveBridgeRouteHit 记录一次路由命中事件的服务池/实例维度计数。
func (metrics *Metrics) ObserveBridgeRouteHit(serviceID string, serviceInstanceID string) {
	if metrics == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	metrics.serviceRouteHitTotals[normalizedServiceID]++
	if normalizedServiceInstanceID == "" {
		return
	}
	if metrics.serviceInstanceRouteHitTotals[normalizedServiceID] == nil {
		metrics.serviceInstanceRouteHitTotals[normalizedServiceID] = make(map[string]uint64)
	}
	metrics.serviceInstanceRouteHitTotals[normalizedServiceID][normalizedServiceInstanceID]++
}

// BridgeServiceRouteHitTotal 返回指定服务池路由命中累计值。
func (metrics *Metrics) BridgeServiceRouteHitTotal(serviceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	return metrics.serviceRouteHitTotals[normalizedServiceID]
}

// BridgeServiceInstanceRouteHitTotal 返回指定实例路由命中累计值。
func (metrics *Metrics) BridgeServiceInstanceRouteHitTotal(serviceID string, serviceInstanceID string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if normalizedServiceID == "" || normalizedServiceInstanceID == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	instanceTotals, exists := metrics.serviceInstanceRouteHitTotals[normalizedServiceID]
	if !exists {
		return 0
	}
	return instanceTotals[normalizedServiceInstanceID]
}

// ObserveBridgeInstanceSelectorPick 记录一次实例选择命中。
func (metrics *Metrics) ObserveBridgeInstanceSelectorPick(serviceInstanceID string, policy string) {
	if metrics == nil {
		return
	}
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	normalizedPolicy := strings.TrimSpace(policy)
	if normalizedServiceInstanceID == "" || normalizedPolicy == "" {
		return
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	if metrics.instanceSelectorPickTotals[normalizedServiceInstanceID] == nil {
		metrics.instanceSelectorPickTotals[normalizedServiceInstanceID] = make(map[string]uint64)
	}
	metrics.instanceSelectorPickTotals[normalizedServiceInstanceID][normalizedPolicy]++
}

// BridgeInstanceSelectorPickTotal 返回指定实例+策略的选择次数。
func (metrics *Metrics) BridgeInstanceSelectorPickTotal(serviceInstanceID string, policy string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	normalizedPolicy := strings.TrimSpace(policy)
	if normalizedServiceInstanceID == "" || normalizedPolicy == "" {
		return 0
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	return metrics.instanceSelectorPickTotals[normalizedServiceInstanceID][normalizedPolicy]
}

// ObserveBridgeRouteFailureReason 记录一次路由失败原因的服务池/实例维度计数。
func (metrics *Metrics) ObserveBridgeRouteFailureReason(serviceID string, serviceInstanceID string, reason string) {
	if metrics == nil {
		return
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return
	}
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = defaultBridgeRouteFailureReason
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	if metrics.serviceRouteFailureReasonTotals[normalizedServiceID] == nil {
		metrics.serviceRouteFailureReasonTotals[normalizedServiceID] = make(map[string]uint64)
	}
	metrics.serviceRouteFailureReasonTotals[normalizedServiceID][normalizedReason]++
	if normalizedServiceInstanceID == "" {
		return
	}
	if metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID] == nil {
		metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID] = make(map[string]map[string]uint64)
	}
	if metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID][normalizedServiceInstanceID] == nil {
		metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID][normalizedServiceInstanceID] = make(map[string]uint64)
	}
	metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID][normalizedServiceInstanceID][normalizedReason]++
}

// BridgeServiceRouteFailureReasonTotal 返回服务池在指定失败原因上的累计值。
func (metrics *Metrics) BridgeServiceRouteFailureReasonTotal(serviceID string, reason string) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	if normalizedServiceID == "" {
		return 0
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = defaultBridgeRouteFailureReason
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	reasonTotals := metrics.serviceRouteFailureReasonTotals[normalizedServiceID]
	return reasonTotals[normalizedReason]
}

// BridgeServiceInstanceRouteFailureReasonTotal 返回实例在指定失败原因上的累计值。
func (metrics *Metrics) BridgeServiceInstanceRouteFailureReasonTotal(
	serviceID string,
	serviceInstanceID string,
	reason string,
) uint64 {
	if metrics == nil {
		return 0
	}
	normalizedServiceID := strings.TrimSpace(serviceID)
	normalizedServiceInstanceID := strings.TrimSpace(serviceInstanceID)
	if normalizedServiceID == "" || normalizedServiceInstanceID == "" {
		return 0
	}
	normalizedReason := strings.TrimSpace(reason)
	if normalizedReason == "" {
		normalizedReason = defaultBridgeRouteFailureReason
	}
	metrics.serviceDimensionMu.Lock()
	defer metrics.serviceDimensionMu.Unlock()
	instanceReasonTotals := metrics.serviceInstanceRouteFailureReasonTotals[normalizedServiceID]
	if len(instanceReasonTotals) == 0 {
		return 0
	}
	return instanceReasonTotals[normalizedServiceInstanceID][normalizedReason]
}
