use serde::Serialize;
use serde_json::{json, Value};
use std::sync::{Arc, Mutex, OnceLock};
use std::time::Instant;
use sysinfo::Networks;
use tauri::State;

use crate::agent_host::ipc_client::LOCAL_RPC_DEFAULT_TIMEOUT_MS;
use crate::state::app_state::{now_ms, push_host_log, with_rpc_metrics, AppRuntimeState};

const RATE_SMOOTHING_ALPHA: f64 = 0.45;
const MIN_SAMPLE_WINDOW_MS: u64 = 120;

/// 流量统计快照：统一供前端所有网速相关组件消费。
#[derive(Debug, Clone, Serialize)]
pub struct TrafficStatsSnapshot {
    pub upload_bytes_per_sec: f64,
    pub download_bytes_per_sec: f64,
    pub upload_total_bytes: u64,
    pub download_total_bytes: u64,
    pub sample_window_ms: u64,
    pub interface_count: u64,
    pub updated_at_ms: u64,
    pub source: String,
}

/// 宿主侧网速采样器：基于系统网卡累计字节计算速率。
#[derive(Debug)]
struct HostTrafficSampler {
    last_upload_bytes_per_sec: f64,
    last_download_bytes_per_sec: f64,
    last_upload_total_bytes: Option<u64>,
    last_download_total_bytes: Option<u64>,
    upload_total_bytes_accumulated: u64,
    download_total_bytes_accumulated: u64,
    last_sample_instant: Option<Instant>,
}

impl HostTrafficSampler {
    fn new() -> Self {
        Self {
            last_upload_bytes_per_sec: 0.0,
            last_download_bytes_per_sec: 0.0,
            last_upload_total_bytes: None,
            last_download_total_bytes: None,
            upload_total_bytes_accumulated: 0,
            download_total_bytes_accumulated: 0,
            last_sample_instant: None,
        }
    }

    /// 对当前系统网卡字节累计值进行一次采样并估算速率。
    fn sample(&mut self) -> Result<TrafficStatsSnapshot, String> {
        let current_sample = read_network_counters()?;
        let now_instant = Instant::now();
        let now_epoch_ms = now_ms();
        let previous_upload_total_bytes = self.last_upload_total_bytes;
        let previous_download_total_bytes = self.last_download_total_bytes;

        let elapsed_ms = self
            .last_sample_instant
            .map(|instant| now_instant.saturating_duration_since(instant).as_millis() as u64)
            .unwrap_or(0);

        let raw_upload_rate = if elapsed_ms >= MIN_SAMPLE_WINDOW_MS {
            compute_rate(
                current_sample.upload_total_bytes,
                previous_upload_total_bytes.unwrap_or(current_sample.upload_total_bytes),
                elapsed_ms,
            )
        } else {
            self.last_upload_bytes_per_sec
        };
        let raw_download_rate = if elapsed_ms >= MIN_SAMPLE_WINDOW_MS {
            compute_rate(
                current_sample.download_total_bytes,
                previous_download_total_bytes.unwrap_or(current_sample.download_total_bytes),
                elapsed_ms,
            )
        } else {
            self.last_download_bytes_per_sec
        };

        let upload_bytes_per_sec = smooth_rate(
            self.last_upload_bytes_per_sec,
            raw_upload_rate,
            RATE_SMOOTHING_ALPHA,
        );
        let download_bytes_per_sec = smooth_rate(
            self.last_download_bytes_per_sec,
            raw_download_rate,
            RATE_SMOOTHING_ALPHA,
        );

        self.last_upload_bytes_per_sec = upload_bytes_per_sec;
        self.last_download_bytes_per_sec = download_bytes_per_sec;
        self.last_upload_total_bytes = Some(current_sample.upload_total_bytes);
        self.last_download_total_bytes = Some(current_sample.download_total_bytes);
        self.last_sample_instant = Some(now_instant);
        self.upload_total_bytes_accumulated = accumulate_monotonic_total(
            previous_upload_total_bytes,
            current_sample.upload_total_bytes,
            self.upload_total_bytes_accumulated,
        );
        self.download_total_bytes_accumulated = accumulate_monotonic_total(
            previous_download_total_bytes,
            current_sample.download_total_bytes,
            self.download_total_bytes_accumulated,
        );

        Ok(TrafficStatsSnapshot {
            upload_bytes_per_sec,
            download_bytes_per_sec,
            upload_total_bytes: self.upload_total_bytes_accumulated,
            download_total_bytes: self.download_total_bytes_accumulated,
            sample_window_ms: elapsed_ms,
            interface_count: current_sample.interface_count,
            updated_at_ms: now_epoch_ms,
            source: "host.net".to_string(),
        })
    }
}

