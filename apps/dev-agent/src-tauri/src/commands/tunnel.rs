use serde::Serialize;
use serde_json::{json, Value};
use std::sync::Arc;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{now_ms, push_host_log, with_rpc_metrics, AppRuntimeState};

/// 通道列表项：供前端“通道列表”页面展示。
#[derive(Debug, Clone, Serialize)]
pub struct TunnelListItem {
    pub tunnel_id: String,
    pub service_id: String,
    pub state: String,
    pub local_addr: String,
    pub remote_addr: String,
    pub latency_ms: u64,
    pub last_error: Option<String>,
    pub updated_at_ms: u64,
}

/// 从 JSON 读取字符串字段，缺失时回落到默认值。
fn value_str_or(payload: &Value, keys: &[&str], default_value: &str) -> String {
    for key in keys {
        if let Some(value) = payload.get(*key).and_then(Value::as_str) {
            // 去掉首尾空白，避免展示“空白字符串”污染 UI。
            let trimmed = value.trim();
            if !trimmed.is_empty() {
                return trimmed.to_string();
            }
        }
    }
    default_value.to_string()
}

/// 从 JSON 读取无符号整数字段，缺失时回落到默认值。
fn value_u64_or(payload: &Value, key: &str, default_value: u64) -> u64 {
    payload
        .get(key)
        .and_then(Value::as_u64)
        .unwrap_or(default_value)
}

/// 将 localrpc 返回体解析为通道列表，兼容 array/object 两种外层结构。
fn parse_tunnel_list(payload: &Value) -> Vec<TunnelListItem> {
    let raw_items = if let Some(items) = payload.get("tunnels").and_then(Value::as_array) {
        items.clone()
    } else if let Some(items) = payload.as_array() {
        items.clone()
    } else {
        Vec::new()
    };

    raw_items
        .iter()
        .enumerate()
        .map(|(index, item)| {
            let tunnel_id = value_str_or(item, &["tunnel_id", "id"], "");
            let normalized_tunnel_id = if tunnel_id.is_empty() {
                // 兜底构造可追踪 id，避免列表渲染时丢失 key。
                format!("tunnel-{}", index + 1)
            } else {
                tunnel_id
            };
            TunnelListItem {
                tunnel_id: normalized_tunnel_id,
                service_id: value_str_or(item, &["service_id", "service"], "--"),
                state: value_str_or(item, &["state", "status"], "unknown"),
                local_addr: value_str_or(item, &["local_addr", "local_target"], "--"),
                remote_addr: value_str_or(item, &["remote_addr", "public_url"], "--"),
                latency_ms: value_u64_or(item, "latency_ms", 0),
                last_error: item
                    .get("last_error")
                    .and_then(Value::as_str)
                    .map(|value| value.to_string()),
                updated_at_ms: value_u64_or(item, "updated_at_ms", now_ms()),
            }
        })
        .collect()
}

/// Tauri command：读取通道列表快照。
#[tauri::command]
pub fn tunnel_list_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<TunnelListItem>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "读取通道列表失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.as_mut() else {
            // Agent 尚未连上时返回空列表，避免前端页面直接报错。
            return Ok(Vec::new());
        };

        let payload =
            match ipc_client.request("tunnel.list", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS) {
                Ok(value) => value,
                Err(err) => {
                    if err.contains("METHOD_NOT_ALLOWED") {
                        // 真实 Agent 尚未落地该方法时，回退为空列表并记录告警。
                        push_host_log(
                            &mut supervisor,
                            "warn",
                            "commands.tunnel",
                            "TUNNEL_LIST_METHOD_NOT_READY",
                            "tunnel.list 尚未可用，已回退为空列表",
                        );
                        return Ok(Vec::new());
                    }
                    return Err(format!("读取通道列表失败: {err}"));
                }
            };
        let items = parse_tunnel_list(&payload);
        push_host_log(
            &mut supervisor,
            "info",
            "commands.tunnel",
            "TUNNEL_LIST_SNAPSHOT",
            format!("通道列表快照已刷新，items={}", items.len()),
        );
        Ok(items)
    })
}
