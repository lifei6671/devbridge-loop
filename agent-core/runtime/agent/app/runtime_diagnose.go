package app

import (
	"strings"
	"time"
)

const (
	// runtimeDiagnoseEventBufferSize 控制 runtime 诊断事件环形缓冲容量。
	runtimeDiagnoseEventBufferSize = 256
	// runtimeDiagnoseDefaultLogLimit 控制 diagnose.logs 默认返回最近 N 条事件。
	runtimeDiagnoseDefaultLogLimit = 128
)

// runtimeDiagnoseEvent 定义 runtime 内部诊断事件模型。
type runtimeDiagnoseEvent struct {
	TimestampMS  uint64
	Level        string
	Module       string
	Code         string
	Message      string
	ConnectorID  string
	SessionID    string
	SessionEpoch uint64
	BridgeState  string
	RequestID    string
	Trigger      string
	Reason       string
}

// runtimeDiagnoseSummary 定义诊断快照需要的聚合统计。
type runtimeDiagnoseSummary struct {
	EventTotal       uint64
	ErrorCount       uint64
	StateChangeCount uint64
	ReconnectCount   uint64
	RefillEventCount uint64
	LastEventAtMS    *uint64
	LastEventCode    string
	LastEventMessage string
}

// appendDiagnoseEvent 将单条诊断事件写入 runtime 内部缓冲，超限时丢弃最老事件。
func (r *Runtime) appendDiagnoseEvent(event runtimeDiagnoseEvent) {
	if r == nil {
		return
	}
	normalizedEvent := event
	if normalizedEvent.TimestampMS == 0 {
		normalizedEvent.TimestampMS = runtimeNowMillis()
	}
	normalizedEvent.Level = strings.ToLower(strings.TrimSpace(normalizedEvent.Level))
	if normalizedEvent.Level == "" {
		normalizedEvent.Level = "info"
	}
	normalizedEvent.Module = strings.TrimSpace(normalizedEvent.Module)
	if normalizedEvent.Module == "" {
		normalizedEvent.Module = "agent.runtime"
	}
	normalizedEvent.Code = strings.TrimSpace(normalizedEvent.Code)
	if normalizedEvent.Code == "" {
		normalizedEvent.Code = "RUNTIME_EVENT"
	}
	normalizedEvent.Message = strings.TrimSpace(normalizedEvent.Message)
	if normalizedEvent.Message == "" {
		normalizedEvent.Message = normalizedEvent.Code
	}
	if strings.TrimSpace(normalizedEvent.ConnectorID) == "" {
		normalizedEvent.ConnectorID = strings.TrimSpace(r.cfg.AgentID)
	}

	r.diagnoseMu.Lock()
	defer r.diagnoseMu.Unlock()
	if r.diagnoseEvents == nil {
		r.diagnoseEvents = make([]runtimeDiagnoseEvent, 0, runtimeDiagnoseEventBufferSize)
	}
	if len(r.diagnoseEvents) >= runtimeDiagnoseEventBufferSize {
		// 缓冲满时前移覆盖，保证始终保留最近窗口内事件。
		copy(r.diagnoseEvents, r.diagnoseEvents[1:])
		r.diagnoseEvents[len(r.diagnoseEvents)-1] = normalizedEvent
	} else {
		r.diagnoseEvents = append(r.diagnoseEvents, normalizedEvent)
	}
	r.diagnoseUpdatedAt = time.UnixMilli(int64(normalizedEvent.TimestampMS)).UTC()
}

// snapshotDiagnoseEvents 返回最近 limit 条诊断事件（按时间升序）。
func (r *Runtime) snapshotDiagnoseEvents(limit int) []runtimeDiagnoseEvent {
	if r == nil {
		return []runtimeDiagnoseEvent{}
	}
	normalizedLimit := limit
	if normalizedLimit <= 0 || normalizedLimit > runtimeDiagnoseEventBufferSize {
		normalizedLimit = runtimeDiagnoseDefaultLogLimit
	}
	r.diagnoseMu.RLock()
	defer r.diagnoseMu.RUnlock()
	if len(r.diagnoseEvents) == 0 {
		return []runtimeDiagnoseEvent{}
	}
	startIndex := len(r.diagnoseEvents) - normalizedLimit
	if startIndex < 0 {
		startIndex = 0
	}
	events := make([]runtimeDiagnoseEvent, len(r.diagnoseEvents[startIndex:]))
	copy(events, r.diagnoseEvents[startIndex:])
	return events
}

