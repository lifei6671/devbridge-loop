use serde::Serialize;
use serde_json::{json, Value};
use std::sync::Arc;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{now_ms, push_host_log, with_rpc_metrics, AppRuntimeState};

/// Bridge 会话快照：用于 UI 展示 Agent 与 Bridge 的真实连接状态。
#[derive(Debug, Clone, Serialize)]
pub struct SessionSnapshot {
    pub state: String,
    pub session_id: Option<String>,
    pub session_epoch: Option<u64>,
    pub last_heartbeat_at_ms: Option<u64>,
    pub last_heartbeat_sent_at_ms: Option<u64>,
    pub last_heartbeat_at_text: Option<String>,
    pub reconnect_total: Option<u64>,
    pub retry_fail_streak: Option<u64>,
    pub retry_backoff_ms: Option<u64>,
    pub next_retry_at_ms: Option<u64>,
    pub last_error: Option<String>,
    pub updated_at_ms: u64,
    pub source: String,
    pub unavailable_reason: Option<String>,
}

fn session_unavailable_snapshot(
    state: &str,
    source: &str,
    reason: Option<String>,
) -> SessionSnapshot {
    let normalized_reason = reason.and_then(|value| {
        let trimmed = value.trim();
        if trimmed.is_empty() {
            None
        } else {
            Some(trimmed.to_string())
        }
    });
    SessionSnapshot {
        state: state.to_string(),
        session_id: None,
        session_epoch: None,
        last_heartbeat_at_ms: None,
        last_heartbeat_sent_at_ms: None,
        last_heartbeat_at_text: None,
        reconnect_total: None,
        retry_fail_streak: None,
        retry_backoff_ms: None,
        next_retry_at_ms: None,
        last_error: normalized_reason.clone(),
        updated_at_ms: now_ms(),
        source: source.to_string(),
        unavailable_reason: normalized_reason,
    }
}

