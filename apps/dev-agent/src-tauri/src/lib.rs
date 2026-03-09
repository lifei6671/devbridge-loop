mod agent_api;
mod agent_manager;

use agent_api::AgentApiClient;
use agent_manager::{AgentManager, AgentRuntime};
use serde_json::Value;
use std::sync::Mutex;
use std::time::Duration;
use tauri::{AppHandle, Emitter, Manager};

const AGENT_RUNTIME_EVENT: &str = "agent-runtime-changed";

struct AppState {
    manager: Mutex<AgentManager>,
    api: AgentApiClient,
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
async fn trigger_reconnect(state: tauri::State<'_, AppState>) -> Result<Value, String> {
    state.api.reconnect().await
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
            let mut startup_runtime = None;
            {
                let state = app.state::<AppState>();
                if let Ok(mut manager) = state.manager.lock() {
                    match manager.ensure_started() {
                        Ok(runtime) => {
                            startup_runtime = Some(runtime);
                        }
                        Err(err) => {
                            eprintln!("failed to auto start agent-core: {err}");
                            startup_runtime = Some(manager.runtime());
                        }
                    }
                }
            }

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
            trigger_reconnect,
            agent_runtime,
            restart_agent_process
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

fn spawn_agent_supervisor(app_handle: AppHandle) {
    tauri::async_runtime::spawn(async move {
        loop {
            tauri::async_runtime::sleep(Duration::from_secs(2)).await;

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
