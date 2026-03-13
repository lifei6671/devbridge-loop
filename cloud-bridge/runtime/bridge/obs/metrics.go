package obs

import "sync/atomic"

const (
	// MetricBridgeTrafficOpenAckLateTotal 统计超时后被丢弃的迟到 open_ack 数量。
	MetricBridgeTrafficOpenAckLateTotal = "bridge_traffic_open_ack_late_total"
)

// Metrics holds metric collectors for the bridge runtime.
type Metrics struct {
	bridgeTrafficOpenAckLateTotal atomic.Uint64
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
