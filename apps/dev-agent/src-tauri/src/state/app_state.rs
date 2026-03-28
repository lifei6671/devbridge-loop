use serde::{Deserialize, Serialize};
use serde_json::{json, Value};
use std::collections::VecDeque;
use std::fs::{self, File};
use std::path::{Path, PathBuf};
use std::process::Child;
use std::sync::atomic::AtomicBool;
use std::sync::{Arc, Mutex};
use std::time::{Duration, Instant, SystemTime, UNIX_EPOCH};

use crate::agent_host::ipc_client::{LocalRpcClient, LOCAL_RPC_DEFAULT_TIMEOUT_MS};
use crate::agent_host::launcher::default_ipc_endpoint;
use crate::agent_host::router::{ALLOWED_METHOD_DOMAINS, DENIED_LOW_LEVEL_METHODS};

/// 前端统一订阅的宿主事件名。
pub const EVENT_AGENT_RUNTIME_CHANGED: &str = "agent-runtime-changed";
/// 宿主事件桥节流窗口。
pub const EVENT_THROTTLE_MS: u64 = 120;
/// Supervisor 后台轮询间隔。
pub const SUPERVISOR_POLL_MS: u64 = 320;
/// 停止 Agent 的最大优雅等待时长。
pub const STOP_TIMEOUT_MS: u64 = 1200;

const MAX_RPC_LATENCY_SAMPLES: usize = 32;
const MAX_HOST_LOG_ENTRIES: usize = 256;
const HOST_CONFIG_DIR_NAME: &str = ".dev-agent";
const HOST_CONFIG_FILE_NAME: &str = "host-runtime-config.yaml";

fn default_bridge_transport() -> String {
    "tcp_framed".to_string()
}

fn default_bridge_addr_for_transport(bridge_transport: &str) -> &'static str {
    match bridge_transport.trim() {
        "grpc_h2" => "127.0.0.1:39082",
        "quic_native" => "127.0.0.1:39083",
        _ => "127.0.0.1:39081",
    }
}

fn is_supported_bridge_transport(bridge_transport: &str) -> bool {
    matches!(bridge_transport, "tcp_framed" | "grpc_h2" | "quic_native")
}

fn default_diagnose_category_enabled() -> bool {
    true
}

/// 手动添加服务配置：持久化在宿主配置文件，供 Agent 重启后自动恢复。
#[derive(Debug, Clone, Serialize, Deserialize)]
pub struct ManualServiceConfig {
    #[serde(default)]
    pub instance_id: Option<String>,
    #[serde(default)]
    pub namespace: Option<String>,
    #[serde(default)]
    pub environment: Option<String>,
    pub service_name: String,
    pub protocol: String,
    pub host: String,
    pub port: u16,
    #[serde(default)]
    pub sni_name: Option<String>,
    #[serde(default)]
    pub exposure: Option<Value>,
    #[serde(default)]
    pub route_hint: Option<Value>,
}

impl ManualServiceConfig {
    /// 归一化手动服务配置；字段不合法时返回 `None`。
    pub fn normalized(&self) -> Option<Self> {
        let service_name = self.service_name.trim().to_string();
        let protocol = self.protocol.trim().to_ascii_lowercase();
        let host = self.host.trim().to_string();
        if service_name.is_empty() || protocol.is_empty() || host.is_empty() || self.port == 0 {
            return None;
        }
        let normalized_namespace = self
            .namespace
            .as_ref()
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty());
        let normalized_environment = self
            .environment
            .as_ref()
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty());
        let normalized_sni_name = self
            .sni_name
            .as_ref()
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty());
        if normalized_namespace.is_none() || normalized_environment.is_none() {
            return None;
        }
        Some(Self {
            instance_id: self
                .instance_id
                .as_ref()
                .map(|value| value.trim().to_string())
                .filter(|value| !value.is_empty()),
            namespace: normalized_namespace,
            environment: normalized_environment,
            service_name,
            protocol,
            host,
            port: self.port,
            sni_name: normalized_sni_name,
            exposure: self
                .exposure
                .as_ref()
                .filter(|value| !value.is_null())
                .cloned(),
            route_hint: self
                .route_hint
                .as_ref()
                .filter(|value| !value.is_null())
                .cloned(),
        })
    }
}

/// 判断两个手动服务配置是否指向同一条逻辑服务记录。
/// 优先按 instance_id，对无 instance_id 的配置按 namespace/environment/service_name 组合匹配。
fn same_manual_service_identity(left: &ManualServiceConfig, right: &ManualServiceConfig) -> bool {
    match (&left.instance_id, &right.instance_id) {
        (Some(left_id), Some(right_id)) => left_id == right_id,
        _ => {
            left.namespace == right.namespace
                && left.environment == right.environment
                && left.service_name == right.service_name
        }
    }
}

/// YAML 文件中的宿主配置格式（用户目录持久化）。
#[derive(Debug, Clone, Serialize, Deserialize)]
struct PersistedHostRuntimeConfig {
    pub runtime_program: String,
    #[serde(default)]
    pub runtime_args: Vec<String>,
    pub agent_id: String,
    pub bridge_addr: String,
    #[serde(default = "default_bridge_transport")]
    pub bridge_transport: String,
    #[serde(default)]
    pub bridge_tls_enabled: bool,
    #[serde(default)]
    pub bridge_tls_root_ca_file: String,
    #[serde(default)]
    pub bridge_tls_server_name: String,
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_endpoint: String,
    #[serde(default = "default_diagnose_category_enabled")]
    pub diagnose_show_ipc: bool,
    #[serde(default = "default_diagnose_category_enabled")]
    pub diagnose_show_bridge: bool,
    #[serde(default = "default_diagnose_category_enabled")]
    pub diagnose_show_tunnel: bool,
    #[serde(default)]
    pub manual_services: Vec<ManualServiceConfig>,
}

/// 宿主期望状态：用于决定是否应保持 Agent 运行。
#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum DesiredState {
    Running,
    Stopped,
}

impl Default for DesiredState {
    /// 提供默认值，确保宿主初始状态可预测。
    fn default() -> Self {
        Self::Stopped
    }
}

/// 退出语义：用于区分主动停止与异常退出。
#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ExitKind {
    Expected,
    Unexpected,
}

impl Default for ExitKind {
    /// 默认视为预期退出，避免首次启动前出现误告警。
    fn default() -> Self {
        Self::Expected
    }
}

/// 连接状态：用于表达 IPC 生命周期阶段。
#[derive(Debug, Clone, Copy, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ConnectionState {
    Disconnected,
    Reconnecting,
    Resyncing,
    Connected,
}

impl Default for ConnectionState {
    /// 默认是离线态，等待 bootstrap 拉起流程。
    fn default() -> Self {
        Self::Disconnected
    }
}

