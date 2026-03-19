use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::sync::Arc;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{
    now_ms, push_host_log, remove_manual_service_config, upsert_manual_service_config,
    with_rpc_metrics, AppRuntimeState, ManualServiceConfig,
};

/// 服务 scope：前后端统一使用 namespace/environment 二元组表达。
#[derive(Debug, Clone, Default, Deserialize, Serialize)]
pub struct ServiceScope {
    pub namespace: Option<String>,
    pub environment: Option<String>,
}

/// 服务列表项：供前端“服务列表”页面展示。
#[derive(Debug, Clone, Serialize)]
pub struct ServiceListItem {
    pub logical_service_id: String,
    pub instance_id: String,
    pub scope: ServiceScope,
    pub service_name: String,
    pub protocol: String,
    pub host: String,
    pub port: u16,
    pub sni_name: String,
    pub status: String,
    pub endpoint_count: u64,
    pub last_error: Option<String>,
    pub updated_at_ms: u64,
}

/// 新增服务输入体：由前端“服务菜单”提交。
#[derive(Debug, Deserialize)]
pub struct ServiceAddInput {
    pub instance_id: Option<String>,
    pub service_name: String,
    pub scope: Option<ServiceScope>,
    pub protocol: String,
    pub host: String,
    pub port: u16,
    pub sni_name: Option<String>,
}

/// 删除服务输入体：支持按 logical_service_id 或 instance_id 删除。
#[derive(Debug, Deserialize)]
pub struct ServiceDeleteInput {
    pub logical_service_id: Option<String>,
    pub instance_id: Option<String>,
    pub scope: Option<ServiceScope>,
    pub service_name: Option<String>,
}

/// 删除服务返回体：供前端刷新与提示使用。
#[derive(Debug, Serialize)]
pub struct ServiceDeleteResult {
    pub accepted: bool,
    pub deleted: bool,
    pub logical_service_id: String,
    pub instance_id: String,
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

/// 从 JSON 读取 u16 字段，超出范围或缺失时回落默认值。
fn value_u16_or(payload: &Value, key: &str, default_value: u16) -> u16 {
    payload
        .get(key)
        .and_then(Value::as_u64)
        .and_then(|value| u16::try_from(value).ok())
        .unwrap_or(default_value)
}

fn value_scope(payload: &Value, key: &str) -> ServiceScope {
    let Some(scope_payload) = payload.get(key) else {
        return ServiceScope::default();
    };
    ServiceScope {
        namespace: scope_payload
            .get("namespace")
            .and_then(Value::as_str)
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty()),
        environment: scope_payload
            .get("environment")
            .and_then(Value::as_str)
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty()),
    }
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

/// 从 `endpoints` 数组推断首个 endpoint 的 host/port。
fn infer_primary_endpoint_host_port(payload: &Value) -> Option<(String, u16)> {
    let endpoints = payload.get("endpoints")?.as_array()?;
    for endpoint in endpoints {
        let host = endpoint
            .get("host")
            .and_then(Value::as_str)
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty())
            .unwrap_or_default();
        let port = endpoint
            .get("port")
            .and_then(Value::as_u64)
            .and_then(|value| u16::try_from(value).ok())
            .unwrap_or(0);
        if host.is_empty() && port == 0 {
            continue;
        }
        return Some((host, port));
    }
    None
}

