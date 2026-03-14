use serde::Serialize;
use serde_json::{json, Value};
use std::sync::Arc;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{
    now_ms, push_host_log, with_rpc_metrics, AppRuntimeState, HostLogEntry,
};

/// runtime 诊断日志条目：对应 agent `diagnose.logs` 返回体。
#[derive(Debug, Clone, Serialize)]
pub struct DiagnoseLogEntry {
    pub ts_ms: u64,
    pub level: String,
    pub module: String,
    pub code: String,
    pub message: String,
    pub session_id: Option<String>,
    pub session_epoch: Option<u64>,
    pub bridge_state: Option<String>,
    pub request_id: Option<String>,
    pub trigger: Option<String>,
    pub reason: Option<String>,
}

/// runtime 诊断快照：对应 agent `diagnose.snapshot` 返回体。
#[derive(Debug, Clone, Serialize)]
pub struct DiagnoseSnapshot {
    pub state: String,
    pub last_error: Option<String>,
    pub retry_fail_streak: u64,
    pub retry_backoff_ms: u64,
    pub next_retry_at_ms: Option<u64>,
    pub tunnel_idle_count: u64,
    pub tunnel_active_count: u64,
    pub event_total: u64,
    pub event_error_count: u64,
    pub event_state_changes: u64,
    pub event_reconnects: u64,
    pub event_refill_total: u64,
    pub last_event_at_ms: Option<u64>,
    pub last_event_code: Option<String>,
    pub last_event_message: Option<String>,
    pub updated_at_ms: u64,
    pub source: String,
}

fn value_str_opt(payload: &Value, keys: &[&str]) -> Option<String> {
    for key in keys {
        if let Some(value) = payload.get(*key).and_then(Value::as_str) {
            let trimmed = value.trim();
            if !trimmed.is_empty() {
                return Some(trimmed.to_string());
            }
        }
    }
    None
}

fn value_u64_opt(payload: &Value, keys: &[&str]) -> Option<u64> {
    for key in keys {
        let Some(value) = payload.get(*key) else {
            continue;
        };
        if let Some(number) = value.as_u64() {
            return Some(number);
        }
        if let Some(number) = value.as_f64() {
            if number.is_finite() && number >= 0.0 {
                return Some(number as u64);
            }
        }
        if let Some(text) = value.as_str() {
            if let Ok(number) = text.trim().parse::<u64>() {
                return Some(number);
            }
        }
    }
    None
}

fn value_u64_or(payload: &Value, keys: &[&str], default_value: u64) -> u64 {
    value_u64_opt(payload, keys).unwrap_or(default_value)
}

fn is_method_unavailable(err: &str) -> bool {
    err.contains("METHOD_NOT_ALLOWED")
        || err.contains("METHOD_NOT_FOUND")
        || err.contains("NOT_IMPLEMENTED")
}

/// 通过短锁模式请求 runtime 诊断方法，避免 RPC 超时期间阻塞 supervisor 全局锁。
fn request_diagnose_payload(
    state: &Arc<AppRuntimeState>,
    method: &str,
) -> Result<Option<Value>, String> {
    let mut ipc_client = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| format!("读取 {method} 失败：supervisor 锁异常"))?;
        let Some(ipc_client) = supervisor.ipc_client.take() else {
            return Ok(None);
        };
        ipc_client
    };

    let request_result = ipc_client.request(method, json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS);

    {
        // 请求结束后立即归还 client，保证其它 command 可以继续复用连接。
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| format!("读取 {method} 失败：supervisor 锁异常"))?;
        supervisor.ipc_client = Some(ipc_client);
    }

    match request_result {
        Ok(payload) => Ok(Some(payload)),
        Err(err) => Err(err),
    }
}

