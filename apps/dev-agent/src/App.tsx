import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  Activity,
  AlertTriangle,
  Cable,
  Search,
  Server,
  Settings2,
  ShieldCheck,
  SquareTerminal,
  Waypoints,
  Wrench,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { cn } from "@/lib/utils";
import { registerManagedListener } from "@/runtime_subscription";

type DesiredState = "running" | "stopped";
type ExitKind = "expected" | "unexpected";
type ConnectionState = "disconnected" | "reconnecting" | "resyncing" | "connected";
type RuntimeCommand = "agent_start" | "agent_stop" | "agent_restart" | "agent_crash_inject" | "app_shutdown";
type NavKey = "dashboard" | "runtime" | "config" | "services" | "tunnels" | "diagnostics";

interface HostMetricsSnapshot {
  agent_host_ipc_connected: boolean;
  agent_host_ipc_reconnect_total: number;
  agent_host_rpc_latency_ms: number;
  agent_host_supervisor_restart_total: number;
}

interface AgentRuntimeSnapshot {
  desired_state: DesiredState;
  exit_kind: ExitKind;
  connection_state: ConnectionState;
  process_alive: boolean;
  pid: number | null;
  started_at_ms: number | null;
  updated_at_ms: number;
  last_error: string | null;
  metrics: HostMetricsSnapshot;
}

interface HostConfigSnapshot {
  runtime_program: string;
  runtime_args: string[];
  agent_id: string;
  bridge_addr: string;
  tunnel_pool_min_idle: number;
  tunnel_pool_max_idle: number;
  tunnel_pool_max_inflight: number;
  tunnel_pool_ttl_ms: number;
  tunnel_pool_open_rate: number;
  tunnel_pool_open_burst: number;
  tunnel_pool_reconcile_gap_ms: number;
  ipc_transport: string;
  ipc_endpoint: string;
  allowed_method_domains: string[];
  denied_low_level_methods: string[];
}

interface HostRuntimeConfigDraft {
  runtimeProgram: string;
  runtimeArgsText: string;
  agentId: string;
  bridgeAddr: string;
  tunnelPoolMinIdleText: string;
  tunnelPoolMaxIdleText: string;
  tunnelPoolMaxInflightText: string;
  tunnelPoolTtlMsText: string;
  tunnelPoolOpenRateText: string;
  tunnelPoolOpenBurstText: string;
  tunnelPoolReconcileGapMsText: string;
  ipcEndpoint: string;
}

interface AppBootstrapPayload {
  snapshot: AgentRuntimeSnapshot;
  host_config: HostConfigSnapshot;
}

interface HostLogEntry {
  ts_ms: number;
  level: string;
  module: string;
  code: string;
  message: string;
}

interface AgentRuntimeChangedEvent {
  schema_version: number;
  reason: string;
  dropped_event_count: number;
  snapshot: AgentRuntimeSnapshot;
}

interface ServiceListItem {
  service_id: string;
  service_name: string;
  protocol: string;
  status: string;
  endpoint_count: number;
  last_error: string | null;
  updated_at_ms: number;
}

interface TunnelListItem {
  tunnel_id: string;
  service_id: string;
  state: string;
  local_addr: string;
  remote_addr: string;
  latency_ms: number;
  last_error: string | null;
  updated_at_ms: number;
}

interface NavItem {
  key: NavKey;
  title: string;
  description: string;
  icon: LucideIcon;
}

const NAV_ITEMS: NavItem[] = [
  { key: "dashboard", title: "总览", description: "运行态摘要", icon: Activity },
  { key: "runtime", title: "运行控制", description: "生命周期 + IPC", icon: Wrench },
  { key: "config", title: "配置管理", description: "agent-core + IPC", icon: Settings2 },
  { key: "services", title: "服务列表", description: "服务健康与端点", icon: Server },
  { key: "tunnels", title: "通道列表", description: "通道状态与延迟", icon: Waypoints },
  { key: "diagnostics", title: "诊断中心", description: "事件与宿主日志", icon: SquareTerminal },
];

const TABLE_HEADER_CLASS = "px-4 py-3 text-left text-xs font-semibold tracking-wide text-[#5f6d87]";
const TABLE_CELL_CLASS = "px-4 py-3 text-sm text-[#27344d]";

/** 将毫秒时间戳转换为易读时间文本。 */
function formatTime(ts: number | null): string {
  if (!ts) {
    return "--";
  }
  // 统一按本地时区显示，方便用户对照本机日志时间线。
  return new Date(ts).toLocaleString("zh-CN", { hour12: false });
}

/** 将启动时间转换为“时:分”粒度的运行时长。 */
function formatUptime(startedAtMs: number | null): string {
  if (!startedAtMs) {
    return "--";
  }
  const diffMs = Math.max(0, Date.now() - startedAtMs);
  const totalSeconds = Math.floor(diffMs / 1000);
  const hours = Math.floor(totalSeconds / 3600);
  const minutes = Math.floor((totalSeconds % 3600) / 60);
  const seconds = totalSeconds % 60;
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  if (minutes > 0) {
    return `${minutes}m ${seconds}s`;
  }
  return `${seconds}s`;
}

/** 规范化参数输入字符串，避免空白字符导致伪变更。 */
function normalizeArgsText(text: string): string[] {
  return text
    .split(/\s+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}

/** 将宿主配置快照映射为前端可编辑表单草稿。 */
function toHostRuntimeConfigDraft(snapshot: HostConfigSnapshot): HostRuntimeConfigDraft {
  return {
    runtimeProgram: snapshot.runtime_program,
    runtimeArgsText: snapshot.runtime_args.join(" "),
    agentId: snapshot.agent_id,
    bridgeAddr: snapshot.bridge_addr,
    tunnelPoolMinIdleText: String(snapshot.tunnel_pool_min_idle),
    tunnelPoolMaxIdleText: String(snapshot.tunnel_pool_max_idle),
    tunnelPoolMaxInflightText: String(snapshot.tunnel_pool_max_inflight),
    tunnelPoolTtlMsText: String(snapshot.tunnel_pool_ttl_ms),
    tunnelPoolOpenRateText: String(snapshot.tunnel_pool_open_rate),
    tunnelPoolOpenBurstText: String(snapshot.tunnel_pool_open_burst),
    tunnelPoolReconcileGapMsText: String(snapshot.tunnel_pool_reconcile_gap_ms),
    ipcEndpoint: snapshot.ipc_endpoint,
  };
}

/** 解析正整数文本配置：用于 tunnel pool 的整数参数。 */
function parsePositiveInteger(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (!/^\d+$/.test(normalized)) {
    throw new Error(`${fieldLabel} 必须是非负整数`);
  }
  const parsedValue = Number.parseInt(normalized, 10);
  if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
    throw new Error(`${fieldLabel} 必须大于 0`);
  }
  return parsedValue;
}

