use serde_json::{json, Value};
#[cfg(unix)]
use std::fs;
#[cfg(unix)]
use std::os::unix::fs::{FileTypeExt, PermissionsExt};
#[cfg(unix)]
use std::os::unix::net::UnixListener;
#[cfg(unix)]
use std::path::PathBuf;
use std::sync::atomic::Ordering;
use std::sync::Arc;
use std::thread;
use std::time::{Duration, Instant};
use tauri::AppHandle;

use super::auth::{
    compute_agent_proof, compute_client_proof, constant_time_eq, generate_random_hex,
    validate_hex_token, AUTH_PROTOCOL_VERSION, PROOF_HEX_LEN,
};
use super::codec::{parse_json_body, read_local_rpc_frame, write_local_rpc_frame};
use super::event_bridge::emit_runtime_changed;
use super::frame::{LocalRpcFrame, LocalRpcFrameType};
use super::ipc_client::{LocalRpcClient, LOCAL_RPC_DEFAULT_TIMEOUT_MS};
#[cfg(unix)]
use super::launcher::ensure_secure_dir;
use super::launcher::{generate_session_secret, prepare_ipc_endpoint, spawn_agent_process};
use crate::state::app_state::{
    build_runtime_snapshot, current_host_config_snapshot, current_runtime_config, now_ms,
    push_host_log, AgentRuntimeSnapshot, AppBootstrapPayload, AppRuntimeState, ConnectionState,
    DesiredState, ExitKind, RESTART_BACKOFF_MS, STOP_TIMEOUT_MS, SUPERVISOR_POLL_MS,
};

/// 后台事件泵间隔：主动触发一次 ping 以携带服务端事件。
const IPC_EVENT_PUMP_INTERVAL_MS: u64 = 1_000;

/// 设置连接状态并生成快照，供事件桥统一下发。
fn set_connection_state(
    state: &Arc<AppRuntimeState>,
    next_state: ConnectionState,
    log_code: &str,
    log_message: &str,
) -> Result<AgentRuntimeSnapshot, String> {
    let mut supervisor = state
        .supervisor
        .lock()
        .map_err(|_| "更新连接状态失败：supervisor 锁异常".to_string())?;
    if supervisor.child.is_none() {
        return Ok(build_runtime_snapshot(&supervisor));
    }
    supervisor.connection_state = next_state;
    supervisor.updated_at_ms = now_ms();
    push_host_log(&mut supervisor, "info", "ipc_client", log_code, log_message);
    Ok(build_runtime_snapshot(&supervisor))
}

