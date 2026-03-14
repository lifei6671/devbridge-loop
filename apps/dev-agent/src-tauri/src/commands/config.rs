use serde::Deserialize;
use std::path::Path;
use std::path::PathBuf;
use std::sync::Arc;
use tauri::State;

use crate::agent_host::launcher::default_ipc_endpoint;
use crate::state::app_state::{
    current_host_config_snapshot, current_runtime_config, now_ms, push_host_log,
    update_runtime_config, with_rpc_metrics, AppRuntimeState, HostConfigSnapshot,
    HostRuntimeConfig,
};

/// 可更新的宿主运行配置输入体（均为真实可生效字段）。
#[derive(Debug, Deserialize)]
pub struct HostConfigUpdateInput {
    pub runtime_program: String,
    pub runtime_args: Vec<String>,
    pub agent_id: String,
    pub bridge_addr: String,
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_endpoint: String,
}

/// 校验运行程序路径：必须是非空字符串。
fn validate_runtime_program(value: &str) -> Result<PathBuf, String> {
    let normalized = value.trim();
    if normalized.is_empty() {
        return Err("runtime_program 不能为空".to_string());
    }
    Ok(PathBuf::from(normalized))
}

/// 规范化启动参数：去掉空白项，避免传入无意义参数。
fn normalize_runtime_args(args: &[String]) -> Vec<String> {
    args.iter()
        .map(|value| value.trim())
        .filter(|value| !value.is_empty())
        .map(|value| value.to_string())
        .collect()
}

/// 校验必填字符串：去掉空白后不可为空。
fn validate_required_text(field_name: &str, value: &str) -> Result<String, String> {
    let normalized = value.trim();
    if normalized.is_empty() {
        return Err(format!("{field_name} 不能为空"));
    }
    Ok(normalized.to_string())
}

/// 校验 IPC 端点：为空时自动回填当前 transport 的默认值。
fn validate_ipc_endpoint(transport: &str, endpoint: &str) -> Result<String, String> {
    let normalized = endpoint.trim();
    if normalized.is_empty() {
        // 避免保存空值导致后续启动失败，这里直接回填默认端点。
        return Ok(default_ipc_endpoint(transport));
    }
    if transport == "named_pipe" {
        if !normalized.starts_with(r"\\.\pipe\") {
            return Err("named_pipe 端点必须以 \\\\.\\pipe\\ 开头".to_string());
        }
        if normalized.contains('/') {
            return Err("named_pipe 端点不能包含 '/'".to_string());
        }
        return Ok(normalized.to_string());
    }
    if transport == "uds" {
        let endpoint_path = Path::new(normalized);
        let parent_dir = endpoint_path
            .parent()
            .filter(|parent| !parent.as_os_str().is_empty())
            .ok_or_else(|| "uds 端点必须包含父目录路径".to_string())?;
        if endpoint_path
            .file_name()
            .filter(|name| !name.is_empty())
            .is_none()
        {
            return Err("uds 端点必须包含 socket 文件名".to_string());
        }
        if parent_dir
            .file_name()
            .filter(|name| !name.is_empty())
            .is_none()
            && !parent_dir.is_absolute()
        {
            return Err("uds 端点父目录路径不能为空".to_string());
        }
        return Ok(normalized.to_string());
    }
    Err(format!("不支持的 ipc_transport={transport}"))
}

/// Tauri command：获取宿主配置快照。
#[tauri::command]
pub fn host_config_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<HostConfigSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || current_host_config_snapshot(&shared))
}

/// Tauri command：更新宿主运行配置（重启 Agent 后生效）。
#[tauri::command]
pub fn host_config_update(
    input: HostConfigUpdateInput,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<HostConfigSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let runtime_program = validate_runtime_program(&input.runtime_program)?;
        let runtime_args = normalize_runtime_args(&input.runtime_args);
        let agent_id = validate_required_text("agent_id", &input.agent_id)?;
        let bridge_addr = validate_required_text("bridge_addr", &input.bridge_addr)?;
        let ipc_transport = current_runtime_config(&shared)?.ipc_transport;
        let ipc_endpoint = validate_ipc_endpoint(&ipc_transport, &input.ipc_endpoint)?;

        // IPC 传输方式由平台决定，这里仅允许更新端点，不允许切 transport。
        let next_runtime_config = HostRuntimeConfig {
            runtime_program,
            runtime_args,
            agent_id,
            bridge_addr,
            tunnel_pool_min_idle: input.tunnel_pool_min_idle,
            tunnel_pool_max_idle: input.tunnel_pool_max_idle,
            tunnel_pool_max_inflight: input.tunnel_pool_max_inflight,
            tunnel_pool_ttl_ms: input.tunnel_pool_ttl_ms,
            tunnel_pool_open_rate: input.tunnel_pool_open_rate,
            tunnel_pool_open_burst: input.tunnel_pool_open_burst,
            tunnel_pool_reconcile_gap_ms: input.tunnel_pool_reconcile_gap_ms,
            ipc_transport: ipc_transport.clone(),
            ipc_endpoint: ipc_endpoint.clone(),
        };
        // 统一复用 HostRuntimeConfig 的校验口径，避免与启动校验漂移。
        next_runtime_config.validate_agent_runtime_fields()?;
        update_runtime_config(&shared, next_runtime_config)?;

        {
            let mut supervisor = shared
                .supervisor
                .lock()
                .map_err(|_| "更新配置失败：supervisor 锁异常".to_string())?;
            push_host_log(
                &mut supervisor,
                "info",
                "commands.config",
                "HOST_CONFIG_UPDATED",
                format!(
                    "配置已更新 runtime_program={} agent_id={} bridge_addr={} \
tunnel_pool(min_idle={},max_idle={},max_inflight={},ttl_ms={},open_rate={},open_burst={},reconcile_gap_ms={}) \
ipc_transport={} ipc_endpoint={} ts={}",
                    input.runtime_program.trim(),
                    input.agent_id.trim(),
                    input.bridge_addr.trim(),
                    input.tunnel_pool_min_idle,
                    input.tunnel_pool_max_idle,
                    input.tunnel_pool_max_inflight,
                    input.tunnel_pool_ttl_ms,
                    input.tunnel_pool_open_rate,
                    input.tunnel_pool_open_burst,
                    input.tunnel_pool_reconcile_gap_ms,
                    ipc_transport,
                    ipc_endpoint,
                    now_ms()
                ),
            );
        }

        current_host_config_snapshot(&shared)
    })
}

#[cfg(test)]
mod tests {
    use super::validate_ipc_endpoint;

    #[test]
    fn validate_ipc_endpoint_rejects_uds_without_parent_dir() {
        let result = validate_ipc_endpoint("uds", "agent.sock");
        assert!(result.is_err());
    }

    #[test]
    fn validate_ipc_endpoint_accepts_uds_with_parent_dir() {
        let result = validate_ipc_endpoint("uds", "/tmp/dev-agent/agent.sock");
        assert!(result.is_ok());
    }
}
