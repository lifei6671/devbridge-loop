use std::sync::Arc;
use tauri::{AppHandle, State};

use crate::agent_host::supervisor::{app_bootstrap_impl, app_shutdown_impl};
use crate::state::app_state::{
    with_rpc_metrics, AgentRuntimeSnapshot, AppBootstrapPayload, AppRuntimeState,
};

/// Tauri command：初始化宿主并返回 bootstrap 数据。
#[tauri::command]
pub fn app_bootstrap(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AppBootstrapPayload, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || app_bootstrap_impl(&app, &shared))
}

/// Tauri command：执行宿主关闭流程。
#[tauri::command]
pub fn app_shutdown(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<AgentRuntimeSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || app_shutdown_impl(&app, &shared))
}
