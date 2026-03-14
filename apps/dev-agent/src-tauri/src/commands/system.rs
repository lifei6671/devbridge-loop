use serde::Serialize;
use std::sync::{Arc, Mutex, OnceLock};
use sysinfo::{Disks, System};
use tauri::State;

use crate::state::app_state::{now_ms, with_rpc_metrics, AppRuntimeState};

/// 系统资源快照：供前端「系统资源」卡片展示真实 CPU/内存/磁盘占用。
#[derive(Debug, Clone, Serialize)]
pub struct SystemResourceSnapshot {
    pub cpu_percent: f64,
    pub memory_percent: f64,
    pub disk_percent: f64,
    pub memory_used_bytes: u64,
    pub memory_total_bytes: u64,
    pub disk_used_bytes: u64,
    pub disk_total_bytes: u64,
    pub updated_at_ms: u64,
    pub source: String,
}

#[derive(Debug)]
struct HostSystemSampler {
    system: System,
    disks: Disks,
}

impl HostSystemSampler {
    fn new() -> Self {
        let mut system = System::new();
        // 第一次刷新用于建立 CPU 采样基线，后续 refresh_cpu_usage 才有有效增量。
        system.refresh_memory();
        system.refresh_cpu_usage();

        let mut disks = Disks::new_with_refreshed_list();
        disks.refresh();

        Self { system, disks }
    }

    fn sample(&mut self) -> SystemResourceSnapshot {
        self.system.refresh_memory();
        self.system.refresh_cpu_usage();
        self.disks.refresh();

        let cpu_percent = clamp_percent(self.system.global_cpu_info().cpu_usage() as f64);

        let memory_total_bytes = self.system.total_memory();
        let memory_used_bytes = self.system.used_memory();
        let memory_percent = percent(memory_used_bytes, memory_total_bytes);

        let mut disk_total_bytes = 0_u64;
        let mut disk_used_bytes = 0_u64;
        for disk in &self.disks {
            let total = disk.total_space();
            let available = disk.available_space().min(total);
            disk_total_bytes = disk_total_bytes.saturating_add(total);
            disk_used_bytes = disk_used_bytes.saturating_add(total.saturating_sub(available));
        }
        let disk_percent = percent(disk_used_bytes, disk_total_bytes);

        SystemResourceSnapshot {
            cpu_percent,
            memory_percent,
            disk_percent,
            memory_used_bytes,
            memory_total_bytes,
            disk_used_bytes,
            disk_total_bytes,
            updated_at_ms: now_ms(),
            source: "host.sysinfo".to_string(),
        }
    }
}

fn sampler() -> &'static Mutex<HostSystemSampler> {
    static SAMPLER: OnceLock<Mutex<HostSystemSampler>> = OnceLock::new();
    SAMPLER.get_or_init(|| Mutex::new(HostSystemSampler::new()))
}

fn percent(used: u64, total: u64) -> f64 {
    if total == 0 {
        return 0.0;
    }
    clamp_percent((used as f64) * 100.0 / (total as f64))
}

fn clamp_percent(value: f64) -> f64 {
    if !value.is_finite() {
        return 0.0;
    }
    value.clamp(0.0, 100.0)
}

#[tauri::command]
pub fn system_resource_snapshot(
    state: State<'_, Arc<AppRuntimeState>>,
) -> Result<SystemResourceSnapshot, String> {
    let shared = state.inner().clone();
    with_rpc_metrics(&shared, || {
        let mut guard = sampler()
            .lock()
            .map_err(|_| "采集系统资源失败：sampler 锁异常".to_string())?;
        Ok(guard.sample())
    })
}
