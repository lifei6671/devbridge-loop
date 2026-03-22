import type { ApiRecord, ChartDatum, StateTone, TrendDatum } from "./types";
import { asRecord, readNumber, readText } from "./records";

/**
 * asPrettyTime 将毫秒时间戳格式化为本地时间。
 */
export function asPrettyTime(rawMS: unknown): string {
  const timeMS =
    typeof rawMS === "number"
      ? rawMS
      : typeof rawMS === "string"
        ? Number(rawMS)
        : Number.NaN;
  if (!Number.isFinite(timeMS) || timeMS <= 0) {
    return "--";
  }
  return new Date(timeMS).toLocaleString("zh-CN", {
    hour12: false,
  });
}

/**
 * asPercentText 将比例数值渲染成百分比字符串。
 */
export function asPercentText(value: number): string {
  if (!Number.isFinite(value)) {
    return "--";
  }
  return `${(value * 100).toFixed(1)}%`;
}

/**
 * resolveTone 按状态文本推断标签色阶。
 */
export function resolveTone(rawState: string): StateTone {
  const state = rawState.toUpperCase();
  if (state.includes("UNHEALTHY")) {
    return "danger";
  }
  if (state.includes("ACTIVE") || state.includes("HEALTHY") || state.includes("SUCCESS")) {
    return "ok";
  }
  if (state.includes("DRAIN") || state.includes("RESERVED") || state.includes("WARN")) {
    return "warn";
  }
  if (
    state.includes("BROKEN") ||
    state.includes("STALE") ||
    state.includes("CLOSED") ||
    state.includes("ERROR") ||
    state.includes("FAILED")
  ) {
    return "danger";
  }
  return "normal";
}

/**
 * pickMetricLabel 为指标 key 提供更易读的中文名称。
 */
export function pickMetricLabel(metricKey: string): string {
  const metricLabelMap: Record<string, string> = {
    acquire_wait_count: "Acquire Wait",
    acquire_wait_total_ms: "Acquire Wait MS",
    open_timeout_total: "Open Timeout",
    open_reject_total: "Open Reject",
    open_ack_late_total: "Open Ack Late",
    scope_fallback_total: "Scope Fallback",
    route_conflict_rejection_total: "Route Conflict Reject",
    host_derive_success_total: "Host Derive OK",
    host_derive_failure_total: "Host Derive Fail",
    endpoint_override_total: "Endpoint Override",
  };
  return metricLabelMap[metricKey] ?? metricKey;
}

/**
 * summarizeMetricBars 把最新 metrics 点位转换成条形图数据。
 */
export function summarizeMetricBars(point: ApiRecord): ChartDatum[] {
  const metricKeys = [
    "open_timeout_total",
    "open_reject_total",
    "open_ack_late_total",
    "acquire_wait_count",
    "scope_fallback_total",
    "route_conflict_rejection_total",
    "endpoint_override_total",
  ];
  return metricKeys.map((key) => {
    const value = readNumber(point, key, 0);
    const tone: StateTone = value > 0 ? "warn" : "ok";
    return {
      label: pickMetricLabel(key),
      value,
      tone,
    };
  });
}

/**
 * summarizeLogResults 聚合日志结果分布（success/failed/rejected）。
 */
export function summarizeLogResults(items: ApiRecord[]): ChartDatum[] {
  const counter = new Map<string, number>();
  for (const item of items) {
    const normalizedResult = readText(item, "result", "unknown").toLowerCase();
    counter.set(normalizedResult, (counter.get(normalizedResult) ?? 0) + 1);
  }
  if (counter.size === 0) {
    return [];
  }
  const result: ChartDatum[] = [];
  for (const [label, value] of counter.entries()) {
    let tone: StateTone = "normal";
    if (label === "success") {
      tone = "ok";
    } else if (label === "failed" || label === "rejected") {
      tone = "danger";
    }
    result.push({ label, value, tone });
  }
  return result.sort((left, right) => right.value - left.value);
}

/**
 * summarizeLogActions 统计日志操作类型 TOP N 分布。
 */
export function summarizeLogActions(items: ApiRecord[], maxCount = 6): ChartDatum[] {
  const counter = new Map<string, number>();
  for (const item of items) {
    const action = readText(item, "action", "unknown");
    counter.set(action, (counter.get(action) ?? 0) + 1);
  }
  if (counter.size === 0) {
    return [];
  }
  return [...counter.entries()]
    .sort((left, right) => right[1] - left[1])
    .slice(0, maxCount)
    .map(([label, value]) => ({
      label,
      value,
      tone: "normal",
    }));
}

/**
 * summarizeNamedCounters 把 map[string]number 形式的计数器收敛成可展示的 Top N。
 */
export function summarizeNamedCounters(
  value: unknown,
  maxCount = 3
): Array<{ label: string; value: number }> {
  const items = Object.entries(asRecord(value))
    .map(([label, counter]) => ({
      label,
      value: typeof counter === "number" && Number.isFinite(counter) ? counter : Number(counter),
    }))
    .filter((item) => Number.isFinite(item.value) && item.value > 0)
    .sort((left, right) => right.value - left.value);
  return items.slice(0, maxCount);
}

/**
 * buildMetricTrend 构建指定指标的趋势序列。
 */
export function buildMetricTrend(items: ApiRecord[], metricKey: string): TrendDatum[] {
  return items.map((item, index) => {
    const timeLabel = asPrettyTime(readNumber(item, "ts_ms", 0));
    return {
      label: timeLabel === "--" ? `#${index + 1}` : timeLabel,
      value: readNumber(item, metricKey, 0),
    };
  });
}