#[derive(Debug)]
struct NetworkCounterSample {
    upload_total_bytes: u64,
    download_total_bytes: u64,
    interface_count: u64,
}

fn traffic_sampler() -> &'static Mutex<HostTrafficSampler> {
    static TRAFFIC_SAMPLER: OnceLock<Mutex<HostTrafficSampler>> = OnceLock::new();
    TRAFFIC_SAMPLER.get_or_init(|| Mutex::new(HostTrafficSampler::new()))
}

fn last_fallback_reason() -> &'static Mutex<Option<String>> {
    static LAST_REASON: OnceLock<Mutex<Option<String>>> = OnceLock::new();
    LAST_REASON.get_or_init(|| Mutex::new(None))
}

fn should_emit_fallback_log(reason: &str) -> bool {
    let mut guard = match last_fallback_reason().lock() {
        Ok(guard) => guard,
        Err(_) => return false,
    };
    if guard.as_deref() == Some(reason) {
        return false;
    }
    *guard = Some(reason.to_string());
    true
}

/// 聚合系统网卡累计字节数（跨平台由 sysinfo 统一抽象）。
fn read_network_counters() -> Result<NetworkCounterSample, String> {
    let mut networks = Networks::new_with_refreshed_list();
    networks.refresh();

    let mut upload_total_bytes = 0_u64;
    let mut download_total_bytes = 0_u64;
    let mut interface_count = 0_u64;
    for (_name, data) in &networks {
        upload_total_bytes = upload_total_bytes.saturating_add(data.total_transmitted());
        download_total_bytes = download_total_bytes.saturating_add(data.total_received());
        if data.total_transmitted() > 0 || data.total_received() > 0 {
            interface_count = interface_count.saturating_add(1);
        }
    }

    Ok(NetworkCounterSample {
        upload_total_bytes,
        download_total_bytes,
        interface_count,
    })
}

fn compute_rate(current_total: u64, previous_total: u64, elapsed_ms: u64) -> f64 {
    if elapsed_ms == 0 || current_total < previous_total {
        return 0.0;
    }
    let delta = current_total.saturating_sub(previous_total);
    (delta as f64) * 1000.0 / (elapsed_ms as f64)
}

fn accumulate_monotonic_total(
    previous_total: Option<u64>,
    current_total: u64,
    accumulated_total: u64,
) -> u64 {
    let Some(previous_total) = previous_total else {
        return accumulated_total;
    };
    if current_total < previous_total {
        // 网卡累计计数器重置时不回退累计总量，等待新基线继续累加。
        return accumulated_total;
    }
    accumulated_total.saturating_add(current_total.saturating_sub(previous_total))
}

fn smooth_rate(previous_rate: f64, raw_rate: f64, alpha: f64) -> f64 {
    if !previous_rate.is_finite() || previous_rate <= 0.0 {
        return raw_rate.max(0.0);
    }
    if !raw_rate.is_finite() || raw_rate <= 0.0 {
        return previous_rate * (1.0 - alpha);
    }
    ((1.0 - alpha) * previous_rate + alpha * raw_rate).max(0.0)
}

fn value_f64_or(payload: &Value, keys: &[&str]) -> Option<f64> {
    for key in keys {
        if let Some(value) = payload.get(*key) {
            if let Some(number) = value.as_f64() {
                if number.is_finite() {
                    return Some(number);
                }
            }
            if let Some(text) = value.as_str() {
                if let Ok(number) = text.trim().parse::<f64>() {
                    if number.is_finite() {
                        return Some(number);
                    }
                }
            }
        }
    }
    None
}

fn value_u64_or(payload: &Value, keys: &[&str], default_value: u64) -> u64 {
    for key in keys {
        if let Some(value) = payload.get(*key) {
            if let Some(number) = value.as_u64() {
                return number;
            }
            if let Some(number) = value.as_f64() {
                if number.is_finite() && number >= 0.0 {
                    return number as u64;
                }
            }
            if let Some(text) = value.as_str() {
                if let Ok(number) = text.trim().parse::<u64>() {
                    return number;
                }
            }
        }
    }
    default_value
}

fn value_str_or(payload: &Value, keys: &[&str], default_value: &str) -> String {
    for key in keys {
        if let Some(value) = payload.get(*key).and_then(Value::as_str) {
            let trimmed = value.trim();
            if !trimmed.is_empty() {
                return trimmed.to_string();
            }
        }
    }
    default_value.to_string()
}