/** 解析允许为零的整数文本配置：用于 min_idle / ttl_ms。 */
function parseNonNegativeInteger(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (!/^\d+$/.test(normalized)) {
    throw new Error(`${fieldLabel} 必须是非负整数`);
  }
  const parsedValue = Number.parseInt(normalized, 10);
  if (!Number.isFinite(parsedValue) || parsedValue < 0) {
    throw new Error(`${fieldLabel} 必须大于等于 0`);
  }
  return parsedValue;
}

/** 解析正浮点文本配置：用于 tunnel pool 开闸速率。 */
function parsePositiveFloat(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (normalized.length === 0) {
    throw new Error(`${fieldLabel} 不能为空`);
  }
  const parsedValue = Number.parseFloat(normalized);
  if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
    throw new Error(`${fieldLabel} 必须大于 0`);
  }
  return parsedValue;
}

/** 返回连接状态对应的中文文案。 */
function statusText(state: ConnectionState): string {
  if (state === "connected") {
    return "已连接";
  }
  if (state === "resyncing") {
    return "对账中";
  }
  if (state === "reconnecting") {
    return "重连中";
  }
  return "已断开";
}

/** 将连接状态映射为徽章样式。 */
function statusBadgeVariant(state: ConnectionState): "success" | "warning" | "danger" | "secondary" {
  if (state === "connected") {
    return "success";
  }
  if (state === "resyncing") {
    return "warning";
  }
  if (state === "reconnecting") {
    return "secondary";
  }
  return "danger";
}

/** 将服务状态映射为徽章样式。 */
function serviceBadgeVariant(status: string): "success" | "warning" | "danger" | "secondary" {
  const normalized = status.toLowerCase();
  if (normalized.includes("healthy") || normalized.includes("running")) {
    return "success";
  }
  if (normalized.includes("degraded") || normalized.includes("warning")) {
    return "warning";
  }
  if (normalized.includes("idle") || normalized.includes("pending")) {
    return "secondary";
  }
  return "danger";
}

/** 将通道状态映射为徽章样式。 */
function tunnelBadgeVariant(state: string): "success" | "warning" | "danger" | "secondary" {
  const normalized = state.toLowerCase();
  if (normalized.includes("active") || normalized.includes("connected")) {
    return "success";
  }
  if (normalized.includes("idle") || normalized.includes("pending")) {
    return "secondary";
  }
  if (normalized.includes("reconnecting") || normalized.includes("resync")) {
    return "warning";
  }
  return "danger";
}

/** 简单关键词匹配：用于顶部搜索框过滤当前数据视图。 */
function matchesKeyword(value: string, keyword: string): boolean {
  if (!keyword) {
    return true;
  }
  // 统一转小写后匹配，避免大小写导致命中不一致。
  return value.toLowerCase().includes(keyword);
}

/** 渲染左侧导航项。 */
function SidebarNavItem(props: {
  item: NavItem;
  active: boolean;
  onClick: () => void;
}): JSX.Element {
  const Icon = props.item.icon;
  return (
    <button
      className={cn(
        "flex w-full items-center gap-3 rounded-2xl border px-3 py-3 text-left transition-colors",
        props.active
          ? "border-[#c7d7f5] bg-[#eaf1ff] text-[#1f4ea8]"
          : "border-transparent text-[#27344d] hover:border-[#d8e0ef] hover:bg-[#f7f9fd]",
      )}
      onClick={props.onClick}
    >
      <span className={cn("rounded-xl border p-2", props.active ? "border-[#bfd1f4] bg-white" : "border-[#d9e1ee] bg-white") }>
        <Icon size={16} />
      </span>
      <span className="flex flex-col">
        <strong className="text-[18px] leading-tight">{props.item.title}</strong>
        <small className="mt-0.5 text-xs text-[#6b7790]">{props.item.description}</small>
      </span>
    </button>
  );
}

/** 渲染摘要指标卡。 */
function SummaryMetricCard(props: {
  title: string;
  value: string;
  subtitle: string;
  icon: LucideIcon;
  tone?: "neutral" | "success" | "warning" | "danger";
}): JSX.Element {
  const Icon = props.icon;
  const toneClass =
    props.tone === "success"
      ? "text-[#11865f]"
      : props.tone === "warning"
        ? "text-[#9a6c18]"
        : props.tone === "danger"
          ? "text-[#bf4545]"
          : "text-[#27344d]";

  return (
    <Card>
      <CardContent className="flex h-full flex-col gap-2 p-5">
        <div className="flex items-center gap-2 text-[#5f6d87]">
          <Icon size={18} />
          <span className="text-sm font-medium">{props.title}</span>
        </div>
        <p className={cn("text-[42px] font-semibold leading-none", toneClass)}>{props.value}</p>
        <p className="text-xs text-[#74819a]">{props.subtitle}</p>
      </CardContent>
    </Card>
  );
}

/** 渲染健康检查条目。 */
function HealthCheckRow(props: {
  label: string;
  detail: string;
  level: "ok" | "warn" | "fail";
}): JSX.Element {
  const badgeVariant = props.level === "ok" ? "success" : props.level === "warn" ? "warning" : "danger";
  return (
    <li className="flex items-center justify-between border-b border-[#edf1f7] py-2 text-sm last:border-b-0">
      <span className="font-medium text-[#2a3851]">{props.label}</span>
      <Badge variant={badgeVariant}>{props.detail}</Badge>
    </li>
  );
}

