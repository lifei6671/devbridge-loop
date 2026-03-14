use serde::Serialize;
use serde_json::json;
use std::collections::VecDeque;
use std::fs::{self, File};
use std::path::PathBuf;
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
/// 崩溃恢复自动拉起的退避窗口。
pub const RESTART_BACKOFF_MS: u64 = 900;

const MAX_RPC_LATENCY_SAMPLES: usize = 32;
const MAX_HOST_LOG_ENTRIES: usize = 80;

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
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_transport: String,
    pub ipc_endpoint: String,
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
    pub tunnel_pool_min_idle: u32,
    pub tunnel_pool_max_idle: u32,
    pub tunnel_pool_max_inflight: u32,
    pub tunnel_pool_ttl_ms: u64,
    pub tunnel_pool_open_rate: f64,
    pub tunnel_pool_open_burst: u32,
    pub tunnel_pool_reconcile_gap_ms: u64,
    pub ipc_transport: String,
    pub ipc_endpoint: String,
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

    /// 校验 agent-core 运行参数，保证与 Go runtime 校验口径一致。
    pub fn validate_agent_runtime_fields(&self) -> Result<(), String> {
        if self.agent_id.trim().is_empty() {
            return Err("agent_id 不能为空".to_string());
        }
        if self.bridge_addr.trim().is_empty() {
            return Err("bridge_addr 不能为空".to_string());
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

    /// 从环境变量加载运行配置，未提供时回退到内置 mock runtime。
    pub fn from_env() -> Result<Self, String> {
        let runtime_program_env = std::env::var("DEV_AGENT_RUNTIME_BIN").unwrap_or_default();
        let runtime_args_env = std::env::var("DEV_AGENT_RUNTIME_ARGS").unwrap_or_default();
        let ipc_endpoint_env = std::env::var("DEV_AGENT_IPC_ENDPOINT").unwrap_or_default();

        // 优先使用外部配置的 runtime，可无缝切到真实 Agent 可执行文件。
        let (runtime_program, runtime_args) = if runtime_program_env.trim().is_empty() {
            let current_exe = std::env::current_exe()
                .map_err(|err| format!("读取当前可执行文件路径失败: {err}"))?;
            (current_exe, vec!["--mock-agent-runtime".to_string()])
        } else {
            let args = runtime_args_env
                .split_whitespace()
                .map(|item| item.to_string())
                .collect::<Vec<_>>();
            (PathBuf::from(runtime_program_env.trim()), args)
        };

        // IPC 传输方式与平台强绑定，不允许通过配置自由切换。
        let ipc_transport = if cfg!(windows) {
            "named_pipe".to_string()
        } else {
            "uds".to_string()
        };
        let ipc_endpoint = if ipc_endpoint_env.trim().is_empty() {
            default_ipc_endpoint(&ipc_transport)
        } else {
            ipc_endpoint_env.trim().to_string()
        };

        let config = Self {
            runtime_program,
            runtime_args,
            agent_id: Self::string_env_or_default("DEV_AGENT_CFG_AGENT_ID", "agent-local"),
            bridge_addr: Self::string_env_or_default(
                "DEV_AGENT_CFG_BRIDGE_ADDR",
                "127.0.0.1:39080",
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
                90_000,
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
        };
        // 启动时先做一次宿主侧校验，尽早暴露配置问题。
        config.validate_agent_runtime_fields()?;
        Ok(config)
    }

    /// 导出给 UI 的配置快照，保持字段稳定可展示。
    pub fn to_snapshot(&self) -> HostConfigSnapshot {
        HostConfigSnapshot {
            runtime_program: self.runtime_program.to_string_lossy().to_string(),
            runtime_args: self.runtime_args.clone(),
            agent_id: self.agent_id.clone(),
            bridge_addr: self.bridge_addr.clone(),
            tunnel_pool_min_idle: self.tunnel_pool_min_idle,
            tunnel_pool_max_idle: self.tunnel_pool_max_idle,
            tunnel_pool_max_inflight: self.tunnel_pool_max_inflight,
            tunnel_pool_ttl_ms: self.tunnel_pool_ttl_ms,
            tunnel_pool_open_rate: self.tunnel_pool_open_rate,
            tunnel_pool_open_burst: self.tunnel_pool_open_burst,
            tunnel_pool_reconcile_gap_ms: self.tunnel_pool_reconcile_gap_ms,
            ipc_transport: self.ipc_transport.clone(),
            ipc_endpoint: self.ipc_endpoint.clone(),
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
    let mut runtime_config = state
        .runtime_config
        .lock()
        .map_err(|_| "更新宿主配置失败：runtime_config 锁异常".to_string())?;
    // 直接替换真相源，避免增量更新遗漏字段。
    *runtime_config = config;
    Ok(())
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
        },
    }
}

/// 向指标缓冲写入一次 RPC 延迟样本。
fn record_rpc_latency(state: &Arc<AppRuntimeState>, elapsed: Duration) {
    let mut supervisor = match state.supervisor.lock() {
        Ok(guard) => guard,
        Err(_) => return,
    };
    supervisor
        .rpc_latency_samples_ms
        .push_back(elapsed.as_millis() as u64);
    // 固定窗口，避免长期运行后样本无限累积。
    while supervisor.rpc_latency_samples_ms.len() > MAX_RPC_LATENCY_SAMPLES {
        let _ = supervisor.rpc_latency_samples_ms.pop_front();
    }
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
    if let Some(ipc_client) = supervisor.ipc_client.as_mut() {
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
            let events = ipc_client.drain_events();
            if !events.is_empty() {
                // 事件仅做摘要记录，避免日志量失控。
                push_host_log(
                    &mut supervisor,
                    "info",
                    "event_bridge",
                    "RPC_EVENT_DRAINED",
                    format!("读取快照时同步到 {} 条本地事件", events.len()),
                );
            }
        }
    }
    Ok(build_runtime_snapshot(&supervisor))
}

#[cfg(test)]
mod tests {
    use super::HostRuntimeConfig;
    use std::path::PathBuf;

    fn build_valid_runtime_config() -> HostRuntimeConfig {
        HostRuntimeConfig {
            runtime_program: PathBuf::from("/tmp/agent-core"),
            runtime_args: vec![],
            agent_id: "agent-local".to_string(),
            bridge_addr: "127.0.0.1:39080".to_string(),
            tunnel_pool_min_idle: 8,
            tunnel_pool_max_idle: 32,
            tunnel_pool_max_inflight: 4,
            tunnel_pool_ttl_ms: 90_000,
            tunnel_pool_open_rate: 10.0,
            tunnel_pool_open_burst: 20,
            tunnel_pool_reconcile_gap_ms: 1_000,
            ipc_transport: "uds".to_string(),
            ipc_endpoint: "/tmp/dev-agent/agent.sock".to_string(),
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
}
