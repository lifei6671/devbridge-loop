mod agent_api;
mod agent_manager;
mod host_config;

use agent_api::AgentApiClient;
use agent_manager::{AgentManager, AgentRuntime};
use host_config::{
    apply_persisted_env_overrides, load_close_to_tray_on_close, load_desktop_config,
    persist_close_to_tray_on_close, resolve_storage_paths,
    save_desktop_config as save_desktop_config_file, DesktopConfigSaveRequest, DesktopConfigView,
    HostStoragePaths,
};
use serde_json::Value;
use std::sync::Mutex;
use std::time::Duration;
use tauri::menu::{Menu, MenuItem, PredefinedMenuItem};
use tauri::tray::{MouseButton, MouseButtonState, TrayIconBuilder, TrayIconEvent};
use tauri::{AppHandle, Emitter, Manager, Window};

const AGENT_RUNTIME_EVENT: &str = "agent-runtime-changed";
const MAIN_WINDOW_LABEL: &str = "main";
const TRAY_ICON_ID: &str = "devloop-main-tray";
const TRAY_MENU_SHOW_MAIN: &str = "tray-show-main";
const TRAY_MENU_HIDE_MAIN: &str = "tray-hide-main";
const TRAY_MENU_QUIT_APP: &str = "tray-quit-app";
const WINDOW_CLOSE_INTENT_EVENT: &str = "window-close-intent";

