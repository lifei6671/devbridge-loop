package registry

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// RuntimeAudit 描述运行态流量最近一次更新的审计字段。
type RuntimeAudit struct {
	CreatedAt   time.Time
	UpdatedAt   time.Time
	LastEventID string
}

// RuntimeTraffic 描述 runtime registry 中的一条流量记录。
type RuntimeTraffic struct {
	Traffic         pb.Traffic
	UpstreamBytes   uint64
	DownstreamBytes uint64
	LastErrorCode   string
	RejectReason    string
	PathKind        pb.RouteTargetType
	FallbackReason  string
	Audit           RuntimeAudit
}

// RuntimeSnapshot 描述 runtime registry 的只读快照视图。
type RuntimeSnapshot struct {
	Traffics    []RuntimeTraffic
	GeneratedAt time.Time
}

// RuntimeTrafficRegistry 维护高频短生命周期运行态流量。
type RuntimeTrafficRegistry struct {
	mu       sync.RWMutex
	traffics map[string]RuntimeTraffic
}

// NewRuntimeTrafficRegistry 创建 runtime traffic registry。
func NewRuntimeTrafficRegistry() *RuntimeTrafficRegistry {
	return &RuntimeTrafficRegistry{
		traffics: make(map[string]RuntimeTraffic),
	}
}

// UpsertTraffic 写入或更新流量运行态记录。
func (registry *RuntimeTrafficRegistry) UpsertTraffic(traffic pb.Traffic, pathKind pb.RouteTargetType) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpsertTrafficWithAudit(traffic, pathKind, "")
}

// UpsertTrafficWithAudit 写入流量并记录最近事件 ID。
func (registry *RuntimeTrafficRegistry) UpsertTrafficWithAudit(traffic pb.Traffic, pathKind pb.RouteTargetType, eventID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	trafficID := strings.TrimSpace(traffic.TrafficID)
	current := registry.traffics[trafficID]
	if current.Audit.CreatedAt.IsZero() {
		// 首次写入流量时记录创建时间，便于问题排查。
		current.Audit.CreatedAt = time.Now().UTC()
	}
	// 只更新业务字段，累计字节计数保持不丢失。
	current.Traffic = traffic
	current.PathKind = pathKind
	current.Audit.UpdatedAt = time.Now().UTC()
	current.Audit.LastEventID = strings.TrimSpace(eventID)
	registry.traffics[trafficID] = current
}

// UpdateState 更新流量状态。
func (registry *RuntimeTrafficRegistry) UpdateState(trafficID string, state pb.TrafficState) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.UpdateStateWithAudit(trafficID, state, "")
}

// UpdateStateWithAudit 更新流量状态并记录最近事件 ID。
func (registry *RuntimeTrafficRegistry) UpdateStateWithAudit(trafficID string, state pb.TrafficState, eventID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	normalizedTrafficID := strings.TrimSpace(trafficID)
	current := registry.traffics[normalizedTrafficID]
	current.Traffic.State = state
	if current.Audit.CreatedAt.IsZero() {
		// 兼容先更新状态后插入流量的调用顺序。
		current.Audit.CreatedAt = time.Now().UTC()
	}
	current.Audit.UpdatedAt = time.Now().UTC()
	current.Audit.LastEventID = strings.TrimSpace(eventID)
	registry.traffics[normalizedTrafficID] = current
}

// RecordBytes 记录上下行字节量增量。
func (registry *RuntimeTrafficRegistry) RecordBytes(trafficID string, upstreamDelta uint64, downstreamDelta uint64) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.RecordBytesWithAudit(trafficID, upstreamDelta, downstreamDelta, "")
}

// RecordBytesWithAudit 记录字节增量并记录最近事件 ID。
func (registry *RuntimeTrafficRegistry) RecordBytesWithAudit(trafficID string, upstreamDelta uint64, downstreamDelta uint64, eventID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	normalizedTrafficID := strings.TrimSpace(trafficID)
	current := registry.traffics[normalizedTrafficID]
	// 通过增量方式累计字节计数，避免覆盖已有统计。
	current.UpstreamBytes += upstreamDelta
	current.DownstreamBytes += downstreamDelta
	if current.Audit.CreatedAt.IsZero() {
		// 兼容先记字节后插入流量的调用顺序。
		current.Audit.CreatedAt = time.Now().UTC()
	}
	current.Audit.UpdatedAt = time.Now().UTC()
	current.Audit.LastEventID = strings.TrimSpace(eventID)
	registry.traffics[normalizedTrafficID] = current
}

