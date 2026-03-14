use serde::Serialize;
use serde_json::{json, Value};
use std::sync::Arc;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{now_ms, push_host_log, with_rpc_metrics, AppRuntimeState};

/// 服务列表项：供前端“服务列表”页面展示。
#[derive(Debug, Clone, Serialize)]
pub struct ServiceListItem {
    pub service_id: String,
    pub service_name: String,
    pub protocol: String,
    pub status: String,
    pub endpoint_count: u64,
    pub last_error: Option<String>,
    pub updated_at_ms: u64,
}

/// 从 JSON 读取字符串字段，缺失时回落到默认值。
fn value_str_or(payload: &Value, keys: &[&str], default_value: &str) -> String {
    for key in keys {
        if let Some(value) = payload.get(*key).and_then(Value::as_str) {
            // 去掉首尾空白，避免前端出现“看似有值但不可读”的脏数据。
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

/// 将 localrpc 返回体解析为服务列表，兼容 array/object 两种外层结构。
fn parse_service_list(payload: &Value) -> Vec<ServiceListItem> {
    let raw_items = if let Some(items) = payload.get("services").and_then(Value::as_array) {
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
            let service_id = value_str_or(item, &["service_id", "id", "service"], "");
            let normalized_service_id = if service_id.is_empty() {
                // 保证列表 key 稳定，不因后端字段缺失导致 React key 冲突。
                format!("service-{}", index + 1)
            } else {
                service_id
            };
            ServiceListItem {
                service_id: normalized_service_id.clone(),
                service_name: value_str_or(
                    item,
                    &["service_name", "name", "display_name"],
                    &normalized_service_id,
                ),
                protocol: value_str_or(item, &["protocol"], "tcp"),
                status: value_str_or(item, &["status"], "unknown"),
                endpoint_count: value_u64_or(item, "endpoint_count", 0),
                last_error: item
                    .get("last_error")
                    .and_then(Value::as_str)
                    .map(|value| value.to_string()),
                updated_at_ms: value_u64_or(item, "updated_at_ms", now_ms()),
            }
        })
        .collect()
}

/// Tauri command：读取服务列表快照。
#[tauri::command]
pub fn service_list_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<ServiceListItem>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "读取服务列表失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.as_mut() else {
            // Agent 尚未连上时返回空列表，避免前端页面直接报错。
            return Ok(Vec::new());
        };

        let payload =
            match ipc_client.request("service.list", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS) {
                Ok(value) => value,
                Err(err) => {
                    if err.contains("METHOD_NOT_ALLOWED") || err.contains("METHOD_NOT_FOUND") {
                        push_host_log(
                            &mut supervisor,
                            "error",
                            "commands.service",
                            "SERVICE_LIST_METHOD_NOT_READY",
                            format!("service.list 尚未在当前 Agent 实现: {err}"),
                        );
                        return Err(format!("当前 Agent 未实现 service.list: {err}"));
                    }
                    return Err(format!("读取服务列表失败: {err}"));
                }
            };
        let items = parse_service_list(&payload);
        push_host_log(
            &mut supervisor,
            "info",
            "commands.service",
            "SERVICE_LIST_SNAPSHOT",
            format!("服务列表快照已刷新，items={}", items.len()),
        );
        Ok(items)
    })
}