/// 从 `endpoints` 数组推断首个 endpoint 的 sni/server_name。
fn infer_primary_endpoint_sni(payload: &Value) -> Option<String> {
    let endpoints = payload.get("endpoints")?.as_array()?;
    for endpoint in endpoints {
        let server_name = endpoint
            .get("server_name")
            .or_else(|| endpoint.get("sni_name"))
            .and_then(Value::as_str)
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty());
        if server_name.is_some() {
            return server_name;
        }
    }
    None
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
            let logical_service_id = value_str_or(item, &["logical_service_id"], "");
            let instance_id = value_str_or(item, &["instance_id"], "");
            let normalized_instance_id = if instance_id.is_empty() {
                format!("service-{}", index + 1)
            } else {
                instance_id
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
            let inferred_host_port = infer_primary_endpoint_host_port(item);
            let inferred_sni_name = infer_primary_endpoint_sni(item);
            ServiceListItem {
                logical_service_id,
                instance_id: normalized_instance_id.clone(),
                scope: value_scope(item, "scope"),
                service_name: value_str_or(
                    item,
                    &["service_name", "name", "display_name"],
                    &normalized_instance_id,
                ),
                protocol: infer_protocol_from_endpoints(item)
                    .unwrap_or_else(|| value_str_or(item, &["protocol", "service_type"], "tcp")),
                host: inferred_host_port
                    .as_ref()
                    .map(|(host, _)| host.clone())
                    .unwrap_or_else(|| value_str_or(item, &["host"], "127.0.0.1")),
                port: inferred_host_port
                    .map(|(_, port)| port)
                    .unwrap_or_else(|| value_u16_or(item, "port", 8080)),
                sni_name: inferred_sni_name
                    .unwrap_or_else(|| value_str_or(item, &["sni_name", "server_name"], "")),
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

fn validate_add_input(input: ServiceAddInput) -> Result<ServiceAddInput, String> {
    let service_name = input.service_name.trim().to_string();
    if service_name.is_empty() {
        return Err("service_name 不能为空".to_string());
    }
    let protocol = input.protocol.trim().to_ascii_lowercase();
    if protocol.is_empty() {
        return Err("protocol 不能为空".to_string());
    }
    let host = input.host.trim().to_string();
    if host.is_empty() {
        return Err("host 不能为空".to_string());
    }
    if input.port == 0 {
        return Err("port 必须大于 0".to_string());
    }
    let input_scope = input.scope.unwrap_or_default();
    let normalized_namespace = normalize_optional_trimmed_text(input_scope.namespace);
    let normalized_environment = normalize_optional_trimmed_text(input_scope.environment);
    let normalized_sni_name = normalize_optional_trimmed_text(input.sni_name);
    if normalized_namespace.is_none() && normalized_environment.is_none() && normalized_sni_name.is_none() {
        return Err("scope.namespace、scope.environment、sni_name 至少填写一个".to_string());
    }
    Ok(ServiceAddInput {
        instance_id: input
            .instance_id
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty()),
        service_name,
        scope: Some(ServiceScope {
            namespace: normalized_namespace,
            environment: normalized_environment,
        }),
        protocol,
        host,
        port: input.port,
        sni_name: normalized_sni_name,
    })
}

fn normalize_optional_trimmed_text(value: Option<String>) -> Option<String> {
    value.map(|item| item.trim().to_string()).filter(|item| !item.is_empty())
}

fn validate_delete_input(input: ServiceDeleteInput) -> Result<ServiceDeleteInput, String> {
    let logical_service_id = normalize_optional_trimmed_text(input.logical_service_id);
    let instance_id = normalize_optional_trimmed_text(input.instance_id);
    if logical_service_id.is_none() && instance_id.is_none() {
        return Err("logical_service_id 或 instance_id 至少填写一个".to_string());
    }
    let normalized_scope = input.scope.unwrap_or_default();
    Ok(ServiceDeleteInput {
        logical_service_id,
        instance_id,
        scope: Some(ServiceScope {
            namespace: normalize_optional_trimmed_text(normalized_scope.namespace),
            environment: normalize_optional_trimmed_text(normalized_scope.environment),
        }),
        service_name: normalize_optional_trimmed_text(input.service_name),
    })
}

fn request_service_add_payload(
    state: &Arc<AppRuntimeState>,
    input: &ServiceAddInput,
) -> Result<Option<Value>, String> {
    let mut ipc_client = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "新增服务失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.take() else {
            return Ok(None);
        };
        ipc_client
    };

    let request_result = ipc_client.request(
        "service.add",
        json!({
            "instance_id": input.instance_id,
            "service_name": input.service_name,
            "scope": input.scope,
            "protocol": input.protocol,
            "host": input.host,
            "port": input.port,
            "sni_name": input.sni_name,
        }),
        LOCAL_RPC_DEFAULT_TIMEOUT_MS,
    );

    {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "新增服务失败：supervisor 锁异常".to_string())?;
        supervisor.ipc_client = Some(ipc_client);
    }

    match request_result {
        Ok(payload) => Ok(Some(payload)),
        Err(err) => Err(err),
    }
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

fn request_service_delete_payload(
    state: &Arc<AppRuntimeState>,
    input: &ServiceDeleteInput,
) -> Result<Option<Value>, String> {
    let mut ipc_client = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "删除服务失败：supervisor 锁异常".to_string())?;
        let Some(ipc_client) = supervisor.ipc_client.take() else {
            return Ok(None);
        };
        ipc_client
    };

    let request_result = ipc_client.request(
        "service.delete",
        json!({
            "logical_service_id": input.logical_service_id,
            "instance_id": input.instance_id,
        }),
        LOCAL_RPC_DEFAULT_TIMEOUT_MS,
    );

    {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "删除服务失败：supervisor 锁异常".to_string())?;
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

