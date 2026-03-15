use std::sync::Arc;
use tauri::{AppHandle, Manager, State};

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

/// Tauri command：隐藏主窗口到托盘，不终止宿主与 Agent 进程。
#[tauri::command]
pub fn app_hide_to_tray(app: AppHandle) -> Result<(), String> {
    let Some(main_window) = app.get_webview_window("main") else {
        return Err("隐藏到托盘失败：主窗口不存在".to_string());
    };
    main_window
        .hide()
        .map_err(|err| format!("隐藏到托盘失败: {err}"))
}

/// Tauri command：确认退出应用，先执行 app_shutdown，再结束宿主进程。
#[tauri::command]
pub fn app_confirm_exit(
    app: AppHandle,
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<(), String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let _ = app_shutdown_impl(&app, &shared)?;
        app.exit(0);
        Ok(())
    })
}