/// 宿主观测指标快照：与 A15 指标命名保持一致。
#[derive(Debug, Clone, Serialize)]
pub struct HostMetricsSnapshot {
    pub agent_host_ipc_connected: bool,
    pub agent_host_ipc_reconnect_total: u64,
    pub agent_host_rpc_latency_ms: f64,
    pub agent_host_supervisor_restart_total: u64,
    pub agent_bridge_last_heartbeat_at_ms: Option<u64>,
    pub agent_bridge_next_retry_at_ms: Option<u64>,
    pub agent_bridge_retry_backoff_ms: u64,
    pub agent_bridge_retry_fail_streak: u32,
    pub agent_bridge_last_reconnect_error: Option<String>,
}

/// 宿主结构化日志条目：用于 UI 展示与本地诊断。
#[derive(Debug, Clone, Serialize)]
pub struct HostLogEntry {
    pub ts_ms: u64,
    pub level: String,
    pub module: String,
    pub code: String,
    pub message: String,
}

/// 对前端公开的运行态快照。
#[derive(Debug, Clone, Serialize)]
pub struct AgentRuntimeSnapshot {
    pub desired_state: DesiredState,
    pub exit_kind: ExitKind,
    pub connection_state: ConnectionState,
    pub process_alive: bool,
    pub pid: Option<u32>,
    pub started_at_ms: Option<u64>,
    pub updated_at_ms: u64,
    pub last_error: Option<String>,
    pub metrics: HostMetricsSnapshot,
}

/// 前端订阅的统一事件模型。
#[derive(Debug, Clone, Serialize)]
pub struct AgentRuntimeChangedEvent {
    pub schema_version: u8,
    pub reason: String,
    pub dropped_event_count: u64,
    pub snapshot: AgentRuntimeSnapshot,
}

/// 宿主运行配置快照。
#[derive(Debug, Clone, Serialize)]
pub struct HostConfigSnapshot {
    pub runtime_program: String,
    pub runtime_args: Vec<String>,
    pub agent_id: String,
    pub bridge_addr: String,
    pub bridge_transport: String,
    pub bridge_tls_enabled: bool,
    pub bridge_tls_root_ca_file: String,
    pub bridge_tls_server_name: String,
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_transport: String,
    pub ipc_endpoint: String,
    pub diagnose_show_ipc: bool,
    pub diagnose_show_bridge: bool,
    pub diagnose_show_tunnel: bool,
    pub allowed_method_domains: Vec<String>,
    pub denied_low_level_methods: Vec<String>,
}

/// bootstrap 返回体：一次性返回运行态与配置态。
#[derive(Debug, Clone, Serialize)]
pub struct AppBootstrapPayload {
    pub snapshot: AgentRuntimeSnapshot,
    pub host_config: HostConfigSnapshot,
}

/// 单实例锁：通过持有锁文件句柄保证实例互斥。
pub struct SingleInstanceGuard {
    pub _file: File,
    pub path: PathBuf,
}

impl Drop for SingleInstanceGuard {
    /// 进程退出时清理锁文件，避免下次启动被误拦截。
    fn drop(&mut self) {
        // 尽力删除即可，不将失败升级为崩溃。
        let _ = fs::remove_file(&self.path);
    }
}

/// 宿主运行配置：定义如何拉起 Agent 子进程。
#[derive(Debug, Clone)]
pub struct HostRuntimeConfig {
    pub runtime_program: PathBuf,
    pub runtime_args: Vec<String>,
    pub agent_id: String,
    pub bridge_addr: String,
    pub bridge_transport: String,
    pub bridge_tls_enabled: bool,
    pub bridge_tls_root_ca_file: String,
    pub bridge_tls_server_name: String,
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_transport: String,
    pub ipc_endpoint: String,
    pub diagnose_show_ipc: bool,
    pub diagnose_show_bridge: bool,
    pub diagnose_show_tunnel: bool,
    pub manual_services: Vec<ManualServiceConfig>,
}

/// 解析配置文件目录：优先用户目录，失败时回落当前目录。
fn resolve_user_config_dir() -> PathBuf {
    #[cfg(windows)]
    {
        if let Ok(user_profile) = std::env::var("USERPROFILE") {
            let trimmed = user_profile.trim();
            if !trimmed.is_empty() {
                return PathBuf::from(trimmed).join(HOST_CONFIG_DIR_NAME);
            }
        }
    }
    if let Ok(home) = std::env::var("HOME") {
        let trimmed = home.trim();
        if !trimmed.is_empty() {
            return PathBuf::from(trimmed).join(HOST_CONFIG_DIR_NAME);
        }
    }
    std::env::current_dir()
        .unwrap_or_else(|_| PathBuf::from("."))
        .join(HOST_CONFIG_DIR_NAME)
}

/// 返回宿主配置文件路径（YAML）。
pub fn host_runtime_config_yaml_path() -> PathBuf {
    resolve_user_config_dir().join(HOST_CONFIG_FILE_NAME)
}

impl HostRuntimeConfig {
    /// 读取字符串环境变量，空值时回落默认值。
    fn string_env_or_default(key: &str, default_value: &str) -> String {
        std::env::var(key)
            .ok()
            .map(|value| value.trim().to_string())
            .filter(|value| !value.is_empty())
            .unwrap_or_else(|| default_value.to_string())
    }

    /// 解析 runtime 参数环境变量，按空白分割并过滤空项。
    fn parse_runtime_args_env(runtime_args_env: &str) -> Vec<String> {
        runtime_args_env
            .split_whitespace()
            .map(|item| item.to_string())
            .collect::<Vec<_>>()
    }

    /// 推导仓库根目录（dev 模式下用于自动发现 agent-core）。
    fn detect_repo_root() -> PathBuf {
        let manifest_dir = PathBuf::from(env!("CARGO_MANIFEST_DIR"));
        let candidate_root = manifest_dir.join("../../..");
        candidate_root.canonicalize().unwrap_or(candidate_root)
    }

    /// 自动探测真实 runtime：仅接受预编译二进制。
    fn detect_runtime_program_and_args(
        runtime_args_env: &str,
    ) -> Result<(PathBuf, Vec<String>), String> {
        let repo_root = Self::detect_repo_root();
        let mut runtime_candidates = Vec::new();
        #[cfg(windows)]
        {
            runtime_candidates.push(repo_root.join("bin/win11/agent-core.exe"));
        }
        #[cfg(not(windows))]
        {
            runtime_candidates.push(repo_root.join("agent-core/agent-core"));
            runtime_candidates.push(repo_root.join("bin/linux/agent-core"));
            runtime_candidates.push(repo_root.join("bin/linux-amd64/agent-core"));
        }
        let detected_runtime = runtime_candidates
            .iter()
            .find(|candidate| candidate.is_file())
            .cloned();
        if let Some(binary_path) = detected_runtime {
            return Ok((binary_path, Self::parse_runtime_args_env(runtime_args_env)));
        }

        let candidate_paths = runtime_candidates
            .into_iter()
            .map(|path| path.to_string_lossy().to_string())
            .collect::<Vec<_>>()
            .join(", ");
        Err(format!(
            "未检测到可用 Agent runtime 二进制。请先构建 agent-core 后重试，或在设置页填写 runtime_program（禁止 go run 包装模式）。candidates=[{}]",
            candidate_paths,
        ))
    }

