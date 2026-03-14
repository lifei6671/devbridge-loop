use std::sync::Arc;
use tauri::State;

use crate::state::app_state::{with_rpc_metrics, AppRuntimeState, HostLogEntry};

/// Tauri command：获取宿主结构化日志快照。
#[tauri::command]
pub fn host_logs_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<Vec<HostLogEntry>, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let supervisor = shared
            .supervisor
            .lock()
            .map_err(|_| "读取宿主日志失败：supervisor 锁异常".to_string())?;
        Ok(supervisor.host_logs.iter().cloned().collect::<Vec<_>>())
    })
}
