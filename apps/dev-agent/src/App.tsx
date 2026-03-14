import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  Activity,
  Bell,
  Cable,
  ChartNoAxesCombined,
  Cloud,
  Cpu,
  Gauge,
  HardDrive,
  Layers,
  Link2,
  Logs,
  Network,
  RefreshCcw,
  Settings,
  ShieldCheck,
  SquareMousePointer,
  Upload,
  Download,
  Wrench,
  type LucideIcon,
} from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { NetworkRateValue } from "@/components/traffic/network_rate_value";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Toaster } from "@/components/ui/sonner";
import { useSystemResources } from "@/features/system/use_system_resources";
import { bytesPerSecToMiB, formatBytesToGiB } from "@/features/traffic/format";
import { useTrafficStats } from "@/features/traffic/use_traffic_stats";
import { cn } from "@/lib/utils";
import { registerManagedListener } from "@/runtime_subscription";

type DesiredState = "running" | "stopped";
type ExitKind = "expected" | "unexpected";
type ConnectionState = "disconnected" | "reconnecting" | "resyncing" | "connected";
type AgentRuntimeCommand = "agent_start" | "agent_stop" | "agent_restart" | "agent_crash_inject" | "app_shutdown";
type BridgeSessionCommand = "session_reconnect" | "session_drain";
type RuntimeCommand = AgentRuntimeCommand | BridgeSessionCommand;
type NavKey = "overview" | "services" | "tunnels" | "traffic" | "connections" | "diagnose" | "settings";

