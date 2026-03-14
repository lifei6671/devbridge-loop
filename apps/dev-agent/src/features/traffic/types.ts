export interface TrafficStatsSnapshot {
  upload_bytes_per_sec: number;
  download_bytes_per_sec: number;
  upload_total_bytes: number;
  download_total_bytes: number;
  sample_window_ms: number;
  interface_count: number;
  updated_at_ms: number;
  source: string;
}

export interface TrafficHistoryPoint {
  uploadBytesPerSec: number;
  downloadBytesPerSec: number;
  tsMs: number;
}