    /// 读取 PATH 搜索目录，供 `runtime_program` 命令名解析使用。
    fn runtime_search_paths_from_env() -> Vec<PathBuf> {
        let Some(path_env) = std::env::var_os("PATH") else {
            return Vec::new();
        };
        std::env::split_paths(&path_env).collect()
    }

    /// 将 `runtime_program` 解析为可比较路径：支持 PATH 命令名搜索。
    fn resolve_runtime_program_with_search_paths(
        runtime_program: &Path,
        search_paths: &[PathBuf],
    ) -> Option<PathBuf> {
        if runtime_program.as_os_str().is_empty() {
            return None;
        }

        let has_explicit_path = runtime_program.is_absolute()
            || runtime_program
                .parent()
                .map(|parent| !parent.as_os_str().is_empty())
                .unwrap_or(false);
        if has_explicit_path {
            if runtime_program.is_file() {
                return Some(
                    runtime_program
                        .canonicalize()
                        .unwrap_or_else(|_| runtime_program.to_path_buf()),
                );
            }
            return None;
        }

        let file_name = runtime_program.file_name()?;
        for search_dir in search_paths {
            if search_dir.as_os_str().is_empty() {
                continue;
            }
            let direct_candidate = search_dir.join(file_name);
            if direct_candidate.is_file() {
                return Some(direct_candidate.canonicalize().unwrap_or(direct_candidate));
            }
            #[cfg(windows)]
            {
                let has_extension = Path::new(file_name).extension().is_some();
                if !has_extension {
                    for extension in Self::windows_pathext_candidates() {
                        let mut candidate_name = file_name.to_os_string();
                        candidate_name.push(extension);
                        let extension_candidate = search_dir.join(candidate_name);
                        if extension_candidate.is_file() {
                            return Some(
                                extension_candidate
                                    .canonicalize()
                                    .unwrap_or(extension_candidate),
                            );
                        }
                    }
                }
            }
        }
        None
    }

    /// Windows 的可执行文件后缀列表（按 PATHEXT 语义）。
    #[cfg(windows)]
    fn windows_pathext_candidates() -> Vec<String> {
        std::env::var("PATHEXT")
            .unwrap_or_else(|_| ".COM;.EXE;.BAT;.CMD".to_string())
            .split(';')
            .map(|item| item.trim())
            .filter(|item| !item.is_empty())
            .map(|item| {
                if item.starts_with('.') {
                    item.to_string()
                } else {
                    format!(".{item}")
                }
            })
            .collect::<Vec<_>>()
    }

    /// 判断两个路径是否指向同一可执行文件。
    fn is_same_executable_path(left: &Path, right: &Path) -> bool {
        if left == right {
            return true;
        }
        match (left.canonicalize(), right.canonicalize()) {
            (Ok(left_path), Ok(right_path)) => left_path == right_path,
            _ => false,
        }
    }

    /// 判断 runtime_program 是否（直接或经 PATH 解析后）指向当前 UI 进程。
    fn runtime_program_targets_current_exe(
        runtime_program: &Path,
        current_exe: &Path,
        search_paths: &[PathBuf],
    ) -> bool {
        if Self::is_same_executable_path(runtime_program, current_exe) {
            return true;
        }
        if let Some(resolved_path) =
            Self::resolve_runtime_program_with_search_paths(runtime_program, search_paths)
        {
            return Self::is_same_executable_path(&resolved_path, current_exe);
        }
        false
    }

    /// 读取 u32 环境变量，缺失时回落默认值，非法值直接报错。
    fn u32_env_or_default(key: &str, default_value: u32) -> Result<u32, String> {
        let value = std::env::var(key).unwrap_or_default();
        let normalized = value.trim();
        if normalized.is_empty() {
            return Ok(default_value);
        }
        normalized
            .parse::<u32>()
            .map_err(|err| format!("环境变量 {key} 解析为 u32 失败: {err}"))
    }

    /// 读取 bool 环境变量，缺失时回落默认值，非法值直接报错。
    fn bool_env_or_default(key: &str, default_value: bool) -> Result<bool, String> {
        let value = std::env::var(key).unwrap_or_default();
        let normalized = value.trim().to_ascii_lowercase();
        if normalized.is_empty() {
            return Ok(default_value);
        }
        match normalized.as_str() {
            "1" | "true" => Ok(true),
            "0" | "false" => Ok(false),
            _ => Err(format!("环境变量 {key} 解析为 bool 失败: expect true/false/1/0")),
        }
    }

    /// 读取 u64 环境变量，缺失时回落默认值，非法值直接报错。
    fn u64_env_or_default(key: &str, default_value: u64) -> Result<u64, String> {
        let value = std::env::var(key).unwrap_or_default();
        let normalized = value.trim();
        if normalized.is_empty() {
            return Ok(default_value);
        }
        normalized
            .parse::<u64>()
            .map_err(|err| format!("环境变量 {key} 解析为 u64 失败: {err}"))
    }

    /// 读取 f64 环境变量，缺失时回落默认值，非法值直接报错。
    fn f64_env_or_default(key: &str, default_value: f64) -> Result<f64, String> {
        let value = std::env::var(key).unwrap_or_default();
        let normalized = value.trim();
        if normalized.is_empty() {
            return Ok(default_value);
        }
        normalized
            .parse::<f64>()
            .map_err(|err| format!("环境变量 {key} 解析为 f64 失败: {err}"))
    }

    /// 平台固定 IPC 传输类型。
    fn platform_ipc_transport() -> String {
        if cfg!(windows) {
            "named_pipe".to_string()
        } else {
            "uds".to_string()
        }
    }

