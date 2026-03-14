export interface SystemResourceSnapshot {
  cpu_percent: number;
  memory_percent: number;
  disk_percent: number;
  memory_used_bytes: number;
  memory_total_bytes: number;
  disk_used_bytes: number;
  disk_total_bytes: number;
  updated_at_ms: number;
  source: string;
}
