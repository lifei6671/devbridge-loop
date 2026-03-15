import {
  type FormEvent,
  type KeyboardEvent as ReactKeyboardEvent,
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
} from "react";
import { Toaster, toast } from "sonner";

type AdminPageKey =
  | "dashboard"
  | "routes"
  | "connectors"
  | "traffic"
  | "ops"
  | "observability";

type ApiRecord = Record<string, unknown>;

type StateTone = "normal" | "ok" | "warn" | "danger";

type DetailDomain = "route" | "connector" | "session" | "tunnel";

type DetailSelection = {
  domain: DetailDomain;
  index: number;
};

type ChartDatum = {
  label: string;
  value: number;
  tone?: StateTone;
};

type TrendDatum = {
  label: string;
  value: number;
};

type DetailSummaryRow = {
  label: string;
  hint: string;
  value: string;
};

const authStorageKey = "bridge.admin.token";
const autoRefreshEnabledStorageKey = "bridge.admin.auto_refresh.enabled";
const autoRefreshIntervalStorageKey = "bridge.admin.auto_refresh.interval_ms";
const autoRefreshIntervalOptions = [3000, 5000, 10000, 30000];
const defaultAutoRefreshIntervalMS = 5000;
const minSSEReconnectIntervalMS = 15000;

type RefreshPageOptions = {
  silentError?: boolean;
};

type RealtimeMode = "off" | "sse" | "polling";

type SSEConnectionState = "idle" | "connecting" | "live" | "error";

type SSEEnvelope = {
  version?: string;
  type?: string;
  topic?: string;
  server_time_ms?: number;
  sequence?: number;
  payload?: unknown;
};

const sseSnapshotEventName = "bridge.snapshot";
const sseReadyEventName = "bridge.ready";
const sseHeartbeatEventName = "bridge.heartbeat";

const pageCatalog: Array<{
  key: AdminPageKey;
  title: string;
  subtitle: string;
}> = [
  { key: "dashboard", title: "总览", subtitle: "运行态健康与关键指标" },
  { key: "routes", title: "路由", subtitle: "Route 配置与命中上下文" },
  { key: "connectors", title: "连接", subtitle: "Connector / Session 运行态" },
  { key: "traffic", title: "隧道流量", subtitle: "Tunnel Pool 与 Traffic 观测" },
  { key: "ops", title: "配置运维", subtitle: "受控写接口与审计入口" },
  { key: "observability", title: "日志诊断", subtitle: "Logs / Metrics / Diagnose" },
];

/**
 * isRecord 用于把未知值缩窄成可安全读取的对象。
 */
function isRecord(value: unknown): value is ApiRecord {
  return typeof value === "object" && value !== null;
}

/**
 * asRecord 将未知值兜底为对象，避免渲染层出现空指针分支。
 */
function asRecord(value: unknown): ApiRecord {
  if (!isRecord(value)) {
    return {};
  }
  return value;
}

/**
 * asRecordArray 把未知值标准化成对象数组，便于统一渲染表格。
 */
function asRecordArray(value: unknown): ApiRecord[] {
  if (!Array.isArray(value)) {
    return [];
  }
  return value.filter((item): item is ApiRecord => isRecord(item));
}

/**
 * readText 按键读取字符串字段，并提供默认值。
 */