struct AppState {
    manager: Mutex<AgentManager>,
    close_to_tray_on_close: Mutex<Option<bool>>,
    api: AgentApiClient,
    storage: HostStoragePaths,
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
async fn get_diagnostics(state: tauri::State<'_, AppState>) -> Result<Value, String> {
    state.api.diagnostics().await
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
fn get_desktop_config(state: tauri::State<'_, AppState>) -> Result<DesktopConfigView, String> {
    load_desktop_config(&state.storage)
}

#[tauri::command]
fn save_desktop_config(
    state: tauri::State<'_, AppState>,
    request: DesktopConfigSaveRequest,
) -> Result<DesktopConfigView, String> {
    let view = save_desktop_config_file(&state.storage, request)?;
    let mut close_to_tray_on_close = state
        .close_to_tray_on_close
        .lock()
        .map_err(|_| "close_to_tray_on_close lock poisoned".to_string())?;
    *close_to_tray_on_close = Some(view.close_to_tray_on_close);
    Ok(view)
}

#[tauri::command]
fn resolve_window_close_action(
    app: tauri::AppHandle,
    state: tauri::State<'_, AppState>,
    action: String,
) -> Result<(), String> {
    let normalized = action.trim().to_ascii_lowercase();
    match normalized.as_str() {
        "tray" | "minimize" => {
            // 用户在首次关闭弹窗里选择最小化时，自动持久化该偏好，后续不再弹窗。
            persist_close_to_tray_on_close(&state.storage, true)?;
            let mut close_to_tray_on_close = state
                .close_to_tray_on_close
                .lock()
                .map_err(|_| "close_to_tray_on_close lock poisoned".to_string())?;
            *close_to_tray_on_close = Some(true);
            drop(close_to_tray_on_close);
            hide_main_window(&app)
        }
        "exit" => {
            request_app_exit(&app, 0);
            Ok(())
        }
        _ => Err(format!("unsupported close action: {action}")),
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
async fn restart_agent_process(
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

    // 重启成功后立即触发一次 bridge 重连，避免等待下个轮询窗口才开始连接。
    let api = state.api.clone();
    tauri::async_runtime::spawn(async move {
        trigger_reconnect_after_restart(api).await;
    });

    Ok(runtime)
}

pub fn run() {
    tauri::Builder::default()
        .on_window_event(|window, event| {
            if window.label() != MAIN_WINDOW_LABEL {
                return;
            }

            // 主窗口关闭行为支持三态：
            // 1) 已配置最小化：直接隐藏到托盘
            // 2) 已配置退出：直接退出应用
            // 3) 未配置：通知前端弹出选择框
            if let tauri::WindowEvent::CloseRequested { api, .. } = event {
                api.prevent_close();
                let _ = handle_main_window_close_requested(window);
            }
        })
        .setup(|app| {
            setup_tray(app.handle()).map_err(|err| std::io::Error::other(err))?;

            let storage =
                resolve_storage_paths(app.handle()).map_err(|err| std::io::Error::other(err))?;
            apply_persisted_env_overrides(&storage).map_err(|err| std::io::Error::other(err))?;
            let close_to_tray_on_close =
                load_close_to_tray_on_close(&storage).map_err(|err| std::io::Error::other(err))?;

            // 先应用配置文件中的环境变量，再创建运行时依赖，确保启动参数与配置页一致。
            app.manage(AppState {
                manager: Mutex::new(AgentManager::new()),
                close_to_tray_on_close: Mutex::new(close_to_tray_on_close),
                api: AgentApiClient::new(
                    std::env::var("DEVLOOP_AGENT_API_BASE")
                        .unwrap_or_else(|_| "http://127.0.0.1:39090".to_string()),
                ),
                storage,
            });

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
            get_diagnostics,
            get_registrations,
            get_recent_errors,
            get_recent_requests,
            get_active_intercepts,
            unregister_registration,
            trigger_reconnect,
            get_desktop_config,
            save_desktop_config,
            resolve_window_close_action,
            agent_runtime,
            restart_agent_process
        ])
        .run(tauri::generate_context!())
        .expect("error while running tauri application");
}

fn handle_main_window_close_requested(window: &Window) -> Result<(), String> {
    let app = window.app_handle();
    let close_to_tray_on_close = {
        let state = app.state::<AppState>();
        let close_to_tray_on_close = state
            .close_to_tray_on_close
            .lock()
            .map_err(|_| "close_to_tray_on_close lock poisoned".to_string())?;
        *close_to_tray_on_close
    };

    match close_to_tray_on_close {
        Some(true) => window
            .hide()
            .map_err(|err| format!("hide main window failed: {err}")),
        Some(false) => {
            request_app_exit(&app, 0);
            Ok(())
        }
        None => app
            .emit(WINDOW_CLOSE_INTENT_EVENT, ())
            .map_err(|err| format!("emit window close intent failed: {err}")),
    }
}

fn request_app_exit(app: &AppHandle, code: i32) {
    // 退出主进程前主动终止由 Host 拉起的 agent-core，避免孤儿进程残留。
    if let Err(err) = shutdown_managed_agent(app) {
        eprintln!("failed to shutdown managed agent-core before exit: {err}");
    }
    app.exit(code);
}

fn shutdown_managed_agent(app: &AppHandle) -> Result<(), String> {
    let state = app.state::<AppState>();
    let mut agent_manager = state
        .manager
        .lock()
        .map_err(|_| "manager lock poisoned".to_string())?;
    let runtime = agent_manager.shutdown();
    drop(agent_manager);

    // 退出前推送一次 agent 运行态，确保前端最后一帧状态一致。
    let _ = emit_runtime_event(app, &runtime);
    Ok(())
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

async fn trigger_reconnect_after_restart(api: AgentApiClient) {
    const MAX_ATTEMPTS: usize = 6;
    let retry_interval = Duration::from_millis(300);

    for attempt in 0..MAX_ATTEMPTS {
        match api.reconnect().await {
            Ok(_) => return,
            Err(err) => {
                if attempt + 1 == MAX_ATTEMPTS {
                    eprintln!("failed to trigger bridge reconnect after agent restart: {err}");
                    return;
                }
            }
        }

        std::thread::sleep(retry_interval);
    }
}

fn setup_tray(app: &AppHandle) -> Result<(), String> {
    // 托盘菜单提供主窗口显隐和完整退出两个核心操作，避免误关窗口导致进程退出。
    let show_main = MenuItem::with_id(app, TRAY_MENU_SHOW_MAIN, "显示主窗口", true, None::<&str>)
        .map_err(|err| format!("create tray menu item(show) failed: {err}"))?;
    let hide_main = MenuItem::with_id(app, TRAY_MENU_HIDE_MAIN, "隐藏到托盘", true, None::<&str>)
        .map_err(|err| format!("create tray menu item(hide) failed: {err}"))?;
    let quit_app = MenuItem::with_id(
        app,
        TRAY_MENU_QUIT_APP,
        "退出 DevLoop Agent",
        true,
        None::<&str>,
    )
    .map_err(|err| format!("create tray menu item(quit) failed: {err}"))?;
    let separator = PredefinedMenuItem::separator(app)
        .map_err(|err| format!("create tray menu separator failed: {err}"))?;
    let tray_menu = Menu::with_items(app, &[&show_main, &hide_main, &separator, &quit_app])
        .map_err(|err| format!("create tray menu failed: {err}"))?;

    let mut tray_builder = TrayIconBuilder::with_id(TRAY_ICON_ID)
        .menu(&tray_menu)
        .tooltip("DevLoop Agent")
        .show_menu_on_left_click(false)
        .on_menu_event(|app, event| {
            if event.id() == TRAY_MENU_SHOW_MAIN {
                let _ = show_main_window(app);
                return;
            }
            if event.id() == TRAY_MENU_HIDE_MAIN {
                let _ = hide_main_window(app);
                return;
            }
            if event.id() == TRAY_MENU_QUIT_APP {
                request_app_exit(app, 0);
            }
        })
        .on_tray_icon_event(|tray, event| {
            // 左键抬起时恢复主窗口，右键仍用于弹出菜单。
            if let TrayIconEvent::Click {
                button: MouseButton::Left,
                button_state: MouseButtonState::Up,
                ..
            } = event
            {
                let _ = show_main_window(tray.app_handle());
            }
        });

    if let Some(icon) = app.default_window_icon().cloned() {
        tray_builder = tray_builder.icon(icon);
    }

    tray_builder
        .build(app)
        .map_err(|err| format!("build tray icon failed: {err}"))?;
    Ok(())
}

fn show_main_window(app: &AppHandle) -> Result<(), String> {
    let window = app
        .get_webview_window(MAIN_WINDOW_LABEL)
        .ok_or_else(|| format!("main window not found: {MAIN_WINDOW_LABEL}"))?;

    // 恢复窗口时尽量取消最小化并聚焦；聚焦失败不影响显示。
    window
        .show()
        .map_err(|err| format!("show main window failed: {err}"))?;
    let _ = window.unminimize();
    let _ = window.set_focus();
    Ok(())
}

fn hide_main_window(app: &AppHandle) -> Result<(), String> {
    let Some(window) = app.get_webview_window(MAIN_WINDOW_LABEL) else {
        return Ok(());
    };
    window
        .hide()
        .map_err(|err| format!("hide main window failed: {err}"))
}
