use serde::Serialize;
use std::path::PathBuf;
use std::process::{Child, Command};
use std::time::SystemTime;

pub struct AgentManager {
    child: Option<Child>,
    last_error: Option<String>,
    started_at: Option<SystemTime>,
    launch_plan: LaunchPlan,
}

struct LaunchPlan {
    command: String,
    args: Vec<String>,
    workdir: Option<PathBuf>,
}

#[derive(Debug, Serialize)]
#[serde(rename_all = "camelCase")]
pub struct AgentRuntime {
    pub status: String,
    pub pid: Option<u32>,
    pub command: String,
    pub started_at: Option<String>,
    pub last_error: Option<String>,
}

impl AgentManager {
    pub fn new() -> Self {
        Self {
            child: None,
            last_error: None,
            started_at: None,
            launch_plan: resolve_launch_plan(),
        }
    }

    pub fn ensure_started(&mut self) -> Result<(), String> {
        if self.is_running() {
            return Ok(());
        }

        let mut command = Command::new(&self.launch_plan.command);
        command.args(&self.launch_plan.args);
        if let Some(workdir) = &self.launch_plan.workdir {
            command.current_dir(workdir);
        }

        let child = command
            .spawn()
            .map_err(|err| format!("failed to start agent-core process: {err}"))?;

        self.started_at = Some(SystemTime::now());
        self.last_error = None;
        self.child = Some(child);
        Ok(())
    }

    pub fn runtime(&mut self) -> AgentRuntime {
        if let Some(child) = &mut self.child {
            if let Ok(Some(status)) = child.try_wait() {
                self.last_error = Some(format!("agent-core exited unexpectedly: {status}"));
                self.child = None;
            }
        }

        AgentRuntime {
            status: if self.is_running() {
                "running".to_string()
            } else {
                "stopped".to_string()
            },
            pid: self.child.as_ref().map(|child| child.id()),
            command: self.display_command(),
            started_at: self
                .started_at
                .and_then(|value| value.duration_since(SystemTime::UNIX_EPOCH).ok())
                .map(|value| value.as_secs().to_string()),
            last_error: self.last_error.clone(),
        }
    }

    fn is_running(&mut self) -> bool {
        if let Some(child) = &mut self.child {
            if let Ok(Some(status)) = child.try_wait() {
                self.last_error = Some(format!("agent-core exited unexpectedly: {status}"));
                self.child = None;
                return false;
            }
            return true;
        }
        false
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
        return LaunchPlan {
            command: binary,
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