/** 应用主界面：保持真实 Agent 功能边界，仅重做视觉风格。 */
export default function App(): JSX.Element {
  const [activeNav, setActiveNav] = useState<NavKey>("dashboard");
  const [runtimeSnapshot, setRuntimeSnapshot] = useState<AgentRuntimeSnapshot | null>(null);
  const [hostConfig, setHostConfig] = useState<HostConfigSnapshot | null>(null);
  const [hostLogs, setHostLogs] = useState<HostLogEntry[]>([]);
  const [eventFeed, setEventFeed] = useState<AgentRuntimeChangedEvent[]>([]);
  const [serviceItems, setServiceItems] = useState<ServiceListItem[]>([]);
  const [tunnelItems, setTunnelItems] = useState<TunnelListItem[]>([]);
  const [busyCommand, setBusyCommand] = useState<string | null>(null);
  const [errorText, setErrorText] = useState<string>("");
  const [searchText, setSearchText] = useState<string>("");
  const [configSaving, setConfigSaving] = useState<boolean>(false);
  const [configDraft, setConfigDraft] = useState<HostRuntimeConfigDraft>({
    runtimeProgram: "",
    runtimeArgsText: "",
    agentId: "",
    bridgeAddr: "",
    tunnelPoolMinIdleText: "8",
    tunnelPoolMaxIdleText: "32",
    tunnelPoolMaxInflightText: "4",
    tunnelPoolTtlMsText: "90000",
    tunnelPoolOpenRateText: "10",
    tunnelPoolOpenBurstText: "20",
    tunnelPoolReconcileGapMsText: "1000",
    ipcEndpoint: "",
  });

  /** 拉取宿主结构化日志，展示最近日志上下文。 */
  const refreshHostLogs = useCallback(async () => {
    const logs = await invoke<HostLogEntry[]>("host_logs_snapshot");
    // 只保留最近 20 条，避免面板过长影响可读性。
    setHostLogs(logs.slice(-20).reverse());
  }, []);

  /** 拉取运行态快照，作为兜底轮询同步。 */
  const refreshSnapshot = useCallback(async () => {
    const snapshot = await invoke<AgentRuntimeSnapshot>("agent_snapshot");
    setRuntimeSnapshot(snapshot);
  }, []);

  /** 拉取宿主配置快照，并同步到配置编辑草稿。 */
  const refreshHostConfig = useCallback(async () => {
    const snapshot = await invoke<HostConfigSnapshot>("host_config_snapshot");
    setHostConfig(snapshot);
    // 每次拉新后同步草稿，确保表单和后端真相源一致。
    setConfigDraft(toHostRuntimeConfigDraft(snapshot));
  }, []);

  /** 拉取服务列表快照。 */
  const refreshServiceList = useCallback(async () => {
    const items = await invoke<ServiceListItem[]>("service_list_snapshot");
    setServiceItems(items);
  }, []);

  /** 拉取通道列表快照。 */
  const refreshTunnelList = useCallback(async () => {
    const items = await invoke<TunnelListItem[]>("tunnel_list_snapshot");
    setTunnelItems(items);
  }, []);

  /** 执行 bootstrap 链路：启动宿主并拉回配置、状态与列表快照。 */
  const bootstrap = useCallback(async () => {
    const payload = await invoke<AppBootstrapPayload>("app_bootstrap");
    setRuntimeSnapshot(payload.snapshot);
    setHostConfig(payload.host_config);
    setConfigDraft(toHostRuntimeConfigDraft(payload.host_config));
    // 并行刷新多个只读视图，缩短首屏等待时间。
    await Promise.all([refreshHostLogs(), refreshServiceList(), refreshTunnelList()]);
  }, [refreshHostLogs, refreshServiceList, refreshTunnelList]);

  /** 执行生命周期命令并刷新视图。 */
  const runCommand = useCallback(
    async (command: RuntimeCommand) => {
      setBusyCommand(command);
      setErrorText("");
      try {
        // 命令返回的是最新 snapshot，可直接用于主视图同步。
        const snapshot = await invoke<AgentRuntimeSnapshot>(command);
        setRuntimeSnapshot(snapshot);
        await Promise.all([refreshHostLogs(), refreshServiceList(), refreshTunnelList(), refreshHostConfig()]);
      } catch (error) {
        // 命令失败时统一在顶部给出中文错误提示。
        setErrorText(String(error));
      } finally {
        setBusyCommand(null);
      }
    },
    [refreshHostConfig, refreshHostLogs, refreshServiceList, refreshTunnelList],
  );

  /** 页面初始化：bootstrap + 事件订阅 + 轮询兜底。 */
  useEffect(() => {
    let unlisten: UnlistenFn | null = null;
    let disposed = false;
    const pollTimer = window.setInterval(() => {
      // 事件桥失联时，通过轮询保证 UI 仍能逐步收敛到真实状态。
      void refreshSnapshot();
      void refreshHostLogs();
      void refreshServiceList();
      void refreshTunnelList();
    }, 2500);

    void (async () => {
      try {
        await bootstrap();
      } catch (error) {
        if (!disposed) {
          setErrorText(String(error));
        }
      }
      if (disposed) {
        return;
      }
      try {
        const nextUnlisten = await registerManagedListener<AgentRuntimeChangedEvent>(
          listen,
          "agent-runtime-changed",
          (payload) => {
            setRuntimeSnapshot(payload.snapshot);
            // 仅保留最近 30 条事件，防止长时间驻留导致面板过重。
            setEventFeed((prev) => [payload, ...prev].slice(0, 30));
          },
          () => disposed,
        );
        unlisten = nextUnlisten;
      } catch (error) {
        if (!disposed) {
          setErrorText(String(error));
        }
      }
    })();

    return () => {
      disposed = true;
      window.clearInterval(pollTimer);
      if (unlisten) {
        void unlisten();
      }
    };
  }, [bootstrap, refreshHostLogs, refreshServiceList, refreshSnapshot, refreshTunnelList]);

  /** 搜索关键词：统一小写处理，避免重复转换。 */
  const searchKeyword = useMemo(() => searchText.trim().toLowerCase(), [searchText]);

  /** 过滤服务列表：仅基于真实快照字段进行匹配。 */
  const filteredServiceItems = useMemo(() => {
    return serviceItems.filter((item) => {
      const haystack = `${item.service_name} ${item.service_id} ${item.protocol} ${item.status} ${item.last_error ?? ""}`;
      return matchesKeyword(haystack, searchKeyword);
    });
  }, [searchKeyword, serviceItems]);

  /** 过滤通道列表：用于总览和通道页共享。 */
  const filteredTunnelItems = useMemo(() => {
    return tunnelItems.filter((item) => {
      const haystack = `${item.tunnel_id} ${item.service_id} ${item.state} ${item.local_addr} ${item.remote_addr} ${item.last_error ?? ""}`;
      return matchesKeyword(haystack, searchKeyword);
    });
  }, [searchKeyword, tunnelItems]);

  /** 过滤日志列表：支持按模块、消息、级别检索。 */
  const filteredHostLogs = useMemo(() => {
    return hostLogs.filter((log) => {
      const haystack = `${log.level} ${log.module} ${log.code} ${log.message}`;
      return matchesKeyword(haystack, searchKeyword);
    });
  }, [hostLogs, searchKeyword]);

  /** 过滤事件列表：支持按 reason 检索。 */
  const filteredEventFeed = useMemo(() => {
    return eventFeed.filter((event) => {
      const haystack = `${event.reason} ${event.snapshot.last_error ?? ""}`;
      return matchesKeyword(haystack, searchKeyword);
    });
  }, [eventFeed, searchKeyword]);

  /** 计算状态条文案，集中处理空状态。 */
  const statusLabel = useMemo(() => {
    if (!runtimeSnapshot) {
      return "初始化中";
    }
    return statusText(runtimeSnapshot.connection_state);
  }, [runtimeSnapshot]);

  /** 计算运行时长文案，给顶部状态栏展示。 */
  const uptimeLabel = useMemo(() => {
    if (!runtimeSnapshot?.process_alive) {
      return "--";
    }
    return formatUptime(runtimeSnapshot.started_at_ms);
  }, [runtimeSnapshot?.process_alive, runtimeSnapshot?.started_at_ms, runtimeSnapshot?.updated_at_ms]);

  /** 统计最近 24 小时 ERROR 数量，用于风险提示。 */
  const errorsIn24h = useMemo(() => {
    const beginMs = Date.now() - 24 * 60 * 60 * 1000;
    return hostLogs.filter((log) => log.ts_ms >= beginMs && log.level.toLowerCase().includes("error")).length;
  }, [hostLogs]);

  /** 统计最近 24 小时 WARNING 数量，用于底部状态栏。 */
  const warningsIn24h = useMemo(() => {
    const beginMs = Date.now() - 24 * 60 * 60 * 1000;
    return hostLogs.filter((log) => log.ts_ms >= beginMs && log.level.toLowerCase().includes("warn")).length;
  }, [hostLogs]);

  /** 统计健康服务数量，用于总览卡片和健康检查。 */
  const healthyServiceCount = useMemo(() => {
    return serviceItems.filter((item) => serviceBadgeVariant(item.status) === "success").length;
  }, [serviceItems]);

  /** 统计活跃通道数量，用于总览卡片和健康检查。 */
  const activeTunnelCount = useMemo(() => {
    return tunnelItems.filter((item) => tunnelBadgeVariant(item.state) === "success").length;
  }, [tunnelItems]);

  /** 输出健康检查列表，避免在 JSX 中堆积条件判断。 */
  const healthChecks = useMemo(() => {
    const connected = runtimeSnapshot?.connection_state === "connected";
    const latencyMs = runtimeSnapshot?.metrics.agent_host_rpc_latency_ms ?? 0;
    const restartTotal = runtimeSnapshot?.metrics.agent_host_supervisor_restart_total ?? 0;
    const lastError = runtimeSnapshot?.last_error;

    return [
      {
        label: "IPC 通道",
        detail: connected ? "OK" : "未连通",
        level: connected ? "ok" : "fail",
      },
      {
        label: "RPC 延迟",
        detail: `${latencyMs.toFixed(1)} ms`,
        level: latencyMs > 250 ? "warn" : "ok",
      },
      {
        label: "服务健康",
        detail: `${healthyServiceCount}/${serviceItems.length}`,
        level: healthyServiceCount === serviceItems.length ? "ok" : serviceItems.length === 0 ? "warn" : "fail",
      },
      {
        label: "活跃通道",
        detail: `${activeTunnelCount}/${tunnelItems.length}`,
        level: activeTunnelCount > 0 ? "ok" : "warn",
      },
      {
        label: "Supervisor 重启",
        detail: `${restartTotal}`,
        level: restartTotal > 0 ? "warn" : "ok",
      },
      {
        label: "最近错误",
        detail: lastError ?? "无",
        level: lastError ? "fail" : "ok",
      },
    ] as const;
  }, [activeTunnelCount, healthyServiceCount, runtimeSnapshot, serviceItems.length, tunnelItems.length]);

  /** 判断配置表单是否有未保存修改。 */
  const configDirty = useMemo(() => {
    if (!hostConfig) {
      return false;
    }
    const draftArgs = normalizeArgsText(configDraft.runtimeArgsText);
    return (
      hostConfig.runtime_program !== configDraft.runtimeProgram.trim() ||
      hostConfig.agent_id !== configDraft.agentId.trim() ||
      hostConfig.bridge_addr !== configDraft.bridgeAddr.trim() ||
      String(hostConfig.tunnel_pool_min_idle) !== configDraft.tunnelPoolMinIdleText.trim() ||
      String(hostConfig.tunnel_pool_max_idle) !== configDraft.tunnelPoolMaxIdleText.trim() ||
      String(hostConfig.tunnel_pool_max_inflight) !== configDraft.tunnelPoolMaxInflightText.trim() ||
      String(hostConfig.tunnel_pool_ttl_ms) !== configDraft.tunnelPoolTtlMsText.trim() ||
      String(hostConfig.tunnel_pool_open_rate) !== configDraft.tunnelPoolOpenRateText.trim() ||
      String(hostConfig.tunnel_pool_open_burst) !== configDraft.tunnelPoolOpenBurstText.trim() ||
      String(hostConfig.tunnel_pool_reconcile_gap_ms) !== configDraft.tunnelPoolReconcileGapMsText.trim() ||
      hostConfig.ipc_endpoint !== configDraft.ipcEndpoint.trim() ||
      JSON.stringify(hostConfig.runtime_args) !== JSON.stringify(draftArgs)
    );
  }, [configDraft, hostConfig]);

  /** 提交宿主运行配置更新：保存真实启动参数并在重启后生效。 */
  const saveHostRuntimeConfig = useCallback(async () => {
    setConfigSaving(true);
    setErrorText("");
    try {
      const normalizedAgentId = configDraft.agentId.trim();
      const normalizedBridgeAddr = configDraft.bridgeAddr.trim();
      if (!normalizedAgentId) {
        throw new Error("agent_id 不能为空");
      }
      if (!normalizedBridgeAddr) {
        throw new Error("bridge_addr 不能为空");
      }
      // 在前端先做一次数字解析，避免明显错误请求打到后端。
      const tunnelPoolMinIdle = parseNonNegativeInteger(configDraft.tunnelPoolMinIdleText, "tunnel_pool_min_idle");
      const tunnelPoolMaxIdle = parsePositiveInteger(configDraft.tunnelPoolMaxIdleText, "tunnel_pool_max_idle");
      const tunnelPoolMaxInflight = parsePositiveInteger(configDraft.tunnelPoolMaxInflightText, "tunnel_pool_max_inflight");
      const tunnelPoolTtlMs = parseNonNegativeInteger(configDraft.tunnelPoolTtlMsText, "tunnel_pool_ttl_ms");
      const tunnelPoolOpenRate = parsePositiveFloat(configDraft.tunnelPoolOpenRateText, "tunnel_pool_open_rate");
      const tunnelPoolOpenBurst = parsePositiveInteger(configDraft.tunnelPoolOpenBurstText, "tunnel_pool_open_burst");
      const tunnelPoolReconcileGapMs = parsePositiveInteger(
        configDraft.tunnelPoolReconcileGapMsText,
        "tunnel_pool_reconcile_gap_ms",
      );
      const snapshot = await invoke<HostConfigSnapshot>("host_config_update", {
        input: {
          runtime_program: configDraft.runtimeProgram.trim(),
          runtime_args: normalizeArgsText(configDraft.runtimeArgsText),
          agent_id: normalizedAgentId,
          bridge_addr: normalizedBridgeAddr,
          tunnel_pool_min_idle: tunnelPoolMinIdle,
          tunnel_pool_max_idle: tunnelPoolMaxIdle,
          tunnel_pool_max_inflight: tunnelPoolMaxInflight,
          tunnel_pool_ttl_ms: tunnelPoolTtlMs,
          tunnel_pool_open_rate: tunnelPoolOpenRate,
          tunnel_pool_open_burst: tunnelPoolOpenBurst,
          tunnel_pool_reconcile_gap_ms: tunnelPoolReconcileGapMs,
          ipc_endpoint: configDraft.ipcEndpoint.trim(),
        },
      });
      setHostConfig(snapshot);
      // 保存成功后以服务端返回值回填，防止前端草稿与后端规范化值不一致。
      setConfigDraft(toHostRuntimeConfigDraft(snapshot));
      await refreshHostLogs();
    } catch (error) {
      // 更新失败直接在顶部显示，便于用户立即感知。
      setErrorText(String(error));
    } finally {
      setConfigSaving(false);
    }
  }, [configDraft, refreshHostLogs]);

  /** 渲染运行控制面板：仅调用已实现的真实命令。 */
  const renderRuntimePanel = useCallback((): JSX.Element => {
    const actionButtons: Array<{ command: RuntimeCommand; label: string; variant: "default" | "secondary" | "outline" | "destructive" }> = [
      { command: "agent_start", label: "启动 Agent", variant: "default" },
      { command: "agent_stop", label: "停止 Agent", variant: "outline" },
      { command: "agent_restart", label: "重启 Agent", variant: "secondary" },
      { command: "agent_crash_inject", label: "崩溃演练", variant: "destructive" },
      { command: "app_shutdown", label: "关闭宿主", variant: "outline" },
    ];

    return (
      <Card>
        <CardHeader>
          <CardTitle>运行控制</CardTitle>
          <CardDescription>生命周期命令 + IPC 连接态监控</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-2 gap-3 xl:grid-cols-3">
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">连接状态</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{statusLabel}</p>
            </div>
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">Desired State</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{runtimeSnapshot?.desired_state ?? "--"}</p>
            </div>
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">Exit Kind</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{runtimeSnapshot?.exit_kind ?? "--"}</p>
            </div>
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">PID</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{runtimeSnapshot?.pid ?? "--"}</p>
            </div>
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">启动时间</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{formatTime(runtimeSnapshot?.started_at_ms ?? null)}</p>
            </div>
            <div className="rounded-xl border border-[#e1e7f2] bg-[#f9fbff] px-3 py-2">
              <p className="text-xs text-[#71809b]">最近更新</p>
              <p className="mt-1 text-sm font-semibold text-[#1f2a44]">{formatTime(runtimeSnapshot?.updated_at_ms ?? null)}</p>
            </div>
          </div>

          <div className="flex flex-wrap gap-2">
            {actionButtons.map((action) => (
              <Button
                key={action.command}
                disabled={Boolean(busyCommand)}
                variant={action.variant}
                onClick={() => void runCommand(action.command)}
              >
                {action.label}
              </Button>
            ))}
          </div>

          <div className="rounded-xl border border-dashed border-[#d8e0ee] bg-[#f8fbff] px-3 py-2 text-xs text-[#67748f]">
            <p>IPC：{hostConfig?.ipc_transport ?? "--"} / {hostConfig?.ipc_endpoint ?? "--"}</p>
            <p className="mt-1">Runtime：{hostConfig?.runtime_program ?? "--"}</p>
          </div>
        </CardContent>
      </Card>
    );
  }, [busyCommand, hostConfig, runCommand, runtimeSnapshot, statusLabel]);

  /** 渲染配置面板：仅编辑真实可生效字段。 */
  const renderConfigPanel = useCallback((): JSX.Element => {
    return (
      <Card>
        <CardHeader>
          <CardTitle>配置管理</CardTitle>
          <CardDescription>直连 Go runtime 的真实配置，保存后重启 Agent 生效</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <label className="block space-y-1">
            <span className="text-sm text-[#5f6d87]">Agent 程序路径</span>
            <Input
              value={configDraft.runtimeProgram}
              onChange={(event) =>
                setConfigDraft((prev) => ({
                  ...prev,
                  runtimeProgram: event.target.value,
                }))
              }
              placeholder="例如：C:\\path\\to\\agent-core.exe"
            />
          </label>

          <label className="block space-y-1">
            <span className="text-sm text-[#5f6d87]">启动参数（空格分隔）</span>
            <textarea
              className="min-h-[88px] w-full rounded-xl border border-[#d8dfeb] bg-white px-3 py-2 text-sm text-[#1f2a44] outline-none transition-colors placeholder:text-[#9aa6bc] focus:border-[#9fb4e4] focus:ring-2 focus:ring-[#dfe9ff]"
              value={configDraft.runtimeArgsText}
              onChange={(event) =>
                setConfigDraft((prev) => ({
                  ...prev,
                  runtimeArgsText: event.target.value,
                }))
              }
              placeholder="例如：--config C:\\path\\agent.yaml"
            />
          </label>

          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">agent_id</span>
              <Input
                value={configDraft.agentId}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    agentId: event.target.value,
                  }))
                }
                placeholder="例如：agent-local"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">bridge_addr</span>
              <Input
                value={configDraft.bridgeAddr}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    bridgeAddr: event.target.value,
                  }))
                }
                placeholder="例如：127.0.0.1:39080"
              />
            </label>
          </div>

          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_min_idle</span>
              <Input
                value={configDraft.tunnelPoolMinIdleText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolMinIdleText: event.target.value,
                  }))
                }
                placeholder="默认 8"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_max_idle</span>
              <Input
                value={configDraft.tunnelPoolMaxIdleText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolMaxIdleText: event.target.value,
                  }))
                }
                placeholder="默认 32"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_max_inflight</span>
              <Input
                value={configDraft.tunnelPoolMaxInflightText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolMaxInflightText: event.target.value,
                  }))
                }
                placeholder="默认 4"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_ttl_ms</span>
              <Input
                value={configDraft.tunnelPoolTtlMsText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolTtlMsText: event.target.value,
                  }))
                }
                placeholder="默认 90000"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_open_rate</span>
              <Input
                value={configDraft.tunnelPoolOpenRateText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolOpenRateText: event.target.value,
                  }))
                }
                placeholder="默认 10"
              />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_open_burst</span>
              <Input
                value={configDraft.tunnelPoolOpenBurstText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolOpenBurstText: event.target.value,
                  }))
                }
                placeholder="默认 20"
              />
            </label>

            <label className="block space-y-1 lg:col-span-2">
              <span className="text-sm text-[#5f6d87]">tunnel_pool_reconcile_gap_ms</span>
              <Input
                value={configDraft.tunnelPoolReconcileGapMsText}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    tunnelPoolReconcileGapMsText: event.target.value,
                  }))
                }
                placeholder="默认 1000"
              />
            </label>
          </div>

          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">IPC 传输方式（平台绑定）</span>
              <Input value={hostConfig?.ipc_transport ?? "--"} readOnly />
            </label>

            <label className="block space-y-1">
              <span className="text-sm text-[#5f6d87]">IPC 端点</span>
              <Input
                value={configDraft.ipcEndpoint}
                onChange={(event) =>
                  setConfigDraft((prev) => ({
                    ...prev,
                    ipcEndpoint: event.target.value,
                  }))
                }
                placeholder="UDS 路径或 \\.\\pipe\\agent-ui-xxx"
              />
            </label>
          </div>

          <div className="rounded-xl border border-dashed border-[#d8e0ee] bg-[#f8fbff] px-3 py-2 text-xs text-[#67748f]">
            <p>当前生效：{hostConfig?.runtime_program ?? "--"}</p>
            <p className="mt-1">agent_id / bridge_addr：{hostConfig?.agent_id ?? "--"} / {hostConfig?.bridge_addr ?? "--"}</p>
            <p className="mt-1">
              tunnel_pool：min={hostConfig?.tunnel_pool_min_idle ?? "--"} max={hostConfig?.tunnel_pool_max_idle ?? "--"} inflight={hostConfig?.tunnel_pool_max_inflight ?? "--"}
            </p>
            <p className="mt-1">当前 IPC：{hostConfig?.ipc_transport ?? "--"} / {hostConfig?.ipc_endpoint ?? "--"}</p>
            <p className="mt-1">说明：配置将通过宿主环境变量直连注入真实 Go runtime，不是仅宿主本地参数。</p>
          </div>

          <div>
            <Button disabled={!configDirty || configSaving} onClick={() => void saveHostRuntimeConfig()}>
              {configSaving ? "保存中..." : "保存配置"}
            </Button>
          </div>
        </CardContent>
      </Card>
    );
  }, [configDirty, configDraft, configSaving, hostConfig, saveHostRuntimeConfig]);

  /** 渲染服务列表面板。 */
  const renderServicePanel = useCallback((): JSX.Element => {
    return (
      <Card>
        <CardHeader>
          <CardTitle>服务列表</CardTitle>
          <CardDescription>来源于 `service_list_snapshot`，不暴露数据面原始读写</CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="min-w-full border-separate border-spacing-0">
              <thead>
                <tr className="bg-[#f7f9fd]">
                  <th className={TABLE_HEADER_CLASS}>服务名</th>
                  <th className={TABLE_HEADER_CLASS}>Service ID</th>
                  <th className={TABLE_HEADER_CLASS}>协议</th>
                  <th className={TABLE_HEADER_CLASS}>状态</th>
                  <th className={TABLE_HEADER_CLASS}>端点数</th>
                  <th className={TABLE_HEADER_CLASS}>更新时间</th>
                </tr>
              </thead>
              <tbody>
                {filteredServiceItems.length === 0 ? (
                  <tr>
                    <td className="px-4 py-8 text-center text-sm text-[#8290a9]" colSpan={6}>
                      暂无服务数据
                    </td>
                  </tr>
                ) : null}
                {filteredServiceItems.map((item) => (
                  <tr key={item.service_id} className="border-t border-[#edf1f7]">
                    <td className={TABLE_CELL_CLASS}>{item.service_name}</td>
                    <td className={TABLE_CELL_CLASS}>{item.service_id}</td>
                    <td className={TABLE_CELL_CLASS}>{item.protocol.toUpperCase()}</td>
                    <td className={TABLE_CELL_CLASS}>
                      <Badge variant={serviceBadgeVariant(item.status)}>{item.status}</Badge>
                    </td>
                    <td className={TABLE_CELL_CLASS}>{item.endpoint_count}</td>
                    <td className={TABLE_CELL_CLASS}>{formatTime(item.updated_at_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
    );
  }, [filteredServiceItems]);

  /** 渲染通道列表面板。 */
  const renderTunnelPanel = useCallback((): JSX.Element => {
    return (
      <Card>
        <CardHeader>
          <CardTitle>通道列表</CardTitle>
          <CardDescription>来源于 `tunnel_list_snapshot`，聚合展示状态、路由与延迟</CardDescription>
        </CardHeader>
        <CardContent className="p-0">
          <div className="overflow-x-auto">
            <table className="min-w-full border-separate border-spacing-0">
              <thead>
                <tr className="bg-[#f7f9fd]">
                  <th className={TABLE_HEADER_CLASS}>Tunnel ID</th>
                  <th className={TABLE_HEADER_CLASS}>Service ID</th>
                  <th className={TABLE_HEADER_CLASS}>本地地址</th>
                  <th className={TABLE_HEADER_CLASS}>远端地址</th>
                  <th className={TABLE_HEADER_CLASS}>状态</th>
                  <th className={TABLE_HEADER_CLASS}>延迟</th>
                  <th className={TABLE_HEADER_CLASS}>更新时间</th>
                </tr>
              </thead>
              <tbody>
                {filteredTunnelItems.length === 0 ? (
                  <tr>
                    <td className="px-4 py-8 text-center text-sm text-[#8290a9]" colSpan={7}>
                      暂无通道数据
                    </td>
                  </tr>
                ) : null}
                {filteredTunnelItems.map((item) => (
                  <tr key={item.tunnel_id} className="border-t border-[#edf1f7]">
                    <td className={TABLE_CELL_CLASS}>{item.tunnel_id}</td>
                    <td className={TABLE_CELL_CLASS}>{item.service_id}</td>
                    <td className={TABLE_CELL_CLASS}>{item.local_addr}</td>
                    <td className={TABLE_CELL_CLASS}>{item.remote_addr}</td>
                    <td className={TABLE_CELL_CLASS}>
                      <Badge variant={tunnelBadgeVariant(item.state)}>{item.state}</Badge>
                    </td>
                    <td className={TABLE_CELL_CLASS}>{item.latency_ms} ms</td>
                    <td className={TABLE_CELL_CLASS}>{formatTime(item.updated_at_ms)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </CardContent>
      </Card>
    );
  }, [filteredTunnelItems]);

  /** 渲染诊断面板，展示事件桥与宿主日志。 */
  const renderDiagnosticsPanel = useCallback((): JSX.Element => {
    return (
      <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
        <Card>
          <CardHeader>
            <CardTitle>事件桥</CardTitle>
            <CardDescription>来源于 `agent-runtime-changed` 事件</CardDescription>
          </CardHeader>
          <CardContent>
            <ul className="agent-scroll max-h-[430px] space-y-2 overflow-y-auto pr-1">
              {filteredEventFeed.length === 0 ? (
                <li className="rounded-xl border border-dashed border-[#dbe2ef] bg-[#fafcff] px-3 py-3 text-sm text-[#8190a9]">
                  暂无事件，等待状态变化...
                </li>
              ) : null}
              {filteredEventFeed.map((item, index) => (
                <li key={`${item.reason}-${index}`} className="rounded-xl border border-[#e6ebf4] bg-[#fbfcff] px-3 py-2">
                  <p className="text-sm font-medium text-[#2a3851]">{item.reason}</p>
                  <p className="mt-1 text-xs text-[#72819b]">
                    dropped={item.dropped_event_count} | {formatTime(item.snapshot.updated_at_ms)}
                  </p>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>

        <Card>
          <CardHeader>
            <CardTitle>宿主日志</CardTitle>
            <CardDescription>来源于 `host_logs_snapshot` 结构化日志</CardDescription>
          </CardHeader>
          <CardContent>
            <ul className="agent-scroll max-h-[430px] space-y-2 overflow-y-auto pr-1">
              {filteredHostLogs.length === 0 ? (
                <li className="rounded-xl border border-dashed border-[#dbe2ef] bg-[#fafcff] px-3 py-3 text-sm text-[#8190a9]">
                  暂无日志
                </li>
              ) : null}
              {filteredHostLogs.map((log, index) => (
                <li key={`${log.ts_ms}-${index}`} className="rounded-xl border border-[#e6ebf4] bg-[#fbfcff] px-3 py-2">
                  <p className="text-sm font-medium text-[#2a3851]">
                    [{log.level}] {log.module}.{log.code}
                  </p>
                  <p className="mt-1 text-xs text-[#72819b]">{log.message}</p>
                  <p className="mt-1 text-xs text-[#8a97ad]">{formatTime(log.ts_ms)}</p>
                </li>
              ))}
            </ul>
          </CardContent>
        </Card>
      </div>
    );
  }, [filteredEventFeed, filteredHostLogs]);

  /** 渲染总览：对齐参考图的统计卡 + 表格 + 诊断分区布局。 */
  const renderDashboard = useCallback((): JSX.Element => {
    const connectionState = runtimeSnapshot?.connection_state ?? "disconnected";
    const rpcLatencyMs = runtimeSnapshot?.metrics.agent_host_rpc_latency_ms ?? 0;

    return (
      <div className="space-y-4">
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2 2xl:grid-cols-5">
          <SummaryMetricCard
            title="Client Status"
            value={runtimeSnapshot?.process_alive ? "Running" : "Stopped"}
            subtitle={`Desired: ${runtimeSnapshot?.desired_state ?? "--"}`}
            icon={Cable}
            tone={runtimeSnapshot?.process_alive ? "success" : "danger"}
          />
          <SummaryMetricCard
            title="Active Tunnels"
            value={`${activeTunnelCount} / ${tunnelItems.length}`}
            subtitle="活跃通道 / 总通道"
            icon={Waypoints}
            tone={activeTunnelCount > 0 ? "success" : "warning"}
          />
          <SummaryMetricCard
            title="Service Healthy"
            value={`${healthyServiceCount} / ${serviceItems.length}`}
            subtitle="健康服务 / 总服务"
            icon={ShieldCheck}
            tone={healthyServiceCount === serviceItems.length && serviceItems.length > 0 ? "success" : "warning"}
          />
          <SummaryMetricCard
            title="RPC Latency"
            value={`${rpcLatencyMs.toFixed(1)} ms`}
            subtitle={`Reconnect: ${runtimeSnapshot?.metrics.agent_host_ipc_reconnect_total ?? 0}`}
            icon={Zap}
            tone={connectionState === "connected" ? "neutral" : "warning"}
          />
          <SummaryMetricCard
            title="Recent Errors"
            value={`${errorsIn24h}`}
            subtitle="最近 24h 结构化日志 ERROR"
            icon={AlertTriangle}
            tone={errorsIn24h > 0 ? "danger" : "success"}
          />
        </div>

        <Card>
          <CardHeader>
            <CardTitle>通道概览</CardTitle>
            <CardDescription>来源于 `tunnel_list_snapshot`，仅展示真实运行态字段</CardDescription>
          </CardHeader>
          <CardContent className="p-0">
            <div className="overflow-x-auto">
              <table className="min-w-full border-separate border-spacing-0">
                <thead>
                  <tr className="bg-[#f7f9fd]">
                    <th className={TABLE_HEADER_CLASS}>Name</th>
                    <th className={TABLE_HEADER_CLASS}>Service</th>
                    <th className={TABLE_HEADER_CLASS}>Local Target</th>
                    <th className={TABLE_HEADER_CLASS}>Remote Address</th>
                    <th className={TABLE_HEADER_CLASS}>Status</th>
                    <th className={TABLE_HEADER_CLASS}>Latency</th>
                  </tr>
                </thead>
                <tbody>
                  {filteredTunnelItems.length === 0 ? (
                    <tr>
                      <td className="px-4 py-8 text-center text-sm text-[#8290a9]" colSpan={6}>
                        暂无通道数据
                      </td>
                    </tr>
                  ) : null}
                  {filteredTunnelItems.map((item) => (
                    <tr key={item.tunnel_id} className="border-t border-[#edf1f7]">
                      <td className={TABLE_CELL_CLASS}>{item.tunnel_id}</td>
                      <td className={TABLE_CELL_CLASS}>{item.service_id}</td>
                      <td className={TABLE_CELL_CLASS}>{item.local_addr}</td>
                      <td className={TABLE_CELL_CLASS}>{item.remote_addr}</td>
                      <td className={TABLE_CELL_CLASS}>
                        <Badge variant={tunnelBadgeVariant(item.state)}>{item.state}</Badge>
                      </td>
                      <td className={TABLE_CELL_CLASS}>{item.latency_ms} ms</td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          </CardContent>
        </Card>

        <div className="grid grid-cols-1 gap-4 xl:grid-cols-2">
          <Card>
            <CardHeader>
              <CardTitle>最近日志</CardTitle>
              <CardDescription>结构化日志（按时间倒序）</CardDescription>
            </CardHeader>
            <CardContent>
              <ul className="agent-scroll max-h-[250px] space-y-2 overflow-y-auto pr-1">
                {filteredHostLogs.slice(0, 12).map((log, index) => (
                  <li key={`${log.ts_ms}-${index}`} className="rounded-xl border border-[#e6ebf4] bg-[#fbfcff] px-3 py-2">
                    <p className="text-sm font-medium text-[#2a3851]">{formatTime(log.ts_ms)} · {log.module}.{log.code}</p>
                    <p className="mt-1 text-xs text-[#72819b]">{log.message}</p>
                  </li>
                ))}
                {filteredHostLogs.length === 0 ? (
                  <li className="rounded-xl border border-dashed border-[#dbe2ef] bg-[#fafcff] px-3 py-3 text-sm text-[#8190a9]">
                    暂无日志
                  </li>
                ) : null}
              </ul>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>健康检查</CardTitle>
              <CardDescription>连接、延迟、服务、通道与错误综合评估</CardDescription>
            </CardHeader>
            <CardContent>
              <ul>
                {healthChecks.map((item) => (
                  <HealthCheckRow key={item.label} label={item.label} detail={item.detail} level={item.level} />
                ))}
              </ul>
            </CardContent>
          </Card>
        </div>
      </div>
    );
  }, [activeTunnelCount, errorsIn24h, filteredHostLogs, filteredTunnelItems, healthChecks, healthyServiceCount, runtimeSnapshot, serviceItems.length, tunnelItems.length]);

  /** 根据导航选择渲染对应主内容。 */
  const renderMainContent = useCallback((): JSX.Element => {
    if (activeNav === "runtime") {
      return renderRuntimePanel();
    }
    if (activeNav === "config") {
      return renderConfigPanel();
    }
    if (activeNav === "services") {
      return renderServicePanel();
    }
    if (activeNav === "tunnels") {
      return renderTunnelPanel();
    }
    if (activeNav === "diagnostics") {
      return renderDiagnosticsPanel();
    }
    return renderDashboard();
  }, [activeNav, renderConfigPanel, renderDashboard, renderDiagnosticsPanel, renderRuntimePanel, renderServicePanel, renderTunnelPanel]);

  const connectionState = runtimeSnapshot?.connection_state ?? "disconnected";

  return (
    <div className="flex h-screen w-screen flex-col overflow-hidden bg-[#edf1f6] text-[#1f2a44]">
      <header className="border-b border-[#d9dfeb] bg-[#f3f6fa] px-6 py-3">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            {/* 按反馈移除副标题，并压缩标题高度。 */}
            <h1 className="text-[34px] font-semibold leading-none tracking-[-0.02em] text-[#1d2740]">Agent Console</h1>
          </div>

          <div className="flex items-center gap-2">
            <div className="relative min-w-[250px]">
              <Search size={16} className="pointer-events-none absolute left-3 top-1/2 -translate-y-1/2 text-[#8b96ad]" />
              <Input
                className="h-10 rounded-xl bg-white pl-9"
                placeholder="搜索服务/通道/日志/事件"
                value={searchText}
                onChange={(event) => setSearchText(event.target.value)}
              />
            </div>
            <Badge variant={statusBadgeVariant(connectionState)}>{statusLabel}</Badge>
            <Badge variant="outline">Uptime {uptimeLabel}</Badge>
            <Badge variant="outline">PID {runtimeSnapshot?.pid ?? "--"}</Badge>
          </div>
        </div>
      </header>

      <div className="grid min-h-0 flex-1 grid-cols-[290px_minmax(0,1fr)]">
        <aside className="flex min-h-0 flex-col border-r border-[#d9dfeb] bg-[#f3f6fa] p-4">
          <p className="mb-3 px-1 text-xs font-semibold tracking-[0.2em] text-[#8090aa]">设置导航</p>
          <nav className="space-y-2">
            {NAV_ITEMS.map((item) => (
              <SidebarNavItem key={item.key} item={item} active={item.key === activeNav} onClick={() => setActiveNav(item.key)} />
            ))}
          </nav>

          <Card className="mt-auto">
            <CardContent className="space-y-1 p-4">
              <p className="flex items-center gap-2 text-sm text-[#55627b]">
                <span className="h-2.5 w-2.5 rounded-full bg-[#18a164]" />
                {statusLabel}
              </p>
              <p className="text-xs text-[#7a879f]">Transport: {hostConfig?.ipc_transport ?? "--"}</p>
              <p className="text-xs text-[#7a879f]">Endpoint: {hostConfig?.ipc_endpoint ?? "--"}</p>
            </CardContent>
          </Card>
        </aside>

        <main className="agent-scroll min-h-0 overflow-y-auto px-5 py-4">
          {errorText ? (
            <div className="mb-4 rounded-xl border border-[#efc6c6] bg-[#fff4f4] px-3 py-2 text-sm text-[#ad4040]">{errorText}</div>
          ) : null}

          {renderMainContent()}
        </main>
      </div>

      <footer className="border-t border-[#d9dfeb] bg-[#f3f6fa] px-5 py-2 text-xs text-[#64728d]">
        <div className="flex flex-wrap items-center gap-x-5 gap-y-1">
          <span className="inline-flex items-center gap-1.5">
            <span className={cn("h-2 w-2 rounded-full", connectionState === "connected" ? "bg-[#18a164]" : "bg-[#c84b4b]")} />
            Last Update {formatTime(runtimeSnapshot?.updated_at_ms ?? null)}
          </span>
          <span>Reconnect {runtimeSnapshot?.metrics.agent_host_ipc_reconnect_total ?? 0}</span>
          <span>Service {serviceItems.length} / Tunnel {tunnelItems.length}</span>
          <span>Logs {warningsIn24h} warning / {errorsIn24h} error</span>
        </div>
      </footer>
    </div>
  );
}
