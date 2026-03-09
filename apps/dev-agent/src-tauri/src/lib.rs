mod agent_api;
mod agent_manager;

use agent_api::AgentApiClient;
use agent_manager::{AgentManager, AgentRuntime};
use serde::Serialize;
use serde_json::Value;
use std::sync::Mutex;
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager};

const AGENT_RUNTIME_EVENT: &str = "agent-runtime-changed";

struct AppState {
    manager: Mutex<AgentManager>,
    api: AgentApiClient,
}

#[derive(Debug, Clone, Serialize)]
#[serde(rename_all = "camelCase")]
struct DesktopConfigView {
    agent_api_base: String,
    agent_binary: Option<String>,
    agent_core_dir: Option<String>,
    agent_auto_restart: bool,
    agent_restart_backoff_ms: Vec<u64>,
    env_resolve_order: Vec<String>,
    tunnel_bridge_address: String,
    tunnel_backflow_base_url: String,
    platform: String,
    arch: String,
}

#[tauri::command]
async fn get_state_summary(state: tauri::State<'_, AppState>) -> Result<Value, String> {
    state.api.state_summary().await
}

#[tauri::command]
async fn get_tunnel_state(state: tauri::State<'_, AppState>) -> Result<Value, String> {
    state.api.tunnel_state().await
}

#[tauri::command]
async fn get_registrations(state: tauri::State<'_, AppState>) -> Result<Vec<Value>, String> {
    state.api.registrations().await
}

#[tauri::command]
async fn get_recent_errors(state: tauri::State<'_, AppState>) -> Result<Vec<Value>, String> {
    state.api.recent_errors().await
}

#[tauri::command]
async fn get_recent_requests(state: tauri::State<'_, AppState>) -> Result<Vec<Value>, String> {
    state.api.recent_requests().await
}

#[tauri::command]
async fn get_active_intercepts(state: tauri::State<'_, AppState>) -> Result<Vec<Value>, String> {
    state.api.active_intercepts().await
}

#[tauri::command]
async fn unregister_registration(
    state: tauri::State<'_, AppState>,
    instance_id: String,
) -> Result<Value, String> {
    state.api.unregister_registration(&instance_id).await
}

#[tauri::command]
async fn trigger_reconnect(state: tauri::State<'_, AppState>) -> Result<Value, String> {
    state.api.reconnect().await
}

#[tauri::command]
fn get_desktop_config() -> DesktopConfigView {
    let agent_http_addr =
        std::env::var("DEVLOOP_AGENT_HTTP_ADDR").unwrap_or_else(|_| "127.0.0.1:19090".to_string());
    let default_api_base =
        if agent_http_addr.starts_with("http://") || agent_http_addr.starts_with("https://") {
            agent_http_addr.clone()
        } else {
            format!("http://{}", agent_http_addr)
        };

    // 配置页统一读取运行时环境变量，保证 UI 展示的是实际启动参数。
    DesktopConfigView {
        agent_api_base: std::env::var("DEVLOOP_AGENT_API_BASE").unwrap_or(default_api_base.clone()),
        agent_binary: std::env::var("DEVLOOP_AGENT_BINARY").ok(),
        agent_core_dir: std::env::var("DEVLOOP_AGENT_CORE_DIR").ok(),
        agent_auto_restart: parse_auto_restart(
            std::env::var("DEVLOOP_AGENT_AUTO_RESTART").unwrap_or_else(|_| "true".to_string()),
        ),
        agent_restart_backoff_ms: parse_backoff(
            std::env::var("DEVLOOP_AGENT_RESTART_BACKOFF_MS")
                .unwrap_or_else(|_| "500,1000,2000,5000".to_string()),
        ),
        env_resolve_order: parse_env_resolve_order(
            std::env::var("DEVLOOP_ENV_RESOLVE_ORDER").unwrap_or_else(|_| {
                "requestHeader,payload,runtimeDefault,baseFallback".to_string()
            }),
        ),
        tunnel_bridge_address: std::env::var("DEVLOOP_TUNNEL_BRIDGE_ADDRESS")
            .unwrap_or_else(|_| "http://127.0.0.1:18080".to_string()),
        tunnel_backflow_base_url: std::env::var("DEVLOOP_TUNNEL_BACKFLOW_BASE_URL")
            .unwrap_or(default_api_base),
        platform: std::env::consts::OS.to_string(),
        arch: std::env::consts::ARCH.to_string(),
    }
}

