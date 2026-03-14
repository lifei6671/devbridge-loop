use std::sync::Arc;
use std::time::Instant;
use tauri::{AppHandle, Emitter};

use crate::state::app_state::{
    push_host_log, AgentRuntimeChangedEvent, AgentRuntimeSnapshot, AppRuntimeState,
    EVENT_AGENT_RUNTIME_CHANGED, EVENT_THROTTLE_MS,
};

/// 统一事件桥发射逻辑，包含节流与被聚合事件计数。
pub fn emit_runtime_changed(
    app: &AppHandle,
    state: &Arc<AppRuntimeState>,
    reason: &str,
    snapshot: AgentRuntimeSnapshot,
    force: bool,
) {
    let mut bridge = match state.event_bridge.lock() {
        Ok(guard) => guard,
        Err(_) => return,
    };
    let now = Instant::now();
    if !force {
        if let Some(last_emit_at) = bridge.last_emit_at {
            if now.duration_since(last_emit_at).as_millis() < u128::from(EVENT_THROTTLE_MS) {
                // 高频区间直接聚合，不向前端泛洪事件。
                bridge.dropped_event_count += 1;
                return;
            }
        }
    }
    let dropped_event_count = bridge.dropped_event_count;
    bridge.dropped_event_count = 0;
    bridge.last_emit_at = Some(now);
    drop(bridge);

    let payload = AgentRuntimeChangedEvent {
        schema_version: 1,
        reason: reason.to_string(),
        dropped_event_count,
        snapshot,
    };
    if let Err(err) = app.emit(EVENT_AGENT_RUNTIME_CHANGED, payload) {
        if let Ok(mut supervisor) = state.supervisor.lock() {
            push_host_log(
                &mut supervisor,
                "warn",
                "event_bridge",
                "EVENT_EMIT_FAILED",
                format!("向前端发射事件失败: {err}"),
            );
        }
    }
}
