use serde::Serialize;
use std::path::PathBuf;
use std::process::{Child, Command};
use std::time::{Duration, SystemTime};
#[cfg(windows)]
use std::os::windows::process::CommandExt;

#[cfg(windows)]
const CREATE_NO_WINDOW: u32 = 0x08000000;

pub struct AgentManager {
    child: Option<Child>,
    last_error: Option<String>,
    started_at: Option<SystemTime>,
    launch_plan: LaunchPlan,
    restart_policy: RestartPolicy,
    restart_attempt: usize,
    restart_count: u32,
    next_restart_at: Option<SystemTime>,
}

struct LaunchPlan {
    command: String,
    args: Vec<String>,
    workdir: Option<PathBuf>,
}

struct RestartPolicy {
    auto_restart: bool,
    backoff_ms: Vec<u64>,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentRuntime {
    pub status: String,
    pub pid: Option<u32>,
    pub command: String,
    pub started_at: Option<String>,
    pub last_error: Option<String>,
    pub auto_restart: bool,
    pub restart_count: u32,
    pub restart_attempt: usize,
    pub next_restart_at: Option<String>,
}

impl AgentManager {
    // 创建进程管理器，并加载启动计划与重启策略。
    pub fn new() -> Self {
        Self {
            child: None,
            last_error: None,
            started_at: None,
            launch_plan: resolve_launch_plan(),
            restart_policy: resolve_restart_policy(),
            restart_attempt: 0,
            restart_count: 0,
            next_restart_at: None,
        }
    }

    // 确保进程已启动；用于应用启动阶段的首次拉起。
    pub fn ensure_started(&mut self) -> Result<AgentRuntime, String> {
        let _ = self.check_process_exit();
        if self.child.is_none() {
            self.spawn_process("initial-start")?;
        }
        Ok(self.runtime())
    }

    // 手动重启进程：先尝试结束旧进程，再立即拉起新进程。
    pub fn restart_now(&mut self) -> Result<AgentRuntime, String> {
        self.stop_running_process();
        self.spawn_process("manual-restart")?;
        Ok(self.runtime())
    }

    // 应用退出前调用：若存在子进程则主动终止，避免遗留孤儿进程。
    pub fn shutdown(&mut self) -> AgentRuntime {
        self.stop_running_process();
        self.runtime()
    }

    // 监督循环：检测退出并按退避策略自动重启。
    // 返回 Some(runtime) 表示状态发生变化，调用方应推送给前端。
    pub fn poll_supervisor(&mut self) -> Option<AgentRuntime> {
        let mut changed = false;

        // 第一步：先检查现有进程是否退出。
        if self.check_process_exit() {
            changed = true;
            self.schedule_next_restart();
        }

        // 第二步：如果当前未运行且启用了自动重启，则按退避窗口尝试拉起。
        if self.child.is_none() && self.restart_policy.auto_restart && self.can_retry_now() {
            match self.spawn_process("auto-restart") {
                Ok(()) => {
                    changed = true;
                }
                Err(err) => {
                    self.last_error = Some(err);
                    self.schedule_next_restart();
                    changed = true;
                }
            }
        }

        if changed {
            return Some(self.runtime());
        }
        None
    }

    // 返回当前运行时快照，不触发启动动作。
    pub fn runtime(&self) -> AgentRuntime {
        AgentRuntime {
            status: if self.child.is_some() {
                "running".to_string()
            } else {
                "stopped".to_string()
            },
            pid: self.child.as_ref().map(Child::id),
            command: self.display_command(),
            started_at: self.started_at.and_then(system_time_to_string),
            last_error: self.last_error.clone(),
            auto_restart: self.restart_policy.auto_restart,
            restart_count: self.restart_count,
            restart_attempt: self.restart_attempt,
            next_restart_at: self.next_restart_at.and_then(system_time_to_string),
        }
    }

    // 检查子进程退出状态，若已退出则清理句柄并记录错误信息。
    fn check_process_exit(&mut self) -> bool {
        let Some(child) = &mut self.child else {
            return false;
        };

        match child.try_wait() {
            Ok(Some(status)) => {
                self.child = None;
                self.last_error = Some(format!("agent-core 进程异常退出: {status}"));
                true
            }
            Ok(None) => false,
            Err(err) => {
                self.child = None;
                self.last_error = Some(format!("检查 agent-core 进程状态失败: {err}"));
                true
            }
        }
    }

    // 实际拉起子进程，并在成功后重置重启窗口。
    fn spawn_process(&mut self, reason: &str) -> Result<(), String> {
        let mut command = Command::new(&self.launch_plan.command);
        command.args(&self.launch_plan.args);
        if let Some(workdir) = &self.launch_plan.workdir {
            command.current_dir(workdir);
        }
        apply_platform_spawn_options(&mut command);

        let child = command
            .spawn()
            .map_err(|err| format!("启动 agent-core 失败（{reason}）: {err}"))?;

        // 只要不是第一次启动，都视为一次“重启”。
        if self.started_at.is_some() {
            self.restart_count = self.restart_count.saturating_add(1);
        }

        self.child = Some(child);
        self.started_at = Some(SystemTime::now());
        self.last_error = None;
        self.restart_attempt = 0;
        self.next_restart_at = None;
        Ok(())
    }

