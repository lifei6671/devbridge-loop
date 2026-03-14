use std::sync::Arc;
use tauri::{AppHandle, State};

use crate::agent_host::supervisor::{
    agent_crash_inject_impl, agent_restart_impl, agent_start_impl, agent_stop_impl,
};
use crate::state::app_state::{
    current_snapshot, with_rpc_metrics, AgentRuntimeSnapshot, AppRuntimeState,
};

/// Tauri command：启动 Agent。
#[tauri::command]
pub fn agent_start(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || agent_start_impl(&app, &shared))
}

/// Tauri command：停止 Agent（expected exit）。
#[tauri::command]
pub fn agent_stop(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || agent_stop_impl(&app, &shared))
}

/// Tauri command：重启 Agent（stop -> start）。
#[tauri::command]
pub fn agent_restart(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || agent_restart_impl(&app, &shared))
}

/// Tauri command：获取最新运行态快照。
#[tauri::command]
pub fn agent_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || current_snapshot(&shared))
}

/// Tauri command：注入崩溃场景，便于验证自动恢复链路。
#[tauri::command]
pub fn agent_crash_inject(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || agent_crash_inject_impl(&app, &shared))
}