/// 执行启动与连接阶段切换：reconnecting -> resyncing -> connected。
fn start_agent_sequence(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
    connected_reason: &str,
) -> Result<AgentRuntimeSnapshot, String> {
    let session_secret = generate_session_secret()?;
    // 每次启动前读取一次最新配置，确保配置页保存后下一次重启可立即生效。
    let runtime_config = current_runtime_config(state)?;
    prepare_ipc_endpoint(&runtime_config.ipc_transport, &runtime_config.ipc_endpoint)?;
    let child = match spawn_agent_process(&runtime_config, &session_secret) {
        Ok(child) => child,
        Err(err) => {
            let snapshot = {
                let mut supervisor = state
                    .supervisor
                    .lock()
                    .map_err(|_| "更新启动失败状态时锁异常".to_string())?;
                supervisor.connection_state = ConnectionState::Disconnected;
                supervisor.exit_kind = ExitKind::Unexpected;
                supervisor.pending_auto_restart =
                    matches!(supervisor.desired_state, DesiredState::Running);
                supervisor.last_error = Some(err.clone());
                supervisor.updated_at_ms = now_ms();
                push_host_log(
                    &mut supervisor,
                    "error",
                    "supervisor",
                    "AGENT_START_FAILED",
                    err.clone(),
                );
                build_runtime_snapshot(&supervisor)
            };
            emit_runtime_changed(app, state, "agent.start.failed", snapshot, true);
            return Err(err);
        }
    };
    let spawned_pid = child.id();

    let reconnecting_snapshot = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "写入启动状态时锁异常".to_string())?;
        let had_started_before = supervisor.started_at_ms.is_some();

        supervisor.desired_state = DesiredState::Running;
        supervisor.exit_kind = ExitKind::Unexpected;
        supervisor.connection_state = ConnectionState::Reconnecting;
        supervisor.expecting_exit = false;
        supervisor.pending_auto_restart = false;
        supervisor.last_error = None;
        supervisor.updated_at_ms = now_ms();
        supervisor.child = Some(child);
        supervisor.pid = supervisor.child.as_ref().map(std::process::Child::id);
        if supervisor.started_at_ms.is_none() {
            supervisor.started_at_ms = Some(now_ms());
        }
        if had_started_before {
            // 仅在非首次启动时累计 reconnect_total。
            supervisor.reconnect_total += 1;
        }
        let started_pid = supervisor.pid;
        push_host_log(
            &mut supervisor,
            "info",
            "launcher",
            "AGENT_STARTED",
            format!("Agent 进程已启动，pid={started_pid:?}"),
        );
        build_runtime_snapshot(&supervisor)
    };
    emit_runtime_changed(app, state, "ipc.reconnecting", reconnecting_snapshot, true);

    let mut rpc_client = match LocalRpcClient::connect(
        &runtime_config.ipc_transport,
        &runtime_config.ipc_endpoint,
        &session_secret,
        Some(spawned_pid),
    ) {
        Ok(client) => client,
        Err(err) => {
            let (snapshot, child_to_kill) = {
                let mut supervisor = state
                    .supervisor
                    .lock()
                    .map_err(|_| "更新连接失败状态时锁异常".to_string())?;
                supervisor.connection_state = ConnectionState::Disconnected;
                supervisor.pending_auto_restart =
                    matches!(supervisor.desired_state, DesiredState::Running);
                supervisor.last_error = Some(err.clone());
                supervisor.updated_at_ms = now_ms();
                let child_to_kill = supervisor.child.take();
                supervisor.pid = None;
                supervisor.ipc_client = None;
                push_host_log(
                    &mut supervisor,
                    "error",
                    "ipc_client",
                    "IPC_CONNECT_FAILED",
                    format!("连接本地 IPC 失败: {err}"),
                );
                (build_runtime_snapshot(&supervisor), child_to_kill)
            };
            if let Some(mut child) = child_to_kill {
                let _ = child.kill();
                let _ = child.wait();
            }
            emit_runtime_changed(app, state, "ipc.connect.failed", snapshot, true);
            return Err(err);
        }
    };

    if let Err(err) = rpc_client.request("app.bootstrap", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS) {
        let (snapshot, child_to_kill) = {
            let mut supervisor = state
                .supervisor
                .lock()
                .map_err(|_| "更新 bootstrap 失败状态时锁异常".to_string())?;
            supervisor.connection_state = ConnectionState::Disconnected;
            supervisor.pending_auto_restart =
                matches!(supervisor.desired_state, DesiredState::Running);
            supervisor.last_error = Some(err.clone());
            supervisor.updated_at_ms = now_ms();
            let child_to_kill = supervisor.child.take();
            supervisor.pid = None;
            supervisor.ipc_client = None;
            push_host_log(
                &mut supervisor,
                "error",
                "ipc_client",
                "RPC_BOOTSTRAP_FAILED",
                format!("执行 app.bootstrap 失败: {err}"),
            );
            (build_runtime_snapshot(&supervisor), child_to_kill)
        };
        if let Some(mut child) = child_to_kill {
            let _ = child.kill();
            let _ = child.wait();
        }
        emit_runtime_changed(app, state, "rpc.bootstrap.failed", snapshot, true);
        return Err(err);
    }

    let resyncing_snapshot = set_connection_state(
        state,
        ConnectionState::Resyncing,
        "IPC_RESYNCING",
        "IPC 重连成功，开始全量状态对账",
    )?;
    emit_runtime_changed(app, state, "ipc.resyncing", resyncing_snapshot, false);

    if let Err(err) = rpc_client.request("agent.snapshot", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS)
    {
        let (snapshot, child_to_kill) = {
            let mut supervisor = state
                .supervisor
                .lock()
                .map_err(|_| "更新 resync 失败状态时锁异常".to_string())?;
            supervisor.connection_state = ConnectionState::Disconnected;
            supervisor.pending_auto_restart =
                matches!(supervisor.desired_state, DesiredState::Running);
            supervisor.last_error = Some(err.clone());
            supervisor.updated_at_ms = now_ms();
            let child_to_kill = supervisor.child.take();
            supervisor.pid = None;
            supervisor.ipc_client = None;
            push_host_log(
                &mut supervisor,
                "error",
                "ipc_client",
                "RPC_RESYNC_FAILED",
                format!("执行 agent.snapshot 失败: {err}"),
            );
            (build_runtime_snapshot(&supervisor), child_to_kill)
        };
        if let Some(mut child) = child_to_kill {
            let _ = child.kill();
            let _ = child.wait();
        }
        emit_runtime_changed(app, state, "rpc.resync.failed", snapshot, true);
        return Err(err);
    }

    if let Err(err) = rpc_client.ping() {
        let (snapshot, child_to_kill) = {
            let mut supervisor = state
                .supervisor
                .lock()
                .map_err(|_| "更新 ping 失败状态时锁异常".to_string())?;
            supervisor.connection_state = ConnectionState::Disconnected;
            supervisor.pending_auto_restart =
                matches!(supervisor.desired_state, DesiredState::Running);
            supervisor.last_error = Some(err.clone());
            supervisor.updated_at_ms = now_ms();
            let child_to_kill = supervisor.child.take();
            supervisor.pid = None;
            supervisor.ipc_client = None;
            push_host_log(
                &mut supervisor,
                "error",
                "ipc_client",
                "RPC_PING_FAILED",
                format!("执行 app.ping 失败: {err}"),
            );
            (build_runtime_snapshot(&supervisor), child_to_kill)
        };
        if let Some(mut child) = child_to_kill {
            let _ = child.kill();
            let _ = child.wait();
        }
        emit_runtime_changed(app, state, "rpc.ping.failed", snapshot, true);
        return Err(err);
    }

    let event_total = rpc_client.drain_events().len();
    {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "保存 IPC 客户端时锁异常".to_string())?;
        push_host_log(
            &mut supervisor,
            "info",
            "ipc_client",
            "RPC_CONNECTED",
            format!(
                "本地 RPC 已建立并完成 bootstrap/resync，{}，event_cache={event_total}",
                rpc_client.describe()
            ),
        );
        // 初始化事件泵时间戳，避免刚连上就重复 ping。
        supervisor.last_ipc_event_pump_ms = now_ms();
        supervisor.ipc_client = Some(rpc_client);
    }

    let connected_snapshot = set_connection_state(
        state,
        ConnectionState::Connected,
        "IPC_CONNECTED",
        "IPC 已进入 connected 状态",
    )?;
    emit_runtime_changed(
        app,
        state,
        connected_reason,
        connected_snapshot.clone(),
        true,
    );
    Ok(connected_snapshot)
}

/// 尝试停止 Agent 进程，并提供超时回收路径。
fn stop_agent_sequence(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
    reason: &str,
) -> Result<AgentRuntimeSnapshot, String> {
    let (child_to_stop, ipc_client_to_close) = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "写入停止状态时锁异常".to_string())?;
        supervisor.desired_state = DesiredState::Stopped;
        supervisor.exit_kind = ExitKind::Expected;
        supervisor.connection_state = ConnectionState::Disconnected;
        supervisor.expecting_exit = true;
        supervisor.pending_auto_restart = false;
        supervisor.last_error = None;
        supervisor.updated_at_ms = now_ms();
        push_host_log(
            &mut supervisor,
            "info",
            "supervisor",
            "AGENT_STOPPING",
            "开始执行 Agent 停止流程",
        );
        let child_to_stop = supervisor.child.take();
        supervisor.pid = None;
        let ipc_client_to_close = supervisor.ipc_client.take();
        (child_to_stop, ipc_client_to_close)
    };

    if let Some(mut ipc_client) = ipc_client_to_close {
        let _ = ipc_client.request("app.shutdown", json!({}), LOCAL_RPC_DEFAULT_TIMEOUT_MS);
        // 先断开 IPC，再终止进程，避免读写线程悬挂。
        ipc_client.close();
    }

    if let Some(mut child) = child_to_stop {
        let deadline = Instant::now() + Duration::from_millis(STOP_TIMEOUT_MS);
        // 先等待自然退出，留出“优雅停机”窗口。
        while Instant::now() < deadline {
            match child.try_wait() {
                Ok(Some(_)) => break,
                Ok(None) => thread::sleep(Duration::from_millis(80)),
                Err(_) => break,
            }
        }
        // 超时后执行强制终止，确保资源最终回收。
        if matches!(child.try_wait(), Ok(None)) {
            let _ = child.kill();
            let _ = child.wait();
        }
    }

    let stopped_snapshot = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "收尾停止流程时锁异常".to_string())?;
        supervisor.expecting_exit = false;
        supervisor.updated_at_ms = now_ms();
        push_host_log(
            &mut supervisor,
            "info",
            "supervisor",
            "AGENT_STOPPED",
            "Agent 停止流程完成",
        );
        build_runtime_snapshot(&supervisor)
    };
    emit_runtime_changed(app, state, reason, stopped_snapshot.clone(), true);
    Ok(stopped_snapshot)
}

