package obs

import "sync/atomic"

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
}

// NewMetrics 创建桥接运行时指标容器。
func NewMetrics() *Metrics {
	return &Metrics{}
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