    /// 从持久化配置转换为运行时配置。
    fn from_persisted_file(payload: PersistedHostRuntimeConfig) -> Result<Self, String> {
        let ipc_transport = Self::platform_ipc_transport();
        let mut normalized_manual_services = Vec::new();
        for service in payload
            .manual_services
            .iter()
            .filter_map(ManualServiceConfig::normalized)
        {
            if let Some(existing_index) = normalized_manual_services
                .iter()
                .position(|item| same_manual_service_identity(item, &service))
            {
                normalized_manual_services[existing_index] = service;
            } else {
                normalized_manual_services.push(service);
            }
        }
        let runtime_config = Self {
            runtime_program: PathBuf::from(payload.runtime_program.trim()),
            runtime_args: payload.runtime_args,
            agent_id: payload.agent_id,
            bridge_addr: payload.bridge_addr,
            bridge_transport: if payload.bridge_transport.trim().is_empty() {
                default_bridge_transport()
            } else {
                payload.bridge_transport.trim().to_string()
            },
            bridge_tls_enabled: payload.bridge_tls_enabled,
            bridge_tls_root_ca_file: payload.bridge_tls_root_ca_file.trim().to_string(),
            bridge_tls_server_name: payload.bridge_tls_server_name.trim().to_string(),
            tunnel_pool_min_idle: payload.tunnel_pool_min_idle,
            tunnel_pool_max_idle: payload.tunnel_pool_max_idle,
            tunnel_pool_max_inflight: payload.tunnel_pool_max_inflight,
            tunnel_pool_ttl_ms: payload.tunnel_pool_ttl_ms,
            tunnel_pool_open_rate: payload.tunnel_pool_open_rate,
            tunnel_pool_open_burst: payload.tunnel_pool_open_burst,
            tunnel_pool_reconcile_gap_ms: payload.tunnel_pool_reconcile_gap_ms,
            ipc_transport: ipc_transport.clone(),
            ipc_endpoint: if payload.ipc_endpoint.trim().is_empty() {
                default_ipc_endpoint(&ipc_transport)
            } else {
                payload.ipc_endpoint.trim().to_string()
            },
            diagnose_show_ipc: payload.diagnose_show_ipc,
            diagnose_show_bridge: payload.diagnose_show_bridge,
            diagnose_show_tunnel: payload.diagnose_show_tunnel,
            manual_services: normalized_manual_services,
        };
        runtime_config.validate_agent_runtime_fields()?;
        Ok(runtime_config)
    }

    /// 将运行时配置转换为 YAML 持久化格式。
    fn to_persisted_file(&self) -> PersistedHostRuntimeConfig {
        PersistedHostRuntimeConfig {
            runtime_program: self.runtime_program.to_string_lossy().to_string(),
            runtime_args: self.runtime_args.clone(),
            agent_id: self.agent_id.clone(),
            bridge_addr: self.bridge_addr.clone(),
            bridge_transport: self.bridge_transport.clone(),
            bridge_tls_enabled: self.bridge_tls_enabled,
            bridge_tls_root_ca_file: self.bridge_tls_root_ca_file.clone(),
            bridge_tls_server_name: self.bridge_tls_server_name.clone(),
            tunnel_pool_min_idle: self.tunnel_pool_min_idle,
            tunnel_pool_max_idle: self.tunnel_pool_max_idle,
            tunnel_pool_max_inflight: self.tunnel_pool_max_inflight,
            tunnel_pool_ttl_ms: self.tunnel_pool_ttl_ms,
            tunnel_pool_open_rate: self.tunnel_pool_open_rate,
            tunnel_pool_open_burst: self.tunnel_pool_open_burst,
            tunnel_pool_reconcile_gap_ms: self.tunnel_pool_reconcile_gap_ms,
            ipc_endpoint: self.ipc_endpoint.clone(),
            diagnose_show_ipc: self.diagnose_show_ipc,
            diagnose_show_bridge: self.diagnose_show_bridge,
            diagnose_show_tunnel: self.diagnose_show_tunnel,
            manual_services: self.manual_services.clone(),
        }
    }

    /// 从用户目录 YAML 文件读取配置，不存在时返回 `None`。
    fn load_from_yaml_file() -> Result<Option<Self>, String> {
        let config_path = host_runtime_config_yaml_path();
        if !config_path.is_file() {
            return Ok(None);
        }
        let raw_yaml = fs::read_to_string(&config_path).map_err(|err| {
            format!(
                "读取配置文件失败(path={}): {err}",
                config_path.to_string_lossy()
            )
        })?;
        let payload =
            serde_yaml::from_str::<PersistedHostRuntimeConfig>(&raw_yaml).map_err(|err| {
                format!(
                    "解析 YAML 配置失败(path={}): {err}",
                    config_path.to_string_lossy()
                )
            })?;
        let runtime_config = Self::from_persisted_file(payload)?;
        Ok(Some(runtime_config))
    }

    /// 将配置写入用户目录 YAML 文件。
    pub fn persist_to_yaml_file(&self) -> Result<PathBuf, String> {
        let config_path = host_runtime_config_yaml_path();
        if let Some(parent_dir) = config_path.parent() {
            fs::create_dir_all(parent_dir).map_err(|err| {
                format!(
                    "创建配置目录失败(path={}): {err}",
                    parent_dir.to_string_lossy()
                )
            })?;
        }
        let yaml_text = serde_yaml::to_string(&self.to_persisted_file())
            .map_err(|err| format!("序列化配置为 YAML 失败: {err}"))?;
        fs::write(&config_path, yaml_text).map_err(|err| {
            format!(
                "写入配置文件失败(path={}): {err}",
                config_path.to_string_lossy()
            )
        })?;
        Ok(config_path)
    }

    /// 校验 agent-core 运行参数，保证与 Go runtime 校验口径一致。
    pub fn validate_agent_runtime_fields(&self) -> Result<(), String> {
        if self.runtime_program.as_os_str().is_empty() {
            return Err("runtime_program 不能为空".to_string());
        }
        let runtime_program_name = self
            .runtime_program
            .file_name()
            .map(|value| value.to_string_lossy().to_ascii_lowercase())
            .unwrap_or_default();
        let is_go_run_wrapper = (runtime_program_name == "go" || runtime_program_name == "go.exe")
            && self
                .runtime_args
                .first()
                .map(|value| value.trim().eq_ignore_ascii_case("run"))
                .unwrap_or(false);
        if is_go_run_wrapper {
            return Err(
                "runtime_program 不支持 go run 包装模式，请改为真实 agent-core 可执行文件"
                    .to_string(),
            );
        }
        if self
            .runtime_args
            .iter()
            .any(|item| item.trim() == "--mock-agent-runtime")
        {
            return Err(
                "禁止使用 --mock-agent-runtime，请配置真实 agent-core 可执行文件".to_string(),
            );
        }
        if let Ok(current_exe) = std::env::current_exe() {
            let search_paths = Self::runtime_search_paths_from_env();
            if Self::runtime_program_targets_current_exe(
                &self.runtime_program,
                &current_exe,
                &search_paths,
            ) {
                return Err(
                    "runtime_program 不能指向当前 dev-agent UI 进程，请改为真实 agent-core 程序"
                        .to_string(),
                );
            }
        }
        if self.agent_id.trim().is_empty() {
            return Err("agent_id 不能为空".to_string());
        }
        if self.bridge_addr.trim().is_empty() {
            return Err("bridge_addr 不能为空".to_string());
        }
        let bridge_transport = self.bridge_transport.trim();
        if bridge_transport.is_empty() {
            return Err("bridge_transport 不能为空".to_string());
        }
        if !is_supported_bridge_transport(bridge_transport) {
            return Err(format!("bridge_transport 不支持: {bridge_transport}"));
        }
        if self.bridge_tls_enabled && self.bridge_tls_root_ca_file.trim().is_empty() {
            return Err("bridge_tls.root_ca_file 不能为空".to_string());
        }
        if bridge_transport == "quic_native" && !self.bridge_tls_enabled {
            return Err("bridge_transport=quic_native requires bridge_tls.enabled=true".to_string());
        }
        if self.tunnel_pool_max_idle == 0 {
            return Err("tunnel_pool_max_idle 必须大于 0".to_string());
        }
        if self.tunnel_pool_min_idle > self.tunnel_pool_max_idle {
            return Err("tunnel_pool_min_idle 不能大于 tunnel_pool_max_idle".to_string());
        }
        if self.tunnel_pool_max_inflight == 0 {
            return Err("tunnel_pool_max_inflight 必须大于 0".to_string());
        }
        if !self.tunnel_pool_open_rate.is_finite() || self.tunnel_pool_open_rate <= 0.0 {
            return Err("tunnel_pool_open_rate 必须大于 0".to_string());
        }
        if self.tunnel_pool_open_burst == 0 {
            return Err("tunnel_pool_open_burst 必须大于 0".to_string());
        }
        if self.tunnel_pool_reconcile_gap_ms == 0 {
            return Err("tunnel_pool_reconcile_gap_ms 必须大于 0".to_string());
        }
        Ok(())
    }