/// Tauri command：新增服务并返回新增后的服务项。
#[tauri::command]
pub fn service_add(
    input: ServiceAddInput,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<ServiceListItem, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let normalized_input = validate_add_input(input)?;
        let payload = match request_service_add_payload(&shared, &normalized_input) {
            Ok(Some(payload)) => payload,
            Ok(None) => return Err("Agent 尚未连接，无法新增服务".to_string()),
            Err(err) => {
                if err.contains("METHOD_NOT_ALLOWED") || err.contains("METHOD_NOT_FOUND") {
                    if let Ok(mut supervisor) = shared.supervisor.lock() {
                        push_host_log(
                            &mut supervisor,
                            "error",
                            "commands.service",
                            "SERVICE_ADD_METHOD_NOT_READY",
                            format!("service.add 尚未在当前 Agent 实现: {err}"),
                        );
                    }
                    return Err(format!("当前 Agent 未实现 service.add: {err}"));
                }
                return Err(format!("新增服务失败: {err}"));
            }
        };

        let mut parsed_items = parse_service_list(&json!([payload]));
        let Some(added_item) = parsed_items.pop() else {
            return Err("新增服务失败：解析响应为空".to_string());
        };
        let persisted_size = upsert_manual_service_config(
            &shared,
            ManualServiceConfig {
                instance_id: if added_item.instance_id.trim().is_empty() {
                    normalized_input.instance_id.clone()
                } else {
                    Some(added_item.instance_id.clone())
                },
                namespace: normalized_input
                    .scope
                    .as_ref()
                    .and_then(|scope| scope.namespace.clone()),
                environment: normalized_input
                    .scope
                    .as_ref()
                    .and_then(|scope| scope.environment.clone()),
                service_name: normalized_input.service_name.clone(),
                protocol: normalized_input.protocol.clone(),
                host: normalized_input.host.clone(),
                port: normalized_input.port,
                sni_name: normalized_input.sni_name.clone(),
            },
        )?;

        {
            let mut supervisor = shared
                .supervisor
                .lock()
                .map_err(|_| "新增服务失败：supervisor 锁异常".to_string())?;
            push_host_log(
                &mut supervisor,
                "info",
                "commands.service",
                "SERVICE_ADD_SUCCEEDED",
                format!(
                    "服务新增成功 logical_service_id={} instance_id={} service_name={} endpoint_count={} persisted_manual_services={}",
                    added_item.logical_service_id,
                    added_item.instance_id,
                    added_item.service_name,
                    added_item.endpoint_count,
                    persisted_size
                ),
            );
        }

        Ok(added_item)
    })
}

/// Tauri command：删除服务，并同步清理持久化手动服务配置。
#[tauri::command]
pub fn service_delete(
    input: ServiceDeleteInput,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<ServiceDeleteResult, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let normalized_input = validate_delete_input(input)?;
        let payload = match request_service_delete_payload(&shared, &normalized_input) {
            Ok(Some(payload)) => payload,
            Ok(None) => return Err("Agent 尚未连接，无法删除服务".to_string()),
            Err(err) => {
                if err.contains("METHOD_NOT_ALLOWED") || err.contains("METHOD_NOT_FOUND") {
                    if let Ok(mut supervisor) = shared.supervisor.lock() {
                        push_host_log(
                            &mut supervisor,
                            "error",
                            "commands.service",
                            "SERVICE_DELETE_METHOD_NOT_READY",
                            format!("service.delete 尚未在当前 Agent 实现: {err}"),
                        );
                    }
                    return Err(format!("当前 Agent 未实现 service.delete: {err}"));
                }
                return Err(format!("删除服务失败: {err}"));
            }
        };

        let accepted = payload
            .get("accepted")
            .and_then(Value::as_bool)
            .unwrap_or(true);
        let deleted = payload
            .get("deleted")
            .and_then(Value::as_bool)
            .unwrap_or(false);
        let result_logical_service_id = value_str_or(
            &payload,
            &["logical_service_id"],
            normalized_input.logical_service_id.as_deref().unwrap_or(""),
        );
        let result_instance_id = value_str_or(
            &payload,
            &["instance_id"],
            normalized_input.instance_id.as_deref().unwrap_or(""),
        );
        let updated_at_ms = value_u64_or(&payload, "updated_at_ms", now_ms());
        let persisted_size = remove_manual_service_config(
            &shared,
            result_instance_id.as_str(),
            normalized_input
                .scope
                .as_ref()
                .and_then(|scope| scope.namespace.as_deref()),
            normalized_input
                .scope
                .as_ref()
                .and_then(|scope| scope.environment.as_deref()),
            normalized_input.service_name.as_deref(),
        )?;

        {
            let mut supervisor = shared
                .supervisor
                .lock()
                .map_err(|_| "删除服务失败：supervisor 锁异常".to_string())?;
            push_host_log(
                &mut supervisor,
                "info",
                "commands.service",
                "SERVICE_DELETE_SUCCEEDED",
                format!(
                    "服务删除已处理 logical_service_id={} instance_id={} deleted={} persisted_manual_services={}",
                    result_logical_service_id, result_instance_id, deleted, persisted_size
                ),
            );
        }

        Ok(ServiceDeleteResult {
            accepted,
            deleted,
            logical_service_id: result_logical_service_id,
            instance_id: result_instance_id,
            updated_at_ms,
        })
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
                    "logical_service_id": "ls-1",
                    "instance_id": "inst-1",
                    "scope": {"namespace": "dev", "environment": "demo"},
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
        assert_eq!(items[0].logical_service_id, "ls-1");
        assert_eq!(items[0].instance_id, "inst-1");
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
                "logical_service_id": "ls-2",
                "instance_id": "inst-2",
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
