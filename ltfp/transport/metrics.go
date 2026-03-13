package transport

import (
	"sync"
	"sync/atomic"
	"time"
)

// TransportMetricsSnapshot 描述 transport 关键指标快照。
type TransportMetricsSnapshot struct {
	HeartbeatRTT      time.Duration
	IdleCount         int
	InUseCount        int
	RefillRatePerSec  float64
	RefillOpenedTotal uint64
	OpenTimeoutCount  uint64
	ResetCount        uint64
	BrokenCount       uint64
}

// Fields 返回可直接用于结构化日志/指标导出的字段映射。
func (snapshot TransportMetricsSnapshot) Fields() map[string]any {
	return map[string]any{
		"heartbeat_rtt_ms":      snapshot.HeartbeatRTT.Milliseconds(),
		"idle_count":            snapshot.IdleCount,
		"in_use_count":          snapshot.InUseCount,
		"refill_rate_per_sec":   snapshot.RefillRatePerSec,
		"refill_opened_total":   snapshot.RefillOpenedTotal,
		"open_timeout_count":    snapshot.OpenTimeoutCount,
		"reset_count":           snapshot.ResetCount,
		"broken_tunnel_count":   snapshot.BrokenCount,
	}
}

// TransportMetricsRecorder 维护 transport 关键指标的并发安全聚合。
type TransportMetricsRecorder struct {
	heartbeatRTTNanos int64
	idleCount         int64
	inUseCount        int64

	openTimeoutCount uint64
	resetCount       uint64
	brokenCount      uint64

	refillMutex       sync.Mutex
	refillFirstAt     time.Time
	refillLastAt      time.Time
	refillOpenedTotal uint64
}

// NewTransportMetricsRecorder 创建默认指标记录器。
func NewTransportMetricsRecorder() *TransportMetricsRecorder {
	return &TransportMetricsRecorder{}
}

// ObserveHeartbeatRTT 记录最近一次 heartbeat RTT。
func (recorder *TransportMetricsRecorder) ObserveHeartbeatRTT(roundTripTime time.Duration) {
	if recorder == nil {
		return
	}
	if roundTripTime < 0 {
		roundTripTime = 0
	}
	atomic.StoreInt64(&recorder.heartbeatRTTNanos, int64(roundTripTime))
}

// ObservePoolCounts 记录当前 pool 的 idle/in-use 数量。
func (recorder *TransportMetricsRecorder) ObservePoolCounts(idleCount int, inUseCount int) {
	if recorder == nil {
		return
	}
	if idleCount < 0 {
		idleCount = 0
	}
	if inUseCount < 0 {
		inUseCount = 0
	}
	atomic.StoreInt64(&recorder.idleCount, int64(idleCount))
	atomic.StoreInt64(&recorder.inUseCount, int64(inUseCount))
}

// ObserveRefill 记录一次补池结果，用于计算补池速率。
func (recorder *TransportMetricsRecorder) ObserveRefill(openedCount int, observedAt time.Time) {
	if recorder == nil || openedCount <= 0 {
		return
	}
	if observedAt.IsZero() {
		observedAt = time.Now().UTC()
	}
	recorder.refillMutex.Lock()
	defer recorder.refillMutex.Unlock()
	if recorder.refillFirstAt.IsZero() || observedAt.Before(recorder.refillFirstAt) {
		recorder.refillFirstAt = observedAt
	}
	if observedAt.After(recorder.refillLastAt) {
		recorder.refillLastAt = observedAt
	}
	recorder.refillOpenedTotal += uint64(openedCount)
}

// IncOpenTimeout 记录一次 open timeout。
func (recorder *TransportMetricsRecorder) IncOpenTimeout() {
	if recorder == nil {
		return
	}
	atomic.AddUint64(&recorder.openTimeoutCount, 1)
}

// IncReset 记录一次 reset 事件。
func (recorder *TransportMetricsRecorder) IncReset() {
	if recorder == nil {
		return
	}
	atomic.AddUint64(&recorder.resetCount, 1)
}

// IncBrokenTunnel 记录一次 broken tunnel 事件。
func (recorder *TransportMetricsRecorder) IncBrokenTunnel() {
	if recorder == nil {
		return
	}
	atomic.AddUint64(&recorder.brokenCount, 1)
}

// Snapshot 返回当前指标快照。
func (recorder *TransportMetricsRecorder) Snapshot(now time.Time) TransportMetricsSnapshot {
	if recorder == nil {
		return TransportMetricsSnapshot{}
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}

	snapshot := TransportMetricsSnapshot{
		HeartbeatRTT:     time.Duration(atomic.LoadInt64(&recorder.heartbeatRTTNanos)),
		IdleCount:        int(atomic.LoadInt64(&recorder.idleCount)),
		InUseCount:       int(atomic.LoadInt64(&recorder.inUseCount)),
		OpenTimeoutCount: atomic.LoadUint64(&recorder.openTimeoutCount),
		ResetCount:       atomic.LoadUint64(&recorder.resetCount),
		BrokenCount:      atomic.LoadUint64(&recorder.brokenCount),
	}

	recorder.refillMutex.Lock()
	refillFirstAt := recorder.refillFirstAt
	refillLastAt := recorder.refillLastAt
	refillOpenedTotal := recorder.refillOpenedTotal
	recorder.refillMutex.Unlock()

	snapshot.RefillOpenedTotal = refillOpenedTotal
	snapshot.RefillRatePerSec = computeRefillRatePerSec(refillFirstAt, refillLastAt, refillOpenedTotal, now)
	return snapshot
}

func computeRefillRatePerSec(firstAt time.Time, lastAt time.Time, openedTotal uint64, now time.Time) float64 {
	if openedTotal == 0 || firstAt.IsZero() {
		return 0
	}
	window := lastAt.Sub(firstAt)
	if window <= 0 && now.After(firstAt) {
		window = now.Sub(firstAt)
	}
	if window <= 0 {
		// 事件发生在同一时刻时，无法估算稳定速率。
		return 0
	}
	return float64(openedTotal) / window.Seconds()
}