    /// 从环境变量和默认值构建运行配置（不读取配置文件）。
    fn from_env_defaults_only() -> Result<Self, String> {
        let runtime_program_env = std::env::var("DEV_AGENT_RUNTIME_BIN").unwrap_or_default();
        let runtime_args_env = std::env::var("DEV_AGENT_RUNTIME_ARGS").unwrap_or_default();
        let ipc_endpoint_env = std::env::var("DEV_AGENT_IPC_ENDPOINT").unwrap_or_default();

        // 优先使用显式配置；未配置时自动探测真实 runtime。
        let (runtime_program, runtime_args) = if runtime_program_env.trim().is_empty() {
            Self::detect_runtime_program_and_args(&runtime_args_env)?
        } else {
            (
                PathBuf::from(runtime_program_env.trim()),
                Self::parse_runtime_args_env(&runtime_args_env),
            )
        };

        // IPC 传输方式与平台强绑定，不允许通过配置自由切换。
        let ipc_transport = Self::platform_ipc_transport();
        let ipc_endpoint = if ipc_endpoint_env.trim().is_empty() {
            default_ipc_endpoint(&ipc_transport)
        } else {
            ipc_endpoint_env.trim().to_string()
        };

        let bridge_transport = Self::string_env_or_default(
            "DEV_AGENT_CFG_BRIDGE_TRANSPORT",
            &default_bridge_transport(),
        );
        let config = Self {
            runtime_program,
            runtime_args,
            agent_id: Self::string_env_or_default("DEV_AGENT_CFG_AGENT_ID", "agent-local"),
            bridge_addr: Self::string_env_or_default(
                "DEV_AGENT_CFG_BRIDGE_ADDR",
                default_bridge_addr_for_transport(&bridge_transport),
            ),
            bridge_transport,
            bridge_tls_enabled: Self::bool_env_or_default("DEV_AGENT_CFG_BRIDGE_TLS_ENABLED", false)?,
            bridge_tls_root_ca_file: Self::string_env_or_default(
                "DEV_AGENT_CFG_BRIDGE_TLS_ROOT_CA_FILE",
                "",
            ),
            bridge_tls_server_name: Self::string_env_or_default(
                "DEV_AGENT_CFG_BRIDGE_TLS_SERVER_NAME",
                "",
            ),
            tunnel_pool_min_idle: Self::u32_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_MIN_IDLE",
                8,
            )?,
            tunnel_pool_max_idle: Self::u32_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_MAX_IDLE",
                32,
            )?,
            tunnel_pool_max_inflight: Self::u32_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_MAX_INFLIGHT",
                4,
            )?,
            tunnel_pool_ttl_ms: Self::u64_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_TTL_MS",
                600_000,
            )?,
            tunnel_pool_open_rate: Self::f64_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_RATE",
                10.0,
            )?,
            tunnel_pool_open_burst: Self::u32_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_OPEN_BURST",
                20,
            )?,
            tunnel_pool_reconcile_gap_ms: Self::u64_env_or_default(
                "DEV_AGENT_CFG_TUNNEL_POOL_RECONCILE_GAP_MS",
                1_000,
            )?,
            ipc_transport,
            ipc_endpoint,
            diagnose_show_ipc: true,
            diagnose_show_bridge: true,
            diagnose_show_tunnel: true,
            manual_services: Vec::new(),
        };
        // 启动时先做一次宿主侧校验，尽早暴露配置问题。
        config.validate_agent_runtime_fields()?;
        Ok(config)
    }

    /// 启动配置加载入口：优先 YAML 文件，失败时回退环境变量+默认值。
    pub fn load_with_yaml_fallback() -> Result<(Self, Option<String>), String> {
        match Self::load_from_yaml_file() {
            Ok(Some(config)) => Ok((config, None)),
            Ok(None) => Ok((Self::from_env_defaults_only()?, None)),
            Err(err) => {
                let fallback_config = Self::from_env_defaults_only()?;
                let warning = format!(
                    "配置文件异常，已回退默认配置启动。{} path={}",
                    err,
                    host_runtime_config_yaml_path().to_string_lossy()
                );
                Ok((fallback_config, Some(warning)))
            }
        }
    }

    /// 导出给 UI 的配置快照，保持字段稳定可展示。
    pub fn to_snapshot(&self) -> HostConfigSnapshot {
        HostConfigSnapshot {
            runtime_program: self.runtime_program.to_string_lossy().to_string(),
            runtime_args: self.runtime_args.clone(),
            agent_id: self.agent_id.clone(),
            bridge_addr: self.bridge_addr.clone(),
            bridge_transport: self.bridge_transport.clone(),
            bridge_tls_enabled: self.bridge_tls_enabled,
            bridge_tls_root_ca_file: self.bridge_tls_root_ca_file.clone(),
            bridge_tls_server_name: self.bridge_tls_server_name.clone(),
            tunnel_pool_min_idle: self.tunnel_pool_min_idle,
            tunnel_pool_max_idle: self.tunnel_pool_max_idle,
            tunnel_pool_max_inflight: self.tunnel_pool_max_inflight,
            tunnel_pool_ttl_ms: self.tunnel_pool_ttl_ms,
            tunnel_pool_open_rate: self.tunnel_pool_open_rate,
            tunnel_pool_open_burst: self.tunnel_pool_open_burst,
            tunnel_pool_reconcile_gap_ms: self.tunnel_pool_reconcile_gap_ms,
            ipc_transport: self.ipc_transport.clone(),
            ipc_endpoint: self.ipc_endpoint.clone(),
            diagnose_show_ipc: self.diagnose_show_ipc,
            diagnose_show_bridge: self.diagnose_show_bridge,
            diagnose_show_tunnel: self.diagnose_show_tunnel,
            allowed_method_domains: ALLOWED_METHOD_DOMAINS
                .iter()
                .map(|item| item.to_string())
                .collect(),
            denied_low_level_methods: DENIED_LOW_LEVEL_METHODS
                .iter()
                .map(|item| item.to_string())
                .collect(),
        }
    }
}