/// 统一启动逻辑：处理重复启动与残留进程态。
fn start_agent(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
    connected_reason: &str,
) -> Result<AgentRuntimeSnapshot, String> {
    // 启动命令显式恢复 Supervisor 监控能力，避免 app.shutdown 后无法恢复。
    state.shutdown_requested.store(false, Ordering::SeqCst);
    let mut should_start = true;
    {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "准备启动 Agent 时锁异常".to_string())?;
        supervisor.desired_state = DesiredState::Running;
        supervisor.updated_at_ms = now_ms();
        if let Some(child) = supervisor.child.as_mut() {
            match child.try_wait() {
                Ok(None) => {
                    // 已经在运行，无需重复拉起。
                    should_start = false;
                }
                Ok(Some(_)) | Err(_) => {
                    // 进程句柄已失效，清理后走新启动流程。
                    supervisor.child = None;
                    supervisor.pid = None;
                    supervisor.ipc_client = None;
                }
            }
        }
    }

    if !should_start {
        let snapshot = crate::state::app_state::current_snapshot(state)?;
        emit_runtime_changed(app, state, "agent.start.noop", snapshot.clone(), false);
        return Ok(snapshot);
    }
    start_agent_sequence(app, state, connected_reason)
}

/// 执行 app.bootstrap：确保单实例、拉起 Agent、回传完整快照。
pub fn app_bootstrap_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AppBootstrapPayload, String> {
    super::launcher::ensure_single_instance_guard(state)?;
    // bootstrap 会恢复监控线程处理能力。
    state.shutdown_requested.store(false, Ordering::SeqCst);
    let snapshot = start_agent(app, state, "app.bootstrap.connected")?;
    Ok(AppBootstrapPayload {
        snapshot,
        host_config: current_host_config_snapshot(state)?,
    })
}

/// 执行 app.shutdown：禁止自动拉起并释放单实例锁。
pub fn app_shutdown_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AgentRuntimeSnapshot, String> {
    state.shutdown_requested.store(true, Ordering::SeqCst);
    let snapshot = stop_agent_sequence(app, state, "app.shutdown.completed")?;
    let mut guard = state
        .single_instance
        .lock()
        .map_err(|_| "释放单实例锁失败：single_instance 锁异常".to_string())?;
    // 丢弃 guard 会触发 Drop 清理 lock 文件。
    *guard = None;
    Ok(snapshot)
}

/// 执行 agent.start 命令。
pub fn agent_start_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AgentRuntimeSnapshot, String> {
    start_agent(app, state, "agent.start.connected")
}

/// 执行 agent.stop 命令。
pub fn agent_stop_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AgentRuntimeSnapshot, String> {
    stop_agent_sequence(app, state, "agent.stop.completed")
}

/// 执行 agent.restart 命令。
pub fn agent_restart_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AgentRuntimeSnapshot, String> {
    let _ = stop_agent_sequence(app, state, "agent.restart.stopped")?;
    start_agent(app, state, "agent.restart.connected")
}

/// 执行崩溃注入：用于验证“异常退出 -> 自动恢复”闭环。
pub fn agent_crash_inject_impl(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
) -> Result<AgentRuntimeSnapshot, String> {
    let (child_to_kill, ipc_client_to_close, crash_snapshot) = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "执行崩溃注入时锁异常".to_string())?;
        if supervisor.child.is_none() {
            return Err("当前没有运行中的 Agent 进程，无法执行崩溃注入".to_string());
        }
        supervisor.desired_state = DesiredState::Running;
        supervisor.exit_kind = ExitKind::Unexpected;
        supervisor.connection_state = ConnectionState::Disconnected;
        supervisor.expecting_exit = false;
        supervisor.pending_auto_restart = true;
        supervisor.updated_at_ms = now_ms();
        push_host_log(
            &mut supervisor,
            "warn",
            "supervisor",
            "CRASH_INJECTED",
            "收到崩溃注入命令，模拟非预期退出",
        );
        let child_to_kill = supervisor.child.take();
        supervisor.pid = None;
        let ipc_client_to_close = supervisor.ipc_client.take();
        let crash_snapshot = build_runtime_snapshot(&supervisor);
        (child_to_kill, ipc_client_to_close, crash_snapshot)
    };
    // 在释放 supervisor 锁后再发事件，避免 emit 失败路径的二次加锁死锁。
    emit_runtime_changed(app, state, "agent.crash.injected", crash_snapshot, true);
    if let Some(mut ipc_client) = ipc_client_to_close {
        // 注入崩溃前主动关闭 IPC 连接，模拟真实断链序列。
        ipc_client.close();
    }
    if let Some(mut child) = child_to_kill {
        let _ = child.kill();
        let _ = child.wait();
    }
    crate::state::app_state::current_snapshot(state)
}

