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

/// 从 `endpoints` 数组推断协议字段，兼容 `service_type` 未填场景。
fn infer_protocol_from_endpoints(payload: &Value) -> Option<String> {
    let endpoints = payload.get("endpoints")?.as_array()?;
    for endpoint in endpoints {
        let Some(protocol_value) = endpoint.get("protocol").and_then(Value::as_str) else {
            continue;
        };
        let trimmed = protocol_value.trim();
        if !trimmed.is_empty() {
            return Some(trimmed.to_string());
        }
    }
    None
}

/// 从 `endpoints` 数组推断 endpoint 数量，兼容后端未显式返回 `endpoint_count`。
fn infer_endpoint_count(payload: &Value) -> Option<u64> {
    let endpoints = payload.get("endpoints")?.as_array()?;
    Some(endpoints.len() as u64)
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
            let health_status = value_str_or(item, &["health_status"], "");
            let lifecycle_status = value_str_or(item, &["status"], "unknown");
            let normalized_health = health_status.trim().to_ascii_lowercase();
            let effective_status = if health_status.is_empty() || normalized_health == "unknown" {
                // 健康状态未知时回落到生命周期状态，避免 UI 误判为异常。
                lifecycle_status
            } else {
                health_status
            };
            ServiceListItem {
                service_id: normalized_service_id.clone(),
                service_name: value_str_or(
                    item,
                    &["service_name", "name", "display_name", "service_key"],
                    &normalized_service_id,
                ),
                protocol: infer_protocol_from_endpoints(item)
                    .unwrap_or_else(|| value_str_or(item, &["protocol", "service_type"], "tcp")),
                status: effective_status,
                endpoint_count: infer_endpoint_count(item)
                    .unwrap_or_else(|| value_u64_or(item, "endpoint_count", 0)),
                last_error: item
                    .get("last_error")
                    .and_then(Value::as_str)
                    .map(|value| value.to_string()),
                updated_at_ms: value_u64_or(item, "updated_at_ms", now_ms()),
            }
        })
        .collect()
}

/// 通过短锁方式读取 `service.list`，避免在 IPC 阻塞期间长期占用 supervisor 锁。
fn request_service_list_payload(state: &Arc<AppRuntimeState>) -> Result<Option<Value>, String> {
    let mut ipc_client = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "读取服务列表失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.take() else {
            // Agent 尚未连上时保持空列表语义，不视为错误。
            return Ok(None);
        };
        ipc_client
    };

    // 在无锁状态执行 IPC 请求，防止其他命令被该请求阻塞。
    let request_result =
        ipc_client.request("service.list", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS);

    {
        // 无论请求成功失败都要归还 client，避免后续命令拿不到连接。
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "读取服务列表失败：supervisor 锁异常".to_string())?;
        supervisor.ipc_client = Some(ipc_client);
    }

    match request_result {
        Ok(payload) => Ok(Some(payload)),
        Err(err) => Err(err),
    }
}

/// Tauri command：读取服务列表快照。
#[tauri::command]
pub fn service_list_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<ServiceListItem>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let payload = match request_service_list_payload(&shared) {
            Ok(Some(payload)) => payload,
            Ok(None) => {
                // Agent 尚未连上时返回空列表，保持原有行为。
                return Ok(Vec::new());
            }
            Err(err) => {
                if err.contains("METHOD_NOT_ALLOWED") || err.contains("METHOD_NOT_FOUND") {
                    if let Ok(mut supervisor) = shared.supervisor.lock() {
                        push_host_log(
                            &mut supervisor,
                            "error",
                            "commands.service",
                            "SERVICE_LIST_METHOD_NOT_READY",
                            format!("service.list 尚未在当前 Agent 实现: {err}"),
                        );
                    }
                    return Err(format!("当前 Agent 未实现 service.list: {err}"));
                }
                return Err(format!("读取服务列表失败: {err}"));
            }
        };

        let items = parse_service_list(&payload);
        {
            let mut supervisor = shared
                .supervisor
                .lock()
                .map_err(|_| "读取服务列表失败：supervisor 锁异常".to_string())?;
            push_host_log(
                &mut supervisor,
                "info",
                "commands.service",
                "SERVICE_LIST_SNAPSHOT",
                format!("服务列表快照已刷新，items={}", items.len()),
            );
        }
        Ok(items)
    })
}

#[cfg(test)]
mod tests {
    use super::parse_service_list;
    use serde_json::json;

    /// 验证 `service.list` 可从真实 payload 解析协议、状态与 endpoint 数量。
    #[test]
    fn parse_service_list_from_runtime_payload() {
        let payload = json!({
            "services": [
                {
                    "service_id": "svc-1",
                    "service_name": "order-service",
                    "service_type": "http",
                    "status": "ACTIVE",
                    "health_status": "HEALTHY",
                    "endpoints": [
                        {"protocol": "http", "host": "127.0.0.1", "port": 18080},
                        {"protocol": "http", "host": "127.0.0.1", "port": 18081}
                    ],
                    "updated_at_ms": 1700000000000u64
                }
            ]
        });

        let items = parse_service_list(&payload);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].service_id, "svc-1");
        assert_eq!(items[0].service_name, "order-service");
        assert_eq!(items[0].protocol, "http");
        assert_eq!(items[0].status, "HEALTHY");
        assert_eq!(items[0].endpoint_count, 2);
    }

    /// 验证缺失 `endpoint_count` 时可回退 `endpoints` 数组长度。
    #[test]
    fn parse_service_list_fallback_endpoint_count() {
        let payload = json!([
            {
                "service_id": "svc-2",
                "service_key": "dev/demo/pay-service",
                "status": "ACTIVE",
                "endpoints": [{"protocol": "tcp", "host": "127.0.0.1", "port": 19090}]
            }
        ]);

        let items = parse_service_list(&payload);
        assert_eq!(items.len(), 1);
        assert_eq!(items[0].endpoint_count, 1);
        assert_eq!(items[0].protocol, "tcp");
    }
}
