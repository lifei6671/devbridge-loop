package observe

import (
	"strings"
	"sync"
	"time"

	"github.com/lifei6671/devbridge-loop/ltfp/pb"
)

// Event 描述一条可观测性事件。
type Event struct {
	Timestamp      time.Time
	TraceID        string
	PathKind       pb.RouteTargetType
	ErrorCode      string
	RejectReason   string
	FallbackReason string
	Message        string
	Metadata       map[string]string
}

// Metrics 描述最小运行态指标聚合结果。
type Metrics struct {
	ByPathKind       map[pb.RouteTargetType]uint64
	ByErrorCode      map[string]uint64
	ByRejectReason   map[string]uint64
	ByFallbackReason map[string]uint64
}

// Snapshot 描述 recorder 的只读快照视图。
type Snapshot struct {
	Events      []Event
	Metrics     Metrics
	GeneratedAt time.Time
}

// Recorder 负责记录结构化事件并维护最小指标。
type Recorder struct {
	mu       sync.RWMutex
	events   []Event
	maxEvent int
	metrics  Metrics
}

// NewRecorder 创建可观测性记录器。
func NewRecorder(maxEvent int) *Recorder {
	if maxEvent <= 0 {
		// 未配置容量时回退到 512，防止事件无限增长。
		maxEvent = 512
	}
	return &Recorder{
		events:   make([]Event, 0, maxEvent),
		maxEvent: maxEvent,
		metrics: Metrics{
			ByPathKind:       make(map[pb.RouteTargetType]uint64),
			ByErrorCode:      make(map[string]uint64),
			ByRejectReason:   make(map[string]uint64),
			ByFallbackReason: make(map[string]uint64),
		},
	}
}

// Record 写入一条结构化事件并更新指标。
func (recorder *Recorder) Record(event Event) {
	recorder.mu.Lock()
	defer recorder.mu.Unlock()

	normalizedEvent := Event{
		Timestamp:      event.Timestamp,
		TraceID:        strings.TrimSpace(event.TraceID),
		PathKind:       event.PathKind,
		ErrorCode:      strings.TrimSpace(event.ErrorCode),
		RejectReason:   strings.TrimSpace(event.RejectReason),
		FallbackReason: strings.TrimSpace(event.FallbackReason),
		Message:        strings.TrimSpace(event.Message),
		Metadata:       event.Metadata,
	}
	if normalizedEvent.Timestamp.IsZero() {
		// 调用方未提供时间时由 recorder 统一补 UTC 时间戳。
		normalizedEvent.Timestamp = time.Now().UTC()
	}
	recorder.events = append(recorder.events, normalizedEvent)
	if len(recorder.events) > recorder.maxEvent {
		// 超出容量时丢弃最旧事件，保留最近窗口用于排障。
		recorder.events = recorder.events[len(recorder.events)-recorder.maxEvent:]
	}

	if normalizedEvent.PathKind != "" {
		recorder.metrics.ByPathKind[normalizedEvent.PathKind]++
	}
	if normalizedEvent.ErrorCode != "" {
		recorder.metrics.ByErrorCode[normalizedEvent.ErrorCode]++
	}
	if normalizedEvent.RejectReason != "" {
		recorder.metrics.ByRejectReason[normalizedEvent.RejectReason]++
	}
	if normalizedEvent.FallbackReason != "" {
		recorder.metrics.ByFallbackReason[normalizedEvent.FallbackReason]++
	}
}

// QueryByTrace 按 traceId 查询事件列表。
func (recorder *Recorder) QueryByTrace(traceID string) []Event {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	normalizedTraceID := strings.TrimSpace(traceID)
	result := make([]Event, 0)
	for _, event := range recorder.events {
		if event.TraceID == normalizedTraceID {
			// trace 命中时返回，便于端到端链路追踪。
			result = append(result, event)
		}
	}
	return result
}

// QueryByErrorCode 按错误码查询事件列表。
func (recorder *Recorder) QueryByErrorCode(errorCode string) []Event {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	normalizedCode := strings.TrimSpace(errorCode)
	result := make([]Event, 0)
	for _, event := range recorder.events {
		if event.ErrorCode == normalizedCode {
			// 错误码命中时返回，便于故障分类统计。
			result = append(result, event)
		}
	}
	return result
}

// Snapshot 输出 recorder 的最小状态快照。
func (recorder *Recorder) Snapshot() Snapshot {
	recorder.mu.RLock()
	defer recorder.mu.RUnlock()
	events := make([]Event, len(recorder.events))
	copy(events, recorder.events)
	return Snapshot{
		Events:      events,
		Metrics:     cloneMetrics(recorder.metrics),
		GeneratedAt: time.Now().UTC(),
	}
}

// cloneMetrics 深拷贝指标对象，避免调用方修改内部状态。
func cloneMetrics(source Metrics) Metrics {
	cloned := Metrics{
		ByPathKind:       make(map[pb.RouteTargetType]uint64, len(source.ByPathKind)),
		ByErrorCode:      make(map[string]uint64, len(source.ByErrorCode)),
		ByRejectReason:   make(map[string]uint64, len(source.ByRejectReason)),
		ByFallbackReason: make(map[string]uint64, len(source.ByFallbackReason)),
	}
	for key, value := range source.ByPathKind {
		cloned.ByPathKind[key] = value
	}
	for key, value := range source.ByErrorCode {
		cloned.ByErrorCode[key] = value
	}
	for key, value := range source.ByRejectReason {
		cloned.ByRejectReason[key] = value
	}
	for key, value := range source.ByFallbackReason {
		cloned.ByFallbackReason[key] = value
	}
	return cloned
}
