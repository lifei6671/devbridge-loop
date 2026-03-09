use serde::{Deserialize, Serialize};
use std::collections::HashSet;
use std::fs;
use std::path::PathBuf;
use tauri::{AppHandle, Manager};

const CONFIG_FILE_NAME: &str = "desktop-config.json";

#[derive(Debug, Clone)]
pub struct HostStoragePaths {
    pub config_dir: PathBuf,
    pub log_dir: PathBuf,
    pub config_file: PathBuf,
}

#[derive(Debug, Clone, Serialize, Deserialize)]
#[serde(rename_all = "camelCase")]
struct DesktopConfigFile {
    agent_api_base: Option<String>,
    agent_binary: Option<String>,
    agent_core_dir: Option<String>,
    agent_auto_restart: Option<bool>,
    agent_restart_backoff_ms: Option<Vec<u64>>,
    env_resolve_order: Option<Vec<String>>,
    tunnel_bridge_address: Option<String>,
    tunnel_backflow_base_url: Option<String>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct DesktopConfigView {
    pub agent_api_base: String,
    pub agent_binary: Option<String>,
    pub agent_core_dir: Option<String>,
    pub agent_auto_restart: bool,
    pub agent_restart_backoff_ms: Vec<u64>,
    pub env_resolve_order: Vec<String>,
    pub tunnel_bridge_address: String,
    pub tunnel_backflow_base_url: String,
    pub platform: String,
    pub arch: String,
    pub config_dir: String,
    pub log_dir: String,
    pub config_file: String,
    pub config_loaded: bool,
}

#[derive(Debug, Clone, Deserialize)]
#[serde(rename_all = "camelCase")]
pub struct DesktopConfigSaveRequest {
    pub agent_api_base: String,
    pub agent_binary: Option<String>,
    pub agent_core_dir: Option<String>,
    pub agent_auto_restart: bool,
    pub agent_restart_backoff_ms: Vec<u64>,
    pub env_resolve_order: Vec<String>,
    pub tunnel_bridge_address: String,
    pub tunnel_backflow_base_url: String,
}

pub fn resolve_storage_paths(app: &AppHandle) -> Result<HostStoragePaths, String> {
    let config_dir = app
        .path()
        .app_config_dir()
        .map_err(|err| format!("resolve app config dir failed: {err}"))?;
    let log_dir = app
        .path()
        .app_log_dir()
        .map_err(|err| format!("resolve app log dir failed: {err}"))?;

    // 启动时确保配置目录和日志目录存在，后续读写不再分支判断目录创建。
    fs::create_dir_all(&config_dir).map_err(|err| format!("create config dir failed: {err}"))?;
    fs::create_dir_all(&log_dir).map_err(|err| format!("create log dir failed: {err}"))?;

    Ok(HostStoragePaths {
        config_file: config_dir.join(CONFIG_FILE_NAME),
        config_dir,
        log_dir,
    })
}

pub fn apply_persisted_env_overrides(storage: &HostStoragePaths) -> Result<(), String> {
    let Some(file) = load_config_file(storage)? else {
        return Ok(());
    };

    // 环境变量优先级高于配置文件：只在环境变量未显式设置时补齐默认值。
    set_env_if_absent("DEVLOOP_AGENT_API_BASE", file.agent_api_base.as_deref());
    set_env_if_absent("DEVLOOP_AGENT_BINARY", file.agent_binary.as_deref());
    set_env_if_absent("DEVLOOP_AGENT_CORE_DIR", file.agent_core_dir.as_deref());
    set_env_if_absent(
        "DEVLOOP_AGENT_AUTO_RESTART",
        file.agent_auto_restart
            .map(|value| if value { "true" } else { "false" }),
    );
    set_env_if_absent(
        "DEVLOOP_AGENT_RESTART_BACKOFF_MS",
        file.agent_restart_backoff_ms
            .as_ref()
            .map(|values| join_u64(values)),
    );
    set_env_if_absent(
        "DEVLOOP_ENV_RESOLVE_ORDER",
        file.env_resolve_order.as_ref().map(|values| values.join(",")),
    );
    set_env_if_absent(
        "DEVLOOP_TUNNEL_BRIDGE_ADDRESS",
        file.tunnel_bridge_address.as_deref(),
    );
    set_env_if_absent(
        "DEVLOOP_TUNNEL_BACKFLOW_BASE_URL",
        file.tunnel_backflow_base_url.as_deref(),
    );
    Ok(())
}

pub fn load_desktop_config(storage: &HostStoragePaths) -> Result<DesktopConfigView, String> {
    let file = load_config_file(storage)?;
    Ok(build_view(storage, file.as_ref()))
}

pub fn save_desktop_config(
    storage: &HostStoragePaths,
    request: DesktopConfigSaveRequest,
) -> Result<DesktopConfigView, String> {
    let file = DesktopConfigFile {
        agent_api_base: normalize_required(&request.agent_api_base),
        agent_binary: normalize_optional(request.agent_binary),
        agent_core_dir: normalize_optional(request.agent_core_dir),
        agent_auto_restart: Some(request.agent_auto_restart),
        agent_restart_backoff_ms: Some(normalize_backoff(request.agent_restart_backoff_ms)),
        env_resolve_order: Some(normalize_env_resolve_order(request.env_resolve_order)),
        tunnel_bridge_address: normalize_required(&request.tunnel_bridge_address),
        tunnel_backflow_base_url: normalize_required(&request.tunnel_backflow_base_url),
    };

    save_config_file(storage, &file)?;
    Ok(build_view(storage, Some(&file)))
}

fn load_config_file(storage: &HostStoragePaths) -> Result<Option<DesktopConfigFile>, String> {
    if !storage.config_file.exists() {
        return Ok(None);
    }

    let raw = fs::read_to_string(&storage.config_file)
        .map_err(|err| format!("read desktop config failed: {err}"))?;
    let parsed: DesktopConfigFile =
        serde_json::from_str(&raw).map_err(|err| format!("parse desktop config failed: {err}"))?;
    Ok(Some(parsed))
}

fn save_config_file(storage: &HostStoragePaths, file: &DesktopConfigFile) -> Result<(), String> {
    let encoded = serde_json::to_string_pretty(file)
        .map_err(|err| format!("encode desktop config failed: {err}"))?;
    fs::write(&storage.config_file, encoded)
        .map_err(|err| format!("write desktop config failed: {err}"))?;
    Ok(())
}

fn build_view(storage: &HostStoragePaths, file: Option<&DesktopConfigFile>) -> DesktopConfigView {
    let agent_http_addr =
        std::env::var("DEVLOOP_AGENT_HTTP_ADDR").unwrap_or_else(|_| "127.0.0.1:19090".to_string());
    let default_api_base =
        if agent_http_addr.starts_with("http://") || agent_http_addr.starts_with("https://") {
            agent_http_addr.clone()
        } else {
            format!("http://{}", agent_http_addr)
        };

    // 一处统一处理配置优先级：环境变量 > 持久化文件 > 内置默认值。
    let agent_api_base = first_non_blank(
        std::env::var("DEVLOOP_AGENT_API_BASE").ok(),
        file.and_then(|item| item.agent_api_base.clone()),
        Some(default_api_base.clone()),
    )
    .unwrap_or(default_api_base.clone());

    let agent_binary = first_non_blank(
        std::env::var("DEVLOOP_AGENT_BINARY").ok(),
        file.and_then(|item| item.agent_binary.clone()),
        None,
    );
    let agent_core_dir = first_non_blank(
        std::env::var("DEVLOOP_AGENT_CORE_DIR").ok(),
        file.and_then(|item| item.agent_core_dir.clone()),
        None,
    );

    let agent_auto_restart = std::env::var("DEVLOOP_AGENT_AUTO_RESTART")
        .ok()
        .map(parse_auto_restart)
        .or_else(|| file.and_then(|item| item.agent_auto_restart))
        .unwrap_or(true);

    let agent_restart_backoff_ms = std::env::var("DEVLOOP_AGENT_RESTART_BACKOFF_MS")
        .ok()
        .map(parse_backoff)
        .or_else(|| file.and_then(|item| item.agent_restart_backoff_ms.clone()))
        .map(normalize_backoff)
        .unwrap_or_else(default_backoff);

    let env_resolve_order = std::env::var("DEVLOOP_ENV_RESOLVE_ORDER")
        .ok()
        .map(parse_env_resolve_order)
        .or_else(|| file.and_then(|item| item.env_resolve_order.clone()))
        .map(normalize_env_resolve_order)
        .unwrap_or_else(default_env_resolve_order);

    let tunnel_bridge_address = first_non_blank(
        std::env::var("DEVLOOP_TUNNEL_BRIDGE_ADDRESS").ok(),
        file.and_then(|item| item.tunnel_bridge_address.clone()),
        Some("http://127.0.0.1:18080".to_string()),
    )
    .unwrap_or_else(|| "http://127.0.0.1:18080".to_string());

    let tunnel_backflow_base_url = first_non_blank(
        std::env::var("DEVLOOP_TUNNEL_BACKFLOW_BASE_URL").ok(),
        file.and_then(|item| item.tunnel_backflow_base_url.clone()),
        Some(default_api_base),
    )
    .unwrap_or_else(|| "http://127.0.0.1:19090".to_string());

    DesktopConfigView {
        agent_api_base,
        agent_binary,
        agent_core_dir,
        agent_auto_restart,
        agent_restart_backoff_ms,
        env_resolve_order,
        tunnel_bridge_address,
        tunnel_backflow_base_url,
        platform: std::env::consts::OS.to_string(),
        arch: std::env::consts::ARCH.to_string(),
        config_dir: storage.config_dir.display().to_string(),
        log_dir: storage.log_dir.display().to_string(),
        config_file: storage.config_file.display().to_string(),
        config_loaded: file.is_some(),
    }
}

fn set_env_if_absent<V>(key: &str, value: V)
where
    V: Into<Option<String>>,
{
    if std::env::var_os(key).is_some() {
        return;
    }
    if let Some(value) = value.into() {
        let normalized = value.trim();
        if !normalized.is_empty() {
            std::env::set_var(key, normalized);
        }
    }
}

fn normalize_required(value: &str) -> Option<String> {
    let normalized = value.trim();
    if normalized.is_empty() {
        None
    } else {
        Some(normalized.to_string())
    }
}

fn normalize_optional(value: Option<String>) -> Option<String> {
    value.and_then(|item| {
        let normalized = item.trim();
        if normalized.is_empty() {
            None
        } else {
            Some(normalized.to_string())
        }
    })
}

fn parse_auto_restart(value: String) -> bool {
    let normalized = value.trim().to_ascii_lowercase();
    !matches!(normalized.as_str(), "0" | "false" | "no" | "off")
}

fn parse_backoff(value: String) -> Vec<u64> {
    value
        .split(',')
        .filter_map(|item| item.trim().parse::<u64>().ok())
        .filter(|item| *item > 0)
        .collect::<Vec<_>>()
}

fn default_backoff() -> Vec<u64> {
    vec![500, 1000, 2000, 5000]
}

fn normalize_backoff(values: Vec<u64>) -> Vec<u64> {
    let normalized = values
        .into_iter()
        .filter(|value| *value > 0)
        .collect::<Vec<_>>();
    if normalized.is_empty() {
        return default_backoff();
    }
    normalized
}

fn parse_env_resolve_order(value: String) -> Vec<String> {
    value
        .split(',')
        .map(|item| item.trim().to_string())
        .filter(|item| !item.is_empty())
        .collect::<Vec<_>>()
}

fn default_env_resolve_order() -> Vec<String> {
    vec![
        "requestHeader".to_string(),
        "payload".to_string(),
        "runtimeDefault".to_string(),
        "baseFallback".to_string(),
    ]
}

fn normalize_env_resolve_order(values: Vec<String>) -> Vec<String> {
    let mut normalized = Vec::new();
    let mut seen = HashSet::new();

    // 配置顺序本身有语义，去重时需要保持用户给定的先后顺序。
    for value in values {
        let item = value.trim();
        if item.is_empty() {
            continue;
        }
        if !seen.insert(item.to_string()) {
            continue;
        }
        normalized.push(item.to_string());
    }
    if normalized.is_empty() {
        return default_env_resolve_order();
    }
    normalized
}

fn first_non_blank(
    env_value: Option<String>,
    file_value: Option<String>,
    fallback_value: Option<String>,
) -> Option<String> {
    for value in [env_value, file_value, fallback_value] {
        if let Some(value) = value {
            let normalized = value.trim();
            if !normalized.is_empty() {
                return Some(normalized.to_string());
            }
        }
    }
    None
}

fn join_u64(values: &[u64]) -> String {
    values
        .iter()
        .map(|value| value.to_string())
        .collect::<Vec<_>>()
        .join(",")
}