/// 后台 Supervisor 线程：检测退出并按状态机规则自动恢复。
pub fn spawn_supervisor_monitor(app: AppHandle, state: Arc<AppRuntimeState>) {
    let _ = thread::Builder::new()
        .name("agent-host-supervisor".to_string())
        .spawn(move || loop {
            if !state.shutdown_requested.load(Ordering::SeqCst) {
                let mut emit_item: Option<(String, AgentRuntimeSnapshot, bool)> = None;
                let mut forwarded_events: Vec<(String, AgentRuntimeSnapshot, bool)> = Vec::new();

                {
                    let mut supervisor = match state.supervisor.lock() {
                        Ok(guard) => guard,
                        Err(_) => {
                            thread::sleep(Duration::from_millis(SUPERVISOR_POLL_MS));
                            continue;
                        }
                    };
                    if let Some(child) = supervisor.child.as_mut() {
                        match child.try_wait() {
                            Ok(Some(status)) => {
                                let expected = supervisor.expecting_exit
                                    || matches!(supervisor.desired_state, DesiredState::Stopped);
                                supervisor.child = None;
                                supervisor.ipc_client = None;
                                supervisor.pid = None;
                                supervisor.connection_state = ConnectionState::Disconnected;
                                supervisor.exit_kind = if expected {
                                    ExitKind::Expected
                                } else {
                                    ExitKind::Unexpected
                                };
                                supervisor.expecting_exit = false;
                                supervisor.updated_at_ms = now_ms();
                                if expected {
                                    supervisor.pending_auto_restart = false;
                                    supervisor.last_error = None;
                                    push_host_log(
                                        &mut supervisor,
                                        "info",
                                        "supervisor",
                                        "AGENT_EXIT_EXPECTED",
                                        format!("Agent 进程按预期退出，status={status}"),
                                    );
                                    emit_item = Some((
                                        "supervisor.exit.expected".to_string(),
                                        build_runtime_snapshot(&supervisor),
                                        true,
                                    ));
                                } else {
                                    supervisor.pending_auto_restart =
                                        matches!(supervisor.desired_state, DesiredState::Running);
                                    supervisor.last_error =
                                        Some(format!("Agent 进程异常退出，status={status}"));
                                    push_host_log(
                                        &mut supervisor,
                                        "error",
                                        "supervisor",
                                        "AGENT_EXIT_UNEXPECTED",
                                        format!("检测到异常退出，status={status}"),
                                    );
                                    emit_item = Some((
                                        "supervisor.exit.unexpected".to_string(),
                                        build_runtime_snapshot(&supervisor),
                                        true,
                                    ));
                                }
                            }
                            Ok(None) => {}
                            Err(err) => {
                                supervisor.last_error =
                                    Some(format!("检查 Agent 进程状态失败: {err}"));
                                supervisor.updated_at_ms = now_ms();
                                push_host_log(
                                    &mut supervisor,
                                    "error",
                                    "supervisor",
                                    "AGENT_WAIT_FAILED",
                                    format!("try_wait 失败: {err}"),
                                );
                                emit_item = Some((
                                    "supervisor.wait.failed".to_string(),
                                    build_runtime_snapshot(&supervisor),
                                    true,
                                ));
                            }
                        }
                    }
                }

                {
                    let mut supervisor = match state.supervisor.lock() {
                        Ok(guard) => guard,
                        Err(_) => {
                            thread::sleep(Duration::from_millis(SUPERVISOR_POLL_MS));
                            continue;
                        }
                    };
                    // 仅在进程存活且 IPC 客户端可用时执行事件泵。
                    if supervisor.child.is_some() {
                        let now = now_ms();
                        let need_pump = now.saturating_sub(supervisor.last_ipc_event_pump_ms)
                            >= IPC_EVENT_PUMP_INTERVAL_MS;
                        if let Some(mut ipc_client) = supervisor.ipc_client.take() {
                            if need_pump {
                                supervisor.last_ipc_event_pump_ms = now;
                                if let Err(err) = ipc_client.ping() {
                                    push_host_log(
                                        &mut supervisor,
                                        "warn",
                                        "ipc_client",
                                        "RPC_EVENT_PUMP_FAILED",
                                        format!("事件泵 ping 失败: {err}"),
                                    );
                                }
                            }
                            let drained_events = ipc_client.drain_events();
                            for event in drained_events {
                                let event_name = event
                                    .get("event")
                                    .and_then(Value::as_str)
                                    .unwrap_or("unknown");
                                let event_seq =
                                    event.get("seq").and_then(Value::as_u64).unwrap_or(0);
                                push_host_log(
                                    &mut supervisor,
                                    "info",
                                    "event_bridge",
                                    "AGENT_EVENT_FORWARDED",
                                    format!("转发 Agent 事件: event={event_name}, seq={event_seq}"),
                                );
                                supervisor.updated_at_ms = now_ms();
                                forwarded_events.push((
                                    format!("agent.event.{event_name}"),
                                    build_runtime_snapshot(&supervisor),
                                    false,
                                ));
                            }
                            // 处理结束后放回 IPC 客户端，保持长连接可复用。
                            supervisor.ipc_client = Some(ipc_client);
                        }
                    }
                }

                if let Some((reason, snapshot, force)) = emit_item {
                    emit_runtime_changed(&app, &state, &reason, snapshot, force);
                }
                for (reason, snapshot, force) in forwarded_events {
                    // 将 Agent 原生事件折叠进统一宿主事件模型。
                    emit_runtime_changed(&app, &state, &reason, snapshot, force);
                }

                let should_restart = {
                    let mut supervisor = match state.supervisor.lock() {
                        Ok(guard) => guard,
                        Err(_) => {
                            thread::sleep(Duration::from_millis(SUPERVISOR_POLL_MS));
                            continue;
                        }
                    };
                    if supervisor.pending_auto_restart
                        && matches!(supervisor.desired_state, DesiredState::Running)
                        && supervisor.child.is_none()
                    {
                        let now = now_ms();
                        if now.saturating_sub(supervisor.last_restart_attempt_ms)
                            >= RESTART_BACKOFF_MS
                        {
                            supervisor.last_restart_attempt_ms = now;
                            supervisor.restart_total += 1;
                            push_host_log(
                                &mut supervisor,
                                "warn",
                                "supervisor",
                                "AUTO_RESTART_TRIGGERED",
                                "触发崩溃恢复自动拉起",
                            );
                            true
                        } else {
                            false
                        }
                    } else {
                        false
                    }
                };

                if should_restart {
                    if let Err(err) =
                        start_agent_sequence(&app, &state, "supervisor.auto_recovered")
                    {
                        let snapshot = {
                            let mut supervisor = match state.supervisor.lock() {
                                Ok(guard) => guard,
                                Err(_) => {
                                    thread::sleep(Duration::from_millis(SUPERVISOR_POLL_MS));
                                    continue;
                                }
                            };
                            supervisor.pending_auto_restart = true;
                            supervisor.last_error = Some(err.clone());
                            supervisor.updated_at_ms = now_ms();
                            push_host_log(
                                &mut supervisor,
                                "error",
                                "supervisor",
                                "AUTO_RESTART_FAILED",
                                format!("自动拉起失败: {err}"),
                            );
                            build_runtime_snapshot(&supervisor)
                        };
                        emit_runtime_changed(
                            &app,
                            &state,
                            "supervisor.auto_recover.failed",
                            snapshot,
                            true,
                        );
                    }
                }
            }

            thread::sleep(Duration::from_millis(SUPERVISOR_POLL_MS));
        });
}

