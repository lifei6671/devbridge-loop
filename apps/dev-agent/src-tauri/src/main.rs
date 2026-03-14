#![cfg_attr(not(debug_assertions), windows_subsystem = "windows")]

mod agent_host;
mod commands;
mod state;

#[cfg(windows)]
use std::ffi::OsStr;
use std::fs::{self, OpenOptions};
use std::io::Write;
#[cfg(windows)]
use std::os::windows::ffi::OsStrExt;
use std::path::PathBuf;
use std::sync::Arc;
use tauri::Manager;

use agent_host::launcher::{ensure_single_instance_guard, resolve_runtime_dir};
use agent_host::supervisor::{run_mock_agent_runtime_if_requested, spawn_supervisor_monitor};
use commands::{
    agent_crash_inject, agent_restart, agent_snapshot, agent_start, agent_stop, app_bootstrap,
    app_shutdown, host_config_snapshot, host_config_update, host_logs_snapshot,
    service_list_snapshot, tunnel_list_snapshot,
};
use state::app_state::{now_ms, AppRuntimeState, HostRuntimeConfig};

/// 计算启动日志文件路径：默认放到运行目录下的 logs 目录。
fn startup_log_path() -> PathBuf {
    let runtime_dir = resolve_runtime_dir();
    // 日志目录放在 runtime_dir 下，确保在 XDG_RUNTIME_DIR 场景可写。
    runtime_dir.join("logs").join("startup.log")
}

/// 追加一条启动日志，便于排查“闪退无输出”问题。
fn append_startup_log(level: &str, message: &str) {
    let log_path = startup_log_path();
    if let Some(parent_dir) = log_path.parent() {
        let _ = fs::create_dir_all(parent_dir);
    }
    if let Ok(mut file) = OpenOptions::new().create(true).append(true).open(&log_path) {
        // 记录时间戳、级别和消息，方便用户直接贴日志排查。
        let _ = writeln!(file, "[{}][{}] {}", now_ms(), level, message);
    }
}

/// Windows 平台弹出错误提示，避免 GUI 子系统下错误被静默吞掉。
#[cfg(windows)]
fn show_windows_error_dialog(title: &str, body: &str) {
    type Handle = *mut std::ffi::c_void;

    unsafe extern "system" {
        fn MessageBoxW(
            hwnd: Handle,
            text: *const u16,
            caption: *const u16,
            message_box_type: u32,
        ) -> i32;
    }

    const MB_OK: u32 = 0x0000_0000;
    const MB_ICONERROR: u32 = 0x0000_0010;

    // Win32 API 需要 UTF-16 + NUL 结尾字符串。
    let title_wide = OsStr::new(title)
        .encode_wide()
        .chain(std::iter::once(0))
        .collect::<Vec<_>>();
    let body_wide = OsStr::new(body)
        .encode_wide()
        .chain(std::iter::once(0))
        .collect::<Vec<_>>();
    unsafe {
        let _ = MessageBoxW(
            std::ptr::null_mut(),
            body_wide.as_ptr(),
            title_wide.as_ptr(),
            MB_OK | MB_ICONERROR,
        );
    }
}

/// 统一处理启动阶段致命错误：写日志 + 标准错误 + Windows 弹窗。
fn report_fatal_startup_error(context: &str, err: &str) {
    let detail = format!("{context}: {err}");
    eprintln!("{detail}");
    append_startup_log("error", &detail);
    #[cfg(windows)]
    {
        let log_hint = format!(
            "{detail}\n\n请查看日志：{}",
            startup_log_path().to_string_lossy()
        );
        show_windows_error_dialog("dev-agent 启动失败", &log_hint);
    }
}

/// 程序入口：先处理 mock runtime，再启动 Tauri 宿主。
fn main() {
    if run_mock_agent_runtime_if_requested() {
        return;
    }

    let runtime_config = match HostRuntimeConfig::from_env() {
        Ok(config) => config,
        Err(err) => {
            report_fatal_startup_error("初始化宿主运行配置失败", &err);
            std::process::exit(1);
        }
    };
    let shared_state = Arc::new(AppRuntimeState::new(runtime_config));

    let run_result = tauri::Builder::default()
        .manage(shared_state.clone())
        .setup(|app| {
            let state = app.state::<Arc<AppRuntimeState>>().inner().clone();
            ensure_single_instance_guard(&state)
                .map_err(|err| std::io::Error::new(std::io::ErrorKind::AlreadyExists, err))?;
            spawn_supervisor_monitor(app.handle().clone(), state);
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            app_bootstrap,
            app_shutdown,
            agent_start,
            agent_stop,
            agent_restart,
            agent_snapshot,
            host_config_snapshot,
            host_config_update,
            host_logs_snapshot,
            service_list_snapshot,
            tunnel_list_snapshot,
            agent_crash_inject
        ])
        .run(tauri::generate_context!());

    if let Err(err) = run_result {
        report_fatal_startup_error("运行 dev-agent Tauri 宿主失败", &err.to_string());
        std::process::exit(1);
    }
}
