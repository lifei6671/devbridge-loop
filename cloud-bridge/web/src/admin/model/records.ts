import { bridgeTunnelIDPrefix } from "./constants";
import type { ApiRecord, SSEEnvelope } from "./types";

/**
 * isRecord 用于把未知值缩窄成可安全读取的对象。
 */
export function isRecord(value: unknown): value is ApiRecord {
  return typeof value === "object" && value !== null;
}

/**
 * asRecord 将未知值兜底为对象，避免渲染层出现空指针分支。
 */
export function asRecord(value: unknown): ApiRecord {
  if (!isRecord(value)) {
    return {};
  }
  return value;
}

/**
 * asRecordArray 把未知值标准化成对象数组，便于统一渲染表格。
 */
export function asRecordArray(value: unknown): ApiRecord[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is ApiRecord => isRecord(item));
}

/**
 * readText 按键读取字符串字段，并提供默认值。
 */
export function readText(record: ApiRecord, key: string, fallback = "-"): string {
  const rawValue = record[key];
  if (typeof rawValue === "string" && rawValue.trim() !== "") {
    return rawValue.trim();
  }
  if (typeof rawValue === "number" && Number.isFinite(rawValue)) {
    return String(rawValue);
  }
  if (typeof rawValue === "boolean") {
    return rawValue ? "true" : "false";
  }
  return fallback;
}

export function readScopeText(record: ApiRecord, key = "scope", fallback = "-"): string {
  const rawValue = record[key];
  if (!isRecord(rawValue)) {
    return fallback;
  }
  const namespaceValue = readText(rawValue, "namespace", "").trim();
  const environmentValue = readText(rawValue, "environment", "").trim();
  if (namespaceValue === "" && environmentValue === "") {
    return fallback;
  }
  if (namespaceValue !== "" && environmentValue !== "") {
    return `${namespaceValue}/${environmentValue}`;
  }
  return namespaceValue || environmentValue;
}

/**
 * formatTunnelIDForDisplay 将 Bridge/Agent 不同命名空间的 tunnel_id 统一为可对照展示格式。
 */
export function formatTunnelIDForDisplay(rawTunnelID: string): string {
  const normalizedTunnelID = rawTunnelID.trim();
  if (normalizedTunnelID === "") {
    return "-";
  }
  if (normalizedTunnelID.startsWith(bridgeTunnelIDPrefix)) {
    const suffix = normalizedTunnelID.slice(bridgeTunnelIDPrefix.length).trim();
    if (/^\d+$/.test(suffix)) {
      return `tun-${suffix}`;
    }
  }
  return normalizedTunnelID;
}

/**
 * readTunnelID 读取并格式化 tunnel_id，避免 Bridge/Agent 面板对照时出现格式不一致。
 */
export function readTunnelID(record: ApiRecord, key = "tunnel_id", fallback = "-"): string {
  return formatTunnelIDForDisplay(readText(record, key, fallback));
}

/**
 * readNumber 按键读取数值字段，支持字符串数值。
 */
export function readNumber(record: ApiRecord, key: string, fallback = 0): number {
  const rawValue = record[key];
  if (typeof rawValue === "number" && Number.isFinite(rawValue)) {
    return rawValue;
  }
  if (typeof rawValue === "string") {
    const parsedValue = Number(rawValue);
    if (Number.isFinite(parsedValue)) {
      return parsedValue;
    }
  }
  return fallback;
}

/**
 * encodeQuery 拼接查询参数，自动过滤空值。
 */
export function encodeQuery(params: Record<string, string | number | undefined>): string {
  const searchParams = new URLSearchParams();
  for (const [key, value] of Object.entries(params)) {
    if (value === undefined) {
      continue;
    }
    const normalizedValue = String(value).trim();
    if (normalizedValue === "") {
      continue;
    }
    searchParams.set(key, normalizedValue);
  }
  const queryString = searchParams.toString();
  return queryString === "" ? "" : `?${queryString}`;
}

/**
 * parsePatchValue 解析配置补丁值，并做基础入参校验。
 */
export function parsePatchValue(patchKey: string, patchRawValue: string): unknown {
  const normalizedValue = patchRawValue.trim();
  if (
    patchKey === "admin.ui_enabled" ||
    patchKey === "admin.enabled" ||
    patchKey === "admin.allow_shared_listener"
  ) {
    if (normalizedValue === "true" || normalizedValue === "1") {
      return true;
    }
    if (normalizedValue === "false" || normalizedValue === "0") {
      return false;
    }
    throw new Error(`${patchKey} 仅支持 true/false`);
  }
  if (patchKey === "control_plane.heartbeat_timeout_ms") {
    const parsedValue = Number(normalizedValue);
    if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
      throw new Error("control_plane.heartbeat_timeout_ms 仅支持正整数");
    }
    return Math.trunc(parsedValue);
  }
  if (normalizedValue === "") {
    throw new Error("补丁值不能为空");
  }
  return normalizedValue;
}

/**
 * normalizeOperationError 将后端错误转换成更易理解的中文提示。
 */
export function normalizeOperationError(error: unknown): string {
  const rawMessage = error instanceof Error ? error.message : "执行失败";
  if (rawMessage.includes("FORBIDDEN: permission denied for role")) {
    return "权限不足：当前 Token 仅有只读权限，请切换 operator/admin Token 后重试。";
  }
  return rawMessage;
}

/**
 * parseSSEEnvelope 安全解析 SSE 事件体，解析失败返回 null。
 */
export function parseSSEEnvelope(rawData: string): SSEEnvelope | null {
  if (rawData.trim() === "") {
    return null;
  }
  try {
    const parsedValue = JSON.parse(rawData) as unknown;
    if (!isRecord(parsedValue)) {
      return null;
    }
    return parsedValue as SSEEnvelope;
  } catch {
    return null;
  }
}