/// Supervisor 可变状态：承载最小宿主真相源。
pub struct SupervisorState {
    pub desired_state: DesiredState,
    pub exit_kind: ExitKind,
    pub connection_state: ConnectionState,
    pub expecting_exit: bool,
    pub pending_auto_restart: bool,
    pub last_restart_attempt_ms: u64,
    pub restart_total: u64,
    pub reconnect_total: u64,
    pub rpc_latency_samples_ms: VecDeque<u64>,
    pub last_heartbeat_at_ms: Option<u64>,
    pub next_retry_at_ms: Option<u64>,
    pub retry_backoff_ms: u64,
    pub retry_fail_streak: u32,
    pub last_reconnect_error: Option<String>,
    pub host_logs: VecDeque<HostLogEntry>,
    pub child: Option<Child>,
    pub ipc_client: Option<LocalRpcClient>,
    pub last_ipc_event_pump_ms: u64,
    pub pid: Option<u32>,
    pub started_at_ms: Option<u64>,
    pub updated_at_ms: u64,
    pub last_error: Option<String>,
}

impl SupervisorState {
    /// 初始化 Supervisor 状态，所有字段均给出显式默认值。
    pub fn new() -> Self {
        Self {
            desired_state: DesiredState::Stopped,
            exit_kind: ExitKind::Expected,
            connection_state: ConnectionState::Disconnected,
            expecting_exit: false,
            pending_auto_restart: false,
            last_restart_attempt_ms: 0,
            restart_total: 0,
            reconnect_total: 0,
            rpc_latency_samples_ms: VecDeque::new(),
            last_heartbeat_at_ms: None,
            next_retry_at_ms: None,
            retry_backoff_ms: 0,
            retry_fail_streak: 0,
            last_reconnect_error: None,
            host_logs: VecDeque::new(),
            child: None,
            ipc_client: None,
            last_ipc_event_pump_ms: 0,
            pid: None,
            started_at_ms: None,
            updated_at_ms: now_ms(),
            last_error: None,
        }
    }
}

/// 事件桥状态：用于做节流和聚合统计。
pub struct EventBridgeState {
    pub last_emit_at: Option<Instant>,
    pub dropped_event_count: u64,
}

/// 进程级共享状态：由 Tauri command 与后台监控线程共同访问。
pub struct AppRuntimeState {
    pub runtime_config: Mutex<HostRuntimeConfig>,
    pub supervisor: Mutex<SupervisorState>,
    pub event_bridge: Mutex<EventBridgeState>,
    pub single_instance: Mutex<Option<SingleInstanceGuard>>,
    pub shutdown_requested: AtomicBool,
}

impl AppRuntimeState {
    /// 构造共享状态对象，供全局管理与注入。
    pub fn new(runtime_config: HostRuntimeConfig) -> Self {
        Self {
            runtime_config: Mutex::new(runtime_config),
            supervisor: Mutex::new(SupervisorState::new()),
            event_bridge: Mutex::new(EventBridgeState {
                last_emit_at: None,
                dropped_event_count: 0,
            }),
            single_instance: Mutex::new(None),
            shutdown_requested: AtomicBool::new(false),
        }
    }
}

/// 读取当前可生效的宿主运行配置。
pub fn current_runtime_config(state: &Arc<AppRuntimeState>) -> Result<HostRuntimeConfig, String> {
    state
        .runtime_config
        .lock()
        .map_err(|_| "读取宿主配置失败：runtime_config 锁异常".to_string())
        .map(|config| config.clone())
}

/// 更新宿主运行配置：保存后由下一次拉起链路生效。
pub fn update_runtime_config(
    state: &Arc<AppRuntimeState>,
    config: HostRuntimeConfig,
) -> Result<(), String> {
    // 先落盘再更新内存，保证“保存成功”必定可在下次启动生效。
    config.persist_to_yaml_file()?;
    let mut runtime_config = state
        .runtime_config
        .lock()
        .map_err(|_| "更新宿主配置失败：runtime_config 锁异常".to_string())?;
    // 直接替换真相源，避免增量更新遗漏字段。
    *runtime_config = config;
    Ok(())
}

/// 持久化手动服务配置：按 `instance_id` 或 `namespace/environment/service_name` 执行 upsert。
pub fn upsert_manual_service_config(
    state: &Arc<AppRuntimeState>,
    service: ManualServiceConfig,
) -> Result<usize, String> {
    let normalized_service = service
        .normalized()
        .ok_or_else(|| "持久化手动服务失败：服务字段不合法".to_string())?;
    let mut next_runtime_config = current_runtime_config(state)?;
    if let Some(existing_index) = next_runtime_config
        .manual_services
        .iter()
        .position(|item| same_manual_service_identity(item, &normalized_service))
    {
        next_runtime_config.manual_services[existing_index] = normalized_service;
    } else {
        next_runtime_config.manual_services.push(normalized_service);
    }
    let persisted_size = next_runtime_config.manual_services.len();
    update_runtime_config(state, next_runtime_config)?;
    Ok(persisted_size)
}

/// 删除手动服务配置：优先按 instance_id 匹配；其次按 service_name + 可选 scope 匹配。
pub fn remove_manual_service_config(
    state: &Arc<AppRuntimeState>,
    instance_id: &str,
    namespace: Option<&str>,
    environment: Option<&str>,
    service_name: Option<&str>,
) -> Result<usize, String> {
    let normalized_instance_id = instance_id.trim().to_string();
    let normalized_namespace = namespace.unwrap_or_default().trim().to_string();
    let normalized_environment = environment.unwrap_or_default().trim().to_string();
    let normalized_service_name = service_name.unwrap_or_default().trim().to_string();

    let mut next_runtime_config = current_runtime_config(state)?;
    next_runtime_config.manual_services.retain(|item| {
        if !normalized_instance_id.is_empty()
            && item
                .instance_id
                .as_ref()
                .map(|value| value == &normalized_instance_id)
                .unwrap_or(false)
        {
            return false;
        }
        if normalized_instance_id.is_empty()
            && !normalized_service_name.is_empty()
            && item.service_name == normalized_service_name
        {
            let item_namespace = item.namespace.as_deref().unwrap_or_default().trim();
            if !normalized_namespace.is_empty() && item_namespace != normalized_namespace {
                return true;
            }
            let item_environment = item.environment.as_deref().unwrap_or_default().trim();
            if !normalized_environment.is_empty() && item_environment != normalized_environment {
                return true;
            }
            return false;
        }
        true
    });

    let persisted_size = next_runtime_config.manual_services.len();
    update_runtime_config(state, next_runtime_config)?;
    Ok(persisted_size)
}