function readText(record: ApiRecord, key: string, fallback = "-"): string {
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

/**
 * readNumber 按键读取数值字段，支持字符串数值。
 */
function readNumber(record: ApiRecord, key: string, fallback = 0): number {
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
 * asPrettyTime 将毫秒时间戳格式化为本地时间。
 */
function asPrettyTime(rawMS: unknown): string {
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
function asPercentText(value: number): string {
  if (!Number.isFinite(value)) {
    return "--";
  }
  return `${(value * 100).toFixed(1)}%`;
}

/**
 * encodeQuery 拼接查询参数，自动过滤空值。
 */
function encodeQuery(params: Record<string, string | number | undefined>): string {
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
 * resolveTone 按状态文本推断标签色阶。
 */
function resolveTone(rawState: string): StateTone {
  const state = rawState.toUpperCase();
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
 * parsePatchValue 解析配置补丁值，并做基础入参校验。
 */
function parsePatchValue(patchKey: string, patchRawValue: string): unknown {
  const normalizedValue = patchRawValue.trim();
  if (patchKey === "admin.ui_enabled") {
    if (normalizedValue === "true" || normalizedValue === "1") {
      return true;
    }
    if (normalizedValue === "false" || normalizedValue === "0") {
      return false;
    }
    throw new Error("admin.ui_enabled 仅支持 true/false");
  }
  if (normalizedValue === "") {
    throw new Error("补丁值不能为空");
  }
  return normalizedValue;
}

/**
 * normalizeOperationError 将后端错误转换成更易理解的中文提示。
 */
function normalizeOperationError(error: unknown): string {
  const rawMessage = error instanceof Error ? error.message : "执行失败";
  if (rawMessage.includes("FORBIDDEN: permission denied for role")) {
    return "权限不足：当前 Token 仅有只读权限，请切换 operator/admin Token 后重试。";
  }
  return rawMessage;
}

/**
 * pickPageMeta 根据页面 key 查找标题与副标题。
 */
function pickPageMeta(page: AdminPageKey): { title: string; subtitle: string } {
  return pageCatalog.find((item) => item.key === page) ?? pageCatalog[0];
}

/**
 * pickSSETopicByPage 把当前页面映射到 SSE topic，保证前后端契约一致。
 */
function pickSSETopicByPage(page: AdminPageKey): string {
  if (page === "dashboard") {
    return "dashboard";
  }
  if (page === "routes") {
    return "routes";
  }
  if (page === "connectors") {
    return "connectors";
  }
  if (page === "traffic") {
    return "traffic";
  }
  if (page === "ops") {
    return "ops";
  }
  return "observability";
}

/**
 * parseSSEEnvelope 安全解析 SSE 事件体，解析失败返回 null。
 */
function parseSSEEnvelope(rawData: string): SSEEnvelope | null {
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

/**
 * buildDetailSummaryRows 根据详情类型生成核心字段摘要。
 */
function buildDetailSummaryRows(domain: DetailDomain, record: ApiRecord): DetailSummaryRow[] {
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
    { label: "Tunnel ID", hint: "隧道实例唯一标识。", value: readText(record, "tunnel_id") },
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
function pickDetailTitle(domain: DetailDomain, record: ApiRecord): string {
  if (domain === "route") {
    return `Route ${readText(record, "route_id", "unknown")}`;
  }
  if (domain === "connector") {
    return `Connector ${readText(record, "connector_id", "unknown")}`;
  }
  if (domain === "session") {
    return `Session ${readText(record, "session_id", "unknown")}`;
  }
  return `Tunnel ${readText(record, "tunnel_id", "unknown")}`;
}

/**
 * buildDetailRowElementID 生成详情行 DOM id，用于抽屉切换时自动定位表格行。
 */
function buildDetailRowElementID(domain: DetailDomain, index: number): string {
  return `detail-row-${domain}-${index}`;
}

/**
 * isUsableID 判断详情对象中的资源 ID 是否可用于快捷运维动作。
 */
function isUsableID(rawID: string): boolean {
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
async function copyTextToClipboard(text: string): Promise<boolean> {
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

/**
 * pickMetricLabel 为指标 key 提供更易读的中文名称。
 */
function pickMetricLabel(metricKey: string): string {
  const metricLabelMap: Record<string, string> = {
    acquire_wait_count: "Acquire Wait",
    acquire_wait_total_ms: "Acquire Wait MS",
    open_timeout_total: "Open Timeout",
    open_reject_total: "Open Reject",
    open_ack_late_total: "Open Ack Late",
    hybrid_fallback_total: "Hybrid Fallback",
    endpoint_override_total: "Endpoint Override",
  };
  return metricLabelMap[metricKey] ?? metricKey;
}

/**
 * summarizeMetricBars 把最新 metrics 点位转换成条形图数据。
 */
function summarizeMetricBars(point: ApiRecord): ChartDatum[] {
  const metricKeys = [
    "open_timeout_total",
    "open_reject_total",
    "open_ack_late_total",
    "acquire_wait_count",
    "hybrid_fallback_total",
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
function summarizeLogResults(items: ApiRecord[]): ChartDatum[] {
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
function summarizeLogActions(items: ApiRecord[], maxCount = 6): ChartDatum[] {
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
 * buildMetricTrend 构建指定指标的趋势序列。
 */
function buildMetricTrend(items: ApiRecord[], metricKey: string): TrendDatum[] {
  return items.map((item, index) => ({
    label: asPrettyTime(readNumber(item, "ts_ms", 0)) === "--" ? `#${index + 1}` : asPrettyTime(readNumber(item, "ts_ms", 0)),
    value: readNumber(item, metricKey, 0),
  }));
}

export default function App() {
  const [activePage, setActivePage] = useState<AdminPageKey>("dashboard");
  const [token, setToken] = useState("");
  const [tokenDraft, setTokenDraft] = useState("");
  const [isLoading, setIsLoading] = useState(false);
  const [lastSyncMS, setLastSyncMS] = useState(0);
  const [detailSelection, setDetailSelection] = useState<DetailSelection | null>(null);

  const [overview, setOverview] = useState<ApiRecord>({});
  const [routeItems, setRouteItems] = useState<ApiRecord[]>([]);
  const [connectorItems, setConnectorItems] = useState<ApiRecord[]>([]);
  const [sessionItems, setSessionItems] = useState<ApiRecord[]>([]);
  const [tunnelSummary, setTunnelSummary] = useState<ApiRecord>({});
  const [agentPoolSummary, setAgentPoolSummary] = useState<ApiRecord>({});
  const [tunnelItems, setTunnelItems] = useState<ApiRecord[]>([]);
  const [trafficSummary, setTrafficSummary] = useState<ApiRecord>({});
  const [configSnapshot, setConfigSnapshot] = useState<ApiRecord>({});
  const [logItems, setLogItems] = useState<ApiRecord[]>([]);
  const [metricPoints, setMetricPoints] = useState<ApiRecord[]>([]);
  const [diagnoseSummary, setDiagnoseSummary] = useState<ApiRecord>({});

  const [sessionStateFilter, setSessionStateFilter] = useState("ALL");
  const [tunnelStateFilter, setTunnelStateFilter] = useState("ALL");
  const [tunnelConnectorFilter, setTunnelConnectorFilter] = useState("ALL");
  const [timeRangeMinutes, setTimeRangeMinutes] = useState(30);
  const [activeMetricKey, setActiveMetricKey] = useState("open_timeout_total");

  const [drainSessionID, setDrainSessionID] = useState("");
  const [drainConnectorID, setDrainConnectorID] = useState("");
  const [drainReason, setDrainReason] = useState("manual_ops");
  const [patchKey, setPatchKey] = useState("observability.log_level");
  const [patchValue, setPatchValue] = useState("debug");
  const [exportDownloadURL, setExportDownloadURL] = useState("");
  const [autoRefreshEnabled, setAutoRefreshEnabled] = useState(true);
  const [autoRefreshIntervalMS, setAutoRefreshIntervalMS] = useState(defaultAutoRefreshIntervalMS);
  const [isAutoRefreshing, setIsAutoRefreshing] = useState(false);
  const [realtimeMode, setRealtimeMode] = useState<RealtimeMode>("off");
  const [sseConnectionState, setSSEConnectionState] = useState<SSEConnectionState>("idle");
  const [sseReconnectTrigger, setSSEReconnectTrigger] = useState(0);
  const isAutoRefreshInFlightRef = useRef(false);
  const sseEventSourceRef = useRef<EventSource | null>(null);

  const detailDomainItems = useMemo(
    () => ({
      route: routeItems,
      connector: connectorItems,
      session: sessionItems,
      tunnel: tunnelItems,
    }),
    [routeItems, connectorItems, sessionItems, tunnelItems]
  );

  const currentDetailItems = useMemo(() => {
    if (detailSelection === null) {
      return [] as ApiRecord[];
    }
    return detailDomainItems[detailSelection.domain] ?? [];
  }, [detailDomainItems, detailSelection]);

  const currentDetailRecord = useMemo(() => {
    if (detailSelection === null) {
      return null;
    }
    if (detailSelection.index < 0 || detailSelection.index >= currentDetailItems.length) {
      return null;
    }
    return currentDetailItems[detailSelection.index];
  }, [currentDetailItems, detailSelection]);

  const currentDetailTitle = useMemo(() => {
    if (detailSelection === null || currentDetailRecord === null) {
      return "";
    }
    return pickDetailTitle(detailSelection.domain, currentDetailRecord);
  }, [currentDetailRecord, detailSelection]);

  /**
   * trafficConnectorOptions 汇总可选的 connector 列表，供 Tunnel 页按 Agent 过滤。
   */
  const trafficConnectorOptions = useMemo(() => {
    const connectorIDSet = new Set<string>();
    for (const item of connectorItems) {
      const connectorID = readText(item, "connector_id", "");
      if (connectorID !== "") {
        connectorIDSet.add(connectorID);
      }
    }
    for (const item of tunnelItems) {
      const connectorID = readText(item, "connector_id", "");
      if (connectorID !== "") {
        connectorIDSet.add(connectorID);
      }
    }
    if (tunnelConnectorFilter !== "ALL") {
      connectorIDSet.add(tunnelConnectorFilter);
    }
    return ["ALL", ...Array.from(connectorIDSet).sort((left, right) => left.localeCompare(right))];
  }, [connectorItems, tunnelConnectorFilter, tunnelItems]);

  /**
 * openDetailDrawer 打开右侧详情抽屉，统一用于 route/connector/session/tunnel 四类对象。
   */
  const openDetailDrawer = useCallback(
    (domain: DetailDomain, index: number) => {
      if (index < 0) {
        return;
      }
      setDetailSelection({
        domain,
        index,
      });
    },
    []
  );

  /**
   * closeDetailDrawer 关闭详情抽屉。
   */
  const closeDetailDrawer = useCallback(() => {
    setDetailSelection(null);
  }, []);

  /**
   * moveDetailSelection 在当前抽屉内切换上一条/下一条记录。
   */
  const moveDetailSelection = useCallback(
    (offset: number) => {
      setDetailSelection((previousSelection) => {
        if (previousSelection === null) {
          return previousSelection;
        }
        const items = detailDomainItems[previousSelection.domain] ?? [];
        const nextIndex = previousSelection.index + offset;
        if (nextIndex < 0 || nextIndex >= items.length) {
          return previousSelection;
        }
        return {
          ...previousSelection,
          index: nextIndex,
        };
      });
    },
    [detailDomainItems]
  );

  useEffect(() => {
    const persistedToken = window.localStorage.getItem(authStorageKey) ?? "devbridge-viewer-token";
    const persistedAutoRefreshEnabled = window.localStorage.getItem(autoRefreshEnabledStorageKey);
    const persistedAutoRefreshInterval = Number(
      window.localStorage.getItem(autoRefreshIntervalStorageKey)
    );
    setToken(persistedToken);
    setTokenDraft(persistedToken);
    // 首次加载时恢复自动刷新开关，默认开启。
    setAutoRefreshEnabled(persistedAutoRefreshEnabled !== "false");
    // 仅接受预定义档位，避免异常值导致轮询频率失控。
    if (autoRefreshIntervalOptions.includes(persistedAutoRefreshInterval)) {
      setAutoRefreshIntervalMS(persistedAutoRefreshInterval);
    }
  }, []);

  useEffect(() => {
    if (token.trim() === "") {
      return;
    }
    window.localStorage.setItem(authStorageKey, token);
  }, [token]);

  useEffect(() => {
    window.localStorage.setItem(
      autoRefreshEnabledStorageKey,
      autoRefreshEnabled ? "true" : "false"
    );
  }, [autoRefreshEnabled]);

  useEffect(() => {
    window.localStorage.setItem(autoRefreshIntervalStorageKey, String(autoRefreshIntervalMS));
  }, [autoRefreshIntervalMS]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    const handleEscapePress = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeDetailDrawer();
        return;
      }
      if (event.key === "ArrowLeft") {
        moveDetailSelection(-1);
        return;
      }
      if (event.key === "ArrowRight") {
        moveDetailSelection(1);
      }
    };
    window.addEventListener("keydown", handleEscapePress);
    return () => {
      window.removeEventListener("keydown", handleEscapePress);
    };
  }, [closeDetailDrawer, detailSelection, moveDetailSelection]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    if (currentDetailRecord !== null) {
      return;
    }
    // 列表刷新后索引失效时自动关闭抽屉，避免展示脏状态。
    closeDetailDrawer();
  }, [closeDetailDrawer, currentDetailRecord, detailSelection]);

  /**
   * requestAdmin 统一封装后台 API 调用，集中处理认证与错误语义。
   */
  const requestAdmin = useCallback(
    async (path: string, init?: RequestInit): Promise<ApiRecord> => {
      const normalizedToken = token.trim();
      if (normalizedToken === "") {
        throw new Error("请先填写 Bearer Token");
      }
      const headers = new Headers(init?.headers);
      headers.set("Accept", "application/json");
      headers.set("Authorization", `Bearer ${normalizedToken}`);
      // 非 FormData 请求统一按 JSON 发送，减少接口层分支判断。
      if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
      const response = await fetch(path, {
        ...init,
        headers,
      });
      const rawText = await response.text();
      let parsedPayload: unknown = {};
      if (rawText.trim() !== "") {
        try {
          parsedPayload = JSON.parse(rawText);
        } catch {
          parsedPayload = { raw_text: rawText };
        }
      }
      const responseRecord = asRecord(parsedPayload);
      if (!response.ok) {
        const errorRecord = asRecord(responseRecord.error);
        const errorCode = readText(errorRecord, "code", "REQUEST_FAILED");
        const errorText = readText(errorRecord, "message", `HTTP ${response.status}`);
        throw new Error(`${errorCode}: ${errorText}`);
      }
      return responseRecord;
    },
    [token]
  );

  /**
   * refreshPageData 按页面上下文拉取数据，避免一次性加载全量接口。
   */
  const refreshPageData = useCallback(
    async (page: AdminPageKey, options?: RefreshPageOptions) => {
      setIsLoading(true);
      try {
        if (page === "dashboard") {
          const [overviewResponse, tunnelSummaryResponse, trafficSummaryResponse, diagnoseResponse] =
            await Promise.all([
              requestAdmin("/api/admin/bridge/overview"),
              requestAdmin("/api/admin/tunnels/summary"),
              requestAdmin("/api/admin/traffic/summary"),
              requestAdmin("/api/admin/diagnose/summary"),
            ]);
          setOverview(asRecord(overviewResponse.overview));
          setTunnelSummary(asRecord(tunnelSummaryResponse.summary));
          setTrafficSummary(asRecord(trafficSummaryResponse.summary));
          setDiagnoseSummary(asRecord(diagnoseResponse.summary));
        }

        if (page === "routes") {
          const response = await requestAdmin(`/api/admin/routes${encodeQuery({ limit: 100 })}`);
          setRouteItems(asRecordArray(response.items));
        }

        if (page === "connectors") {
          const [connectorResponse, sessionResponse] = await Promise.all([
            requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 100 })}`),
            requestAdmin(
              `/api/admin/sessions${encodeQuery({
                limit: 100,
                state: sessionStateFilter === "ALL" ? undefined : sessionStateFilter,
              })}`
            ),
          ]);
          setConnectorItems(asRecordArray(connectorResponse.items));
          setSessionItems(asRecordArray(sessionResponse.items));
        }

        if (page === "traffic") {
          const connectorFilter = tunnelConnectorFilter === "ALL" ? undefined : tunnelConnectorFilter;
          const [tunnelSummaryResponse, tunnelResponse, trafficResponse, connectorResponse] =
            await Promise.all([
              // 汇总与列表都带 connector 过滤，保证“池子数”和“明细”口径一致。
              requestAdmin(
                `/api/admin/tunnels/summary${encodeQuery({
                  connector_id: connectorFilter,
                })}`
              ),
              requestAdmin(
                `/api/admin/tunnels${encodeQuery({
                  limit: 120,
                  state: tunnelStateFilter === "ALL" ? undefined : tunnelStateFilter.toLowerCase(),
                  connector_id: connectorFilter,
                })}`
              ),
              requestAdmin("/api/admin/traffic/summary"),
              requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 120 })}`),
            ]);
          setTunnelSummary(asRecord(tunnelSummaryResponse.summary));
          setAgentPoolSummary(asRecord(tunnelSummaryResponse.agent_pool_summary));
          setTunnelItems(asRecordArray(tunnelResponse.items));
          setTrafficSummary(asRecord(trafficResponse.summary));
          setConnectorItems(asRecordArray(connectorResponse.items));
        }

        if (page === "ops") {
          const [configResponse, connectorResponse, sessionResponse] = await Promise.all([
            requestAdmin("/api/admin/config/snapshot"),
            requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 100 })}`),
            requestAdmin(`/api/admin/sessions${encodeQuery({ limit: 100 })}`),
          ]);
          setConfigSnapshot(asRecord(configResponse.snapshot));
          setConnectorItems(asRecordArray(connectorResponse.items));
          setSessionItems(asRecordArray(sessionResponse.items));
        }

        if (page === "observability") {
          const nowMS = Date.now();
          const fromMS = nowMS - timeRangeMinutes * 60 * 1000;
          const [logsResponse, metricsResponse, diagnoseResponse] = await Promise.all([
            requestAdmin(
              `/api/admin/logs/search${encodeQuery({
                from: fromMS,
                to: nowMS,
                limit: 80,
              })}`
            ),
            requestAdmin(
              `/api/admin/metrics/query${encodeQuery({
                from: fromMS,
                to: nowMS,
              })}`
            ),
            requestAdmin("/api/admin/diagnose/summary"),
          ]);
          setLogItems(asRecordArray(logsResponse.items));
          setMetricPoints(asRecordArray(metricsResponse.points));
          setDiagnoseSummary(asRecord(diagnoseResponse.summary));
        }
        setLastSyncMS(Date.now());
      } catch (error) {
        if (!options?.silentError) {
          toast.error(normalizeOperationError(error));
        }
      } finally {
        setIsLoading(false);
      }
    },
    [requestAdmin, sessionStateFilter, timeRangeMinutes, tunnelConnectorFilter, tunnelStateFilter]
  );

  /**
   * applySSESnapshot 把 SSE 快照载荷映射到页面状态，保持与 REST 刷新结果一致。
   */
  const applySSESnapshot = useCallback((topic: string, payload: ApiRecord) => {
    if (topic === "dashboard") {
      setOverview(asRecord(payload.overview));
      setTunnelSummary(asRecord(payload.tunnel_summary));
      setTrafficSummary(asRecord(payload.traffic_summary));
      setDiagnoseSummary(asRecord(payload.diagnose_summary));
      return;
    }
    if (topic === "routes") {
      setRouteItems(asRecordArray(payload.items));
      return;
    }
    if (topic === "connectors") {
      setConnectorItems(asRecordArray(payload.connectors));
      setSessionItems(asRecordArray(payload.sessions));
      return;
    }
    if (topic === "traffic") {
      setTunnelSummary(asRecord(payload.tunnel_summary));
      setAgentPoolSummary(asRecord(payload.agent_pool_summary));
      setTunnelItems(asRecordArray(payload.tunnels));
      setConnectorItems(asRecordArray(payload.connectors));
      setTrafficSummary(asRecord(payload.traffic_summary));
      return;
    }
    if (topic === "ops") {
      setConfigSnapshot(asRecord(payload.snapshot));
      setConnectorItems(asRecordArray(payload.connectors));
      setSessionItems(asRecordArray(payload.sessions));
      return;
    }
    if (topic === "observability") {
      setLogItems(asRecordArray(payload.logs));
      setMetricPoints(asRecordArray(payload.metrics));
      setDiagnoseSummary(asRecord(payload.diagnose_summary));
    }
  }, []);

  useEffect(() => {
    if (token.trim() === "") {
      return;
    }
    void refreshPageData(activePage);
  }, [activePage, refreshPageData, token]);

  /**
   * scheduleSSEReconnect 在轮询兜底期间定时触发 SSE 重连尝试，避免长期停留在 polling。
   */
  useEffect(() => {
    if (!autoRefreshEnabled || token.trim() === "" || realtimeMode !== "polling") {
      return;
    }
    const reconnectIntervalMS = Math.max(
      autoRefreshIntervalMS * 3,
      minSSEReconnectIntervalMS
    );
    const timerID = window.setInterval(() => {
      setSSEReconnectTrigger((previousValue) => previousValue + 1);
    }, reconnectIntervalMS);
    return () => {
      window.clearInterval(timerID);
    };
  }, [autoRefreshEnabled, autoRefreshIntervalMS, realtimeMode, token]);

  /**
   * establishSSEStream 建立 SSE 实时流连接；失败时自动回退到轮询。
   */
  useEffect(() => {
    if (sseEventSourceRef.current !== null) {
      sseEventSourceRef.current.close();
      sseEventSourceRef.current = null;
    }
    if (!autoRefreshEnabled) {
      setRealtimeMode("off");
      setSSEConnectionState("idle");
      setIsAutoRefreshing(false);
      return;
    }
    const normalizedToken = token.trim();
    if (normalizedToken === "") {
      setRealtimeMode("off");
      setSSEConnectionState("idle");
      setIsAutoRefreshing(false);
      return;
    }
    if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
      setRealtimeMode("polling");
      setSSEConnectionState("error");
      return;
    }

    const activeTopic = pickSSETopicByPage(activePage);
    const streamQuery: Record<string, string | number | undefined> = {
      access_token: normalizedToken,
      topics: activeTopic,
      interval_ms: autoRefreshIntervalMS,
    };
    if (activeTopic === "connectors" && sessionStateFilter !== "ALL") {
      streamQuery.session_state = sessionStateFilter;
    }
    if (activeTopic === "traffic" && tunnelStateFilter !== "ALL") {
      streamQuery.tunnel_state = tunnelStateFilter.toLowerCase();
    }
    if (activeTopic === "traffic" && tunnelConnectorFilter !== "ALL") {
      streamQuery.connector_id = tunnelConnectorFilter;
    }
    if (activeTopic === "observability") {
      streamQuery.time_range_minutes = timeRangeMinutes;
    }
    const streamURL = `/api/admin/events/stream${encodeQuery(streamQuery)}`;
    const eventSource = new window.EventSource(streamURL);
    sseEventSourceRef.current = eventSource;
    setRealtimeMode("sse");
    setSSEConnectionState("connecting");
    let hasReceivedReadyOrSnapshot = false;

    const handleReady = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null) {
        return;
      }
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      hasReceivedReadyOrSnapshot = true;
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    const handleSnapshot = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null || typeof envelope.topic !== "string" || envelope.topic.trim() === "") {
        return;
      }
      applySSESnapshot(envelope.topic, asRecord(envelope.payload));
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      hasReceivedReadyOrSnapshot = true;
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    const handleHeartbeat = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null) {
        return;
      }
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    eventSource.addEventListener(sseReadyEventName, handleReady as EventListener);
    eventSource.addEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
    eventSource.addEventListener(sseHeartbeatEventName, handleHeartbeat as EventListener);
    eventSource.onerror = () => {
      // 首次握手失败时切到轮询，避免页面持续“连接中”无数据。
      if (!hasReceivedReadyOrSnapshot) {
        eventSource.close();
        if (sseEventSourceRef.current === eventSource) {
          sseEventSourceRef.current = null;
        }
        setRealtimeMode("polling");
        setSSEConnectionState("error");
        return;
      }
      setSSEConnectionState("connecting");
    };
    return () => {
      eventSource.removeEventListener(sseReadyEventName, handleReady as EventListener);
      eventSource.removeEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
      eventSource.removeEventListener(sseHeartbeatEventName, handleHeartbeat as EventListener);
      eventSource.close();
      if (sseEventSourceRef.current === eventSource) {
        sseEventSourceRef.current = null;
      }
    };
  }, [
    activePage,
    applySSESnapshot,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    sessionStateFilter,
    sseReconnectTrigger,
    timeRangeMinutes,
    tunnelConnectorFilter,
    token,
    tunnelStateFilter,
  ]);

  /**
   * 自动轮询兜底：仅在 SSE 不可用或握手失败后启用。
   */
  useEffect(() => {
    if (!autoRefreshEnabled || token.trim() === "" || realtimeMode !== "polling") {
      setIsAutoRefreshing(false);
      return;
    }
    const timerID = window.setInterval(() => {
      // 若当前仍在请求中则跳过本轮，下一轮继续尝试。
      if (isAutoRefreshInFlightRef.current) {
        return;
      }
      isAutoRefreshInFlightRef.current = true;
      setIsAutoRefreshing(true);
      void refreshPageData(activePage, { silentError: true }).finally(() => {
        isAutoRefreshInFlightRef.current = false;
        setIsAutoRefreshing(false);
      });
    }, autoRefreshIntervalMS);
    return () => {
      window.clearInterval(timerID);
      isAutoRefreshInFlightRef.current = false;
      setIsAutoRefreshing(false);
    };
  }, [activePage, autoRefreshEnabled, autoRefreshIntervalMS, realtimeMode, refreshPageData, token]);

  /**
   * performReload 调用受控 reload 接口并刷新配置页数据。
   */
  const performReload = useCallback(async () => {
    try {
      const response = await requestAdmin("/api/admin/ops/config/reload", {
        method: "POST",
      });
      const result = asRecord(response.result);
      toast.success(
        `配置已触发重载，版本 ${readNumber(result, "config_version")}，时间 ${asPrettyTime(
          readNumber(result, "reloaded_at_ms")
        )}`
      );
      await refreshPageData("ops");
    } catch (error) {
      toast.error(normalizeOperationError(error));
    }
  }, [refreshPageData, requestAdmin]);

  /**
   * requestSessionDrain 直接调用 session drain API，返回成功提示文本。
   */
  const requestSessionDrain = useCallback(
    async (sessionID: string, reason: string) => {
      const normalizedSessionID = sessionID.trim();
      if (normalizedSessionID === "") {
        throw new Error("请先输入 Session ID");
      }
      const normalizedReason = reason.trim() || "manual_ops";
      const response = await requestAdmin(`/api/admin/ops/session/${normalizedSessionID}/drain`, {
        method: "POST",
        body: JSON.stringify({
          reason: normalizedReason,
        }),
      });
      const result = asRecord(response.result);
      const message = `Session ${readText(result, "session_id")} -> ${readText(
        result,
        "current_state"
      )}，purged_tunnels=${readText(result, "purged_tunnel_count", "0")}`;
      await refreshPageData("connectors");
      await refreshPageData("traffic");
      return message;
    },
    [refreshPageData, requestAdmin]
  );

  /**
   * executeSessionDrain 执行 session drain，并在页面顶部展示结果。
   */
  const executeSessionDrain = useCallback(
    async (sessionID: string, reason: string) => {
      try {
        const message = await requestSessionDrain(sessionID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestSessionDrain]
  );

  /**
   * performDrainSession 从 Ops 页输入框执行 session drain。
   */
  const performDrainSession = useCallback(async () => {
    await executeSessionDrain(drainSessionID, drainReason);
  }, [drainReason, drainSessionID, executeSessionDrain]);

  /**
   * requestConnectorDrain 直接调用 connector drain API，返回成功提示文本。
   */
  const requestConnectorDrain = useCallback(
    async (connectorID: string, reason: string) => {
      const normalizedConnectorID = connectorID.trim();
      if (normalizedConnectorID === "") {
        throw new Error("请先输入 Connector ID");
      }
      const normalizedReason = reason.trim() || "manual_ops";
      const response = await requestAdmin(
        `/api/admin/ops/connector/${normalizedConnectorID}/drain`,
        {
          method: "POST",
          body: JSON.stringify({
            reason: normalizedReason,
          }),
        }
      );
      const result = asRecord(response.result);
      const message = `Connector ${readText(result, "connector_id")} drain 完成，session=${readText(
        result,
        "session_id"
      )}，result=${readText(result, "result")}`;
      await refreshPageData("connectors");
      await refreshPageData("traffic");
      return message;
    },
    [refreshPageData, requestAdmin]
  );

  /**
   * executeConnectorDrain 执行 connector drain，并在页面顶部展示结果。
   */
  const executeConnectorDrain = useCallback(
    async (connectorID: string, reason: string) => {
      try {
        const message = await requestConnectorDrain(connectorID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestConnectorDrain]
  );

  /**
   * performDrainConnector 从 Ops 页输入框执行 connector drain。
   */
  const performDrainConnector = useCallback(async () => {
    await executeConnectorDrain(drainConnectorID, drainReason);
  }, [drainConnectorID, drainReason, executeConnectorDrain]);

  /**
   * prefillOpsFromDetail 把详情中的目标 ID 填充到 Ops 页，方便人工确认后执行。
   */
  const prefillOpsFromDetail = useCallback(
    (target: "session" | "connector", targetID: string) => {
      const normalizedTargetID = targetID.trim();
      if (normalizedTargetID === "") {
        return;
      }
      if (target === "session") {
        setDrainSessionID(normalizedTargetID);
      } else {
        setDrainConnectorID(normalizedTargetID);
      }
      setActivePage("ops");
      toast.info(`已填充 ${target}=${normalizedTargetID} 到 Ops 页面`);
    },
    []
  );

  /**
   * quickDrainFromDetail 在详情抽屉中直接执行 drain 快捷动作。
   */
  const quickDrainFromDetail = useCallback(
    async (target: "session" | "connector", targetID: string) => {
      const normalizedTargetID = targetID.trim();
      if (normalizedTargetID === "") {
        return;
      }
      const reason = "detail_quick_drain";
      try {
        if (target === "session") {
          setDrainSessionID(normalizedTargetID);
          const message = await requestSessionDrain(normalizedTargetID, reason);
          toast.success(message);
          return;
        }
        setDrainConnectorID(normalizedTargetID);
        const message = await requestConnectorDrain(normalizedTargetID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestConnectorDrain, requestSessionDrain]
  );

  /**
   * isActiveDetailRow 判断当前表格行是否与抽屉详情对象一致，用于联动高亮。
   */
  const isActiveDetailRow = useCallback(
    (domain: DetailDomain, index: number): boolean => {
      if (detailSelection === null) {
        return false;
      }
      return detailSelection.domain === domain && detailSelection.index === index;
    },
    [detailSelection]
  );

  /**
   * handleDetailRowKeyDown 让行级详情入口支持键盘触发（Enter/Space）。
   */
  const handleDetailRowKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLTableRowElement>, domain: DetailDomain, index: number) => {
      if (event.key !== "Enter" && event.key !== " ") {
        return;
      }
      // 避免 Space 键触发表格滚动，统一转成“打开详情”动作。
      event.preventDefault();
      openDetailDrawer(domain, index);
    },
    [openDetailDrawer]
  );

  /**
   * performConfigPatch 提交配置 patch，并使用 config_version 做并发保护。
   */
  const performConfigPatch = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      try {
        const configVersion = readNumber(configSnapshot, "config_version");
        if (configVersion <= 0) {
          throw new Error("未读取到 config_version，请先刷新配置快照");
        }
        const patchBody = {
          if_match_version: configVersion,
          patch: {
            [patchKey]: parsePatchValue(patchKey, patchValue),
          },
        };
        const response = await requestAdmin("/api/admin/config", {
          method: "PUT",
          body: JSON.stringify(patchBody),
        });
        const result = asRecord(response.result);
        toast.success(
          `配置更新成功，new_version=${readText(
            result,
            "config_version",
            "--"
          )}，apply_mode=${readText(result, "apply_mode", "--")}`
        );
        await refreshPageData("ops");
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [configSnapshot, patchKey, patchValue, refreshPageData, requestAdmin]
  );

  /**
   * performExportDiagnose 生成诊断包导出链接（admin 权限）。
   */
  const performExportDiagnose = useCallback(async () => {
    setExportDownloadURL("");
    try {
      const response = await requestAdmin("/api/admin/ops/diagnose/export", {
        method: "POST",
      });
      const downloadURL = readText(response, "download_url", "");
      if (downloadURL === "") {
        throw new Error("导出接口未返回 download_url");
      }
      setExportDownloadURL(downloadURL);
      toast.success(
        `诊断包已生成，过期时间 ${asPrettyTime(readNumber(response, "expires_at_ms"))}`
      );
    } catch (error) {
      toast.error(normalizeOperationError(error));
    }
  }, [requestAdmin]);

  const activeMeta = pickPageMeta(activePage);

  const dashboardCards = useMemo(
    () => [
      { label: "Connector", value: readNumber(overview, "connector_total", 0) },
      { label: "Session Active", value: readNumber(overview, "session_active", 0) },
      { label: "Route Total", value: readNumber(overview, "route_total", 0) },
      { label: "Tunnel Idle", value: readNumber(overview, "tunnel_idle", 0) },
      { label: "Open Timeout", value: readNumber(trafficSummary, "open_timeout_total", 0) },
      {
        label: "Fallback",
        value: readNumber(trafficSummary, "hybrid_fallback_total", 0),
      },
    ],
    [overview, trafficSummary]
  );

  const diagnoseIssues = useMemo(() => {
    if (!Array.isArray(diagnoseSummary.issues)) {
      return [] as string[];
    }
    return diagnoseSummary.issues.map((item) => String(item));
  }, [diagnoseSummary]);

  const metricKeyOptions = useMemo(() => {
    const defaultKeys = [
      "open_timeout_total",
      "open_reject_total",
      "open_ack_late_total",
      "acquire_wait_count",
      "acquire_wait_total_ms",
      "hybrid_fallback_total",
      "endpoint_override_total",
    ];
    const keySet = new Set<string>(defaultKeys);
    const firstPoint = metricPoints[0];
    if (firstPoint !== undefined) {
      for (const [key, value] of Object.entries(firstPoint)) {
        if (typeof value === "number" && Number.isFinite(value) && key !== "ts_ms") {
          keySet.add(key);
        }
      }
    }
    return [...keySet];
  }, [metricPoints]);

  useEffect(() => {
    if (metricKeyOptions.includes(activeMetricKey)) {
      return;
    }
    if (metricKeyOptions.length === 0) {
      return;
    }
    setActiveMetricKey(metricKeyOptions[0]);
  }, [activeMetricKey, metricKeyOptions]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    const rowElementID = buildDetailRowElementID(detailSelection.domain, detailSelection.index);
    const targetRowElement = document.getElementById(rowElementID);
    if (!(targetRowElement instanceof HTMLElement)) {
      return;
    }
    // 抽屉切换记录时把对应行滚动到可视范围，减少上下文丢失。
    targetRowElement.scrollIntoView({
      behavior: "smooth",
      block: "nearest",
      inline: "nearest",
    });
  }, [detailSelection]);

  const metricSummaryBars = useMemo(() => {
    const latestPoint = metricPoints.length > 0 ? metricPoints[metricPoints.length - 1] : {};
    return summarizeMetricBars(asRecord(latestPoint));
  }, [metricPoints]);

  const metricTrend = useMemo(
    () => buildMetricTrend(metricPoints, activeMetricKey),
    [activeMetricKey, metricPoints]
  );

  const logResultBars = useMemo(() => summarizeLogResults(logItems), [logItems]);

  const logActionBars = useMemo(() => summarizeLogActions(logItems), [logItems]);

  const renderDashboard = () => (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>关键指标</h3>
          <span className="panel-sub">更新时间 {asPrettyTime(readNumber(overview, "updated_at_ms"))}</span>
        </header>
        <div className="kpi-grid">
          {dashboardCards.map((card) => (
            <article key={card.label} className="kpi-card">
              <p className="kpi-label">{card.label}</p>
              <p className="kpi-value">{card.value}</p>
            </article>
          ))}
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>健康摘要</h3>
          <StatePill label={readText(diagnoseSummary, "health", "unknown")} />
        </header>
        <ul className="issue-list">
          {diagnoseIssues.length === 0 && <li>暂无风险告警，运行状态稳定。</li>}
          {diagnoseIssues.map((issue, index) => (
            <li key={`${issue}-${index}`}>{issue}</li>
          ))}
        </ul>
      </section>
    </div>
  );

  const renderRoutes = () => (
    <section className="panel">
      <header className="panel-head">
        <h3>Route 列表</h3>
        <span className="panel-sub">共 {routeItems.length} 条</span>
      </header>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Route ID</th>
              <th>Target</th>
              <th>Host</th>
              <th>Path Prefix</th>
              <th>Priority</th>
              <th>Version</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            {routeItems.map((item, index) => (
              <tr
                id={buildDetailRowElementID("route", index)}
                key={`${readText(item, "route_id", "route")}-${index}`}
                className={`table-row-clickable ${isActiveDetailRow("route", index) ? "table-row-active" : ""}`}
                onClick={() => openDetailDrawer("route", index)}
                onKeyDown={(event) => handleDetailRowKeyDown(event, "route", index)}
                tabIndex={0}
                role="button"
                aria-label={`查看 Route ${readText(item, "route_id", "unknown")} 详情`}
              >
                <td>{readText(item, "route_id")}</td>
                <td>{readText(item, "target_type")}</td>
                <td>{readText(item, "host")}</td>
                <td>{readText(item, "path_prefix")}</td>
                <td>{readText(item, "priority", "0")}</td>
                <td>{readText(item, "resource_version", "0")}</td>
                <td>
                  <button
                    type="button"
                    className="row-action-btn"
                    onClick={(event) => {
                      // 阻止事件冒泡，避免触发行级点击导致重复调用。
                      event.stopPropagation();
                      openDetailDrawer("route", index);
                    }}
                  >
                    查看详情
                  </button>
                </td>
              </tr>
            ))}
            {routeItems.length === 0 && (
              <tr>
                <td colSpan={7} className="empty-cell">
                  当前没有路由数据。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );

  const renderConnectors = () => (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>Connector 列表</h3>
          <span className="panel-sub">共 {connectorItems.length} 个</span>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Connector ID</th>
                <th>Session</th>
                <th>State</th>
                <th>Service</th>
                <th>Health Rate</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {connectorItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("connector", index)}
                  key={`${readText(item, "connector_id", "connector")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("connector", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("connector", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "connector", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Connector ${readText(item, "connector_id", "unknown")} 详情`}
                >
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "session_id")}</td>
                  <td>
                    <StatePill label={readText(item, "session_state", "UNKNOWN")} />
                  </td>
                  <td>
                    {readText(item, "active_service_count", "0")} /{" "}
                    {readText(item, "service_count", "0")}
                  </td>
                  <td>{asPercentText(readNumber(item, "health_rate"))}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        // 阻止事件冒泡，避免触发行级点击导致重复调用。
                        event.stopPropagation();
                        openDetailDrawer("connector", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {connectorItems.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    当前没有 connector 数据。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Session 列表</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>状态</span>
              <select
                value={sessionStateFilter}
                onChange={(event) => setSessionStateFilter(event.target.value)}
              >
                <option value="ALL">ALL</option>
                <option value="ACTIVE">ACTIVE</option>
                <option value="DRAINING">DRAINING</option>
                <option value="STALE">STALE</option>
                <option value="CLOSED">CLOSED</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("connectors")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Connector ID</th>
                <th>Epoch</th>
                <th>State</th>
                <th>Last Heartbeat</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {sessionItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("session", index)}
                  key={`${readText(item, "session_id", "session")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("session", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("session", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "session", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Session ${readText(item, "session_id", "unknown")} 详情`}
                >
                  <td>{readText(item, "session_id")}</td>
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "epoch", "0")}</td>
                  <td>
                    <StatePill label={readText(item, "state", "UNKNOWN")} />
                  </td>
                  <td>{asPrettyTime(readNumber(item, "last_heartbeat_ms"))}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        // 阻止事件冒泡，避免触发行级点击导致重复调用。
                        event.stopPropagation();
                        openDetailDrawer("session", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {sessionItems.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    当前没有 session 数据。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );

  const renderTraffic = () => (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>Tunnel Pool 摘要</h3>
          <span className="panel-sub">
            {tunnelConnectorFilter === "ALL"
              ? "Bridge 运行态 + Agent 上报池（全部 Agent）"
              : `已按 Agent 过滤：${tunnelConnectorFilter}`}{" "}
            · 更新时间 {asPrettyTime(readNumber(tunnelSummary, "updated_at_ms"))}
          </span>
        </header>
        <p className="panel-note">说明：Bridge 统计是已登记 tunnel；Agent 统计来自 TunnelPoolReport 上报。</p>
        <div className="kpi-grid compact">
          <article className="kpi-card">
            <p className="kpi-label">Idle</p>
            <p className="kpi-value">{readText(tunnelSummary, "idle", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Reserved</p>
            <p className="kpi-value">{readText(tunnelSummary, "reserved", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Active</p>
            <p className="kpi-value">{readText(tunnelSummary, "active", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Broken</p>
            <p className="kpi-value">{readText(tunnelSummary, "broken", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Open Timeout</p>
            <p className="kpi-value">{readText(trafficSummary, "open_timeout_total", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Open Ack Late</p>
            <p className="kpi-value">{readText(trafficSummary, "open_ack_late_total", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent Connected</p>
            <p className="kpi-value">{readText(agentPoolSummary, "connected", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent Idle</p>
            <p className="kpi-value">{readText(agentPoolSummary, "idle", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent In Use</p>
            <p className="kpi-value">{readText(agentPoolSummary, "in_use", "0")}</p>
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Tunnel 列表</h3>
          <div className="inline-actions">
            <label className="field-inline field-inline-wide">
              <span>Agent</span>
              <select
                value={tunnelConnectorFilter}
                onChange={(event) => setTunnelConnectorFilter(event.target.value)}
              >
                {trafficConnectorOptions.map((connectorID) => (
                  <option key={connectorID} value={connectorID}>
                    {connectorID}
                  </option>
                ))}
              </select>
            </label>
            <label className="field-inline">
              <span>State</span>
              <select
                value={tunnelStateFilter}
                onChange={(event) => setTunnelStateFilter(event.target.value)}
              >
                <option value="ALL">ALL</option>
                <option value="idle">idle</option>
                <option value="reserved">reserved</option>
                <option value="active">active</option>
                <option value="closed">closed</option>
                <option value="broken">broken</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("traffic")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Tunnel ID</th>
                <th>Connector</th>
                <th>Session</th>
                <th>Traffic</th>
                <th>State</th>
                <th>Last Error</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {tunnelItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("tunnel", index)}
                  key={`${readText(item, "tunnel_id", "tunnel")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("tunnel", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("tunnel", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "tunnel", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Tunnel ${readText(item, "tunnel_id", "unknown")} 详情`}
                >
                  <td>{readText(item, "tunnel_id")}</td>
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "session_id")}</td>
                  <td>{readText(item, "traffic_id", "--")}</td>
                  <td>
                    <StatePill label={readText(item, "state", "unknown")} />
                  </td>
                  <td>{readText(item, "last_error", "--")}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        // 阻止事件冒泡，避免触发行级点击导致重复调用。
                        event.stopPropagation();
                        openDetailDrawer("tunnel", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {tunnelItems.length === 0 && (
                <tr>
                  <td colSpan={8} className="empty-cell">
                    {tunnelConnectorFilter === "ALL"
                      ? readNumber(agentPoolSummary, "connected", 0) > 0
                        ? `Bridge 当前暂无已登记 tunnel（Agent 上报 connected=${readText(agentPoolSummary, "connected", "0")}）。`
                        : "当前没有 tunnel 数据。"
                      : readNumber(agentPoolSummary, "connected", 0) > 0
                        ? `Agent ${tunnelConnectorFilter} 已上报 connected=${readText(agentPoolSummary, "connected", "0")}，但 Bridge 侧暂无已登记 tunnel。`
                        : `Agent ${tunnelConnectorFilter} 当前没有 tunnel 数据。`}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );

  const renderOps = () => (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>受控运维命令</h3>
          <span className="panel-sub">写操作将记录审计日志</span>
        </header>
        <div className="ops-grid">
          <article className="ops-card">
            <h4>配置重载</h4>
            <p>触发 `POST /api/admin/ops/config/reload`。</p>
            <button
              type="button"
              className="solid-btn ops-action-btn ops-action-reload"
              onClick={() => void performReload()}
            >
              触发 reload
            </button>
          </article>

          <article className="ops-card">
            <h4>Session Drain</h4>
            <p>把指定会话标记为 DRAINING 并收敛 service/tunnel。</p>
            <div className="field-stack">
              <label>
                <span>Session ID</span>
                <input
                  value={drainSessionID}
                  onChange={(event) => setDrainSessionID(event.target.value)}
                  placeholder="session-xxx"
                />
              </label>
              <label>
                <span>Reason</span>
                <input
                  value={drainReason}
                  onChange={(event) => setDrainReason(event.target.value)}
                  placeholder="manual_ops"
                />
              </label>
            </div>
            <button
              type="button"
              className="danger-btn ops-action-btn ops-action-drain-session"
              onClick={() => void performDrainSession()}
            >
              Drain Session
            </button>
          </article>

          <article className="ops-card">
            <h4>Connector Drain</h4>
            <p>按 connector 当前会话执行 drain。</p>
            <div className="field-stack">
              <label>
                <span>Connector ID</span>
                <input
                  value={drainConnectorID}
                  onChange={(event) => setDrainConnectorID(event.target.value)}
                  placeholder="connector-xxx"
                />
              </label>
            </div>
            <button
              type="button"
              className="danger-btn ops-action-btn ops-action-drain-connector"
              onClick={() => void performDrainConnector()}
            >
              Drain Connector
            </button>
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>配置并发更新</h3>
          <span className="panel-sub">
            当前版本 {readText(configSnapshot, "config_version", "--")}
          </span>
        </header>
        <form className="patch-form" onSubmit={(event) => void performConfigPatch(event)}>
          <label>
            <span>Patch Key</span>
            <select value={patchKey} onChange={(event) => setPatchKey(event.target.value)}>
              <option value="observability.log_level">observability.log_level</option>
              <option value="admin.base_path">admin.base_path</option>
              <option value="admin.ui_enabled">admin.ui_enabled</option>
            </select>
          </label>
          <label>
            <span>Patch Value</span>
            <input
              value={patchValue}
              onChange={(event) => setPatchValue(event.target.value)}
              placeholder="debug / /admin / true"
            />
          </label>
          <button type="submit" className="solid-btn">
            提交配置更新
          </button>
        </form>

        <div className="snapshot-box">
          <pre>{JSON.stringify(configSnapshot, null, 2)}</pre>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>诊断导出</h3>
          <span className="panel-sub">仅 admin 角色可调用</span>
        </header>
        <div className="inline-actions">
          <button type="button" className="solid-btn" onClick={() => void performExportDiagnose()}>
            生成导出链接
          </button>
          {exportDownloadURL !== "" && (
            <a className="link-btn" href={exportDownloadURL} target="_blank" rel="noreferrer">
              下载诊断包
            </a>
          )}
        </div>
      </section>
    </div>
  );

  const renderObservability = () => (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>趋势面板</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>指标</span>
              <select
                value={activeMetricKey}
                onChange={(event) => setActiveMetricKey(event.target.value)}
              >
                {metricKeyOptions.map((key) => (
                  <option key={key} value={key}>
                    {pickMetricLabel(key)}
                  </option>
                ))}
              </select>
            </label>
          </div>
        </header>
        <div className="observ-grid">
          <article className="chart-card">
            <h4>{pickMetricLabel(activeMetricKey)} 趋势</h4>
            <TrendLineChart items={metricTrend} emptyText="当前时间窗口无指标数据" />
          </article>
          <article className="chart-card">
            <h4>最新指标快照</h4>
            <BarDistributionChart items={metricSummaryBars} emptyText="暂无快照点位" />
          </article>
          <article className="chart-card">
            <h4>日志结果分布</h4>
            <BarDistributionChart items={logResultBars} emptyText="暂无日志结果统计" />
          </article>
          <article className="chart-card">
            <h4>操作类型 TOP</h4>
            <BarDistributionChart items={logActionBars} emptyText="暂无操作统计" />
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>日志检索</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>窗口</span>
              <select
                value={String(timeRangeMinutes)}
                onChange={(event) => setTimeRangeMinutes(Number(event.target.value))}
              >
                <option value="10">最近 10 分钟</option>
                <option value="30">最近 30 分钟</option>
                <option value="60">最近 60 分钟</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("observability")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Actor</th>
                <th>Path</th>
                <th>Action</th>
                <th>Status</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {logItems.map((item, index) => (
                <tr key={`${readText(item, "ts_ms", String(index))}-${index}`}>
                  <td>{asPrettyTime(readNumber(item, "ts_ms"))}</td>
                  <td>{readText(item, "actor", "--")}</td>
                  <td>{readText(item, "path", "--")}</td>
                  <td>{readText(item, "action", "--")}</td>
                  <td>{readText(item, "status", "--")}</td>
                  <td>{readText(item, "result", "--")}</td>
                </tr>
              ))}
              {logItems.length === 0 && (
                <tr>
                  <td colSpan={6} className="empty-cell">
                    当前时间窗口无日志。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Metrics 快照</h3>
          <span className="panel-sub">points={metricPoints.length}</span>
        </header>
        <div className="snapshot-box">
          <pre>{JSON.stringify(metricPoints[0] ?? {}, null, 2)}</pre>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Diagnose</h3>
          <StatePill label={readText(diagnoseSummary, "health", "unknown")} />
        </header>
        <ul className="issue-list">
          {diagnoseIssues.map((issue, index) => (
            <li key={`${issue}-${index}`}>{issue}</li>
          ))}
          {diagnoseIssues.length === 0 && <li>暂无诊断问题。</li>}
        </ul>
      </section>
    </div>
  );

  const renderMainContent = () => {
    switch (activePage) {
      case "dashboard":
        return renderDashboard();
      case "routes":
        return renderRoutes();
      case "connectors":
        return renderConnectors();
      case "traffic":
        return renderTraffic();
      case "ops":
        return renderOps();
      case "observability":
        return renderObservability();
      default:
        return null;
    }
  };

  return (
    <>
      <div className="admin-shell">
        <aside className="sidebar panel">
        <div className="brand">
          <p className="brand-eyebrow">Bridge Admin</p>
          <h1>Control Console</h1>
          <p className="brand-sub">简洁运维风格 · API 驱动视图</p>
        </div>
        <nav className="nav-list">
          {pageCatalog.map((item) => (
            <button
              key={item.key}
              type="button"
              className={`nav-btn ${activePage === item.key ? "active" : ""}`}
              onClick={() => setActivePage(item.key)}
            >
              <span>{item.title}</span>
              <small>{item.subtitle}</small>
            </button>
          ))}
        </nav>
        <div className="sidebar-foot">
          <p>Last Sync</p>
          <strong>{lastSyncMS > 0 ? asPrettyTime(lastSyncMS) : "--"}</strong>
        </div>
        </aside>

        <main className="main-area">
          <header className="topbar panel">
            <div>
              <p className="topbar-title">{activeMeta.title}</p>
              <p className="topbar-sub">{activeMeta.subtitle}</p>
            </div>
            <div className="auth-actions">
              <label>
                <span>Bearer Token</span>
                <input
                  value={tokenDraft}
                  onChange={(event) => setTokenDraft(event.target.value)}
                  placeholder="devbridge-viewer-token"
                />
              </label>
              <button
                type="button"
                className="ghost-btn"
                onClick={() => {
                  const normalizedToken = tokenDraft.trim();
                  setToken(normalizedToken);
                  toast.success("Token 已更新");
                }}
              >
                应用 Token
              </button>
              <button
                type="button"
                className="solid-btn"
                onClick={() => void refreshPageData(activePage)}
              >
                {isLoading ? "加载中..." : "刷新"}
              </button>
              <div className="auto-refresh-tools">
                <button
                  type="button"
                  className={`ghost-btn auto-refresh-toggle ${autoRefreshEnabled ? "active" : ""}`}
                  onClick={() => {
                    setAutoRefreshEnabled((previousValue) => !previousValue);
                  }}
                >
                  {autoRefreshEnabled ? "自动刷新已开启" : "自动刷新已暂停"}
                </button>
                <label className="field-inline auto-refresh-interval">
                  <span>刷新间隔</span>
                  <select
                    value={String(autoRefreshIntervalMS)}
                    disabled={!autoRefreshEnabled}
                    onChange={(event) => {
                      const nextIntervalMS = Number(event.target.value);
                      if (!autoRefreshIntervalOptions.includes(nextIntervalMS)) {
                        return;
                      }
                      setAutoRefreshIntervalMS(nextIntervalMS);
                    }}
                  >
                    {autoRefreshIntervalOptions.map((intervalMS) => (
                      <option key={intervalMS} value={String(intervalMS)}>
                        {intervalMS / 1000} 秒
                      </option>
                    ))}
                  </select>
                </label>
                <p className="auto-refresh-hint">
                  {!autoRefreshEnabled
                    ? "自动刷新已关闭"
                    : realtimeMode === "sse"
                      ? sseConnectionState === "live"
                        ? "实时流已连接（SSE）"
                        : sseConnectionState === "connecting"
                          ? "实时流连接中..."
                          : "实时流异常，准备回退轮询"
                      : isAutoRefreshing
                        ? "轮询刷新中..."
                        : `轮询模式：每 ${autoRefreshIntervalMS / 1000} 秒刷新当前页（并自动尝试恢复 SSE）`}
                </p>
              </div>
            </div>
          </header>

          {renderMainContent()}
        </main>
      </div>
      <SideDetailDrawer
        selection={detailSelection}
        record={currentDetailRecord}
        title={currentDetailTitle}
        totalCount={currentDetailItems.length}
        onClose={closeDetailDrawer}
        onMove={moveDetailSelection}
        onQuickDrainSession={(sessionID) => {
          void quickDrainFromDetail("session", sessionID);
        }}
        onQuickDrainConnector={(connectorID) => {
          void quickDrainFromDetail("connector", connectorID);
        }}
        onPrefillOps={(target, targetID) => {
          prefillOpsFromDetail(target, targetID);
          closeDetailDrawer();
        }}
      />
      <Toaster richColors position="top-right" />
    </>
  );
}

/**
 * SideDetailDrawer 渲染右侧详情抽屉，用于承载 route/connector/session/tunnel 详情信息。
 */
function SideDetailDrawer(props: {
  selection: DetailSelection | null;
  record: ApiRecord | null;
  title: string;
  totalCount: number;
  onClose: () => void;
  onMove: (offset: number) => void;
  onQuickDrainSession: (sessionID: string) => void;
  onQuickDrainConnector: (connectorID: string) => void;
  onPrefillOps: (target: "session" | "connector", targetID: string) => void;
}) {
  if (props.selection === null || props.record === null) {
    return null;
  }
  const summaryRows = buildDetailSummaryRows(props.selection.domain, props.record);
  const stateText =
    props.selection.domain === "route"
      ? readText(props.record, "target_type", "unknown")
      : props.selection.domain === "connector"
        ? readText(props.record, "session_state", "unknown")
        : readText(props.record, "state", "unknown");

  const hasPrevious = props.selection.index > 0;
  const hasNext = props.selection.index < props.totalCount - 1;
  const orderText = `${props.selection.index + 1} / ${props.totalCount}`;
  const sessionID = readText(props.record, "session_id", "");
  const connectorID = readText(props.record, "connector_id", "");
  const canUseSessionID = isUsableID(sessionID);
  const canUseConnectorID = isUsableID(connectorID);

  const copyRawJSON = async () => {
    const copySucceeded = await copyTextToClipboard(JSON.stringify(props.record, null, 2));
    if (copySucceeded) {
      toast.success("原始对象 JSON 已复制");
      return;
    }
    toast.error("复制失败，请手动复制");
  };

  return (
    <div className="detail-overlay" onClick={props.onClose}>
      <aside
        className="detail-drawer"
        onClick={(event) => {
          // 阻止冒泡，避免点击抽屉内容时触发遮罩关闭。
          event.stopPropagation();
        }}
      >
        <header className="detail-head">
          <div>
            <p className="detail-eyebrow">{props.selection.domain.toUpperCase()} DETAIL</p>
            <h3>{props.title}</h3>
          </div>
          <button type="button" className="ghost-btn" onClick={props.onClose}>
            关闭
          </button>
        </header>

        <section className="detail-nav">
          <button
            type="button"
            className="ghost-btn"
            disabled={!hasPrevious}
            onClick={() => props.onMove(-1)}
          >
            上一条
          </button>
          <span>{orderText}</span>
          <button
            type="button"
            className="ghost-btn"
            disabled={!hasNext}
            onClick={() => props.onMove(1)}
          >
            下一条
          </button>
        </section>

        <section className="detail-state-row">
          <span>当前状态</span>
          <StatePill label={stateText} />
        </section>

        {(canUseSessionID || canUseConnectorID) && (
          <section className="detail-actions">
            {canUseSessionID && (
              <>
                <button
                  type="button"
                  className="danger-btn detail-action-btn"
                  onClick={() => props.onQuickDrainSession(sessionID)}
                >
                  <span className="detail-action-title">立即 Drain Session</span>
                  <span className="detail-action-help">直接执行：把当前 Session 置为 DRAINING 并收敛关联资源。</span>
                </button>
                <button
                  type="button"
                  className="ghost-btn detail-action-btn"
                  onClick={() => props.onPrefillOps("session", sessionID)}
                >
                  <span className="detail-action-title">填充 Session 到 Ops</span>
                  <span className="detail-action-help">仅预填 Session ID 到 Ops 页面，不会立即执行 Drain。</span>
                </button>
              </>
            )}
            {canUseConnectorID && (
              <>
                <button
                  type="button"
                  className="danger-btn detail-action-btn"
                  onClick={() => props.onQuickDrainConnector(connectorID)}
                >
                  <span className="detail-action-title">立即 Drain Connector</span>
                  <span className="detail-action-help">直接执行：对当前 Connector 发起 Drain 收敛。</span>
                </button>
                <button
                  type="button"
                  className="ghost-btn detail-action-btn"
                  onClick={() => props.onPrefillOps("connector", connectorID)}
                >
                  <span className="detail-action-title">填充 Connector 到 Ops</span>
                  <span className="detail-action-help">仅预填 Connector ID 到 Ops 页面，便于确认后再执行。</span>
                </button>
              </>
            )}
          </section>
        )}

        <section className="detail-grid">
          {summaryRows.map((item) => (
            <article key={item.label} className="detail-kv">
              <p>{item.label}</p>
              <span className="detail-kv-help">{item.hint}</span>
              <strong>{item.value}</strong>
            </article>
          ))}
        </section>

        <section className="detail-json">
          <header>
            <div>
              <h4>原始对象</h4>
              <span>用于快速排障与字段核对</span>
            </div>
            <div className="detail-json-actions">
              <button type="button" className="ghost-btn compact" onClick={() => void copyRawJSON()}>
                复制 JSON
              </button>
            </div>
          </header>
          <pre>{JSON.stringify(props.record, null, 2)}</pre>
        </section>
      </aside>
    </div>
  );
}

/**
 * BarDistributionChart 用横向条展示分布值，适合离散统计结果。
 */
function BarDistributionChart(props: { items: ChartDatum[]; emptyText: string }) {
  if (props.items.length === 0) {
    return <p className="chart-empty">{props.emptyText}</p>;
  }
  const maxValue = props.items.reduce((max, item) => Math.max(max, item.value), 0);
  return (
    <div className="bar-chart">
      {props.items.map((item) => {
        const ratio = maxValue <= 0 ? 0 : item.value / maxValue;
        return (
          <div key={item.label} className="bar-row">
            <div className="bar-row-head">
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
            <div className="bar-track">
              <div
                className={`bar-fill ${item.tone ?? "normal"}`}
                style={{ width: `${Math.max(ratio * 100, ratio > 0 ? 3 : 0)}%` }}
              />
            </div>
          </div>
        );
      })}
    </div>
  );
}

/**
 * TrendLineChart 用轻量 SVG 绘制折线趋势，避免引入额外图表依赖。
 */
function TrendLineChart(props: { items: TrendDatum[]; emptyText: string }) {
  if (props.items.length === 0) {
    return <p className="chart-empty">{props.emptyText}</p>;
  }
  const width = 520;
  const height = 168;
  const paddingX = 12;
  const paddingY = 18;
  const values = props.items.map((item) => item.value);
  const minValue = Math.min(...values);
  const maxValue = Math.max(...values);
  const valueRange = maxValue - minValue || 1;

  const points = props.items.map((item, index) => {
    const x =
      props.items.length === 1
        ? width / 2
        : paddingX + (index / (props.items.length - 1)) * (width - paddingX * 2);
    const y = height - paddingY - ((item.value - minValue) / valueRange) * (height - paddingY * 2);
    return {
      x,
      y,
      label: item.label,
      value: item.value,
    };
  });
  const polylinePoints = points.map((point) => `${point.x},${point.y}`).join(" ");

  return (
    <div className="trend-chart">
      <svg viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
        <path
          className="trend-area"
          d={`M ${points[0].x} ${height - paddingY} L ${polylinePoints} L ${
            points[points.length - 1].x
          } ${height - paddingY} Z`}
        />
        <polyline className="trend-line" points={polylinePoints} />
        {points.map((point, index) => (
          <circle key={`${point.label}-${index}`} className="trend-dot" cx={point.x} cy={point.y} r={3} />
        ))}
      </svg>
      <div className="trend-foot">
        <span>{points[0]?.label ?? "--"}</span>
        <span>{points[points.length - 1]?.label ?? "--"}</span>
      </div>
    </div>
  );
}

/**
 * StatePill 渲染状态徽标，统一不同状态的视觉语义。
 */
function StatePill(props: { label: string }) {
  const normalizedLabel = props.label.trim() === "" ? "unknown" : props.label;
  const tone = resolveTone(normalizedLabel);
  return <span className={`state-pill ${tone}`}>{normalizedLabel}</span>;
}