/// 把 runtime `diagnose.logs` 返回体解析为前端可直接渲染的日志条目。
fn parse_diagnose_logs(payload: &Value) -> Vec<DiagnoseLogEntry> {
    let raw_items = if let Some(items) = payload.get("items").and_then(Value::as_array) {
        items.clone()
    } else if let Some(items) = payload.get("logs").and_then(Value::as_array) {
        // 兼容 mock localrpc 历史 `logs` 字段。
        items.clone()
    } else if let Some(items) = payload.as_array() {
        items.clone()
    } else {
        Vec::new()
    };

    raw_items
        .iter()
        .map(|item| DiagnoseLogEntry {
            ts_ms: value_u64_or(item, &["ts_ms", "timestamp_ms", "updated_at_ms"], now_ms()),
            level: value_str_opt(item, &["level"]).unwrap_or_else(|| "info".to_string()),
            module: value_str_opt(item, &["module"]).unwrap_or_else(|| "agent.runtime".to_string()),
            code: value_str_opt(item, &["code"]).unwrap_or_else(|| "RUNTIME_EVENT".to_string()),
            message: value_str_opt(item, &["message"])
                .unwrap_or_else(|| "runtime event".to_string()),
            session_id: value_str_opt(item, &["session_id"]),
            session_epoch: value_u64_opt(item, &["session_epoch"]),
            bridge_state: value_str_opt(item, &["bridge_state", "state"]),
            request_id: value_str_opt(item, &["request_id"]),
            trigger: value_str_opt(item, &["trigger"]),
            reason: value_str_opt(item, &["reason"]),
        })
        .collect()
}

/// 解析 runtime `diagnose.snapshot`，并在缺省字段时回落到安全默认值。
fn parse_diagnose_snapshot(payload: &Value) -> DiagnoseSnapshot {
    let normalized = payload.get("snapshot").unwrap_or(payload);
    DiagnoseSnapshot {
        state: value_str_opt(normalized, &["state"]).unwrap_or_else(|| "UNKNOWN".to_string()),
        last_error: value_str_opt(normalized, &["last_error"]),
        retry_fail_streak: value_u64_or(normalized, &["retry_fail_streak"], 0),
        retry_backoff_ms: value_u64_or(normalized, &["retry_backoff_ms"], 0),
        next_retry_at_ms: value_u64_opt(normalized, &["next_retry_at_ms"]),
        tunnel_idle_count: value_u64_or(normalized, &["tunnel_idle_count"], 0),
        tunnel_active_count: value_u64_or(normalized, &["tunnel_active_count"], 0),
        event_total: value_u64_or(normalized, &["event_total"], 0),
        event_error_count: value_u64_or(normalized, &["event_error_count"], 0),
        event_state_changes: value_u64_or(normalized, &["event_state_changes"], 0),
        event_reconnects: value_u64_or(normalized, &["event_reconnects"], 0),
        event_refill_total: value_u64_or(normalized, &["event_refill_total"], 0),
        last_event_at_ms: value_u64_opt(normalized, &["last_event_at_ms"]),
        last_event_code: value_str_opt(normalized, &["last_event_code"]),
        last_event_message: value_str_opt(normalized, &["last_event_message"]),
        updated_at_ms: value_u64_or(normalized, &["updated_at_ms"], now_ms()),
        source: value_str_opt(normalized, &["source"])
            .unwrap_or_else(|| "agent.runtime.diagnose".to_string()),
    }
}

/// 构造兜底诊断快照，确保 UI 在 runtime 未连上时也有稳定结构可渲染。
fn unavailable_diagnose_snapshot(reason: Option<String>, source: &str) -> DiagnoseSnapshot {
    DiagnoseSnapshot {
        state: "UNAVAILABLE".to_string(),
        last_error: reason,
        retry_fail_streak: 0,
        retry_backoff_ms: 0,
        next_retry_at_ms: None,
        tunnel_idle_count: 0,
        tunnel_active_count: 0,
        event_total: 0,
        event_error_count: 0,
        event_state_changes: 0,
        event_reconnects: 0,
        event_refill_total: 0,
        last_event_at_ms: None,
        last_event_code: None,
        last_event_message: None,
        updated_at_ms: now_ms(),
        source: source.to_string(),
    }
}

/// Tauri command：获取宿主结构化日志快照。
#[tauri::command]
pub fn host_logs_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<HostLogEntry>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "读取宿主日志失败：supervisor 锁异常".to_string())?;
        Ok(supervisor.host_logs.iter().cloned().collect::<Vec<_>>())
    })
}

