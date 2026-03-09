mod agent_api;
mod agent_manager;

use agent_api::AgentApiClient;
use agent_manager::{AgentManager, AgentRuntime};
use serde_json::Value;
use std::sync::Mutex;

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
fn agent_runtime(state: tauri::State<'_, AppState>) -> Result<AgentRuntime, String> {
    let mut manager = state
        .manager
        .lock()
        .map_err(|_| "manager lock poisoned".to_string())?;
    Ok(manager.runtime())
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
        .setup(|app| {
            let state = app.state::<AppState>();
            if let Ok(mut manager) = state.manager.lock() {
                if let Err(err) = manager.ensure_started() {
                    eprintln!("failed to auto start agent-core: {err}");
                }
            }
            Ok(())
        })
        .manage(state)
        .invoke_handler(tauri::generate_handler![
            get_state_summary,
            get_tunnel_state,
            get_registrations,
            get_recent_errors,
            trigger_reconnect,
            agent_runtime
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}
