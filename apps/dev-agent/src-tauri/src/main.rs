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
use std::sync::atomic::Ordering;
use std::sync::Arc;
use tauri::menu::{Menu, MenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{Emitter, Manager};

use agent_host::launcher::{ensure_single_instance_guard, resolve_runtime_dir};
use agent_host::supervisor::{
    app_shutdown_impl, run_mock_agent_runtime_if_requested, spawn_supervisor_monitor,
};
use commands::{
    agent_crash_inject, agent_restart, agent_snapshot, agent_start, agent_stop, app_bootstrap,
    app_confirm_exit, app_hide_to_tray, app_shutdown, diagnose_logs_snapshot, diagnose_snapshot,
    host_config_snapshot, host_config_update, host_config_update_diagnose_filter,
    host_logs_snapshot, service_add,
    service_list_snapshot, session_drain, session_reconnect, session_snapshot,
    system_resource_snapshot, traffic_stats_snapshot, tunnel_list_snapshot,
};
use state::app_state::{now_ms, push_host_log, AppRuntimeState, HostRuntimeConfig};

const EVENT_APP_CLOSE_REQUESTED: &str = "app-close-requested";
const TRAY_MENU_SHOW_MAIN_ID: &str = "tray-show-main";
const TRAY_MENU_EXIT_ID: &str = "tray-exit-app";

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

fn show_main_window(app: &tauri::AppHandle) {
    if let Some(main_window) = app.get_webview_window("main") {
        let _ = main_window.show();
        let _ = main_window.unminimize();
        let _ = main_window.set_focus();
    }
}

fn request_app_exit(app: &tauri::AppHandle) {
    let runtime_state = app.state::<Arc<AppRuntimeState>>().inner().clone();
    if let Err(err) = app_shutdown_impl(app, &runtime_state) {
        append_startup_log("error", &format!("托盘退出时执行 app_shutdown 失败: {err}"));
    }
    app.exit(0);
}

fn setup_tray_icon(app: &tauri::AppHandle) -> Result<(), String> {
    let show_main_item = MenuItem::with_id(
        app,
        TRAY_MENU_SHOW_MAIN_ID,
        "显示主窗口",
        true,
        None::<&str>,
    )
    .map_err(|err| format!("创建托盘菜单项失败(显示主窗口): {err}"))?;
    let exit_item = MenuItem::with_id(app, TRAY_MENU_EXIT_ID, "退出应用", true, None::<&str>)
        .map_err(|err| format!("创建托盘菜单项失败(退出应用): {err}"))?;
    let tray_menu = Menu::with_items(app, &[&show_main_item, &exit_item])
        .map_err(|err| format!("创建托盘菜单失败: {err}"))?;

    let mut tray_builder = TrayIconBuilder::with_id("dev-agent-tray")
        .menu(&tray_menu)
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| match event.id().as_ref() {
            TRAY_MENU_SHOW_MAIN_ID => show_main_window(app),
            TRAY_MENU_EXIT_ID => request_app_exit(app),
            _ => {}
        })
        .on_tray_icon_event(|tray, event| {
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                show_main_window(&tray.app_handle());
            }
        });
    if let Some(icon) = app.default_window_icon().cloned() {
        tray_builder = tray_builder.icon(icon);
    }
    let _tray_icon = tray_builder
        .build(app)
        .map_err(|err| format!("创建托盘图标失败: {err}"))?;
    Ok(())
}

/// 程序入口：启动 Tauri 宿主并拉起真实 Agent runtime。
fn main() {
    if run_mock_agent_runtime_if_requested() {
        // 当前进程被用于 mock runtime，已完成无 UI 执行路径。
        return;
    }

    let (runtime_config, config_warning) = match HostRuntimeConfig::load_with_yaml_fallback() {
        Ok(payload) => payload,
        Err(err) => {
            report_fatal_startup_error("初始化宿主运行配置失败", &err);
            std::process::exit(1);
        }
    };
    let shared_state = Arc::new(AppRuntimeState::new(runtime_config));
    if let Some(warning) = config_warning {
        append_startup_log("warn", &warning);
        if let Ok(mut supervisor) = shared_state.supervisor.lock() {
            push_host_log(
                &mut supervisor,
                "warn",
                "startup",
                "HOST_CONFIG_YAML_INVALID",
                warning,
            );
        }
    }

    let app = match tauri::Builder::default()
        .plugin(tauri_plugin_single_instance::init(|app, _args, _cwd| {
            // 第二个实例启动时触发这里
            // 把已有窗口拉到前台
            let window = app.get_webview_window("main").unwrap();
            window.show().unwrap();
            window.set_focus().unwrap();
        }))
        .manage(shared_state.clone())
        .setup(|app| {
            let state = app.state::<Arc<AppRuntimeState>>().inner().clone();
            ensure_single_instance_guard(&state)
                .map_err(|err| std::io::Error::new(std::io::ErrorKind::AlreadyExists, err))?;
            spawn_supervisor_monitor(app.handle().clone(), state);
            setup_tray_icon(&app.handle())
                .map_err(|err| std::io::Error::new(std::io::ErrorKind::Other, err))?;
            Ok(())
        })
        .invoke_handler(tauri::generate_handler![
            app_bootstrap,
            app_shutdown,
            app_hide_to_tray,
            app_confirm_exit,
            agent_start,
            agent_stop,
            agent_restart,
            agent_snapshot,
            host_config_snapshot,
            host_config_update,
            host_config_update_diagnose_filter,
            host_logs_snapshot,
            diagnose_snapshot,
            diagnose_logs_snapshot,
            session_snapshot,
            session_reconnect,
            session_drain,
            service_add,
            service_list_snapshot,
            system_resource_snapshot,
            traffic_stats_snapshot,
            tunnel_list_snapshot,
            agent_crash_inject
        ])
        .build(tauri::generate_context!())
    {
        Ok(app) => app,
        Err(err) => {
            report_fatal_startup_error("初始化 dev-agent Tauri 宿主失败", &err.to_string());
            std::process::exit(1);
        }
    };

    let mut exit_cleanup_done = false;
    app.run(move |app_handle, run_event| {
        if let tauri::RunEvent::WindowEvent { label, event, .. } = &run_event {
            if label == "main" {
                if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                    let runtime_state = app_handle.state::<Arc<AppRuntimeState>>().inner().clone();
                    // 已经进入主动退出流程时放行 close，避免确认弹窗阻塞真实退出。
                    if !runtime_state.shutdown_requested.load(Ordering::SeqCst) {
                        api.prevent_close();
                        let _ = app_handle.emit_to("main", EVENT_APP_CLOSE_REQUESTED, ());
                    }
                }
            }
        }

        let should_cleanup = matches!(
            run_event,
            tauri::RunEvent::ExitRequested { .. } | tauri::RunEvent::Exit
        );
        if !should_cleanup || exit_cleanup_done {
            return;
        }
        exit_cleanup_done = true;
        let runtime_state = app_handle.state::<Arc<AppRuntimeState>>().inner().clone();
        if !runtime_state.shutdown_requested.load(Ordering::SeqCst) {
            if let Err(err) = app_shutdown_impl(app_handle, &runtime_state) {
                append_startup_log("error", &format!("退出时执行 app_shutdown 失败: {err}"));
            }
        }
    });
}