/// Tauri command：读取 runtime 诊断日志快照。
#[tauri::command]
pub fn diagnose_logs_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<DiagnoseLogEntry>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let payload = match request_diagnose_payload(&shared, "diagnose.logs") {
            Ok(Some(payload)) => payload,
            Ok(None) => return Ok(Vec::new()),
            Err(err) => {
                if is_method_unavailable(&err) {
                    if let Ok(mut supervisor) = shared.supervisor.lock() {
                        push_host_log(
                            &mut supervisor,
                            "warn",
                            "commands.diagnose",
                            "DIAGNOSE_LOGS_METHOD_NOT_READY",
                            format!("diagnose.logs 尚未在当前 Agent 实现: {err}"),
                        );
                    }
                    return Ok(Vec::new());
                }
                return Err(format!("读取 runtime 诊断日志失败: {err}"));
            }
        };

        let items = parse_diagnose_logs(&payload);
        if let Ok(mut supervisor) = shared.supervisor.lock() {
            push_host_log(
                &mut supervisor,
                "info",
                "commands.diagnose",
                "DIAGNOSE_LOGS_SNAPSHOT",
                format!("runtime 诊断日志已刷新，items={}", items.len()),
            );
        }
        Ok(items)
    })
}

/// Tauri command：读取 runtime 诊断快照。
#[tauri::command]
pub fn diagnose_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<DiagnoseSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let payload = match request_diagnose_payload(&shared, "diagnose.snapshot") {
            Ok(Some(payload)) => payload,
            Ok(None) => {
                return Ok(unavailable_diagnose_snapshot(
                    Some("agent ipc client unavailable".to_string()),
                    "host.fallback",
                ))
            }
            Err(err) => {
                if is_method_unavailable(&err) {
                    if let Ok(mut supervisor) = shared.supervisor.lock() {
                        push_host_log(
                            &mut supervisor,
                            "warn",
                            "commands.diagnose",
                            "DIAGNOSE_SNAPSHOT_METHOD_NOT_READY",
                            format!("diagnose.snapshot 尚未在当前 Agent 实现: {err}"),
                        );
                    }
                    return Ok(unavailable_diagnose_snapshot(Some(err), "host.fallback"));
                }
                return Err(format!("读取 runtime 诊断快照失败: {err}"));
            }
        };
        Ok(parse_diagnose_snapshot(&payload))
    })
}

#[cfg(test)]
mod tests {
    use super::{parse_diagnose_logs, parse_diagnose_snapshot};
    use serde_json::json;

    /// 验证 diagnose.logs 解析可兼容 runtime 的 `items` 结构。
    #[test]
    fn parse_diagnose_logs_from_items_payload() {
        let payload = json!({
            "items": [
                {
                    "ts_ms": 1700000000000u64,
                    "level": "error",
                    "module": "agent.runtime.bridge",
                    "code": "BRIDGE_STATE_STALE",
                    "message": "heartbeat timeout",
                    "session_id": "session-1",
                    "session_epoch": 3
                }
            ]
        });
        let items = parse_diagnose_logs(&payload);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].code, "BRIDGE_STATE_STALE");
        assert_eq!(items[0].session_id.as_deref(), Some("session-1"));
        assert_eq!(items[0].session_epoch, Some(3));
    }

    /// 验证 diagnose.snapshot 解析可读取事件聚合字段。
    #[test]
    fn parse_diagnose_snapshot_with_event_stats() {
        let payload = json!({
            "state": "ACTIVE",
            "event_total": 8u64,
            "event_error_count": 2u64,
            "event_refill_total": 3u64,
            "last_event_code": "TUNNEL_REFILL_APPLIED",
            "updated_at_ms": 1700000002000u64,
            "source": "agent.runtime.diagnose"
        });
        let snapshot = parse_diagnose_snapshot(&payload);
        assert_eq!(snapshot.state, "ACTIVE");
        assert_eq!(snapshot.event_total, 8);
        assert_eq!(snapshot.event_error_count, 2);
        assert_eq!(snapshot.event_refill_total, 3);
        assert_eq!(
            snapshot.last_event_code.as_deref(),
            Some("TUNNEL_REFILL_APPLIED")
        );
        assert_eq!(snapshot.source, "agent.runtime.diagnose");
    }
}