/// 若当前进程用于 mock runtime，则运行无 UI 子进程逻辑并立即返回 true。
pub fn run_mock_agent_runtime_if_requested() -> bool {
    let is_mock_runtime = std::env::args().any(|arg| arg == "--mock-agent-runtime");
    if !is_mock_runtime {
        return false;
    }
    run_mock_agent_runtime();
    true
}

/// 内置 mock runtime：用于本地演示进程拉起/停止/崩溃恢复链路。
fn run_mock_agent_runtime() {
    let crash_after_ms = std::env::var("DEV_AGENT_RUNTIME_CRASH_AFTER_MS")
        .ok()
        .and_then(|value| value.parse::<u64>().ok());
    let ipc_transport =
        std::env::var("DEV_AGENT_IPC_TRANSPORT").unwrap_or_else(|_| "uds".to_string());
    let ipc_endpoint = std::env::var("DEV_AGENT_IPC_ENDPOINT").unwrap_or_default();
    let session_secret = std::env::var("DEV_AGENT_SESSION_SECRET").unwrap_or_default();
    if let Err(err) = validate_hex_token(&session_secret, 64, "DEV_AGENT_SESSION_SECRET") {
        eprintln!("mock runtime 会话密钥无效: {err}");
        std::process::exit(2);
    }

    #[cfg(unix)]
    {
        if ipc_transport == "uds" && !ipc_endpoint.trim().is_empty() {
            if let Err(err) =
                run_mock_localrpc_uds_server(&ipc_endpoint, crash_after_ms, &session_secret)
            {
                eprintln!("mock localrpc server 退出: {err}");
                std::process::exit(2);
            }
            return;
        }
    }
    #[cfg(windows)]
    {
        if ipc_transport == "named_pipe" && !ipc_endpoint.trim().is_empty() {
            if let Err(err) =
                run_mock_localrpc_named_pipe_server(&ipc_endpoint, crash_after_ms, &session_secret)
            {
                eprintln!("mock localrpc named pipe server 退出: {err}");
                std::process::exit(2);
            }
            return;
        }
    }

    // 非 UDS 模式下回退为简化 runtime 循环，仅用于兼容调试。
    let start_at = Instant::now();
    loop {
        thread::sleep(Duration::from_millis(300));
        if let Some(limit_ms) = crash_after_ms {
            if start_at.elapsed().as_millis() >= u128::from(limit_ms) {
                // 主动异常退出，供 Supervisor 自动恢复逻辑验证。
                std::process::exit(1);
            }
        }
    }
}

/// 提取 payload 内的字符串字段，统一错误语义。
fn payload_str_field<'a>(payload: &'a Value, field_name: &str) -> Result<&'a str, String> {
    payload
        .get(field_name)
        .and_then(Value::as_str)
        .ok_or_else(|| format!("缺少字段 {field_name}"))
}

/// 写入成功响应帧，保持 request/response 协议壳一致。
fn write_mock_ok_response(
    stream: &mut (impl std::io::Read + std::io::Write),
    request_id: [u8; 16],
    payload: Value,
) -> Result<(), String> {
    let response = json!({
        "type": "response",
        "ok": true,
        "payload": payload,
    });
    let response_body =
        serde_json::to_vec(&response).map_err(|err| format!("序列化响应失败: {err}"))?;
    let response_frame = LocalRpcFrame {
        frame_type: LocalRpcFrameType::Response,
        flags: 0,
        request_id,
        body: response_body,
    };
    write_local_rpc_frame(stream, &response_frame)
}

/// 写入失败响应帧，统一错误结构以便客户端直接复用解析逻辑。
fn write_mock_error_response(
    stream: &mut (impl std::io::Read + std::io::Write),
    request_id: [u8; 16],
    code: &str,
    message: impl Into<String>,
    retryable: bool,
) -> Result<(), String> {
    let response = json!({
        "type": "response",
        "ok": false,
        "error": {
            "code": code,
            "message": message.into(),
            "retryable": retryable,
        }
    });
    let response_body =
        serde_json::to_vec(&response).map_err(|err| format!("序列化错误响应失败: {err}"))?;
    let response_frame = LocalRpcFrame {
        frame_type: LocalRpcFrameType::Response,
        flags: 0,
        request_id,
        body: response_body,
    };
    write_local_rpc_frame(stream, &response_frame)
}

