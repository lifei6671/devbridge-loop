import { invoke } from "@tauri-apps/api/core";
import { useCallback, useState } from "react";

import type { TrafficHistoryPoint, TrafficStatsSnapshot } from "@/features/traffic/types";

const MAX_HISTORY_POINTS = 36;

const EMPTY_TRAFFIC_SNAPSHOT: TrafficStatsSnapshot = {
  upload_bytes_per_sec: 0,
  download_bytes_per_sec: 0,
  upload_total_bytes: 0,
  download_total_bytes: 0,
  sample_window_ms: 0,
  interface_count: 0,
  updated_at_ms: 0,
  source: "none",
};

interface UseTrafficStatsResult {
  trafficSnapshot: TrafficStatsSnapshot;
  trafficHistory: TrafficHistoryPoint[];
  refreshTrafficStats: () => Promise<void>;
}

/** 统一维护网速快照与趋势历史，供页面不同区块复用。 */
export function useTrafficStats(): UseTrafficStatsResult {
  const [trafficSnapshot, setTrafficSnapshot] =
    useState<TrafficStatsSnapshot>(EMPTY_TRAFFIC_SNAPSHOT);
  const [trafficHistory, setTrafficHistory] = useState<TrafficHistoryPoint[]>([]);

  const refreshTrafficStats = useCallback(async () => {
    const snapshot = await invoke<TrafficStatsSnapshot>("traffic_stats_snapshot");
    setTrafficSnapshot(snapshot);
    setTrafficHistory((previous) => {
      const nextPoint: TrafficHistoryPoint = {
        uploadBytesPerSec: snapshot.upload_bytes_per_sec,
        downloadBytesPerSec: snapshot.download_bytes_per_sec,
        tsMs: snapshot.updated_at_ms || Date.now(),
      };
      const next = previous.concat(nextPoint);
      if (next.length <= MAX_HISTORY_POINTS) {
        return next;
      }
      return next.slice(next.length - MAX_HISTORY_POINTS);
    });
  }, []);

  return {
    trafficSnapshot,
    trafficHistory,
    refreshTrafficStats,
  };
}