interface HostMetricsSnapshot {
  agent_host_ipc_connected: boolean;
  agent_host_ipc_reconnect_total: number;
  agent_host_rpc_latency_ms: number;
  agent_host_supervisor_restart_total: number;
  agent_bridge_last_heartbeat_at_ms: number | null;
  agent_bridge_next_retry_at_ms: number | null;
  agent_bridge_retry_backoff_ms: number;
  agent_bridge_retry_fail_streak: number;
  agent_bridge_last_reconnect_error: string | null;
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
  bridge_transport: string;
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

interface SessionSnapshot {
  state: string;
  session_id: string | null;
  session_epoch: number | null;
  last_heartbeat_at_ms: number | null;
  last_heartbeat_sent_at_ms: number | null;
  last_heartbeat_at_text: string | null;
  reconnect_total: number | null;
  retry_fail_streak: number | null;
  retry_backoff_ms: number | null;
  next_retry_at_ms: number | null;
  last_error: string | null;
  updated_at_ms: number;
  source: string;
  unavailable_reason: string | null;
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

interface HostConfigUpdateInput {
  runtime_program: string;
  runtime_args: string[];
  agent_id: string;
  bridge_addr: string;
  bridge_transport: string;
  tunnel_pool_min_idle: number;
  tunnel_pool_max_idle: number;
  tunnel_pool_max_inflight: number;
  tunnel_pool_ttl_ms: number;
  tunnel_pool_open_rate: number;
  tunnel_pool_open_burst: number;
  tunnel_pool_reconcile_gap_ms: number;
  ipc_endpoint: string;
}

interface SettingsDraft {
  runtimeProgram: string;
  runtimeArgsText: string;
  agentId: string;
  bridgeAddr: string;
  transport: string;
  authMode: string;
  endpoint: string;
  tunnelPoolMinIdleText: string;
  tunnelPoolMaxIdleText: string;
  tunnelPoolMaxInflightText: string;
  tunnelPoolTtlMsText: string;
  tunnelPoolOpenRateText: string;
  tunnelPoolOpenBurstText: string;
  tunnelPoolReconcileGapMsText: string;
}

interface NavItem {
  key: NavKey;
  title: string;
  icon: LucideIcon;
}

const NAV_ITEMS: NavItem[] = [
  { key: "overview", title: "总览", icon: Activity },
  { key: "services", title: "服务", icon: Layers },
  { key: "tunnels", title: "隧道", icon: Network },
  { key: "traffic", title: "流量", icon: ChartNoAxesCombined },
  { key: "connections", title: "连接", icon: Link2 },
  { key: "diagnose", title: "日志与诊断", icon: Logs },
  { key: "settings", title: "设置", icon: Settings },
];

const TABLE_HEAD_CLASS = "px-4 py-2.5 text-left text-[11px] font-semibold uppercase tracking-[0.08em] text-[#66748f]";
const TABLE_CELL_CLASS = "px-4 py-3 text-sm text-[#293145]";

function normalizeErrorMessage(error: unknown): string {
  if (error instanceof Error) {
    return error.message;
  }
  return String(error);
}

function authModeFromTransport(_transport: string): string {
  return "hmac_auth_v1";
}

function toSettingsDraft(snapshot: HostConfigSnapshot): SettingsDraft {
  return {
    runtimeProgram: snapshot.runtime_program,
    runtimeArgsText: snapshot.runtime_args.join(" "),
    agentId: snapshot.agent_id,
    bridgeAddr: snapshot.bridge_addr,
    transport: snapshot.bridge_transport,
    authMode: authModeFromTransport(snapshot.ipc_transport),
    endpoint: snapshot.ipc_endpoint,
    tunnelPoolMinIdleText: String(snapshot.tunnel_pool_min_idle),
    tunnelPoolMaxIdleText: String(snapshot.tunnel_pool_max_idle),
    tunnelPoolMaxInflightText: String(snapshot.tunnel_pool_max_inflight),
    tunnelPoolTtlMsText: String(snapshot.tunnel_pool_ttl_ms),
    tunnelPoolOpenRateText: String(snapshot.tunnel_pool_open_rate),
    tunnelPoolOpenBurstText: String(snapshot.tunnel_pool_open_burst),
    tunnelPoolReconcileGapMsText: String(snapshot.tunnel_pool_reconcile_gap_ms),
  };
}

function normalizeRuntimeArgsText(text: string): string[] {
  return text
    .split(/\s+/)
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
}

function parsePositiveInteger(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (!/^\d+$/.test(normalized)) {
    throw new Error(`${fieldLabel} 必须是正整数`);
  }
  const parsedValue = Number.parseInt(normalized, 10);
  if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
    throw new Error(`${fieldLabel} 必须大于 0`);
  }
  return parsedValue;
}

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

function SettingsField(props: {
  label: string;
  hint?: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <label className="block space-y-1.5">
      <div className="flex items-end justify-between gap-3">
        <span className="text-sm font-medium text-[#44516d]">{props.label}</span>
        {props.hint ? <span className="text-[11px] text-[#8290a8]">{props.hint}</span> : null}
      </div>
      {props.children}
    </label>
  );
}

function formatTime(tsMs: number | null): string {
  if (!tsMs) {
    return "--";
  }
  return new Date(tsMs).toLocaleTimeString("zh-CN", { hour12: false });
}

function formatDateTime(tsMs: number | null): string {
  if (!tsMs) {
    return "--";
  }
  return new Date(tsMs).toLocaleString("zh-CN", { hour12: false });
}

function formatUptime(startedAtMs: number | null): string {
  if (!startedAtMs) {
    return "--";
  }
  const totalSeconds = Math.max(0, Math.floor((Date.now() - startedAtMs) / 1000));
  const hour = Math.floor(totalSeconds / 3600);
  const minute = Math.floor((totalSeconds % 3600) / 60);
  const second = totalSeconds % 60;
  return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}:${String(second).padStart(2, "0")}`;
}

function formatRelativeMs(tsMs: number | null, nowTsMs: number): string {
  if (!tsMs) {
    return "--";
  }
  const diffMs = Math.max(0, nowTsMs - tsMs);
  const diffSeconds = Math.floor(diffMs / 1000);
  if (diffSeconds < 60) {
    return `${diffSeconds} 秒前`;
  }
  const diffMinutes = Math.floor(diffSeconds / 60);
  if (diffMinutes < 60) {
    return `${diffMinutes} 分钟前`;
  }
  const diffHours = Math.floor(diffMinutes / 60);
  if (diffHours < 24) {
    return `${diffHours} 小时前`;
  }
  const diffDays = Math.floor(diffHours / 24);
  return `${diffDays} 天前`;
}

function formatCountdownText(remainingMs: number): string {
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
    return "0 秒";
  }
  const remainingSeconds = Math.ceil(remainingMs / 1000);
  const minute = Math.floor(remainingSeconds / 60);
  const second = remainingSeconds % 60;
  if (minute <= 0) {
    return `${second} 秒`;
  }
  return `${minute} 分 ${String(second).padStart(2, "0")} 秒`;
}

function serviceVariant(status: string): "success" | "warning" | "danger" | "secondary" {
  const normalized = status.trim().toLowerCase();
  if (normalized.includes("run") || normalized.includes("healthy")) {
    return "success";
  }
  if (normalized.includes("degraded") || normalized.includes("warn")) {
    return "warning";
  }
  if (normalized.includes("idle") || normalized.includes("pending")) {
    return "secondary";
  }
  return "danger";
}

function formatServiceStatus(status: string): string {
  const trimmed = status.trim();
  if (!trimmed) {
    return "未知";
  }
  if (/[\u4e00-\u9fa5]/.test(trimmed)) {
    return trimmed;
  }
  const normalized = trimmed.toLowerCase();
  if (normalized.includes("healthy")) {
    return "健康";
  }
  if (normalized.includes("running") || normalized.includes("run")) {
    return "运行中";
  }
  if (normalized.includes("degraded")) {
    return "性能降级";
  }
  if (normalized.includes("warning") || normalized.includes("warn")) {
    return "告警";
  }
  if (normalized.includes("idle")) {
    return "空闲";
  }
  if (normalized.includes("pending")) {
    return "等待中";
  }
  if (
    normalized.includes("stop")
    || normalized.includes("down")
    || normalized.includes("fail")
    || normalized.includes("error")
  ) {
    return "异常";
  }
  if (normalized.includes("unknown")) {
    return "未知";
  }
  return trimmed;
}

function tunnelVariant(state: string): "success" | "warning" | "danger" | "secondary" {
  const normalized = state.trim().toLowerCase();
  if (normalized.includes("active") || normalized.includes("connected")) {
    return "success";
  }
  if (normalized.includes("idle")) {
    return "secondary";
  }
  if (normalized.includes("reconnect") || normalized.includes("resync")) {
    return "warning";
  }
  return "danger";
}

function formatTunnelState(state: string): string {
  const trimmed = state.trim();
  if (!trimmed) {
    return "未知";
  }
  if (/[\u4e00-\u9fa5]/.test(trimmed)) {
    return trimmed;
  }
  const normalized = trimmed.toLowerCase();
  if (
    normalized.includes("active")
    || normalized.includes("connected")
    || normalized.includes("in_use")
    || normalized.includes("inuse")
  ) {
    return "已连接";
  }
  if (normalized.includes("idle")) {
    return "空闲";
  }
  if (normalized.includes("reconnect")) {
    return "重连中";
  }
  if (normalized.includes("resync")) {
    return "对账中";
  }
  if (normalized.includes("init") || normalized.includes("starting")) {
    return "初始化中";
  }
  if (normalized.includes("pending")) {
    return "等待中";
  }
  if (
    normalized.includes("closed")
    || normalized.includes("stop")
    || normalized.includes("fail")
    || normalized.includes("error")
    || normalized.includes("timeout")
  ) {
    return "异常";
  }
  if (normalized.includes("unknown")) {
    return "未知";
  }
  return trimmed;
}

function clampPercent(value: number): number {
  if (!Number.isFinite(value)) {
    return 0;
  }
  return Math.max(0, Math.min(100, value));
}

function normalizeSessionState(state: string | null | undefined): string {
  return state?.trim().toUpperCase() ?? "";
}

function isBridgeConnectedSessionState(state: string): boolean {
  return (
    state === "ACTIVE"
    || state === "READY"
    || state === "AUTHENTICATED"
    || state === "CONNECTED"
  );
}

function MiniLineChart(props: {
  valuesA: number[];
  valuesB: number[];
  className?: string;
}): JSX.Element {
  const width = 360;
  const height = 120;
  const maxValue = Math.max(1, ...props.valuesA, ...props.valuesB);

  const toPoints = (values: number[]): string =>
    values
      .map((value, index) => {
        const x = (index / Math.max(1, values.length - 1)) * width;
        const y = height - (value / maxValue) * (height - 10) - 5;
        return `${x},${y}`;
      })
      .join(" ");

  return (
    <svg className={cn("h-[150px] w-full", props.className)} viewBox={`0 0 ${width} ${height}`} preserveAspectRatio="none">
      <defs>
        <linearGradient id="upload-area" x1="0" x2="0" y1="0" y2="1">
          <stop offset="0%" stopColor="#2563eb" stopOpacity="0.32" />
          <stop offset="100%" stopColor="#2563eb" stopOpacity="0" />
        </linearGradient>
      </defs>
      <polyline points={toPoints(props.valuesA)} fill="none" stroke="#1d63e8" strokeWidth="2.5" />
      <polyline points={toPoints(props.valuesB)} fill="none" stroke="#27b15d" strokeWidth="2.5" />
      <polyline
        points={`${toPoints(props.valuesA)} ${width},${height} 0,${height}`}
        fill="url(#upload-area)"
        stroke="none"
      />
    </svg>
  );
}

function NavButton(props: {
  item: NavItem;
  active: boolean;
  onClick: () => void;
}): JSX.Element {
  const Icon = props.item.icon;
  return (
    <button
      onClick={props.onClick}
      className={cn(
        "group flex w-full items-center gap-3 rounded-xl border px-3 py-2.5 text-left transition",
        props.active
          ? "border-[#5f8ce0]/60 bg-gradient-to-r from-[#2f5ca7] to-[#29569e] text-white shadow-[0_8px_18px_rgba(27,76,154,0.36)]"
          : "border-transparent text-[#d7deee] hover:border-white/20 hover:bg-white/10 hover:text-white",
      )}
    >
      <span
        className={cn(
          "inline-flex h-8 w-8 items-center justify-center rounded-lg border",
          props.active ? "border-white/30 bg-white/15" : "border-white/20 bg-[#1f2d47]",
        )}
      >
        <Icon size={17} />
      </span>
      <span className="text-[15px] font-semibold leading-tight">{props.item.title}</span>
    </button>
  );
}

function InfoRow(props: { label: string; value: string; valueClassName?: string }): JSX.Element {
  return (
    <div className="flex items-center justify-between gap-3 border-b border-[#ebeff6] py-2.5 last:border-b-0">
      <span className="text-sm text-[#4f5b74]">{props.label}</span>
      <span className={cn("text-base font-semibold text-[#1f293d]", props.valueClassName)}>{props.value}</span>
    </div>
  );
}

interface ConnectionBadgeSummary {
  label: string;
  variant: "success" | "warning" | "danger" | "secondary";
}

export default function App(): JSX.Element {
  const [activeNav, setActiveNav] = useState<NavKey>("overview");
  const [runtimeSnapshot, setRuntimeSnapshot] = useState<AgentRuntimeSnapshot | null>(null);
  const [sessionSnapshot, setSessionSnapshot] = useState<SessionSnapshot | null>(null);
  const [hostConfig, setHostConfig] = useState<HostConfigSnapshot | null>(null);
  const [hostLogs, setHostLogs] = useState<HostLogEntry[]>([]);
  const [serviceItems, setServiceItems] = useState<ServiceListItem[]>([]);
  const [tunnelItems, setTunnelItems] = useState<TunnelListItem[]>([]);
  const [busyCommand, setBusyCommand] = useState<RuntimeCommand | null>(null);
  const [savingSettings, setSavingSettings] = useState(false);
  const [nowTsMs, setNowTsMs] = useState(() => Date.now());
  const [settingsDraft, setSettingsDraft] = useState<SettingsDraft | null>(null);
  const shownHostLogToastKeysRef = useRef<Set<string>>(new Set());
  const shownRuntimeErrorRef = useRef<string | null>(null);
  const { trafficSnapshot, trafficHistory, refreshTrafficStats } = useTrafficStats();
  const { systemResourceSnapshot, refreshSystemResourceStats } = useSystemResources();

  const notify = useCallback(
    (type: "success" | "warning" | "error", title: string, description?: string) => {
      if (type === "success") {
        toast.success(title, { description });
        return;
      }
      if (type === "warning") {
        toast.warning(title, { description });
        return;
      }
      toast.error(title, { description });
    },
    [],
  );

  const refreshTrafficStatsSafely = useCallback(async () => {
    try {
      await refreshTrafficStats();
    } catch (error) {
      notify("warning", "流量采样失败", normalizeErrorMessage(error));
    }
  }, [notify, refreshTrafficStats]);

  const refreshSystemResourceStatsSafely = useCallback(async () => {
    try {
      await refreshSystemResourceStats();
    } catch (error) {
      notify("warning", "系统资源采样失败", normalizeErrorMessage(error));
    }
  }, [notify, refreshSystemResourceStats]);

  const refreshHostLogs = useCallback(async () => {
    const logs = await invoke<HostLogEntry[]>("host_logs_snapshot");
    setHostLogs(logs.slice(-18).reverse());
  }, []);

  const refreshSnapshot = useCallback(async () => {
    const snapshot = await invoke<AgentRuntimeSnapshot>("agent_snapshot");
    setRuntimeSnapshot(snapshot);
  }, []);

  const refreshHostConfig = useCallback(async () => {
    const config = await invoke<HostConfigSnapshot>("host_config_snapshot");
    setHostConfig(config);
  }, []);

  const refreshSessionSnapshot = useCallback(async () => {
    const snapshot = await invoke<SessionSnapshot>("session_snapshot");
    setSessionSnapshot(snapshot);
  }, []);

  const refreshServiceList = useCallback(async () => {
    const items = await invoke<ServiceListItem[]>("service_list_snapshot");
    setServiceItems(items);
  }, []);

  const refreshTunnelList = useCallback(async () => {
    const items = await invoke<TunnelListItem[]>("tunnel_list_snapshot");
    setTunnelItems(items);
  }, []);

  const bootstrap = useCallback(async () => {
    const payload = await invoke<AppBootstrapPayload>("app_bootstrap");
    setRuntimeSnapshot(payload.snapshot);
    setHostConfig(payload.host_config);
    await Promise.all([
      refreshHostLogs(),
      refreshSessionSnapshot(),
      refreshServiceList(),
      refreshTunnelList(),
      refreshTrafficStatsSafely(),
      refreshSystemResourceStatsSafely(),
    ]);
  }, [refreshHostLogs, refreshServiceList, refreshSessionSnapshot, refreshSystemResourceStatsSafely, refreshTrafficStatsSafely, refreshTunnelList]);

  const runCommand = useCallback(
    async (command: RuntimeCommand) => {
      setBusyCommand(command);
      try {
        if (command === "session_reconnect" || command === "session_drain") {
          const session = await invoke<SessionSnapshot>(command);
          setSessionSnapshot(session);
          const nextState = normalizeSessionState(session.state);
          const stateLabel = session.state?.trim() || "--";
          const unavailableDetail = session.unavailable_reason?.trim();
          if (command === "session_reconnect") {
            if (isBridgeConnectedSessionState(nextState)) {
              notify("success", "Bridge 会话已连接", `当前状态: ${stateLabel}`);
            } else {
              notify(
                "warning",
                "重连请求已发送，等待 Bridge 会话恢复",
                unavailableDetail ? `当前状态: ${stateLabel}，原因: ${unavailableDetail}` : `当前状态: ${stateLabel}`,
              );
            }
          } else if (nextState === "CLOSED" || nextState === "DRAINING" || nextState === "DISCONNECTED") {
            notify("success", "Bridge 会话已断开", `当前状态: ${stateLabel}`);
          } else {
            notify("warning", "断开请求已发送", `当前状态: ${stateLabel}`);
          }
        } else {
          const snapshot = await invoke<AgentRuntimeSnapshot>(command);
          setRuntimeSnapshot(snapshot);
          const commandLabelMap: Record<AgentRuntimeCommand, string> = {
            agent_start: "启动内核",
            agent_stop: "停止内核",
            agent_restart: "重启内核",
            agent_crash_inject: "注入崩溃",
            app_shutdown: "关闭应用",
          };
          notify("success", `${commandLabelMap[command]}成功`);
        }
        await Promise.all([
          refreshSnapshot(),
          refreshHostLogs(),
          refreshSessionSnapshot(),
          refreshServiceList(),
          refreshTunnelList(),
          refreshHostConfig(),
          refreshTrafficStatsSafely(),
          refreshSystemResourceStatsSafely(),
        ]);
      } catch (error) {
        notify("error", "操作执行失败", normalizeErrorMessage(error));
      } finally {
        setBusyCommand(null);
      }
    },
    [
      notify,
      refreshHostConfig,
      refreshHostLogs,
      refreshServiceList,
      refreshSessionSnapshot,
      refreshSnapshot,
      refreshSystemResourceStatsSafely,
      refreshTrafficStatsSafely,
      refreshTunnelList,
    ],
  );

  useEffect(() => {
    let disposed = false;
    let unlisten: UnlistenFn | null = null;

    const pollTimer = window.setInterval(() => {
      void refreshSnapshot();
      void refreshSessionSnapshot();
      void refreshHostLogs();
      void refreshServiceList();
      void refreshTunnelList();
      void refreshTrafficStatsSafely();
      void refreshSystemResourceStatsSafely();
    }, 3000);

    void (async () => {
      try {
        await bootstrap();
      } catch (error) {
        if (!disposed) {
          notify("error", "初始化失败", normalizeErrorMessage(error));
        }
      }
      if (disposed) {
        return;
      }
      try {
        unlisten = await registerManagedListener<AgentRuntimeChangedEvent>(
          listen,
          "agent-runtime-changed",
          (payload) => {
            setRuntimeSnapshot(payload.snapshot);
          },
          () => disposed,
        );
      } catch (error) {
        if (!disposed) {
          notify("error", "事件订阅失败", normalizeErrorMessage(error));
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
  }, [
    bootstrap,
    notify,
    refreshHostLogs,
    refreshServiceList,
    refreshSessionSnapshot,
    refreshSnapshot,
    refreshSystemResourceStatsSafely,
    refreshTrafficStatsSafely,
    refreshTunnelList,
  ]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setNowTsMs(Date.now());
    }, 1000);
    return () => {
      window.clearInterval(timer);
    };
  }, []);

  useEffect(() => {
    if (!hostConfig) {
      return;
    }
    setSettingsDraft(toSettingsDraft(hostConfig));
  }, [hostConfig]);

  useEffect(() => {
    const runtimeError = runtimeSnapshot?.last_error?.trim() ?? "";
    if (!runtimeError) {
      return;
    }
    if (shownRuntimeErrorRef.current === runtimeError) {
      return;
    }
    shownRuntimeErrorRef.current = runtimeError;
    notify("error", "运行异常", runtimeError);
  }, [notify, runtimeSnapshot?.last_error]);

  useEffect(() => {
    hostLogs.forEach((log) => {
      const dedupeKey = `${log.ts_ms}-${log.code}`;
      if (shownHostLogToastKeysRef.current.has(dedupeKey)) {
        return;
      }
      if (log.code === "HOST_CONFIG_YAML_INVALID") {
        shownHostLogToastKeysRef.current.add(dedupeKey);
        notify("warning", "配置文件异常，已回退默认值", log.message);
      }
      if (log.level.toLowerCase().includes("error")) {
        shownHostLogToastKeysRef.current.add(dedupeKey);
        notify("error", `${log.module}.${log.code}`, log.message);
      }
    });
  }, [hostLogs, notify]);

  const filteredServices = serviceItems;
  const filteredTunnels = tunnelItems;

  const connectionState = runtimeSnapshot?.connection_state ?? "disconnected";
  const connected = connectionState === "connected";
  const connectionMetrics = runtimeSnapshot?.metrics;
  const nextRetryAtMs = sessionSnapshot?.next_retry_at_ms ?? connectionMetrics?.agent_bridge_next_retry_at_ms ?? null;
  const retryDelayMs = nextRetryAtMs ? Math.max(0, nextRetryAtMs - nowTsMs) : 0;
  const hasRetryDelay = retryDelayMs > 0;
  const retryBackoffMs = sessionSnapshot?.retry_backoff_ms ?? connectionMetrics?.agent_bridge_retry_backoff_ms ?? 0;
  const retryFailStreak = sessionSnapshot?.retry_fail_streak ?? connectionMetrics?.agent_bridge_retry_fail_streak ?? 0;
  const lastReconnectError = sessionSnapshot?.last_error ?? sessionSnapshot?.unavailable_reason ?? connectionMetrics?.agent_bridge_last_reconnect_error ?? null;
  const sessionStateRaw = sessionSnapshot?.state?.trim() ?? "";
  const sessionStateUpper = sessionStateRaw.toUpperCase();
  const bridgeConnected = useMemo(
    () =>
      sessionStateUpper === "ACTIVE"
      || sessionStateUpper === "READY"
      || sessionStateUpper === "AUTHENTICATED"
      || sessionStateUpper === "CONNECTED",
    [sessionStateUpper],
  );
  const bridgeHeartbeatText = sessionSnapshot?.last_heartbeat_at_ms
    ? formatRelativeMs(sessionSnapshot.last_heartbeat_at_ms, nowTsMs)
    : sessionSnapshot?.last_heartbeat_at_text || "--";
  const bridgeHeartbeatSentText = sessionSnapshot?.last_heartbeat_sent_at_ms
    ? formatRelativeMs(sessionSnapshot.last_heartbeat_sent_at_ms, nowTsMs)
    : "--";
  const retryCountdownText = formatCountdownText(retryDelayMs);
  const bridgeHeartbeatAgeMs = sessionSnapshot?.last_heartbeat_at_ms
    ? Math.max(0, nowTsMs - sessionSnapshot.last_heartbeat_at_ms)
    : null;

  const tunnelStats = useMemo(() => {
    const total = tunnelItems.length;
    const idle = tunnelItems.filter((item) => item.state.toLowerCase().includes("idle")).length;
    const inUse = tunnelItems.filter((item) => {
      const state = item.state.toLowerCase();
      return state.includes("active") || state.includes("connected") || state.includes("in_use");
    }).length;
    const safeInUse = inUse > 0 ? inUse : Math.max(0, total - idle);
    return { total, idle, inUse: safeInUse };
  }, [tunnelItems]);

  const serviceHealthStats = useMemo(() => {
    let success = 0;
    let warning = 0;
    let danger = 0;
    let secondary = 0;
    serviceItems.forEach((item) => {
      const variant = serviceVariant(item.status);
      if (variant === "success") {
        success += 1;
      } else if (variant === "warning") {
        warning += 1;
      } else if (variant === "danger") {
        danger += 1;
      } else {
        secondary += 1;
      }
    });
    return { success, warning, danger, secondary };
  }, [serviceItems]);

  const tunnelHealthStats = useMemo(() => {
    let success = 0;
    let warning = 0;
    let danger = 0;
    let secondary = 0;
    tunnelItems.forEach((item) => {
      const variant = tunnelVariant(item.state);
      if (variant === "success") {
        success += 1;
      } else if (variant === "warning") {
        warning += 1;
      } else if (variant === "danger") {
        danger += 1;
      } else {
        secondary += 1;
      }
    });
    return { success, warning, danger, secondary };
  }, [tunnelItems]);

  const kernelConnectionSummary = useMemo<ConnectionBadgeSummary>(() => {
    if (connectionState === "connected") {
      return { label: "内核 IPC 已连接", variant: "success" };
    }
    if (connectionState === "reconnecting") {
      return { label: "内核 IPC 重连中", variant: "warning" };
    }
    if (connectionState === "resyncing") {
      return { label: "内核 IPC 对账中", variant: "warning" };
    }
    return { label: "内核 IPC 未连接", variant: "danger" };
  }, [connectionState]);

  const serviceConnectionSummary = useMemo<ConnectionBadgeSummary>(() => {
    if (!connected) {
      return { label: "等待内核 IPC 建链", variant: "secondary" };
    }
    if (sessionStateUpper === "" || sessionStateUpper === "UNAVAILABLE") {
      return { label: "Bridge 状态未知", variant: "secondary" };
    }
    if (
      sessionStateUpper === "CONNECTING"
      || sessionStateUpper === "RECONNECTING"
      || sessionStateUpper === "RESYNCING"
      || sessionStateUpper === "AUTHENTICATING"
    ) {
      return { label: "服务连接重试中", variant: "warning" };
    }
    if (
      sessionStateUpper === "STALE"
      || sessionStateUpper === "FAILED"
      || sessionStateUpper === "CLOSED"
      || sessionStateUpper === "DRAINING"
    ) {
      return { label: "服务连接异常", variant: "danger" };
    }
    if (hasRetryDelay || retryFailStreak > 0) {
      return { label: "服务连接重试中", variant: "warning" };
    }
    if (bridgeHeartbeatAgeMs !== null && bridgeHeartbeatAgeMs > 12_000) {
      return { label: "服务连接不稳定", variant: "warning" };
    }
    if (serviceItems.length === 0) {
      return { label: "未注册服务", variant: "secondary" };
    }
    if (serviceHealthStats.danger > 0 || tunnelHealthStats.danger > 0) {
      return { label: "服务部分异常", variant: "warning" };
    }
    return { label: "服务连接正常", variant: "success" };
  }, [
    bridgeHeartbeatAgeMs,
    connected,
    hasRetryDelay,
    retryFailStreak,
    sessionStateUpper,
    serviceHealthStats.danger,
    serviceItems.length,
    tunnelHealthStats.danger,
  ]);
  const overviewStatusTone = useMemo(() => {
    if (kernelConnectionSummary.variant === "success") {
      return { borderClass: "border-[#27b15d]", textClass: "text-[#27b15d]" };
    }
    if (kernelConnectionSummary.variant === "warning") {
      return { borderClass: "border-[#d28b2d]", textClass: "text-[#d28b2d]" };
    }
    if (kernelConnectionSummary.variant === "danger") {
      return { borderClass: "border-[#c94f4f]", textClass: "text-[#c94f4f]" };
    }
    return { borderClass: "border-[#8f9ab2]", textClass: "text-[#8f9ab2]" };
  }, [kernelConnectionSummary.variant]);

  const donutBackground = useMemo(() => {
    const total = Math.max(1, tunnelStats.total);
    const idlePct = (tunnelStats.idle / total) * 100;
    const inUsePct = (tunnelStats.inUse / total) * 100;
    const unknownPct = Math.max(0, 100 - idlePct - inUsePct);
    return `conic-gradient(#17a751 0% ${idlePct}%, #1f67e5 ${idlePct}% ${idlePct + inUsePct}%, #8cb2ea ${idlePct + inUsePct}% ${idlePct + inUsePct + unknownPct}%)`;
  }, [tunnelStats.idle, tunnelStats.inUse, tunnelStats.total]);

  const trafficSeries = useMemo(() => {
    const source = trafficHistory.slice(-12);
    if (source.length === 0) {
      const upload = bytesPerSecToMiB(trafficSnapshot.upload_bytes_per_sec);
      const download = bytesPerSecToMiB(trafficSnapshot.download_bytes_per_sec);
      return {
        upload: [upload],
        download: [download],
      };
    }
    return {
      upload: source.map((item) => bytesPerSecToMiB(item.uploadBytesPerSec)),
      download: source.map((item) => bytesPerSecToMiB(item.downloadBytesPerSec)),
    };
  }, [trafficHistory, trafficSnapshot.download_bytes_per_sec, trafficSnapshot.upload_bytes_per_sec]);

  const trafficSummary = useMemo(
    () => ({
      uploadGb: formatBytesToGiB(trafficSnapshot.upload_total_bytes),
      downloadGb: formatBytesToGiB(trafficSnapshot.download_total_bytes),
      uploadRateBps: trafficSnapshot.upload_bytes_per_sec,
      downloadRateBps: trafficSnapshot.download_bytes_per_sec,
      source: trafficSnapshot.source,
    }),
    [
      trafficSnapshot.download_bytes_per_sec,
      trafficSnapshot.download_total_bytes,
      trafficSnapshot.source,
      trafficSnapshot.upload_bytes_per_sec,
      trafficSnapshot.upload_total_bytes,
    ],
  );

  const systemMetrics = useMemo(() => {
    const cpu = clampPercent(systemResourceSnapshot.cpu_percent);
    const memory = clampPercent(systemResourceSnapshot.memory_percent);
    const disk = clampPercent(systemResourceSnapshot.disk_percent);
    return { cpu, memory, disk };
  }, [
    systemResourceSnapshot.cpu_percent,
    systemResourceSnapshot.disk_percent,
    systemResourceSnapshot.memory_percent,
  ]);

  const recentLogs = useMemo(() => hostLogs.slice(0, 3), [hostLogs]);

  const agentVersion = useMemo(() => {
    const entry = hostLogs.find((log) => log.message.toLowerCase().includes("version"));
    if (!entry) {
      return "v1.2.x";
    }
    const matched = entry.message.match(/v\d+\.\d+\.\d+/i);
    return matched?.[0] ?? "v1.2.x";
  }, [hostLogs]);

  const settingsDirty = useMemo(() => {
    if (!hostConfig || !settingsDraft) {
      return false;
    }
    return (
      settingsDraft.runtimeProgram.trim() !== hostConfig.runtime_program ||
      settingsDraft.runtimeArgsText.trim() !== hostConfig.runtime_args.join(" ") ||
      settingsDraft.agentId.trim() !== hostConfig.agent_id ||
      settingsDraft.bridgeAddr.trim() !== hostConfig.bridge_addr ||
      settingsDraft.transport.trim() !== hostConfig.bridge_transport ||
      settingsDraft.endpoint.trim() !== hostConfig.ipc_endpoint ||
      settingsDraft.tunnelPoolMinIdleText.trim() !== String(hostConfig.tunnel_pool_min_idle) ||
      settingsDraft.tunnelPoolMaxIdleText.trim() !== String(hostConfig.tunnel_pool_max_idle) ||
      settingsDraft.tunnelPoolMaxInflightText.trim() !== String(hostConfig.tunnel_pool_max_inflight) ||
      settingsDraft.tunnelPoolTtlMsText.trim() !== String(hostConfig.tunnel_pool_ttl_ms) ||
      settingsDraft.tunnelPoolOpenRateText.trim() !== String(hostConfig.tunnel_pool_open_rate) ||
      settingsDraft.tunnelPoolOpenBurstText.trim() !== String(hostConfig.tunnel_pool_open_burst) ||
      settingsDraft.tunnelPoolReconcileGapMsText.trim() !== String(hostConfig.tunnel_pool_reconcile_gap_ms)
    );
  }, [hostConfig, settingsDraft]);

  const resetSettingsDraft = useCallback(() => {
    if (!hostConfig) {
      return;
    }
    setSettingsDraft(toSettingsDraft(hostConfig));
  }, [hostConfig]);

  const saveSettings = useCallback(async () => {
    if (!hostConfig || !settingsDraft) {
      return;
    }
    setSavingSettings(true);
    try {
      const payload: HostConfigUpdateInput = {
        runtime_program: settingsDraft.runtimeProgram.trim(),
        runtime_args: normalizeRuntimeArgsText(settingsDraft.runtimeArgsText),
        agent_id: settingsDraft.agentId.trim(),
        bridge_addr: settingsDraft.bridgeAddr.trim(),
        bridge_transport: settingsDraft.transport.trim(),
        tunnel_pool_min_idle: parseNonNegativeInteger(
          settingsDraft.tunnelPoolMinIdleText,
          "tunnel_pool_min_idle",
        ),
        tunnel_pool_max_idle: parsePositiveInteger(settingsDraft.tunnelPoolMaxIdleText, "tunnel_pool_max_idle"),
        tunnel_pool_max_inflight: parsePositiveInteger(
          settingsDraft.tunnelPoolMaxInflightText,
          "tunnel_pool_max_inflight",
        ),
        tunnel_pool_ttl_ms: parseNonNegativeInteger(settingsDraft.tunnelPoolTtlMsText, "tunnel_pool_ttl_ms"),
        tunnel_pool_open_rate: parsePositiveFloat(settingsDraft.tunnelPoolOpenRateText, "tunnel_pool_open_rate"),
        tunnel_pool_open_burst: parsePositiveInteger(settingsDraft.tunnelPoolOpenBurstText, "tunnel_pool_open_burst"),
        tunnel_pool_reconcile_gap_ms: parsePositiveInteger(
          settingsDraft.tunnelPoolReconcileGapMsText,
          "tunnel_pool_reconcile_gap_ms",
        ),
        ipc_endpoint: settingsDraft.endpoint.trim(),
      };
      const snapshot = await invoke<HostConfigSnapshot>("host_config_update", { input: payload });
      setHostConfig(snapshot);
      setSettingsDraft(toSettingsDraft(snapshot));
      notify("success", "配置保存成功", "已写入本地 YAML 文件，重连/重启后生效");
      await refreshHostLogs();
    } catch (error) {
      notify("error", "配置保存失败", normalizeErrorMessage(error));
    } finally {
      setSavingSettings(false);
    }
  }, [hostConfig, notify, refreshHostLogs, settingsDraft]);

  const renderServicesTable = (): JSX.Element => (
    <Card className="overflow-hidden">
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div>
          <CardTitle className="text-[28px] leading-none tracking-[-0.02em]">服务列表</CardTitle>
          <CardDescription className="mt-1 text-xs">快照来源 `service_list_snapshot`</CardDescription>
        </div>
        <Button
          className="h-9 rounded-lg bg-[#1f67e5] px-4 text-sm font-semibold hover:bg-[#1a58c7]"
          onClick={() => notify("warning", "功能规划中", "新增服务入口将随 config.replace 一并接入")}
        >
          + 新增服务
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        <div className="overflow-x-auto">
          <table className="min-w-full border-separate border-spacing-0">
            <thead>
              <tr className="bg-[#f4f6fb]">
                <th className={TABLE_HEAD_CLASS}>名称</th>
                <th className={TABLE_HEAD_CLASS}>本地地址</th>
                <th className={TABLE_HEAD_CLASS}>公网地址</th>
                <th className={TABLE_HEAD_CLASS}>状态</th>
                <th className={TABLE_HEAD_CLASS}>流量</th>
              </tr>
            </thead>
            <tbody>
              {filteredServices.length === 0 ? (
                <tr>
                  <td className="px-4 py-8 text-center text-sm text-[#7c879e]" colSpan={5}>
                    当前没有可展示的服务
                  </td>
                </tr>
              ) : null}
              {filteredServices.map((item) => {
                const tunnel = filteredTunnels.find((entry) => entry.service_id === item.service_id);
                return (
                  <tr key={item.service_id} className="border-t border-[#edf1f8]">
                    <td className={TABLE_CELL_CLASS}>{item.service_name}</td>
                    <td className={TABLE_CELL_CLASS}>{tunnel?.local_addr ?? "--"}</td>
                    <td className={TABLE_CELL_CLASS}>{tunnel?.remote_addr ?? "--"}</td>
                    <td className={TABLE_CELL_CLASS}>
                      <Badge variant={serviceVariant(item.status)} title={item.status}>
                        {formatServiceStatus(item.status)}
                      </Badge>
                    </td>
                    <td className={TABLE_CELL_CLASS}>
                      <span
                        className={cn(
                          "inline-flex h-7 w-12 items-center rounded-full p-1 transition",
                          serviceVariant(item.status) === "success" ? "bg-[#1f67e5]" : "bg-[#bcc5d7]",
                        )}
                      >
                        <span
                          className={cn(
                            "h-5 w-5 rounded-full bg-white transition",
                            serviceVariant(item.status) === "success" ? "translate-x-5" : "translate-x-0",
                          )}
                        />
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );

  const renderLogsCard = (): JSX.Element => (
    <Card>
      <CardHeader className="flex flex-row items-center justify-between pb-2">
        <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">最近日志</CardTitle>
        <button
          className="text-base font-semibold text-[#1d63e8]"
          onClick={() => setActiveNav("diagnose")}
        >
          查看全部
        </button>
      </CardHeader>
      <CardContent>
        <div className="space-y-3">
          {recentLogs.length === 0 ? (
            <p className="rounded-xl border border-dashed border-[#dbe1ed] bg-[#fbfcff] px-4 py-6 text-sm text-[#7a879f]">暂无日志</p>
          ) : null}
          {recentLogs.map((log, index) => (
            <div key={`${log.ts_ms}-${index}`} className="flex items-start gap-3 border-b border-[#edf1f7] pb-3 last:border-b-0">
              <span
                className={cn(
                  "mt-1 h-3 w-3 rounded-full",
                  log.level.toLowerCase().includes("error")
                    ? "bg-[#d84a4a]"
                    : log.level.toLowerCase().includes("warn")
                      ? "bg-[#f3a33b]"
                      : "bg-[#29b262]",
                )}
              />
              <div className="min-w-0 flex-1">
                <p className="text-xs text-[#76839b]">{formatTime(log.ts_ms)} · {log.module}.{log.code}</p>
                <p className="mt-1 truncate text-base text-[#2a344a]">{log.message}</p>
              </div>
            </div>
          ))}
        </div>
      </CardContent>
    </Card>
  );

  const renderTrafficCard = (): JSX.Element => (
    <Card>
      <CardHeader>
        <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">流量概览</CardTitle>
      </CardHeader>
      <CardContent>
        <div className="mb-3 grid grid-cols-2 gap-3">
          <div className="rounded-xl border border-[#e5eaf4] bg-[#f9fbff] px-3 py-2">
            <p className="flex items-center gap-2 text-sm text-[#33405b]"><Upload size={16} className="text-[#1f67e5]" />上传 <strong className="text-xl text-[#273249]">{trafficSummary.uploadGb} GB</strong></p>
            <div className="mt-1 text-xs text-[#7a879f]">
              ≈{" "}
              <NetworkRateValue
                bytesPerSec={trafficSummary.uploadRateBps}
                valueClassName="text-xs font-semibold text-[#5e6a84]"
                unitClassName="text-[11px] text-[#7a879f]"
              />
            </div>
          </div>
          <div className="rounded-xl border border-[#e5eaf4] bg-[#f9fbff] px-3 py-2">
            <p className="flex items-center gap-2 text-sm text-[#33405b]"><Download size={16} className="text-[#29b262]" />下载 <strong className="text-xl text-[#273249]">{trafficSummary.downloadGb} GB</strong></p>
            <div className="mt-1 text-xs text-[#7a879f]">
              ≈{" "}
              <NetworkRateValue
                bytesPerSec={trafficSummary.downloadRateBps}
                valueClassName="text-xs font-semibold text-[#5e6a84]"
                unitClassName="text-[11px] text-[#7a879f]"
              />
            </div>
          </div>
        </div>
        <MiniLineChart valuesA={trafficSeries.upload} valuesB={trafficSeries.download} />
        <p className="mt-2 text-[11px] text-[#8390a8]">数据来源: {trafficSummary.source}</p>
      </CardContent>
    </Card>
  );

  const renderSystemResourceCard = (): JSX.Element => {
    const items = [
      { label: "CPU", value: systemMetrics.cpu, color: "bg-[#29b262]" },
      { label: "内存", value: systemMetrics.memory, color: "bg-[#1f67e5]" },
      { label: "磁盘", value: systemMetrics.disk, color: "bg-[#f09f36]" },
    ];

    return (
      <Card>
        <CardHeader>
          <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">系统资源</CardTitle>
        </CardHeader>
        <CardContent className="space-y-3">
          {items.map((item) => (
            <div key={item.label}>
              <div className="mb-1 flex items-center justify-between text-sm">
                <span className="font-semibold text-[#38445f]">{item.label}</span>
                <span className="font-semibold text-[#2b344b]">{item.value.toFixed(0)}%</span>
              </div>
              <div className="h-2.5 overflow-hidden rounded-full bg-[#e6ebf4]">
                <div className={cn("h-full rounded-full", item.color)} style={{ width: `${item.value}%` }} />
              </div>
            </div>
          ))}
        </CardContent>
      </Card>
    );
  };

  const renderTunnelPanel = (): JSX.Element => (
    <Card className="overflow-hidden">
      <CardHeader>
        <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">隧道详情</CardTitle>
        <CardDescription className="text-xs">快照来源 `tunnel_list_snapshot`</CardDescription>
      </CardHeader>
      <CardContent className="p-0">
        <div className="overflow-x-auto">
          <table className="min-w-full border-separate border-spacing-0">
            <thead>
              <tr className="bg-[#f4f6fb]">
                <th className={TABLE_HEAD_CLASS}>隧道 ID</th>
                <th className={TABLE_HEAD_CLASS}>服务 ID</th>
                <th className={TABLE_HEAD_CLASS}>本地地址</th>
                <th className={TABLE_HEAD_CLASS}>远端地址</th>
                <th className={TABLE_HEAD_CLASS}>状态</th>
                <th className={TABLE_HEAD_CLASS}>延迟</th>
                <th className={TABLE_HEAD_CLASS}>更新时间</th>
              </tr>
            </thead>
            <tbody>
              {filteredTunnels.length === 0 ? (
                <tr>
                  <td className="px-4 py-8 text-center text-sm text-[#7c879e]" colSpan={7}>
                    当前没有可展示的隧道
                  </td>
                </tr>
              ) : null}
              {filteredTunnels.map((item) => (
                <tr key={item.tunnel_id} className="border-t border-[#edf1f8]">
                  <td className={TABLE_CELL_CLASS}>{item.tunnel_id}</td>
                  <td className={TABLE_CELL_CLASS}>{item.service_id}</td>
                  <td className={TABLE_CELL_CLASS}>{item.local_addr}</td>
                  <td className={TABLE_CELL_CLASS}>{item.remote_addr}</td>
                  <td className={TABLE_CELL_CLASS}>
                    <Badge variant={tunnelVariant(item.state)} title={item.state}>
                      {formatTunnelState(item.state)}
                    </Badge>
                  </td>
                  <td className={TABLE_CELL_CLASS}>{item.latency_ms} ms</td>
                  <td className={TABLE_CELL_CLASS}>{formatDateTime(item.updated_at_ms)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );

  const renderConfigPanel = (): JSX.Element => {
    if (!hostConfig || !settingsDraft) {
      return (
        <Card>
          <CardHeader>
            <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">统一设置</CardTitle>
            <CardDescription className="text-xs">正在加载 Agent 与 Bridge 参数...</CardDescription>
          </CardHeader>
        </Card>
      );
    }

    return (
      <Card>
        <CardHeader className="pb-3">
          <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">统一设置</CardTitle>
          <CardDescription className="text-xs">在一个页面分别配置 Agent 内核参数与 Bridge 服务端参数</CardDescription>
        </CardHeader>
        <CardContent className="space-y-5">
          <section className="space-y-3">
            <h4 className="text-sm font-semibold uppercase tracking-[0.08em] text-[#5f6d87]">Agent 内核参数</h4>
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              <SettingsField label="Agent ID" hint="必填">
                <Input
                  value={settingsDraft.agentId}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, agentId: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：agent-local"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="Runtime 程序路径" hint="Agent 可执行文件路径">
                <Input
                  value={settingsDraft.runtimeProgram}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, runtimeProgram: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：/usr/local/bin/dev-agent-core"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="Runtime 参数" hint="空格分隔">
                <Input
                  value={settingsDraft.runtimeArgsText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, runtimeArgsText: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：--config /etc/dev-agent/config.yaml"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="IPC 端点" hint="按平台规则校验">
                <Input
                  value={settingsDraft.endpoint}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, endpoint: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：/tmp/dev-agent/agent.sock"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="Bridge 传输方式" hint="tcp_framed 与 grpc_h2 已打通（grpc_h2 默认监听 :39082）">
                <select
                  value={settingsDraft.transport}
                  className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]"
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, transport: event.target.value } : prev,
                    )
                  }
                >
                  <option value="tcp_framed">tcp_framed（已支持）</option>
                  <option value="grpc_h2">grpc_h2（已支持）</option>
                </select>
              </SettingsField>
              <SettingsField label="认证方式" hint="LocalRPC 握手鉴权">
                <select
                  value={settingsDraft.authMode}
                  disabled
                  className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-[#f3f6fb] px-3 text-sm text-[#43506b]"
                >
                  <option value="hmac_auth_v1">hmac_auth_v1 (app.auth)</option>
                </select>
              </SettingsField>
            </div>
          </section>

          <section className="space-y-3">
            <h4 className="text-sm font-semibold uppercase tracking-[0.08em] text-[#5f6d87]">Bridge 服务端参数</h4>
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              <SettingsField label="Bridge 地址" hint="必填">
                <Input
                  value={settingsDraft.bridgeAddr}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, bridgeAddr: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：bridge.example.com:443"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
            </div>
          </section>

          <section className="space-y-3">
            <h4 className="text-sm font-semibold uppercase tracking-[0.08em] text-[#5f6d87]">Tunnel 池参数</h4>
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              <SettingsField label="最小空闲数">
                <Input
                  value={settingsDraft.tunnelPoolMinIdleText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolMinIdleText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="最大空闲数">
                <Input
                  value={settingsDraft.tunnelPoolMaxIdleText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolMaxIdleText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="最大并发打开数">
                <Input
                  value={settingsDraft.tunnelPoolMaxInflightText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolMaxInflightText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="TTL（毫秒）">
                <Input
                  value={settingsDraft.tunnelPoolTtlMsText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolTtlMsText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="打开速率">
                <Input
                  value={settingsDraft.tunnelPoolOpenRateText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolOpenRateText: event.target.value } : prev,
                    )
                  }
                  inputMode="decimal"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="打开突发值">
                <Input
                  value={settingsDraft.tunnelPoolOpenBurstText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolOpenBurstText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="对账间隔（毫秒）">
                <Input
                  value={settingsDraft.tunnelPoolReconcileGapMsText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolReconcileGapMsText: event.target.value } : prev,
                    )
                  }
                  inputMode="numeric"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
            </div>
          </section>

          <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-[#e2e8f2] bg-[#f8fbff] px-3 py-2.5">
            <p className="text-xs text-[#697792]">
              保存后立即写入宿主；Bridge 相关参数在下次重连生效，Agent 内核参数在重启后生效。
            </p>
            <div className="flex items-center gap-2">
              <Button
                variant="outline"
                className="h-9 rounded-lg text-xs"
                disabled={!settingsDirty || savingSettings}
                onClick={resetSettingsDraft}
              >
                重置
              </Button>
              <Button
                className="h-9 rounded-lg bg-[#1f67e5] px-4 text-xs font-semibold hover:bg-[#1a58c7]"
                disabled={!settingsDirty || savingSettings}
                onClick={() => void saveSettings()}
              >
                {savingSettings ? "保存中..." : "保存配置"}
              </Button>
            </div>
          </div>
        </CardContent>
      </Card>
    );
  };

  const renderDiagnosePanel = (): JSX.Element => (
    <Card>
      <CardHeader>
        <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">日志与诊断</CardTitle>
        <CardDescription className="text-xs">结构化日志字段（时间/级别/模块/错误码）</CardDescription>
      </CardHeader>
      <CardContent>
        <div className="agent-scroll max-h-[560px] overflow-y-auto rounded-xl border border-[#e5eaf4]">
          <table className="min-w-full border-separate border-spacing-0">
            <thead>
              <tr className="bg-[#f4f6fb]">
                <th className={TABLE_HEAD_CLASS}>时间</th>
                <th className={TABLE_HEAD_CLASS}>级别</th>
                <th className={TABLE_HEAD_CLASS}>模块</th>
                <th className={TABLE_HEAD_CLASS}>错误码</th>
                <th className={TABLE_HEAD_CLASS}>消息</th>
              </tr>
            </thead>
            <tbody>
              {hostLogs.map((log, index) => (
                <tr key={`${log.ts_ms}-${index}`} className="border-t border-[#edf1f8]">
                  <td className={TABLE_CELL_CLASS}>{formatDateTime(log.ts_ms)}</td>
                  <td className={TABLE_CELL_CLASS}><Badge variant={log.level.toLowerCase().includes("error") ? "danger" : log.level.toLowerCase().includes("warn") ? "warning" : "success"}>{log.level}</Badge></td>
                  <td className={TABLE_CELL_CLASS}>{log.module}</td>
                  <td className={TABLE_CELL_CLASS}>{log.code}</td>
                  <td className={TABLE_CELL_CLASS}>{log.message}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );

  const renderOverview = (): JSX.Element => (
    <div className="space-y-3.5">
      <Card className="rounded-2xl border-[#dce3ef]">
        <CardContent className="grid grid-cols-1 gap-3 px-4 py-3 sm:grid-cols-2 lg:grid-cols-[1.25fr_0.72fr_0.72fr_0.72fr_0.52fr]">
          <div className="flex items-center gap-3 lg:border-r lg:border-[#e4e8f0] lg:pr-3.5">
            <span
              className={cn(
                "inline-flex h-9 w-9 items-center justify-center rounded-full border-2",
                overviewStatusTone.borderClass,
                overviewStatusTone.textClass,
              )}
            >
              <ShieldCheck size={20} />
            </span>
            <div>
              <p className="text-[29px] font-semibold leading-none tracking-[-0.01em] text-[#1f2b40]">{kernelConnectionSummary.label}</p>
              <p className="mt-1 text-xs text-[#6d7893]">Bridge 服务: {serviceConnectionSummary.label}</p>
              <p className="mt-1 text-xs text-[#6d7893]">
                心跳发送: {bridgeHeartbeatSentText} / 收到: {bridgeHeartbeatText}
              </p>
            </div>
          </div>
          <div className="lg:border-r lg:border-[#e4e8f0] lg:pr-3.5">
            <p className="text-xs font-medium uppercase tracking-[0.08em] text-[#58637b]">延迟</p>
            <p className="mt-1 text-[34px] font-semibold leading-none text-[#202b40]">
              {(runtimeSnapshot?.metrics.agent_host_rpc_latency_ms ?? 0).toFixed(0)}
              <span className="ml-1 text-sm text-[#27b15d]">ms</span>
            </p>
          </div>
          <div className="lg:border-r lg:border-[#e4e8f0] lg:pr-3.5">
            <p className="text-xs font-medium uppercase tracking-[0.08em] text-[#58637b]">上传</p>
            <div className="mt-1 flex items-center gap-2">
              <Upload size={18} className="text-[#1d63e8]" />
              <NetworkRateValue
                bytesPerSec={trafficSummary.uploadRateBps}
                valueClassName="text-[32px] font-semibold leading-none text-[#202b40]"
                unitClassName="text-sm text-[#5b667d]"
              />
            </div>
          </div>
          <div className="lg:border-r lg:border-[#e4e8f0] lg:pr-3.5">
            <p className="text-xs font-medium uppercase tracking-[0.08em] text-[#58637b]">下载</p>
            <div className="mt-1 flex items-center gap-2">
              <Download size={18} className="text-[#3bb96e]" />
              <NetworkRateValue
                bytesPerSec={trafficSummary.downloadRateBps}
                valueClassName="text-[32px] font-semibold leading-none text-[#202b40]"
                unitClassName="text-sm text-[#5b667d]"
              />
            </div>
          </div>
          <div className="flex flex-col gap-2">
            <Button
              className="h-9 rounded-lg bg-[#1f67e5] px-3 text-sm font-semibold hover:bg-[#1958c6]"
              disabled={Boolean(busyCommand)}
              onClick={() => void runCommand("session_reconnect")}
            >
              <RefreshCcw size={15} className="mr-1" /> 立即重连
            </Button>
            <Button className="h-9 rounded-lg text-sm" variant="outline" onClick={() => setActiveNav("connections")}>
              连接详情
            </Button>
          </div>
        </CardContent>
      </Card>

      <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[1.42fr_0.98fr]">
        <Card>
          <CardHeader className="pb-1.5">
            <CardTitle className="text-[25px] leading-none tracking-[-0.01em]">Agent 信息</CardTitle>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[1fr_1.08fr]">
              <div>
                <div className="rounded-xl border border-[#e5eaf4] bg-[#f9fbff] p-3.5">
                  <div className="mb-2.5 flex items-center gap-2 text-sm font-semibold text-[#26324a]">
                    <Gauge size={16} className="text-[#1d63e8]" /> 宿主状态
                  </div>
                  <p className="text-sm text-[#495674]">
                    进程 PID: <strong className="text-[#1f2b40]">{runtimeSnapshot?.pid ?? "--"}</strong>
                  </p>
                  <p className="mt-1 text-sm text-[#495674]">
                    重连总次数: <strong className="text-[#1f2b40]">{runtimeSnapshot?.metrics.agent_host_ipc_reconnect_total ?? 0}</strong>
                  </p>
                </div>
                <div className="mt-3 flex gap-2">
                  <Button
                    className="h-9 flex-1 rounded-lg text-sm"
                    variant="outline"
                    disabled={Boolean(busyCommand)}
                    onClick={() => void runCommand("agent_restart")}
                  >
                    <RefreshCcw size={14} className="mr-1" /> 重启
                  </Button>
                  <Button
                    className="h-9 flex-1 rounded-lg text-sm"
                    variant="outline"
                    disabled={Boolean(busyCommand)}
                    onClick={() => void runCommand("agent_start")}
                  >
                    <Wrench size={14} className="mr-1" /> 刷新
                  </Button>
                </div>
              </div>

              <div className="space-y-1">
                <InfoRow label="Agent ID" value={hostConfig?.agent_id ?? "--"} />
                <InfoRow label="版本" value={agentVersion} valueClassName="text-[#17a751]" />
                <InfoRow label="运行时长" value={formatUptime(runtimeSnapshot?.started_at_ms ?? null)} />
              </div>
            </div>
          </CardContent>
        </Card>

        <Card>
          <CardHeader className="flex flex-row items-center justify-between pb-1.5">
            <CardTitle className="text-[25px] leading-none tracking-[-0.01em]">隧道池</CardTitle>
            <Button variant="outline" className="h-8 rounded-lg px-3 text-xs" onClick={() => setActiveNav("tunnels")}>
              管理
            </Button>
          </CardHeader>
          <CardContent>
            <div className="grid grid-cols-[126px_1fr] gap-3.5">
              <div className="flex items-center justify-center">
                <div className="grid h-24 w-24 place-items-center rounded-full" style={{ background: donutBackground }}>
                  <div className="h-[70px] w-[70px] rounded-full bg-white" />
                </div>
              </div>
              <div className="space-y-1">
                <InfoRow label="总数" value={String(tunnelStats.total)} />
                <InfoRow label="空闲" value={String(tunnelStats.idle)} valueClassName="text-[#17a751]" />
                <InfoRow label="使用中" value={String(tunnelStats.inUse)} valueClassName="text-[#1f67e5]" />
              </div>
            </div>
          </CardContent>
        </Card>
      </div>

      <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[1.56fr_0.96fr]">
        {renderServicesTable()}
        {renderTrafficCard()}
      </div>

      <div className="grid grid-cols-1 gap-3.5 lg:grid-cols-[1.56fr_0.96fr]">
        {renderLogsCard()}
        {renderSystemResourceCard()}
      </div>
    </div>
  );

  const renderConnectionsPanel = (): JSX.Element => (
    <div className="space-y-4">
      <Card>
        <CardHeader className="pb-2">
          <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">Bridge 连接状态</CardTitle>
          <CardDescription className="text-xs">展示 Agent 与 Bridge 的实时连接健康信息</CardDescription>
        </CardHeader>
        <CardContent className="space-y-4">
          <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
            <InfoRow label="内核 IPC 状态" value={kernelConnectionSummary.label} />
            <InfoRow label="Bridge 服务状态" value={serviceConnectionSummary.label} />
            <InfoRow label="Bridge 会话状态" value={sessionStateRaw || "--"} />
            <InfoRow label="Bridge 地址" value={hostConfig?.bridge_addr ?? "--"} valueClassName="text-[#1f67e5]" />
            <InfoRow
              label="本地 RPC 延迟"
              value={`${(runtimeSnapshot?.metrics.agent_host_rpc_latency_ms ?? 0).toFixed(0)} ms`}
            />
            <InfoRow label="最后发送心跳" value={bridgeHeartbeatSentText} />
            <InfoRow label="最后收到心跳" value={bridgeHeartbeatText} />
            <InfoRow label="Session ID" value={sessionSnapshot?.session_id ?? "--"} />
            <InfoRow label="Session Epoch" value={sessionSnapshot?.session_epoch ? String(sessionSnapshot.session_epoch) : "--"} />
            <InfoRow label="会话重连次数" value={String(sessionSnapshot?.reconnect_total ?? runtimeSnapshot?.metrics.agent_host_ipc_reconnect_total ?? 0)} />
            <InfoRow label="连续失败次数" value={String(retryFailStreak)} />
            <InfoRow label="退避窗口" value={retryBackoffMs > 0 ? `${Math.ceil(retryBackoffMs / 1000)} 秒` : "--"} />
            <InfoRow label="下次自动重试" value={hasRetryDelay ? retryCountdownText : "无等待中任务"} />
            <InfoRow
              label="重试计划时间"
              value={nextRetryAtMs ? formatDateTime(nextRetryAtMs) : "--"}
            />
          </div>
          {lastReconnectError ? (
            <div className="rounded-lg border border-[#f1d4d4] bg-[#fff6f6] px-3 py-2.5 text-xs text-[#ba4b4b]">
              最近重连失败原因: {lastReconnectError}
            </div>
          ) : null}
          <div className="flex flex-wrap items-center gap-2">
            <Button
              className="h-9 rounded-lg bg-[#1f67e5] px-4 text-xs font-semibold hover:bg-[#1a58c7]"
              disabled={Boolean(busyCommand)}
              onClick={() => void runCommand("session_reconnect")}
            >
              <RefreshCcw size={14} className="mr-1" />
              立即重连
            </Button>
            <Button
              variant="outline"
              className="h-9 rounded-lg text-xs"
              disabled={Boolean(busyCommand)}
              onClick={() => void runCommand(bridgeConnected ? "session_drain" : "session_reconnect")}
            >
              {bridgeConnected ? "断开服务" : "建立服务"}
            </Button>
          </div>
        </CardContent>
      </Card>
      {renderTunnelPanel()}
    </div>
  );

  const renderPageByNav = (): JSX.Element => {
    if (activeNav === "services") {
      return renderServicesTable();
    }
    if (activeNav === "tunnels") {
      return renderTunnelPanel();
    }
    if (activeNav === "connections") {
      return renderConnectionsPanel();
    }
    if (activeNav === "traffic") {
      return (
        <div className="space-y-4">
          {renderTrafficCard()}
          {renderSystemResourceCard()}
        </div>
      );
    }
    if (activeNav === "settings") {
      return renderConfigPanel();
    }
    if (activeNav === "diagnose") {
      return renderDiagnosePanel();
    }
    return renderOverview();
  };

  return (
    <div className="h-screen w-screen overflow-hidden bg-[radial-gradient(circle_at_top_left,#f2f5fb_0%,#e8edf6_48%,#e0e7f2_100%)]">
      <div className="agent-ui-scale flex h-full overflow-hidden text-[#1f2b40]">
        <aside className="flex w-[248px] shrink-0 flex-col border-r border-[#223350] bg-[radial-gradient(circle_at_top_left,#1f2d4d_0%,#16243c_56%,#121b2f_100%)] px-4 py-4 text-[#e8eefb] lg:w-[258px]">
          <div className="mb-5 flex items-center gap-3">
            <div className="grid h-10 w-10 place-items-center rounded-xl bg-[#2e6de7] text-white shadow-[0_8px_20px_rgba(46,109,231,0.35)]">
              <Cloud size={20} />
            </div>
            <div>
              <p className="text-[32px] font-semibold leading-tight tracking-[-0.015em] text-white">DevBridge Agent</p>
            </div>
          </div>

          <nav className="space-y-1.5">
            {NAV_ITEMS.map((item) => (
              <NavButton key={item.key} item={item} active={item.key === activeNav} onClick={() => setActiveNav(item.key)} />
            ))}
          </nav>

          <div className="mt-auto space-y-3 rounded-2xl border border-white/12 bg-white/6 p-3.5 backdrop-blur">
            <div>
              <p className="text-xs uppercase tracking-[0.1em] text-[#b9c8e6]">Agent {agentVersion}</p>
              <p className="mt-1 flex items-center gap-2 text-lg font-semibold text-white">
                <span className={cn("h-2.5 w-2.5 rounded-full", runtimeSnapshot?.process_alive ? "bg-[#2fca6f]" : "bg-[#e06d6d]")} />
                {runtimeSnapshot?.process_alive ? "运行中" : "已停止"}
              </p>
            </div>
            <div>
              <div className="mb-1.5 flex items-center justify-between text-xs text-[#b4c1de]">
                <span>存储</span>
                <span>{tunnelStats.total.toFixed(1)} / {(hostConfig?.tunnel_pool_max_idle ?? 5).toFixed(1)} GB</span>
              </div>
              <div className="h-2 overflow-hidden rounded-full bg-white/10">
                <div
                  className="h-full rounded-full bg-[#2e6de7]"
                  style={{ width: `${Math.min(100, (tunnelStats.total / Math.max(1, hostConfig?.tunnel_pool_max_idle ?? 1)) * 100)}%` }}
                />
              </div>
            </div>
            <div className="flex items-center gap-2 text-[#d8e1f2]">
              <button className="grid h-8 w-8 place-items-center rounded-lg border border-white/15 bg-white/10 transition hover:bg-white/20">
                <SquareMousePointer size={14} />
              </button>
              <button className="grid h-8 w-8 place-items-center rounded-lg border border-white/15 bg-white/10 transition hover:bg-white/20">
                <Bell size={14} />
              </button>
              <button
                className="grid h-8 w-8 place-items-center rounded-lg border border-white/15 bg-white/10 transition hover:bg-white/20"
                onClick={() => setActiveNav("settings")}
              >
                <Settings size={14} />
              </button>
            </div>
          </div>
        </aside>

        <main className="flex min-w-0 flex-1 flex-col">
          <header className="border-b border-[#d7deeb] bg-white/80 px-4 py-3 backdrop-blur-xl lg:px-5">
            <div className="flex flex-wrap items-center gap-2.5">
              <Badge variant={kernelConnectionSummary.variant} className="px-3 py-1 text-xs font-semibold">
                {kernelConnectionSummary.label}
              </Badge>
              <Badge variant={serviceConnectionSummary.variant} className="px-3 py-1 text-xs font-semibold">
                {serviceConnectionSummary.label}
              </Badge>

              <span className="text-sm text-[#5f6c86]">
                Bridge 地址: <strong className="text-[#1f67e5]">{hostConfig?.bridge_addr ?? "--"}</strong>
              </span>
              <span className="text-xs text-[#6c7891]">心跳: {bridgeHeartbeatSentText}</span>
              <span className="inline-flex items-center gap-1 rounded-full bg-[#f2f6ff] px-2 py-1 text-xs text-[#475677]">
                注册服务 <strong className="text-[#22304a]">{serviceItems.length}</strong>
              </span>
              <span className="inline-flex items-center gap-1 rounded-full bg-[#eefbf3] px-2 py-1 text-xs text-[#2b7a52]">
                运行服务 <strong className="text-[#19653f]">{serviceHealthStats.success}</strong>
              </span>
              <span className="inline-flex items-center gap-1 rounded-full bg-[#f2f6ff] px-2 py-1 text-xs text-[#475677]">
                隧道总数 <strong className="text-[#22304a]">{tunnelStats.total}</strong>
              </span>
              <span className="inline-flex items-center gap-1 rounded-full bg-[#edf6ff] px-2 py-1 text-xs text-[#2f5ea9]">
                活跃隧道 <strong className="text-[#204985]">{tunnelStats.inUse}</strong>
              </span>
              {serviceHealthStats.danger > 0 || tunnelHealthStats.danger > 0 ? (
                <span className="inline-flex items-center gap-1 rounded-full bg-[#fff4f4] px-2 py-1 text-xs text-[#c24747]">
                  异常项 <strong>{serviceHealthStats.danger + tunnelHealthStats.danger}</strong>
                </span>
              ) : null}
              <Button
                variant="outline"
                className="h-9 rounded-lg text-xs"
                disabled={Boolean(busyCommand)}
                onClick={() => void runCommand("session_reconnect")}
              >
                <RefreshCcw size={14} className="mr-1" /> 重连
              </Button>
              <Button
                variant="outline"
                className="h-9 rounded-lg border-[#f2d3d3] text-xs text-[#d54b4b] hover:bg-[#fff4f4]"
                disabled={Boolean(busyCommand)}
                onClick={() => void runCommand(bridgeConnected ? "session_drain" : "session_reconnect")}
              >
                {bridgeConnected ? "断开服务" : "建立服务"}
              </Button>
            </div>
          </header>

          <section className="agent-scroll min-h-0 flex-1 overflow-y-auto px-4 py-3.5 lg:px-5">
            <div className="mb-2.5 flex items-center justify-end gap-2 text-[11px] text-[#6e7a93]">
              <span className="inline-flex items-center gap-1 rounded-full bg-white/65 px-2 py-1">
                <Cable size={12} /> IPC {hostConfig?.ipc_transport ?? "--"}
              </span>
              <span className="inline-flex items-center gap-1 rounded-full bg-white/65 px-2 py-1">
                <Cpu size={12} /> PID {runtimeSnapshot?.pid ?? "--"}
              </span>
              <span className="inline-flex items-center gap-1 rounded-full bg-white/65 px-2 py-1">
                <HardDrive size={12} /> 更新时间 {formatTime(runtimeSnapshot?.updated_at_ms ?? null)}
              </span>
            </div>

            {renderPageByNav()}
          </section>
        </main>
      </div>
      <Toaster />
    </div>
  );
}