// RecordFailure 记录流量失败信息。
func (registry *RuntimeTrafficRegistry) RecordFailure(trafficID string, errorCode string, fallbackReason string) {
	// 兼容旧调用方，未提供 event 元信息时仅更新时间戳。
	registry.RecordFailureWithAudit(trafficID, errorCode, fallbackReason, "")
}

// RecordFailureWithAudit 记录失败原因并记录最近事件 ID。
func (registry *RuntimeTrafficRegistry) RecordFailureWithAudit(trafficID string, errorCode string, fallbackReason string, eventID string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	normalizedTrafficID := strings.TrimSpace(trafficID)
	current := registry.traffics[normalizedTrafficID]
	current.LastErrorCode = strings.TrimSpace(errorCode)
	current.FallbackReason = strings.TrimSpace(fallbackReason)
	if current.Audit.CreatedAt.IsZero() {
		// 兼容先记失败再插入流量的调用顺序。
		current.Audit.CreatedAt = time.Now().UTC()
	}
	current.Audit.UpdatedAt = time.Now().UTC()
	current.Audit.LastEventID = strings.TrimSpace(eventID)
	registry.traffics[normalizedTrafficID] = current
}

// RecordRejectReason 记录拒绝原因，用于管理面查询 reject reason。
func (registry *RuntimeTrafficRegistry) RecordRejectReason(trafficID string, rejectReason string) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	normalizedTrafficID := strings.TrimSpace(trafficID)
	current := registry.traffics[normalizedTrafficID]
	current.RejectReason = strings.TrimSpace(rejectReason)
	if current.Audit.CreatedAt.IsZero() {
		// 兼容先记拒绝原因再插入流量的调用顺序。
		current.Audit.CreatedAt = time.Now().UTC()
	}
	current.Audit.UpdatedAt = time.Now().UTC()
	registry.traffics[normalizedTrafficID] = current
}

// GetTraffic 按 trafficId 查询流量运行态。
func (registry *RuntimeTrafficRegistry) GetTraffic(trafficID string) (RuntimeTraffic, bool) {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	traffic, exists := registry.traffics[strings.TrimSpace(trafficID)]
	return traffic, exists
}

// ListTraffics 返回当前全部运行态流量记录。
func (registry *RuntimeTrafficRegistry) ListTraffics() []RuntimeTraffic {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	traffics := make([]RuntimeTraffic, 0, len(registry.traffics))
	for _, traffic := range registry.traffics {
		traffics = append(traffics, traffic)
	}
	return traffics
}

// ListTrafficsByPath 返回指定路径类型的流量集合。
func (registry *RuntimeTrafficRegistry) ListTrafficsByPath(pathKind pb.RouteTargetType) []RuntimeTraffic {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	traffics := make([]RuntimeTraffic, 0)
	for _, traffic := range registry.traffics {
		if traffic.PathKind == pathKind {
			// 路径类型匹配时返回记录，便于区分 connector/direct。
			traffics = append(traffics, traffic)
		}
	}
	return traffics
}

// Snapshot 生成 runtime registry 的最小状态快照。
func (registry *RuntimeTrafficRegistry) Snapshot() RuntimeSnapshot {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	snapshot := RuntimeSnapshot{
		Traffics:    make([]RuntimeTraffic, 0, len(registry.traffics)),
		GeneratedAt: time.Now().UTC(),
	}
	for _, traffic := range registry.traffics {
		snapshot.Traffics = append(snapshot.Traffics, traffic)
	}
	return snapshot
}

// BuildTraceKey 构建 trace 查询键，便于上层日志系统聚合。
func BuildTraceKey(traceID string, trafficID string) string {
	// traceID 为空时回退 trafficID，保证键稳定可用。
	normalizedTraceID := strings.TrimSpace(traceID)
	if normalizedTraceID == "" {
		return fmt.Sprintf("trace:traffic:%s", strings.TrimSpace(trafficID))
	}
	return fmt.Sprintf("trace:id:%s", normalizedTraceID)
}