/// mock localrpc 通用处理循环：统一 request/response/event/ping/pong 逻辑。
fn run_mock_localrpc_server_loop(
    stream: &mut (impl std::io::Read + std::io::Write),
    crash_after_ms: Option<u64>,
    session_secret: &str,
) -> Result<(), String> {
    let started_at_ms = now_ms();
    let start_instant = Instant::now();
    let mut event_seq = 1_u64;
    let mut authenticated = false;
    let mut last_client_nonce = String::new();
    let mut last_agent_nonce = String::new();
    let mut negotiated_protocol_version = AUTH_PROTOCOL_VERSION.to_string();
    loop {
        if let Some(limit_ms) = crash_after_ms {
            if start_instant.elapsed().as_millis() >= u128::from(limit_ms) {
                // 模拟 Agent 崩溃，供宿主自动恢复链路验证。
                std::process::exit(1);
            }
        }

        let frame = read_local_rpc_frame(stream)?;
        match frame.frame_type {
            LocalRpcFrameType::Request => {
                let body = parse_json_body(&frame.body)?;
                let method = body
                    .get("method")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_string();
                let payload = body.get("payload").cloned().unwrap_or(Value::Null);
                if method == "app.auth.begin" {
                    let client_nonce = payload_str_field(&payload, "client_nonce")?.to_string();
                    let protocol_version = payload
                        .get("protocol_version")
                        .and_then(Value::as_str)
                        .unwrap_or(AUTH_PROTOCOL_VERSION)
                        .to_string();
                    validate_hex_token(&client_nonce, 16, "client_nonce")?;
                    let agent_nonce = generate_random_hex(16)?;
                    let agent_proof = compute_agent_proof(
                        session_secret,
                        &client_nonce,
                        &agent_nonce,
                        &protocol_version,
                    )?;
                    // 仅在 begin 成功后刷新挑战上下文，避免旧挑战被重用。
                    last_client_nonce = client_nonce;
                    last_agent_nonce = agent_nonce.clone();
                    negotiated_protocol_version = protocol_version.clone();
                    write_mock_ok_response(
                        stream,
                        frame.request_id,
                        json!({
                            "agent_id": "mock-agent",
                            "version": "mock-agent-0.1.0",
                            "feature_set": ["request", "response", "event", "hmac_auth"],
                            "protocol_version": protocol_version,
                            "agent_nonce": agent_nonce,
                            "agent_proof": agent_proof,
                        }),
                    )?;
                    continue;
                }
                if method == "app.auth.complete" {
                    if last_client_nonce.is_empty() || last_agent_nonce.is_empty() {
                        write_mock_error_response(
                            stream,
                            frame.request_id,
                            "AUTH_FLOW_INVALID",
                            "未完成 app.auth.begin，拒绝 app.auth.complete",
                            false,
                        )?;
                        continue;
                    }
                    let client_nonce = payload_str_field(&payload, "client_nonce")?;
                    let agent_nonce = payload_str_field(&payload, "agent_nonce")?;
                    let protocol_version = payload
                        .get("protocol_version")
                        .and_then(Value::as_str)
                        .unwrap_or(AUTH_PROTOCOL_VERSION);
                    let client_proof = payload_str_field(&payload, "client_proof")?;
                    validate_hex_token(client_nonce, 16, "client_nonce")?;
                    validate_hex_token(agent_nonce, 16, "agent_nonce")?;
                    validate_hex_token(client_proof, PROOF_HEX_LEN, "client_proof")?;

                    let expected_client_proof = compute_client_proof(
                        session_secret,
                        &last_client_nonce,
                        &last_agent_nonce,
                        &negotiated_protocol_version,
                    )?;
                    let challenge_matches = constant_time_eq(client_nonce, &last_client_nonce)
                        && constant_time_eq(agent_nonce, &last_agent_nonce)
                        && constant_time_eq(protocol_version, &negotiated_protocol_version);
                    if !challenge_matches || !constant_time_eq(client_proof, &expected_client_proof)
                    {
                        write_mock_error_response(
                            stream,
                            frame.request_id,
                            "AUTH_FAILED",
                            "client_proof 校验失败",
                            false,
                        )?;
                        continue;
                    }
                    authenticated = true;
                    write_mock_ok_response(
                        stream,
                        frame.request_id,
                        json!({
                            "authenticated": true,
                        }),
                    )?;
                    continue;
                }
                if !authenticated {
                    write_mock_error_response(
                        stream,
                        frame.request_id,
                        "AUTH_REQUIRED",
                        "请先完成 app.auth.begin/app.auth.complete 握手鉴权",
                        false,
                    )?;
                    continue;
                }
                let mut should_exit = false;
                let response_payload = if method == "app.bootstrap" {
                    json!({
                        "agent_state": "running",
                        "version": "mock-agent-0.1.0",
                        "started_at_ms": started_at_ms,
                    })
                } else if method == "agent.snapshot" {
                    json!({
                        "agent_state": "running",
                        "started_at_ms": started_at_ms,
                        "uptime_ms": now_ms().saturating_sub(started_at_ms),
                    })
                } else if method == "service.list" {
                    // mock 数据用于前端联调服务列表页面。
                    json!({
                        "services": [
                            {
                                "service_id": "svc-dev-api",
                                "service_name": "dev-api",
                                "protocol": "http",
                                "status": "healthy",
                                "endpoint_count": 2,
                                "updated_at_ms": now_ms(),
                            },
                            {
                                "service_id": "svc-metrics",
                                "service_name": "metrics-gateway",
                                "protocol": "tcp",
                                "status": "degraded",
                                "endpoint_count": 1,
                                "last_error": "上游健康检查超时",
                                "updated_at_ms": now_ms(),
                            }
                        ]
                    })
                } else if method == "tunnel.list" {
                    // mock 数据用于前端联调通道列表页面。
                    json!({
                        "tunnels": [
                            {
                                "tunnel_id": "tnl-001",
                                "service_id": "svc-dev-api",
                                "state": "active",
                                "local_addr": "127.0.0.1:18080",
                                "remote_addr": "https://api-dev.example.com",
                                "latency_ms": 52,
                                "updated_at_ms": now_ms(),
                            },
                            {
                                "tunnel_id": "tnl-002",
                                "service_id": "svc-metrics",
                                "state": "idle",
                                "local_addr": "127.0.0.1:19090",
                                "remote_addr": "tcp://metrics-dev.example.com:443",
                                "latency_ms": 88,
                                "last_error": "最近一次重连后等待流量",
                                "updated_at_ms": now_ms(),
                            }
                        ]
                    })
                } else if method == "app.ping" {
                    json!({"pong": true})
                } else if method == "app.shutdown" {
                    // 通过控制面方法执行有序退出，模拟优雅关闭路径。
                    should_exit = true;
                    json!({"shutdown": true})
                } else {
                    write_mock_error_response(
                        stream,
                        frame.request_id,
                        "METHOD_NOT_ALLOWED",
                        format!("mock localrpc 不支持 method={method}"),
                        false,
                    )?;
                    continue;
                };

                write_mock_ok_response(stream, frame.request_id, response_payload)?;

                let event_body = serde_json::to_vec(&json!({
                    "type": "event",
                    "event": "agent.state.changed",
                    "seq": event_seq,
                    "payload": {
                        "state": "running",
                        "method": method,
                    }
                }))
                .map_err(|err| format!("序列化事件失败: {err}"))?;
                let event_frame = LocalRpcFrame {
                    frame_type: LocalRpcFrameType::Event,
                    flags: 0,
                    request_id: [0; 16],
                    body: event_body,
                };
                write_local_rpc_frame(stream, &event_frame)?;
                event_seq = event_seq.saturating_add(1);
                if should_exit {
                    return Ok(());
                }
            }
            LocalRpcFrameType::Ping => {
                let pong_frame = LocalRpcFrame {
                    frame_type: LocalRpcFrameType::Pong,
                    flags: 0,
                    request_id: [0; 16],
                    body: Vec::new(),
                };
                write_local_rpc_frame(stream, &pong_frame)?;
            }
            LocalRpcFrameType::Pong => {}
            LocalRpcFrameType::Response | LocalRpcFrameType::Event => {
                return Err("PROTOCOL_ERROR: mock server 收到非法客户端帧类型".to_string());
            }
        }
    }
}