    // 按退避数组设置下一次自动重试时间。
    fn schedule_next_restart(&mut self) {
        let delay_ms = self.backoff_for_attempt(self.restart_attempt);
        self.next_restart_at = Some(SystemTime::now() + Duration::from_millis(delay_ms));
        self.restart_attempt = self.restart_attempt.saturating_add(1);
    }

    // 判断当前是否到达可重试时间点。
    fn can_retry_now(&self) -> bool {
        match self.next_restart_at {
            Some(next_at) => SystemTime::now().duration_since(next_at).is_ok(),
            None => true,
        }
    }

    fn backoff_for_attempt(&self, attempt: usize) -> u64 {
        if self.restart_policy.backoff_ms.is_empty() {
            return 1000;
        }
        let index = attempt.min(self.restart_policy.backoff_ms.len().saturating_sub(1));
        self.restart_policy.backoff_ms[index]
    }

    // 尽量终止正在运行的旧进程；失败时写入诊断信息。
    fn stop_running_process(&mut self) {
        let Some(mut child) = self.child.take() else {
            return;
        };

        if let Err(err) = child.kill() {
            self.last_error = Some(format!("终止旧 agent-core 进程失败: {err}"));
            return;
        }
        if let Err(err) = child.wait() {
            self.last_error = Some(format!("等待旧 agent-core 进程退出失败: {err}"));
        }
    }

    fn display_command(&self) -> String {
        format!(
            "{} {}",
            self.launch_plan.command,
            self.launch_plan.args.join(" ")
        )
    }
}

fn resolve_launch_plan() -> LaunchPlan {
    if let Ok(binary) = std::env::var("DEVLOOP_AGENT_BINARY") {
        let normalized = binary.trim();
        if !normalized.is_empty() {
            return LaunchPlan {
                command: normalized.to_string(),
                args: vec![],
                workdir: None,
            };
        }
    }

    // Windows 打包场景优先拉起同目录 agent-core.exe，避免依赖本机 Go 环境。
    if let Some(binary) = default_agent_binary() {
        return LaunchPlan {
            command: binary.display().to_string(),
            args: vec![],
            workdir: None,
        };
    }

    let workdir = std::env::var("DEVLOOP_AGENT_CORE_DIR")
        .ok()
        .map(PathBuf::from)
        .or_else(default_agent_core_dir);

    LaunchPlan {
        command: "go".to_string(),
        args: vec!["run".to_string(), "./cmd/agent-core".to_string()],
        workdir,
    }
}

fn default_agent_core_dir() -> Option<PathBuf> {
    let cwd = std::env::current_dir().ok()?;
    let candidate = cwd.join("../../agent-core");
    if candidate.exists() {
        return Some(candidate);
    }
    None
}

#[cfg(windows)]
fn default_agent_binary() -> Option<PathBuf> {
    let exe_path = std::env::current_exe().ok()?;
    let exe_dir = exe_path.parent()?;
    let candidate = exe_dir.join("agent-core.exe");
    if candidate.is_file() {
        return Some(candidate);
    }
    None
}

#[cfg(not(windows))]
fn default_agent_binary() -> Option<PathBuf> {
    None
}

fn apply_platform_spawn_options(command: &mut Command) {
    #[cfg(windows)]
    {
        // GUI 进程拉起 Console 子进程时默认会弹黑窗，显式禁用子进程控制台窗口。
        command.creation_flags(CREATE_NO_WINDOW);
    }
}

fn resolve_restart_policy() -> RestartPolicy {
    RestartPolicy {
        auto_restart: parse_auto_restart(
            std::env::var("DEVLOOP_AGENT_AUTO_RESTART").unwrap_or_else(|_| "true".to_string()),
        ),
        backoff_ms: parse_backoff(
            std::env::var("DEVLOOP_AGENT_RESTART_BACKOFF_MS")
                .unwrap_or_else(|_| "500,1000,2000,5000".to_string()),
        ),
    }
}

fn parse_auto_restart(value: String) -> bool {
    let normalized = value.trim().to_ascii_lowercase();
    !matches!(normalized.as_str(), "0" | "false" | "no" | "off")
}

fn parse_backoff(value: String) -> Vec<u64> {
    let parsed = value
        .split(',')
        .filter_map(|item| item.trim().parse::<u64>().ok())
        .filter(|item| *item > 0)
        .collect::<Vec<_>>();

    if parsed.is_empty() {
        return vec![500, 1000, 2000, 5000];
    }
    parsed
}

fn system_time_to_string(value: SystemTime) -> Option<String> {
    value
        .duration_since(SystemTime::UNIX_EPOCH)
        .ok()
        .map(|duration| duration.as_secs().to_string())
}