// summarizeDiagnoseEvents 对诊断事件窗口做聚合，供 diagnose.snapshot 输出。
func summarizeDiagnoseEvents(events []runtimeDiagnoseEvent) runtimeDiagnoseSummary {
	summary := runtimeDiagnoseSummary{}
	for _, event := range events {
		summary.EventTotal++
		normalizedCode := strings.TrimSpace(event.Code)
		normalizedLevel := strings.ToLower(strings.TrimSpace(event.Level))
		if normalizedLevel == "error" {
			summary.ErrorCount++
		}
		if strings.HasPrefix(normalizedCode, "BRIDGE_STATE_") {
			summary.StateChangeCount++
		}
		if strings.HasPrefix(normalizedCode, "SESSION_RECONNECT_") ||
			strings.HasPrefix(normalizedCode, "BRIDGE_RETRY_") ||
			normalizedCode == "BRIDGE_RECONNECT_ESTABLISHED" {
			summary.ReconnectCount++
		}
		if strings.HasPrefix(normalizedCode, "TUNNEL_REFILL_") ||
			strings.HasPrefix(normalizedCode, "TUNNEL_POOL_REPORT_") {
			summary.RefillEventCount++
		}
		if summary.LastEventAtMS == nil || event.TimestampMS >= *summary.LastEventAtMS {
			lastEventAtMS := event.TimestampMS
			summary.LastEventAtMS = &lastEventAtMS
			summary.LastEventCode = normalizedCode
			summary.LastEventMessage = strings.TrimSpace(event.Message)
		}
	}
	return summary
}

// diagnoseLogsPayload 组装 diagnose.logs 返回体，输出 runtime 事件源的最近日志窗口。
func (r *Runtime) diagnoseLogsPayload() map[string]any {
	events := r.snapshotDiagnoseEvents(runtimeDiagnoseDefaultLogLimit)
	items := make([]map[string]any, 0, len(events))
	for eventIndex := len(events) - 1; eventIndex >= 0; eventIndex-- {
		// diagnose.logs 默认按“新 -> 旧”返回，便于 UI 直接展示最近事件。
		items = append(items, diagnoseEventToPayloadItem(events[eventIndex]))
	}
	updatedAtMS := runtimeNowMillis()
	if len(events) > 0 {
		updatedAtMS = events[len(events)-1].TimestampMS
	}
	if r != nil {
		r.diagnoseMu.RLock()
		if !r.diagnoseUpdatedAt.IsZero() {
			updatedAtMS = uint64(r.diagnoseUpdatedAt.UTC().UnixMilli())
		}
		r.diagnoseMu.RUnlock()
	}
	return map[string]any{
		"items":         items,
		"total":         uint64(len(items)),
		"updated_at_ms": updatedAtMS,
		"source":        "agent.runtime.diagnose",
	}
}

// bridgeRuntimeContext 读取当前 bridge 会话上下文，供诊断事件补全元信息。
func (r *Runtime) bridgeRuntimeContext() (string, uint64, string) {
	if r == nil {
		return "", 0, ""
	}
	r.bridgeMu.RLock()
	defer r.bridgeMu.RUnlock()
	return r.bridgeSession, r.bridgeEpoch, r.bridgeState
}

// diagnoseEventToPayloadItem 把内部事件结构转换为 localrpc 可序列化对象。
func diagnoseEventToPayloadItem(event runtimeDiagnoseEvent) map[string]any {
	item := map[string]any{
		"ts_ms":   event.TimestampMS,
		"level":   event.Level,
		"module":  event.Module,
		"code":    event.Code,
		"message": event.Message,
	}
	if strings.TrimSpace(event.ConnectorID) != "" {
		item["connector_id"] = strings.TrimSpace(event.ConnectorID)
	}
	if strings.TrimSpace(event.SessionID) != "" {
		item["session_id"] = strings.TrimSpace(event.SessionID)
	}
	if event.SessionEpoch > 0 {
		item["session_epoch"] = event.SessionEpoch
	}
	if strings.TrimSpace(event.BridgeState) != "" {
		item["bridge_state"] = strings.TrimSpace(event.BridgeState)
	}
	if strings.TrimSpace(event.RequestID) != "" {
		item["request_id"] = strings.TrimSpace(event.RequestID)
	}
	if strings.TrimSpace(event.Trigger) != "" {
		item["trigger"] = strings.TrimSpace(event.Trigger)
	}
	if strings.TrimSpace(event.Reason) != "" {
		item["reason"] = strings.TrimSpace(event.Reason)
	}
	return item
}
