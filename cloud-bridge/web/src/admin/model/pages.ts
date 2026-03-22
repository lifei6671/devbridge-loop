import { adminPageQueryKey, defaultAdminPage } from "./constants";
import type {
  AdminPageKey,
  NavSection,
  RealtimeMode,
  SSEConnectionState,
  StateTone,
} from "./types";

export const pageCatalog: Array<{
  key: AdminPageKey;
  title: string;
  subtitle: string;
}> = [
  { key: "dashboard", title: "总览", subtitle: "运行态健康与关键指标" },
  { key: "routes", title: "路由", subtitle: "Route 配置与命中上下文" },
  { key: "services", title: "服务", subtitle: "服务明细、Agent 归属与访问方式" },
  { key: "connectors", title: "连接", subtitle: "Connector / Session 运行态" },
  { key: "traffic", title: "隧道流量", subtitle: "Tunnel Pool 与 Traffic 观测" },
  { key: "ops", title: "配置运维", subtitle: "受控写接口与审计入口" },
  { key: "observability", title: "日志诊断", subtitle: "Logs / Metrics / Diagnose" },
];

export const pageSections: NavSection[] = [
  {
    title: "运行视图",
    items: ["dashboard", "routes", "services", "connectors", "traffic"],
  },
  {
    title: "管理与配置",
    items: ["ops", "observability"],
  },
];

export const adminPageKeyLookup: Record<AdminPageKey, AdminPageKey> = {
  dashboard: "dashboard",
  routes: "routes",
  services: "services",
  connectors: "connectors",
  traffic: "traffic",
  ops: "ops",
  observability: "observability",
};

/**
 * normalizeAdminPageKey 将 URL 中的字符串归一化为可识别的菜单 key。
 */
export function normalizeAdminPageKey(rawPage: string): AdminPageKey | null {
  const normalizedPage = rawPage.trim().toLowerCase();
  if (normalizedPage === "") {
    return null;
  }
  if (normalizedPage in adminPageKeyLookup) {
    return adminPageKeyLookup[normalizedPage as AdminPageKey];
  }
  return null;
}

/**
 * resolveAdminPageFromLocation 从 URL 的 query/hash/path 解析当前菜单。
 */
export function resolveAdminPageFromLocation(
  locationValue: Pick<Location, "pathname" | "search" | "hash">
): AdminPageKey {
  const queryPage = normalizeAdminPageKey(
    new URLSearchParams(locationValue.search).get(adminPageQueryKey) ?? ""
  );
  if (queryPage !== null) {
    return queryPage;
  }

  const normalizedHash = locationValue.hash.replace(/^#\/?/, "").split(/[/?#]/)[0] ?? "";
  const hashPage = normalizeAdminPageKey(normalizedHash);
  if (hashPage !== null) {
    return hashPage;
  }

  const pathnameSegments = locationValue.pathname
    .split("/")
    .filter((segment) => segment.trim() !== "");
  const tailSegment = pathnameSegments[pathnameSegments.length - 1] ?? "";
  const pathPage = normalizeAdminPageKey(tailSegment);
  if (pathPage !== null) {
    return pathPage;
  }
  return defaultAdminPage;
}

/**
 * pickPageMeta 根据页面 key 查找标题与副标题。
 */
export function pickPageMeta(page: AdminPageKey): { title: string; subtitle: string } {
  return pageCatalog.find((item) => item.key === page) ?? pageCatalog[0];
}

/**
 * pickSSETopicByPage 把当前页面映射到 SSE topic，保证前后端契约一致。
 */
export function pickSSETopicByPage(page: AdminPageKey): string {
  if (page === "dashboard") {
    return "dashboard";
  }
  if (page === "routes") {
    return "routes";
  }
  if (page === "services") {
    return "services";
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
 * describeRealtimeState 统一生成实时链路文案，避免壳层多处分叉。
 */
export function describeRealtimeState(
  mode: RealtimeMode,
  connectionState: SSEConnectionState,
  isPolling: boolean,
  intervalMS: number,
  enabled: boolean
): { label: string; detail: string; tone: StateTone } {
  if (!enabled) {
    return {
      label: "实时链路暂停",
      detail: "当前仅保留手动刷新。",
      tone: "normal",
    };
  }
  if (mode === "sse") {
    if (connectionState === "live") {
      return {
        label: "SSE 已连接",
        detail: "当前页面由实时事件流驱动更新。",
        tone: "ok",
      };
    }
    if (connectionState === "connecting") {
      return {
        label: "SSE 连接中",
        detail: "等待 ready / snapshot 事件返回。",
        tone: "warn",
      };
    }
    return {
      label: "SSE 异常",
      detail: "正在等待回退轮询或自动重连。",
      tone: "danger",
    };
  }
  if (mode === "polling") {
    return {
      label: isPolling ? "轮询刷新中" : "轮询模式",
      detail: `每 ${intervalMS / 1000} 秒拉取一次当前页面快照。`,
      tone: "warn",
    };
  }
  return {
    label: "待连接",
    detail: "当前页面暂未建立实时链路。",
    tone: "normal",
  };
}