/// 尝试解析 agent 返回的 traffic.stats.snapshot（若已实现则优先使用）。
fn parse_rpc_traffic_snapshot(payload: &Value) -> Option<TrafficStatsSnapshot> {
    let upload_bytes_per_sec = value_f64_or(
        payload,
        &[
            "upload_bytes_per_sec",
            "tx_bytes_per_sec",
            "upload_bps",
            "tx_bps",
            "egress_bytes_per_sec",
        ],
    )?;
    let download_bytes_per_sec = value_f64_or(
        payload,
        &[
            "download_bytes_per_sec",
            "rx_bytes_per_sec",
            "download_bps",
            "rx_bps",
            "ingress_bytes_per_sec",
        ],
    )?;
    if upload_bytes_per_sec < 0.0 || download_bytes_per_sec < 0.0 {
        return None;
    }

    Some(TrafficStatsSnapshot {
        upload_bytes_per_sec,
        download_bytes_per_sec,
        upload_total_bytes: value_u64_or(
            payload,
            &["upload_total_bytes", "tx_total_bytes", "egress_total_bytes"],
            0,
        ),
        download_total_bytes: value_u64_or(
            payload,
            &[
                "download_total_bytes",
                "rx_total_bytes",
                "ingress_total_bytes",
            ],
            0,
        ),
        sample_window_ms: value_u64_or(payload, &["sample_window_ms", "window_ms"], 0),
        interface_count: value_u64_or(payload, &["interface_count"], 0),
        updated_at_ms: value_u64_or(payload, &["updated_at_ms"], now_ms()),
        source: value_str_or(payload, &["source"], "agent.rpc"),
    })
}

/// 通过 IPC 拉取 agent traffic.stats.snapshot，避免长时间持有 supervisor 锁。
fn request_rpc_traffic_snapshot(state: &Arc<AppRuntimeState>) -> Result<TrafficStatsSnapshot, String> {
    let mut ipc_client = {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "读取流量统计失败：supervisor 锁异常".to_string())?;
        supervisor
            .ipc_client
            .take()
            .ok_or_else(|| "agent ipc client unavailable".to_string())?
    };

    let request_result = ipc_client.request(
        "traffic.stats.snapshot",
        json!({}),
        LOCAL_RPC_DEFAULT_TIMEOUT_MS,
    );

    {
        let mut supervisor = state
            .supervisor
            .lock()
            .map_err(|_| "读取流量统计失败：supervisor 锁异常".to_string())?;
        supervisor.ipc_client = Some(ipc_client);
    }

    match request_result {
        Ok(payload) => parse_rpc_traffic_snapshot(&payload)
            .ok_or_else(|| "traffic.stats.snapshot payload invalid".to_string()),
        Err(err) => Err(err),
    }
}

fn sample_host_traffic(
    state: &Arc<AppRuntimeState>,
    fallback_reason: Option<&str>,
) -> Result<TrafficStatsSnapshot, String> {
    let mut sampler = traffic_sampler()
        .lock()
        .map_err(|_| "读取网速失败：traffic sampler 锁异常".to_string())?;
    let snapshot = sampler.sample()?;

    if let Some(reason) = fallback_reason {
        if should_emit_fallback_log(reason) {
            if let Ok(mut supervisor) = state.supervisor.lock() {
                push_host_log(
                    &mut supervisor,
                    "warn",
                    "commands.traffic",
                    "TRAFFIC_STATS_FALLBACK_HOST",
                    format!("traffic.stats.snapshot 已回退宿主采样：{reason}"),
                );
            }
        }
    }

    Ok(snapshot)
}

/// Tauri command：读取流量统计快照（优先 Agent RPC，回退宿主网卡采样）。
#[tauri::command]
pub fn traffic_stats_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<TrafficStatsSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        // UAB-U3: 先使用 Agent runtime 链路指标，宿主网卡仅作为回退视角。
        let rpc_result = request_rpc_traffic_snapshot(&shared);
        match rpc_result {
            Ok(snapshot) => Ok(snapshot),
            Err(rpc_err) => match sample_host_traffic(&shared, Some(&rpc_err)) {
                Ok(snapshot) => Ok(snapshot),
                Err(host_err) => Err(format!(
                    "读取流量统计失败：traffic.stats.snapshot 不可用（{rpc_err}），且系统采样不可用（{host_err}）"
                )),
            },
        }
    })
}

#[cfg(test)]
mod tests {
    use super::accumulate_monotonic_total;

    #[test]
    fn accumulate_monotonic_total_handles_counter_reset() {
        let first = accumulate_monotonic_total(None, 500, 0);
        assert_eq!(first, 0);

        let normal_growth = accumulate_monotonic_total(Some(500), 650, first);
        assert_eq!(normal_growth, 150);

        // 计数器回退不应把累计总量清零或回退。
        let after_reset = accumulate_monotonic_total(Some(650), 20, normal_growth);
        assert_eq!(after_reset, 150);

        // 回退后的新增长量继续叠加。
        let resumed = accumulate_monotonic_total(Some(20), 120, after_reset);
        assert_eq!(resumed, 250);
    }
}