/// 构建宿主配置快照：仅返回真实可生效的启动参数。
pub fn current_host_config_snapshot(
    state: &Arc<AppRuntimeState>,
) -> Result<HostConfigSnapshot, String> {
    let runtime_config = current_runtime_config(state)?;
    Ok(runtime_config.to_snapshot())
}

/// 获取当前毫秒级时间戳，用于统一时间字段口径。
pub fn now_ms() -> u64 {
    // 统一以 Unix epoch 毫秒表示，便于前端直接格式化。
    let duration = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or_else(|_| Duration::from_millis(0));
    duration.as_millis() as u64
}

/// 计算 RPC 延迟均值，用于宿主级指标展示。
fn average_latency_ms(samples: &VecDeque<u64>) -> f64 {
    if samples.is_empty() {
        return 0.0;
    }
    let sum: u64 = samples.iter().sum();
    sum as f64 / samples.len() as f64
}

/// 记录结构化日志并控制缓冲区上限，避免内存无界增长。
pub fn push_host_log(
    supervisor: &mut SupervisorState,
    level: &str,
    module: &str,
    code: &str,
    message: impl Into<String>,
) {
    supervisor.host_logs.push_back(HostLogEntry {
        ts_ms: now_ms(),
        level: level.to_string(),
        module: module.to_string(),
        code: code.to_string(),
        message: message.into(),
    });
    // 使用环形队列策略，持续保留最近日志。
    while supervisor.host_logs.len() > MAX_HOST_LOG_ENTRIES {
        let _ = supervisor.host_logs.pop_front();
    }
}

/// 根据内部状态构建对外快照，确保 UI 只读消费。
pub fn build_runtime_snapshot(supervisor: &SupervisorState) -> AgentRuntimeSnapshot {
    AgentRuntimeSnapshot {
        desired_state: supervisor.desired_state,
        exit_kind: supervisor.exit_kind,
        connection_state: supervisor.connection_state,
        process_alive: supervisor.child.is_some(),
        pid: supervisor.pid,
        started_at_ms: supervisor.started_at_ms,
        updated_at_ms: supervisor.updated_at_ms,
        last_error: supervisor.last_error.clone(),
        metrics: HostMetricsSnapshot {
            agent_host_ipc_connected: matches!(
                supervisor.connection_state,
                ConnectionState::Connected
            ),
            agent_host_ipc_reconnect_total: supervisor.reconnect_total,
            agent_host_rpc_latency_ms: average_latency_ms(&supervisor.rpc_latency_samples_ms),
            agent_host_supervisor_restart_total: supervisor.restart_total,
            agent_bridge_last_heartbeat_at_ms: supervisor.last_heartbeat_at_ms,
            agent_bridge_next_retry_at_ms: supervisor.next_retry_at_ms,
            agent_bridge_retry_backoff_ms: supervisor.retry_backoff_ms,
            agent_bridge_retry_fail_streak: supervisor.retry_fail_streak,
            agent_bridge_last_reconnect_error: supervisor.last_reconnect_error.clone(),
        },
    }
}

/// 向延迟缓冲写入一次样本，供 ping/command 复用。
pub fn push_rpc_latency_sample(supervisor: &mut SupervisorState, sample_ms: u64) {
    supervisor.rpc_latency_samples_ms.push_back(sample_ms);
    while supervisor.rpc_latency_samples_ms.len() > MAX_RPC_LATENCY_SAMPLES {
        let _ = supervisor.rpc_latency_samples_ms.pop_front();
    }
}

/// 向指标缓冲写入一次 RPC 延迟样本。
fn record_rpc_latency(state: &Arc<AppRuntimeState>, elapsed: Duration) {
    let mut supervisor = match state.supervisor.lock() {
        Ok(guard) => guard,
        Err(_) => return,
    };
    push_rpc_latency_sample(&mut supervisor, elapsed.as_millis() as u64);
    supervisor.updated_at_ms = now_ms();
}

/// 包装 command 执行并自动记录 RPC 延迟指标。
pub fn with_rpc_metrics<T, F>(state: &Arc<AppRuntimeState>, action: F) -> Result<T, String>
where
    F: FnOnce() -> Result<T, String>,
{
    let start_at = Instant::now();
    let result = action();
    // 无论成功失败都要记一条延迟，便于排障。
    record_rpc_latency(state, start_at.elapsed());
    result
}

/// 获取当前运行态快照，供 command 返回。
pub fn current_snapshot(state: &Arc<AppRuntimeState>) -> Result<AgentRuntimeSnapshot, String> {
    let mut supervisor = state
        .supervisor
        .lock()
        .map_err(|_| "读取宿主状态失败：supervisor 锁异常".to_string())?;
    if let Some(mut ipc_client) = supervisor.ipc_client.take() {
        let mut drained_event_count = 0usize;
        if let Err(err) = ipc_client.ping() {
            supervisor.connection_state = ConnectionState::Disconnected;
            supervisor.last_error = Some(format!("IPC ping 失败: {err}"));
            supervisor.updated_at_ms = now_ms();
            push_host_log(
                &mut supervisor,
                "warn",
                "ipc_client",
                "RPC_PING_DEGRADED",
                format!("读取快照时 ping 失败: {err}"),
            );
        } else if let Err(err) =
            ipc_client.request("agent.snapshot", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS)
        {
            supervisor.connection_state = ConnectionState::Disconnected;
            supervisor.last_error = Some(format!("agent.snapshot 失败: {err}"));
            supervisor.updated_at_ms = now_ms();
            push_host_log(
                &mut supervisor,
                "warn",
                "ipc_client",
                "RPC_SNAPSHOT_DEGRADED",
                format!("读取快照时 agent.snapshot 失败: {err}"),
            );
        } else {
            drained_event_count = ipc_client.drain_events().len();
            supervisor.last_heartbeat_at_ms = Some(now_ms());
            supervisor.next_retry_at_ms = None;
            supervisor.retry_backoff_ms = 0;
            supervisor.retry_fail_streak = 0;
            supervisor.last_reconnect_error = None;
        }
        supervisor.ipc_client = Some(ipc_client);
        if drained_event_count > 0 {
            // 事件仅做摘要记录，避免日志量失控。
            push_host_log(
                &mut supervisor,
                "info",
                "event_bridge",
                "RPC_EVENT_DRAINED",
                format!("读取快照时同步到 {} 条本地事件", drained_event_count),
            );
        }
    }
    Ok(build_runtime_snapshot(&supervisor))
}

