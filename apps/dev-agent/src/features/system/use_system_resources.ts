import { invoke } from "@tauri-apps/api/core";
import { useCallback, useState } from "react";

import type { SystemResourceSnapshot } from "@/features/system/types";

const EMPTY_SYSTEM_RESOURCE_SNAPSHOT: SystemResourceSnapshot = {
  cpu_percent: 0,
  memory_percent: 0,
  disk_percent: 0,
  memory_used_bytes: 0,
  memory_total_bytes: 0,
  disk_used_bytes: 0,
  disk_total_bytes: 0,
  updated_at_ms: 0,
  source: "none",
};

interface UseSystemResourcesResult {
  systemResourceSnapshot: SystemResourceSnapshot;
  refreshSystemResourceStats: () => Promise<void>;
}

/** 统一维护系统资源快照，供页面多个区块复用。 */
export function useSystemResources(): UseSystemResourcesResult {
  const [systemResourceSnapshot, setSystemResourceSnapshot] = useState<SystemResourceSnapshot>(
    EMPTY_SYSTEM_RESOURCE_SNAPSHOT,
  );

  const refreshSystemResourceStats = useCallback(async () => {
    const snapshot = await invoke<SystemResourceSnapshot>("system_resource_snapshot");
    setSystemResourceSnapshot(snapshot);
  }, []);

  return {
    systemResourceSnapshot,
    refreshSystemResourceStats,
  };
}
