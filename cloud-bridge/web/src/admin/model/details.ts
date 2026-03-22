import { asPercentText, asPrettyTime } from "./metrics";
import { readNumber, readText, readTunnelID } from "./records";
import type { ApiRecord, DetailDomain, DetailSummaryRow } from "./types";

/**
 * buildDetailSummaryRows 根据详情类型生成核心字段摘要。
 */
export function buildDetailSummaryRows(
  domain: DetailDomain,
  record: ApiRecord
): DetailSummaryRow[] {
  if (domain === "route") {
    return [
      { label: "Route ID", hint: "路由规则唯一标识。", value: readText(record, "route_id") },
      { label: "Target", hint: "流量转发目标类型。", value: readText(record, "target_type") },
      { label: "Host", hint: "请求 Host 匹配条件。", value: readText(record, "host") },
      { label: "Path Prefix", hint: "请求路径前缀匹配条件。", value: readText(record, "path_prefix") },
      { label: "Priority", hint: "匹配冲突时的优先级，越大越先。", value: readText(record, "priority", "0") },
      { label: "Resource Version", hint: "控制面下发的版本号。", value: readText(record, "resource_version", "0") },
    ];
  }
  if (domain === "connector") {
    return [
      { label: "Connector ID", hint: "连接器实例唯一标识。", value: readText(record, "connector_id") },
      { label: "Session ID", hint: "当前绑定的会话 ID。", value: readText(record, "session_id", "--") },
      { label: "Session Epoch", hint: "会话代际，重连会递增。", value: readText(record, "session_epoch", "0") },
      { label: "Session State", hint: "会话生命周期状态。", value: readText(record, "session_state") },
      { label: "Service Count", hint: "该连接器发布的服务总数。", value: readText(record, "service_count", "0") },
      { label: "Active Services", hint: "健康可用的服务数量。", value: readText(record, "active_service_count", "0") },
      { label: "Health Rate", hint: "可用服务占服务总数比例。", value: asPercentText(readNumber(record, "health_rate", 0)) },
      { label: "Updated", hint: "最近一次状态刷新时间。", value: asPrettyTime(readNumber(record, "updated_at_ms")) },
    ];
  }
  if (domain === "session") {
    return [
      { label: "Session ID", hint: "会话唯一标识。", value: readText(record, "session_id") },
      { label: "Connector", hint: "所属连接器 ID。", value: readText(record, "connector_id") },
      { label: "Epoch", hint: "会话代际，重建时递增。", value: readText(record, "epoch", "0") },
      { label: "State", hint: "会话当前状态。", value: readText(record, "state") },
      { label: "Last Heartbeat", hint: "最近一次心跳上报时间。", value: asPrettyTime(readNumber(record, "last_heartbeat_ms")) },
      { label: "Updated", hint: "最近一次状态更新时间。", value: asPrettyTime(readNumber(record, "updated_at_ms")) },
    ];
  }
  return [
    { label: "Tunnel ID", hint: "隧道实例唯一标识。", value: readTunnelID(record) },
    { label: "Connector", hint: "隧道所属连接器 ID。", value: readText(record, "connector_id") },
    { label: "Session", hint: "隧道所属会话 ID。", value: readText(record, "session_id") },
    { label: "Traffic", hint: "当前关联的流量请求 ID。", value: readText(record, "traffic_id", "--") },
    { label: "State", hint: "隧道当前状态。", value: readText(record, "state") },
    { label: "Updated", hint: "最近一次状态更新时间。", value: asPrettyTime(readNumber(record, "updated_at_ms")) },
  ];
}

/**
 * pickDetailTitle 为详情抽屉生成标题文本。
 */
export function pickDetailTitle(domain: DetailDomain, record: ApiRecord): string {
  if (domain === "route") {
    return `Route ${readText(record, "route_id", "unknown")}`;
  }
  if (domain === "connector") {
    return `Connector ${readText(record, "connector_id", "unknown")}`;
  }
  if (domain === "session") {
    return `Session ${readText(record, "session_id", "unknown")}`;
  }
  return `Tunnel ${readTunnelID(record, "tunnel_id", "unknown")}`;
}

/**
 * buildDetailRowElementID 生成详情行 DOM id，用于抽屉切换时自动定位表格行。
 */
export function buildDetailRowElementID(domain: DetailDomain, index: number): string {
  return `detail-row-${domain}-${index}`;
}

/**
 * isUsableID 判断详情对象中的资源 ID 是否可用于快捷运维动作。
 */
export function isUsableID(rawID: string): boolean {
  const normalizedID = rawID.trim();
  if (normalizedID === "" || normalizedID === "-" || normalizedID === "--") {
    return false;
  }
  if (normalizedID.toLowerCase() === "unknown") {
    return false;
  }
  return true;
}

/**
 * copyTextToClipboard 统一处理复制动作，兼容不支持 Clipboard API 的环境。
 */
export async function copyTextToClipboard(text: string): Promise<boolean> {
  try {
    if (typeof navigator !== "undefined" && navigator.clipboard?.writeText !== undefined) {
      await navigator.clipboard.writeText(text);
      return true;
    }
  } catch {
    // 回退到 legacy 方案。
  }
  try {
    const textareaElement = document.createElement("textarea");
    textareaElement.value = text;
    textareaElement.setAttribute("readonly", "true");
    textareaElement.style.position = "fixed";
    textareaElement.style.left = "-9999px";
    document.body.appendChild(textareaElement);
    textareaElement.select();
    const copySucceeded = document.execCommand("copy");
    document.body.removeChild(textareaElement);
    return copySucceeded;
  } catch {
    return false;
  }
}