/// Linux mock localrpc 服务端：用于联调本地 IPC 与帧协议实现。
#[cfg(unix)]
fn run_mock_localrpc_uds_server(
    endpoint: &str,
    crash_after_ms: Option<u64>,
    session_secret: &str,
) -> Result<(), String> {
    let endpoint_path = PathBuf::from(endpoint);
    let endpoint_parent = endpoint_path
        .parent()
        .ok_or_else(|| "mock localrpc 端点路径缺少父目录".to_string())?;
    ensure_secure_dir(endpoint_parent)?;
    if let Ok(meta) = fs::symlink_metadata(&endpoint_path) {
        if meta.file_type().is_symlink() {
            return Err("mock localrpc 端点不能是符号链接".to_string());
        }
        if meta.file_type().is_socket() {
            fs::remove_file(&endpoint_path)
                .map_err(|err| format!("清理陈旧 mock socket 失败: {err}"))?;
        } else {
            return Err("mock localrpc 端点已存在且不是 socket".to_string());
        }
    }

    let listener = UnixListener::bind(&endpoint_path)
        .map_err(|err| format!("绑定 mock localrpc socket 失败: {err}"))?;
    fs::set_permissions(&endpoint_path, fs::Permissions::from_mode(0o600))
        .map_err(|err| format!("设置 mock socket 权限失败: {err}"))?;
    let (mut stream, _) = listener
        .accept()
        .map_err(|err| format!("接受 mock localrpc 连接失败: {err}"))?;
    run_mock_localrpc_server_loop(&mut stream, crash_after_ms, session_secret)
}