fn value_str_opt(payload: &Value, keys: &[&str]) -> Option<String> {
    for key in keys {
        let Some(value) = payload.get(*key).and_then(Value::as_str) else {
            continue;
        };
        let trimmed = value.trim();
        if !trimmed.is_empty() {
            return Some(trimmed.to_string());
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

fn parse_session_snapshot(payload: &Value) -> SessionSnapshot {
    let normalized = payload.get("session").unwrap_or(payload);
    let state = value_str_opt(normalized, &["state", "session_state"])
        .unwrap_or_else(|| "UNKNOWN".to_string());
    let last_heartbeat_at_ms = value_u64_opt(
        normalized,
        &[
            "last_heartbeat_at_ms",
            "last_heartbeat_ms",
            "last_heartbeat_ts_ms",
        ],
    );
    let last_heartbeat_at_text = if last_heartbeat_at_ms.is_none() {
        value_str_opt(normalized, &["last_heartbeat_at", "last_heartbeat"])
    } else {
        None
    };
    SessionSnapshot {
        state,
        session_id: value_str_opt(normalized, &["session_id", "id"]),
        session_epoch: value_u64_opt(normalized, &["session_epoch", "epoch"]),
        last_heartbeat_at_ms,
        last_heartbeat_sent_at_ms: value_u64_opt(
            normalized,
            &["last_heartbeat_sent_at_ms", "last_heartbeat_send_at_ms"],
        ),
        last_heartbeat_at_text,
        reconnect_total: value_u64_opt(normalized, &["reconnect_total"]),
        retry_fail_streak: value_u64_opt(normalized, &["retry_fail_streak", "fail_streak"]),
        retry_backoff_ms: value_u64_opt(normalized, &["retry_backoff_ms"]),
        next_retry_at_ms: value_u64_opt(normalized, &["next_retry_at_ms"]),
        last_error: value_str_opt(normalized, &["last_error"]),
        unavailable_reason: value_str_opt(
            normalized,
            &["unavailable_reason", "bridge_unavailable", "reason"],
        ),
        updated_at_ms: value_u64_opt(normalized, &["updated_at_ms", "ts_ms"])
            .unwrap_or_else(now_ms),
        source: "rpc.session.snapshot".to_string(),
    }
}

fn is_method_unavailable(err: &str) -> bool {
    err.contains("METHOD_NOT_ALLOWED")
        || err.contains("METHOD_NOT_FOUND")
        || err.contains("NOT_IMPLEMENTED")
}

fn apply_session_action(
    supervisor: &mut crate::state::app_state::SupervisorState,
    method: &str,
    success_log_code: &str,
    success_message: &str,
    source: &str,
) -> Result<SessionSnapshot, String> {
    let Some(mut ipc_client) = supervisor.ipc_client.take() else {
        return Err("IPC 未建立连接，无法执行 Bridge 会话动作".to_string());
    };

    let action_result = ipc_client.request(method, json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS);
    match action_result {
        Ok(_) => {
            push_host_log(
                supervisor,
                "info",
                "commands.session",
                success_log_code,
                success_message,
            );
        }
        Err(err) => {
            supervisor.ipc_client = Some(ipc_client);
            if is_method_unavailable(&err) {
                return Err(format!("当前 Agent 未实现 {method}: {err}"));
            }
            return Err(format!("执行 {method} 失败: {err}"));
        }
    }

    let snapshot =
        match ipc_client.request("session.snapshot", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS) {
            Ok(payload) => {
                let mut parsed = parse_session_snapshot(&payload);
                parsed.source = source.to_string();
                parsed
            }
            Err(err) => {
                if is_method_unavailable(&err) {
                    session_unavailable_snapshot(
                        "UNAVAILABLE",
                        &format!("{source}.snapshot_unavailable"),
                        Some(err),
                    )
                } else {
                    session_unavailable_snapshot(
                        "UNAVAILABLE",
                        &format!("{source}.snapshot_failed"),
                        Some(err),
                    )
                }
            }
        };

    supervisor.ipc_client = Some(ipc_client);
    Ok(snapshot)
}

/// Tauri command：读取 Bridge 会话快照（真实链路，不返回 mock 值）。
#[tauri::command]
pub fn session_snapshot(state: State<'_, Arc<AppRuntimeState>>) -> Result<SessionSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "读取会话快照失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.as_mut() else {
            return Ok(session_unavailable_snapshot(
                "DISCONNECTED",
                "host.ipc_disconnected",
                Some("IPC 未建立连接".to_string()),
            ));
        };

        let payload =
            match ipc_client.request("session.snapshot", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS) {
                Ok(value) => value,
                Err(err) => {
                    if is_method_unavailable(&err) {
                        push_host_log(
                            &mut supervisor,
                            "warn",
                            "commands.session",
                            "SESSION_SNAPSHOT_METHOD_NOT_READY",
                            "session.snapshot 尚未可用，当前返回不可用状态",
                        );
                        return Ok(session_unavailable_snapshot(
                            "UNAVAILABLE",
                            "rpc.method_unavailable",
                            Some(err),
                        ));
                    }
                    push_host_log(
                        &mut supervisor,
                        "warn",
                        "commands.session",
                        "SESSION_SNAPSHOT_FAILED",
                        format!("读取 session.snapshot 失败: {err}"),
                    );
                    return Ok(session_unavailable_snapshot(
                        "UNAVAILABLE",
                        "rpc.request_failed",
                        Some(err),
                    ));
                }
            };
        Ok(parse_session_snapshot(&payload))
    })
}

/// Tauri command：请求 Bridge 会话立即重连（仅作用于 Bridge，不触发 Agent 内核重启）。
#[tauri::command]
pub fn session_reconnect(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<SessionSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "执行 session.reconnect 失败：supervisor 锁异常".to_string())?;
        apply_session_action(
            &mut supervisor,
            "session.reconnect",
            "SESSION_RECONNECT_TRIGGERED",
            "已触发 Bridge 会话重连",
            "rpc.session.reconnect",
        )
    })
}

/// Tauri command：请求 Bridge 会话 drain（仅断开 Bridge 会话，不影响 Agent 内核进程）。
#[tauri::command]
pub fn session_drain(state: State<'_, Arc<AppRuntimeState>>) -> Result<SessionSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "执行 session.drain 失败：supervisor 锁异常".to_string())?;
        apply_session_action(
            &mut supervisor,
            "session.drain",
            "SESSION_DRAIN_TRIGGERED",
            "已触发 Bridge 会话断开",
            "rpc.session.drain",
        )
    })
}