#[tauri::command]
fn agent_runtime(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<AgentRuntime, String> {
    let mut manager = state
        .manager
        .lock()
        .map_err(|_| "manager lock poisoned".to_string())?;

    let changed = manager.poll_supervisor();
    let runtime = manager.runtime();
    drop(manager);

    if let Some(runtime_changed) = changed {
        let _ = emit_runtime_event(&app, &runtime_changed);
    }
    Ok(runtime)
}

#[tauri::command]
fn restart_agent_process(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
) -> Result<AgentRuntime, String> {
    let mut manager = state
        .manager
        .lock()
        .map_err(|_| "manager lock poisoned".to_string())?;

    let runtime = manager.restart_now()?;
    drop(manager);
    emit_runtime_event(&app, &runtime)?;
    Ok(runtime)
}

pub fn run() {
    let state = AppState {
        manager: Mutex::new(AgentManager::new()),
        api: AgentApiClient::new(
            std::env::var("DEVLOOP_AGENT_API_BASE")
                .unwrap_or_else(|_| "http://127.0.0.1:19090".to_string()),
        ),
    };

    tauri::Builder::default()
        .manage(state)
        .setup(|app| {
            // 启动阶段先拉起 agent-core，再向前端推送首个运行态快照。
            let startup_runtime = {
                let state = app.state::<AppState>();
                let startup = match state.manager.lock() {
                    Ok(mut manager) => match manager.ensure_started() {
                        Ok(runtime) => Some(runtime),
                        Err(err) => {
                            eprintln!("failed to auto start agent-core: {err}");
                            Some(manager.runtime())
                        }
                    },
                    Err(_) => None,
                };
                startup
            };

            if let Some(runtime) = startup_runtime {
                let _ = emit_runtime_event(app.handle(), &runtime);
            }

            // 后台监督循环：定期检测子进程状态并自动重启。
            spawn_agent_supervisor(app.handle().clone());
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            get_state_summary,
            get_tunnel_state,
            get_registrations,
            get_recent_errors,
            get_recent_requests,
            get_active_intercepts,
            unregister_registration,
            trigger_reconnect,
            get_desktop_config,
            agent_runtime,
            restart_agent_process
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

fn spawn_agent_supervisor(app_handle: AppHandle) {
    std::thread::spawn(move || {
        loop {
            std::thread::sleep(Duration::from_secs(2));

            // 加锁仅用于状态更新，随后立即释放，避免阻塞主线程命令调用。
            let runtime_changed = {
                let state = app_handle.state::<AppState>();
                let Ok(mut manager) = state.manager.lock() else {
                    continue;
                };
                manager.poll_supervisor()
            };

            if let Some(runtime) = runtime_changed {
                let _ = emit_runtime_event(&app_handle, &runtime);
            }
        }
    });
}

fn emit_runtime_event(app_handle: &AppHandle, runtime: &AgentRuntime) -> Result<(), String> {
    app_handle
        .emit(AGENT_RUNTIME_EVENT, runtime)
        .map_err(|err| format!("emit runtime event failed: {err}"))
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

fn parse_env_resolve_order(value: String) -> Vec<String> {
    let parsed = value
        .split(',')
        .map(|item| item.trim().to_string())
        .filter(|item| !item.is_empty())
        .collect::<Vec<_>>();
    if parsed.is_empty() {
        return vec![
            "requestHeader".to_string(),
            "payload".to_string(),
            "runtimeDefault".to_string(),
            "baseFallback".to_string(),
        ];
    }
    parsed
}