/// Windows mock localrpc 服务端：用于验证 Named Pipe 与帧协议链路。
#[cfg(windows)]
fn run_mock_localrpc_named_pipe_server(
    endpoint: &str,
    crash_after_ms: Option<u64>,
    session_secret: &str,
) -> Result<(), String> {
    use std::os::windows::io::{AsRawHandle, FromRawHandle, RawHandle};
    use std::ptr::null_mut;

    type Handle = *mut std::ffi::c_void;
    type Dword = u32;
    type Bool = i32;

    const PIPE_ACCESS_DUPLEX: u32 = 0x0000_0003;
    const FILE_FLAG_FIRST_PIPE_INSTANCE: u32 = 0x0008_0000;
    const PIPE_TYPE_BYTE: u32 = 0x0000_0000;
    const PIPE_READMODE_BYTE: u32 = 0x0000_0000;
    const PIPE_WAIT: u32 = 0x0000_0000;
    const PIPE_REJECT_REMOTE_CLIENTS: u32 = 0x0000_0008;
    const ERROR_PIPE_CONNECTED: i32 = 535;
    const ERROR_INSUFFICIENT_BUFFER: i32 = 122;
    const TOKEN_QUERY: Dword = 0x0008;
    const TOKEN_INFORMATION_CLASS_USER: Dword = 1;
    const SDDL_REVISION_1: Dword = 1;

    #[repr(C)]
    struct SidAndAttributes {
        sid: *mut std::ffi::c_void,
        attributes: Dword,
    }

    #[repr(C)]
    struct TokenUser {
        user: SidAndAttributes,
    }

    #[repr(C)]
    struct SecurityAttributes {
        n_length: Dword,
        lp_security_descriptor: *mut std::ffi::c_void,
        b_inherit_handle: Bool,
    }

    unsafe extern "system" {
        fn CreateNamedPipeW(
            lp_name: *const u16,
            open_mode: u32,
            pipe_mode: u32,
            max_instances: u32,
            out_buffer_size: u32,
            in_buffer_size: u32,
            default_timeout: u32,
            security_attributes: *mut SecurityAttributes,
        ) -> Handle;
        fn ConnectNamedPipe(named_pipe: Handle, overlapped: *mut std::ffi::c_void) -> i32;
        fn GetCurrentProcess() -> Handle;
        fn OpenProcessToken(
            process_handle: Handle,
            desired_access: Dword,
            token_handle: *mut Handle,
        ) -> Bool;
        fn GetTokenInformation(
            token_handle: Handle,
            token_information_class: Dword,
            token_information: *mut std::ffi::c_void,
            token_information_length: Dword,
            return_length: *mut Dword,
        ) -> Bool;
        fn ConvertSidToStringSidW(sid: *mut std::ffi::c_void, string_sid: *mut *mut u16) -> Bool;
        fn ConvertStringSecurityDescriptorToSecurityDescriptorW(
            string_security_descriptor: *const u16,
            string_sd_revision: Dword,
            security_descriptor: *mut *mut std::ffi::c_void,
            security_descriptor_size: *mut Dword,
        ) -> Bool;
        fn LocalFree(handle: *mut std::ffi::c_void) -> *mut std::ffi::c_void;
        fn CloseHandle(handle: Handle) -> Bool;
    }

    /// 关闭句柄时忽略错误，避免清理阶段影响主路径返回。
    fn close_handle_silent(handle: Handle) {
        if !handle.is_null() {
            unsafe {
                let _ = CloseHandle(handle);
            }
        }
    }

    /// 将 Win32 UTF-16 指针转换为 Rust String。
    fn utf16_ptr_to_string(wide_ptr: *const u16) -> String {
        let mut text_len = 0_usize;
        unsafe {
            while *wide_ptr.add(text_len) != 0 {
                text_len += 1;
            }
            String::from_utf16_lossy(std::slice::from_raw_parts(wide_ptr, text_len))
        }
    }

    /// 读取当前用户 SID 字符串，用于构造仅当前用户可访问的 DACL。
    fn current_user_sid_string() -> Result<String, String> {
        let mut token_handle: Handle = std::ptr::null_mut();
        let open_token_ok =
            unsafe { OpenProcessToken(GetCurrentProcess(), TOKEN_QUERY, &mut token_handle) };
        if open_token_ok == 0 {
            return Err(format!(
                "读取当前用户令牌失败: {}",
                std::io::Error::last_os_error()
            ));
        }

        let mut required_len: Dword = 0;
        let first_read_ok = unsafe {
            GetTokenInformation(
                token_handle,
                TOKEN_INFORMATION_CLASS_USER,
                std::ptr::null_mut(),
                0,
                &mut required_len,
            )
        };
        if first_read_ok != 0 {
            close_handle_silent(token_handle);
            return Err("读取 TokenUser 长度返回异常".to_string());
        }
        let first_read_error = std::io::Error::last_os_error();
        if first_read_error.raw_os_error().unwrap_or_default() != ERROR_INSUFFICIENT_BUFFER {
            close_handle_silent(token_handle);
            return Err(format!("读取 TokenUser 长度失败: {first_read_error}"));
        }

        let mut token_user_buffer = vec![0_u8; required_len as usize];
        let second_read_ok = unsafe {
            GetTokenInformation(
                token_handle,
                TOKEN_INFORMATION_CLASS_USER,
                token_user_buffer.as_mut_ptr().cast::<std::ffi::c_void>(),
                required_len,
                &mut required_len,
            )
        };
        if second_read_ok == 0 {
            close_handle_silent(token_handle);
            return Err(format!(
                "读取 TokenUser 失败: {}",
                std::io::Error::last_os_error()
            ));
        }
        let token_user = unsafe { &*(token_user_buffer.as_ptr().cast::<TokenUser>()) };
        if token_user.user.sid.is_null() {
            close_handle_silent(token_handle);
            return Err("TokenUser.Sid 为空".to_string());
        }

        let mut sid_wide_ptr: *mut u16 = std::ptr::null_mut();
        let convert_sid_ok =
            unsafe { ConvertSidToStringSidW(token_user.user.sid, &mut sid_wide_ptr) };
        if convert_sid_ok == 0 || sid_wide_ptr.is_null() {
            close_handle_silent(token_handle);
            return Err(format!("SID 转换失败: {}", std::io::Error::last_os_error()));
        }
        let sid_text = utf16_ptr_to_string(sid_wide_ptr);
        unsafe {
            let _ = LocalFree(sid_wide_ptr.cast::<std::ffi::c_void>());
        }
        close_handle_silent(token_handle);
        Ok(sid_text)
    }

    /// 根据当前用户 SID 生成 Pipe 安全描述符（仅当前用户 + LocalSystem 访问）。
    fn build_pipe_security_attributes() -> Result<SecurityAttributes, String> {
        let current_sid = current_user_sid_string()?;
        let sddl_text = format!("D:P(A;;GA;;;SY)(A;;GA;;;{current_sid})");
        let mut sddl_wide = sddl_text.encode_utf16().collect::<Vec<u16>>();
        sddl_wide.push(0);

        let mut security_descriptor_ptr: *mut std::ffi::c_void = std::ptr::null_mut();
        let mut security_descriptor_size: Dword = 0;
        let convert_sddl_ok = unsafe {
            ConvertStringSecurityDescriptorToSecurityDescriptorW(
                sddl_wide.as_ptr(),
                SDDL_REVISION_1,
                &mut security_descriptor_ptr,
                &mut security_descriptor_size,
            )
        };
        if convert_sddl_ok == 0 || security_descriptor_ptr.is_null() {
            return Err(format!(
                "构建 Named Pipe DACL 失败: {}",
                std::io::Error::last_os_error()
            ));
        }
        Ok(SecurityAttributes {
            n_length: std::mem::size_of::<SecurityAttributes>() as Dword,
            lp_security_descriptor: security_descriptor_ptr,
            b_inherit_handle: 0,
        })
    }

    let mut pipe_name = endpoint.encode_utf16().collect::<Vec<u16>>();
    pipe_name.push(0);

    let mut security_attributes = build_pipe_security_attributes()?;
    let pipe_handle = unsafe {
        // 使用拒绝远程客户端 + 最小权限 DACL，收敛 Named Pipe 攻击面。
        CreateNamedPipeW(
            pipe_name.as_ptr(),
            PIPE_ACCESS_DUPLEX | FILE_FLAG_FIRST_PIPE_INSTANCE,
            PIPE_TYPE_BYTE | PIPE_READMODE_BYTE | PIPE_WAIT | PIPE_REJECT_REMOTE_CLIENTS,
            1,
            64 * 1024,
            64 * 1024,
            0,
            &mut security_attributes,
        )
    };
    unsafe {
        // CreateNamedPipeW 返回后可释放安全描述符分配内存，避免泄漏。
        let _ = LocalFree(security_attributes.lp_security_descriptor);
    }
    if (pipe_handle as isize) == -1 {
        return Err(format!(
            "创建 mock named pipe 失败: {}",
            std::io::Error::last_os_error()
        ));
    }

    let mut stream = unsafe {
        // 将原始 Win32 句柄托管给 File，后续由 Rust 自动释放。
        std::fs::File::from_raw_handle(pipe_handle as RawHandle)
    };
    let connect_ret = unsafe { ConnectNamedPipe(stream.as_raw_handle() as Handle, null_mut()) };
    if connect_ret == 0 {
        let os_error = std::io::Error::last_os_error();
        if os_error.raw_os_error().unwrap_or_default() != ERROR_PIPE_CONNECTED {
            return Err(format!("等待 mock named pipe 客户端连接失败: {os_error}"));
        }
    }
    run_mock_localrpc_server_loop(&mut stream, crash_after_ms, session_secret)
}
