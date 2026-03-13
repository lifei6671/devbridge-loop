package obs

import (
	"strings"
	"sync/atomic"
	"time"
)

const (
	// MetricAgentSessionState 表示 Agent 当前 session 状态。
	MetricAgentSessionState = "agent_session_state"
	// MetricAgentTunnelIdleCount 表示 Agent 当前 idle tunnel 数量。
	MetricAgentTunnelIdleCount = "agent_tunnel_idle_count"
	// MetricAgentTunnelActiveCount 表示 Agent 当前 active tunnel 数量。
	MetricAgentTunnelActiveCount = "agent_tunnel_active_count"
	// MetricAgentTrafficOpenAckLatencyMs 表示 Agent open_ack 延迟。
	MetricAgentTrafficOpenAckLatencyMs = "agent_traffic_open_ack_latency_ms"
	// MetricAgentUpstreamDialLatencyMs 表示 Agent upstream dial 延迟。
	MetricAgentUpstreamDialLatencyMs = "agent_upstream_dial_latency_ms"
)

const (
	agentSessionStateUnknown int32 = iota
	agentSessionStateConnecting
	agentSessionStateAuthenticating
	agentSessionStateActive
	agentSessionStateDraining
	agentSessionStateStale
	agentSessionStateClosed
)

// Metrics holds metric collectors for the agent runtime.
type Metrics struct {
	agentSessionState atomic.Int32

	agentTunnelIdleCount   atomic.Int64
	agentTunnelActiveCount atomic.Int64

	agentTrafficOpenAckLatencyTotalMs atomic.Int64
	agentTrafficOpenAckLatencyCount   atomic.Uint64

	agentUpstreamDialLatencyTotalMs atomic.Int64
	agentUpstreamDialLatencyCount   atomic.Uint64
}

// NewMetrics 创建 Agent 运行时指标容器。
func NewMetrics() *Metrics {
	return &Metrics{}
}

// DefaultMetrics 提供 Agent 运行时默认指标容器。
var DefaultMetrics = NewMetrics()

// SetAgentSessionState 写入 Agent 当前 session 状态。
func (metrics *Metrics) SetAgentSessionState(state string) {
	if metrics == nil {
		return
	}
	metrics.agentSessionState.Store(encodeAgentSessionState(state))
}

// AgentSessionState 返回 Agent 当前 session 状态字符串。
func (metrics *Metrics) AgentSessionState() string {
	if metrics == nil {
		return ""
	}
	return decodeAgentSessionState(metrics.agentSessionState.Load())
}

// SetAgentTunnelPoolCounts 写入 Agent tunnel pool idle/active 数量。
func (metrics *Metrics) SetAgentTunnelPoolCounts(idleCount int, activeCount int) {
	if metrics == nil {
		return
	}
	if idleCount < 0 {
		idleCount = 0
	}
	if activeCount < 0 {
		activeCount = 0
	}
	metrics.agentTunnelIdleCount.Store(int64(idleCount))
	metrics.agentTunnelActiveCount.Store(int64(activeCount))
}

// AgentTunnelIdleCount 返回 Agent 当前 idle tunnel 数量。
func (metrics *Metrics) AgentTunnelIdleCount() int64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentTunnelIdleCount.Load()
}

// AgentTunnelActiveCount 返回 Agent 当前 active tunnel 数量。
func (metrics *Metrics) AgentTunnelActiveCount() int64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentTunnelActiveCount.Load()
}

// ObserveAgentTrafficOpenAckLatency 记录一次 open_ack 延迟。
func (metrics *Metrics) ObserveAgentTrafficOpenAckLatency(latency time.Duration) {
	if metrics == nil {
		return
	}
	if latency < 0 {
		latency = 0
	}
	latencyMs := latency.Milliseconds()
	metrics.agentTrafficOpenAckLatencyTotalMs.Add(latencyMs)
	metrics.agentTrafficOpenAckLatencyCount.Add(1)
}

// AgentTrafficOpenAckLatencyTotalMs 返回 open_ack 延迟总毫秒数。
func (metrics *Metrics) AgentTrafficOpenAckLatencyTotalMs() int64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentTrafficOpenAckLatencyTotalMs.Load()
}

// AgentTrafficOpenAckLatencyCount 返回 open_ack 延迟样本数。
func (metrics *Metrics) AgentTrafficOpenAckLatencyCount() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentTrafficOpenAckLatencyCount.Load()
}

// ObserveAgentUpstreamDialLatency 记录一次 upstream dial 延迟。
func (metrics *Metrics) ObserveAgentUpstreamDialLatency(latency time.Duration) {
	if metrics == nil {
		return
	}
	if latency < 0 {
		latency = 0
	}
	latencyMs := latency.Milliseconds()
	metrics.agentUpstreamDialLatencyTotalMs.Add(latencyMs)
	metrics.agentUpstreamDialLatencyCount.Add(1)
}

// AgentUpstreamDialLatencyTotalMs 返回 upstream dial 延迟总毫秒数。
func (metrics *Metrics) AgentUpstreamDialLatencyTotalMs() int64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentUpstreamDialLatencyTotalMs.Load()
}

// AgentUpstreamDialLatencyCount 返回 upstream dial 延迟样本数。
func (metrics *Metrics) AgentUpstreamDialLatencyCount() uint64 {
	if metrics == nil {
		return 0
	}
	return metrics.agentUpstreamDialLatencyCount.Load()
}

// encodeAgentSessionState 把 session 状态字符串编码为可原子存储的整数值。
func encodeAgentSessionState(state string) int32 {
	switch strings.ToUpper(strings.TrimSpace(state)) {
	case "CONNECTING":
		return agentSessionStateConnecting
	case "AUTHENTICATING":
		return agentSessionStateAuthenticating
	case "ACTIVE":
		return agentSessionStateActive
	case "DRAINING":
		return agentSessionStateDraining
	case "STALE":
		return agentSessionStateStale
	case "CLOSED":
		return agentSessionStateClosed
	default:
		return agentSessionStateUnknown
	}
}

// decodeAgentSessionState 把原子存储的状态整数还原为状态字符串。
func decodeAgentSessionState(encodedState int32) string {
	switch encodedState {
	case agentSessionStateConnecting:
		return "CONNECTING"
	case agentSessionStateAuthenticating:
		return "AUTHENTICATING"
	case agentSessionStateActive:
		return "ACTIVE"
	case agentSessionStateDraining:
		return "DRAINING"
	case agentSessionStateStale:
		return "STALE"
	case agentSessionStateClosed:
		return "CLOSED"
	default:
		return ""
	}
}
