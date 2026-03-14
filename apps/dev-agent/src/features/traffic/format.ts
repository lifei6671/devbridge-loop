const KIB = 1024;
const MIB = 1024 * KIB;
const GIB = 1024 * MIB;

export interface RateDisplayParts {
  valueText: string;
  unitText: string;
}

function safeFinite(value: number): number {
  if (!Number.isFinite(value) || value < 0) {
    return 0;
  }
  return value;
}

export function formatBytesPerSecParts(bytesPerSec: number): RateDisplayParts {
  const normalized = safeFinite(bytesPerSec);
  if (normalized >= GIB) {
    return { valueText: (normalized / GIB).toFixed(2), unitText: "GB/s" };
  }
  if (normalized >= MIB) {
    return { valueText: (normalized / MIB).toFixed(1), unitText: "MB/s" };
  }
  if (normalized >= KIB) {
    return { valueText: (normalized / KIB).toFixed(1), unitText: "KB/s" };
  }
  return { valueText: normalized.toFixed(0), unitText: "B/s" };
}

export function bytesPerSecToMiB(bytesPerSec: number): number {
  return safeFinite(bytesPerSec) / MIB;
}

export function formatBytesToGiB(bytes: number): string {
  return (safeFinite(bytes) / GIB).toFixed(1);
}