#[cfg(test)]
mod tests {
    use super::HostRuntimeConfig;
    use std::fs;
    use std::path::{Path, PathBuf};

    fn build_valid_runtime_config() -> HostRuntimeConfig {
        HostRuntimeConfig {
            runtime_program: PathBuf::from("/tmp/agent-core"),
            runtime_args: vec![],
            agent_id: "agent-local".to_string(),
            bridge_addr: "127.0.0.1:39081".to_string(),
            bridge_transport: "tcp_framed".to_string(),
            bridge_tls_enabled: false,
            bridge_tls_root_ca_file: String::new(),
            bridge_tls_server_name: String::new(),
            tunnel_pool_min_idle: 8,
            tunnel_pool_max_idle: 32,
            tunnel_pool_max_inflight: 4,
            tunnel_pool_ttl_ms: 600_000,
            tunnel_pool_open_rate: 10.0,
            tunnel_pool_open_burst: 20,
            tunnel_pool_reconcile_gap_ms: 1_000,
            ipc_transport: "uds".to_string(),
            ipc_endpoint: "/tmp/dev-agent/agent.sock".to_string(),
            diagnose_show_ipc: true,
            diagnose_show_bridge: true,
            diagnose_show_tunnel: true,
            manual_services: Vec::new(),
        }
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_nan_open_rate() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.tunnel_pool_open_rate = f64::NAN;
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_infinite_open_rate() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.tunnel_pool_open_rate = f64::INFINITY;
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_mock_runtime_arg() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.runtime_args = vec!["--mock-agent-runtime".to_string()];
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_go_run_wrapper_mode() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.runtime_program = PathBuf::from("go");
        runtime_config.runtime_args =
            vec!["run".to_string(), "./agent-core/cmd/agent-core".to_string()];
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn validate_agent_runtime_fields_accepts_quic_bridge_transport() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.bridge_transport = "quic_native".to_string();
        runtime_config.bridge_tls_enabled = true;
        runtime_config.bridge_tls_root_ca_file = "/etc/devbridge/root-ca.crt".to_string();
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_ok());
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_quic_without_bridge_tls() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.bridge_transport = "quic_native".to_string();
        runtime_config.bridge_tls_enabled = false;
        runtime_config.bridge_tls_root_ca_file = String::new();
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn validate_agent_runtime_fields_rejects_bridge_tls_without_root_ca() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.bridge_tls_enabled = true;
        runtime_config.bridge_tls_root_ca_file = String::new();
        let validate_result = runtime_config.validate_agent_runtime_fields();
        assert!(validate_result.is_err());
    }

    #[test]
    fn from_env_defaults_only_uses_tcp_bridge_default_addr() {
        std::env::set_var("DEV_AGENT_RUNTIME_BIN", "/tmp/agent-core");
        std::env::remove_var("DEV_AGENT_RUNTIME_ARGS");
        std::env::remove_var("DEV_AGENT_IPC_ENDPOINT");
        std::env::remove_var("DEV_AGENT_CFG_BRIDGE_ADDR");
        std::env::remove_var("DEV_AGENT_CFG_BRIDGE_TRANSPORT");

        let runtime_config =
            HostRuntimeConfig::from_env_defaults_only().expect("读取默认配置失败");
        assert_eq!(runtime_config.bridge_transport, "tcp_framed");
        assert_eq!(runtime_config.bridge_addr, "127.0.0.1:39081");
    }

    #[test]
    fn persisted_runtime_config_round_trip_preserves_bridge_tls_fields() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.bridge_transport = "quic_native".to_string();
        runtime_config.bridge_tls_enabled = true;
        runtime_config.bridge_tls_root_ca_file = "/etc/devbridge/root-ca.crt".to_string();
        runtime_config.bridge_tls_server_name = "bridge.internal.example".to_string();

        let persisted = runtime_config.to_persisted_file();
        assert!(persisted.bridge_tls_enabled);
        assert_eq!(persisted.bridge_tls_root_ca_file, "/etc/devbridge/root-ca.crt");
        assert_eq!(persisted.bridge_tls_server_name, "bridge.internal.example");

        let restored = HostRuntimeConfig::from_persisted_file(persisted)
            .expect("持久化配置回读失败");
        assert!(restored.bridge_tls_enabled);
        assert_eq!(restored.bridge_tls_root_ca_file, "/etc/devbridge/root-ca.crt");
        assert_eq!(restored.bridge_tls_server_name, "bridge.internal.example");
        assert_eq!(restored.bridge_transport, "quic_native");
    }

    #[test]
    fn host_runtime_snapshot_includes_bridge_tls_fields() {
        let mut runtime_config = build_valid_runtime_config();
        runtime_config.bridge_tls_enabled = true;
        runtime_config.bridge_tls_root_ca_file = "/etc/devbridge/root-ca.crt".to_string();
        runtime_config.bridge_tls_server_name = "bridge.internal.example".to_string();

        let snapshot = runtime_config.to_snapshot();
        assert!(snapshot.bridge_tls_enabled);
        assert_eq!(snapshot.bridge_tls_root_ca_file, "/etc/devbridge/root-ca.crt");
        assert_eq!(snapshot.bridge_tls_server_name, "bridge.internal.example");
    }

    #[cfg(unix)]
    #[test]
    fn runtime_program_targets_current_exe_when_path_alias_points_to_self() {
        use std::os::unix::fs::symlink;
        use std::time::{SystemTime, UNIX_EPOCH};

        let current_exe = std::env::current_exe().expect("读取当前可执行文件失败");
        let unique_suffix = SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .expect("读取系统时间失败")
            .as_nanos();
        let temp_dir = std::env::temp_dir().join(format!(
            "dev-agent-runtime-program-alias-{}-{unique_suffix}",
            std::process::id()
        ));
        fs::create_dir_all(&temp_dir).expect("创建临时目录失败");

        let alias_name = "dev-agent-alias";
        let alias_path = temp_dir.join(alias_name);
        symlink(&current_exe, &alias_path).expect("创建 alias 软链接失败");

        let hit = HostRuntimeConfig::runtime_program_targets_current_exe(
            Path::new(alias_name),
            &current_exe,
            &[temp_dir.clone()],
        );
        assert!(hit, "PATH alias 应被识别为当前 UI 进程");

        let _ = fs::remove_file(alias_path);
        let _ = fs::remove_dir_all(temp_dir);
    }
}
