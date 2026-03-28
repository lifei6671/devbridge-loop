import { invoke } from "@tauri-apps/api/core";
import { listen, type UnlistenFn } from "@tauri-apps/api/event";
import {
  Activity,
  Bell,
  Cable,
  ChartNoAxesCombined,
  Check,
  ChevronDown,
  CircleHelp,
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
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { toast } from "sonner";

import { NetworkRateValue } from "@/components/traffic/network_rate_value";
import { AlertDialog } from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Modal } from "@/components/ui/modal";
import { Toaster } from "@/components/ui/sonner";
import { useSystemResources } from "@/features/system/use_system_resources";
import { bytesPerSecToMiB, formatBytesToGiB } from "@/features/traffic/format";
import { useTrafficStats } from "@/features/traffic/use_traffic_stats";
import {
  applyBridgeTransportSelection,
  buildBridgeTransportAddressMemory,
  defaultBridgeAddrForTransport,
  rememberBridgeAddressForTransport,
  type BridgeTransportAddressMemory,
} from "@/bridge_transport_memory";
import { cn } from "@/lib/utils";
import { registerManagedListener } from "@/runtime_subscription";
import { createPortal } from "react-dom";

type DesiredState = "running" | "stopped";
type ExitKind = "expected" | "unexpected";
type ConnectionState = "disconnected" | "reconnecting" | "resyncing" | "connected";
type AgentRuntimeCommand = "agent_start" | "agent_stop" | "agent_restart" | "agent_crash_inject" | "app_shutdown";
type BridgeSessionCommand = "session_reconnect" | "session_drain";
type RuntimeCommand = AgentRuntimeCommand | BridgeSessionCommand;
type DiagnoseCategory = "ipc" | "bridge" | "tunnel";
type DiagnoseCategoryFilterState = Record<DiagnoseCategory, boolean>;
type NavKey = "overview" | "services" | "tunnels" | "traffic" | "connections" | "diagnose" | "settings";
const APP_CLOSE_REQUESTED_EVENT = "app-close-requested";

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
  bridge_tls_enabled: boolean;
  bridge_tls_root_ca_file: string;
  bridge_tls_server_name: string;
  tunnel_pool_min_idle: number;
  tunnel_pool_max_idle: number;
  tunnel_pool_max_inflight: number;
  tunnel_pool_ttl_ms: number;
  tunnel_pool_open_rate: number;
  tunnel_pool_open_burst: number;
  tunnel_pool_reconcile_gap_ms: number;
  ipc_transport: string;
  ipc_endpoint: string;
  diagnose_show_ipc: boolean;
  diagnose_show_bridge: boolean;
  diagnose_show_tunnel: boolean;
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

interface DiagnoseLogEntry {
  ts_ms: number;
  level: string;
  module: string;
  code: string;
  message: string;
  session_id?: string | null;
  session_epoch?: number | null;
  bridge_state?: string | null;
  request_id?: string | null;
  trigger?: string | null;
  reason?: string | null;
}

interface DiagnoseSnapshot {
  state: string;
  last_error: string | null;
  retry_fail_streak: number;
  retry_backoff_ms: number;
  next_retry_at_ms: number | null;
  tunnel_idle_count: number;
  tunnel_active_count: number;
  event_total: number;
  event_error_count: number;
  event_state_changes: number;
  event_reconnects: number;
  event_refill_total: number;
  last_event_at_ms: number | null;
  last_event_code: string | null;
  last_event_message: string | null;
  updated_at_ms: number;
  source: string;
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
  logical_service_id: string;
  instance_id: string;
  scope: {
    namespace?: string;
    environment?: string;
  };
  service_name: string;
  protocol: string;
  host: string;
  port: number;
  sni_name: string;
  status: string;
  endpoint_count: number;
  last_error: string | null;
  updated_at_ms: number;
  exposure?: ServiceExposureInput | null;
  route_hint?: RouteHintInput | null;
}

type ServiceExposureMode = "l7_shared" | "tls_sni_shared" | "l4_dedicated_port";
type RouteHintMatcherMode = "exact" | "prefix" | "regex" | "present";

interface ServiceExposureInput {
  ingress_mode?: ServiceExposureMode;
  host?: string;
  listen_port?: number;
  sni_name?: string;
  path_prefix?: string;
  allow_export?: boolean;
}

interface RouteHintMatcherInput {
  name: string;
  exact?: string;
  prefix?: string;
  regex?: string;
  present?: boolean;
}

interface RouteHintInput {
  match_headers?: RouteHintMatcherInput[];
  match_queries?: RouteHintMatcherInput[];
  priority?: number;
}

interface RouteHintMatcherDraft {
  id: string;
  name: string;
  mode: RouteHintMatcherMode;
  value: string;
}

interface TunnelListItem {
  tunnel_id: string;
  logical_service_id: string;
  instance_id: string;
  state: string;
  protocol: string;
  local_addr: string;
  remote_addr: string;
  latency_ms: number;
  last_heartbeat_at_ms?: number | null;
  last_error: string | null;
  updated_at_ms: number;
}

interface ServiceAddInput {
  instance_id?: string;
  scope?: {
    namespace?: string;
    environment?: string;
  };
  service_name: string;
  protocol: string;
  host: string;
  port: number;
  sni_name?: string;
  exposure?: ServiceExposureInput;
  route_hint?: RouteHintInput;
}

interface ServiceDeleteInput {
  logical_service_id?: string;
  instance_id?: string;
  scope?: {
    namespace?: string;
    environment?: string;
  };
  service_name?: string;
}

interface ServiceDeleteResult {
  accepted: boolean;
  deleted: boolean;
  logical_service_id: string;
  instance_id: string;
  updated_at_ms: number;
}

interface ServiceCreateDraft {
  instanceId: string;
  serviceName: string;
  namespace: string;
  environment: string;
  protocol: string;
  host: string;
  portText: string;
  sniName: string;
  exposureEnabled: boolean;
  exposureMode: ServiceExposureMode;
  exposureHost: string;
  exposureListenPortText: string;
  exposureSniName: string;
  exposurePathPrefix: string;
  exposureAllowExport: boolean;
  routePriorityText: string;
  headerMatchers: RouteHintMatcherDraft[];
  queryMatchers: RouteHintMatcherDraft[];
}

const DEFAULT_SERVICE_CREATE_DRAFT: ServiceCreateDraft = {
  instanceId: "",
  serviceName: "",
  namespace: "",
  environment: "",
  protocol: "tcp",
  host: "127.0.0.1",
  portText: "8080",
  sniName: "",
  exposureEnabled: false,
  exposureMode: "l7_shared",
  exposureHost: "",
  exposureListenPortText: "",
  exposureSniName: "",
  exposurePathPrefix: "/",
  exposureAllowExport: false,
  routePriorityText: "0",
  headerMatchers: [],
  queryMatchers: [],
};

let routeHintMatcherSequence = 0;

function nextRouteHintMatcherID(): string {
  routeHintMatcherSequence += 1;
  return `route-matcher-${routeHintMatcherSequence}`;
}

function createRouteHintMatcherDraft(
  overrides: Partial<RouteHintMatcherDraft> = {},
): RouteHintMatcherDraft {
  return {
    id: nextRouteHintMatcherID(),
    name: "",
    mode: "exact",
    value: "",
    ...overrides,
  };
}

function routeHintMatchersToDrafts(matchers?: RouteHintMatcherInput[] | null): RouteHintMatcherDraft[] {
  if (!matchers || matchers.length === 0) {
    return [];
  }
  return matchers.map((matcher) => {
    const name = matcher.name?.trim() ?? "";
    if (matcher.prefix) {
      return createRouteHintMatcherDraft({ name, mode: "prefix", value: matcher.prefix });
    }
    if (matcher.regex) {
      return createRouteHintMatcherDraft({ name, mode: "regex", value: matcher.regex });
    }
    if (matcher.present) {
      return createRouteHintMatcherDraft({ name, mode: "present", value: "" });
    }
    return createRouteHintMatcherDraft({ name, mode: "exact", value: matcher.exact ?? "" });
  });
}

function routeHintToDraft(routeHint?: RouteHintInput | null): Pick<ServiceCreateDraft, "routePriorityText" | "headerMatchers" | "queryMatchers"> {
  return {
    routePriorityText: String(routeHint?.priority ?? 0),
    headerMatchers: routeHintMatchersToDrafts(routeHint?.match_headers),
    queryMatchers: routeHintMatchersToDrafts(routeHint?.match_queries),
  };
}

function normalizeExposureModeForProtocol(protocol: string, mode?: ServiceExposureMode | null): ServiceExposureMode {
  const normalizedProtocol = protocol.trim().toLowerCase();
  const allowedModes: ServiceExposureMode[] = normalizedProtocol === "https"
    ? ["l7_shared", "tls_sni_shared", "l4_dedicated_port"]
    : normalizedProtocol === "http"
      ? ["l7_shared", "l4_dedicated_port"]
      : ["l4_dedicated_port"];
  if (mode && allowedModes.includes(mode)) {
    return mode;
  }
  return allowedModes[0];
}

function hasServiceExposure(exposure?: ServiceExposureInput | null): boolean {
  if (!exposure) {
    return false;
  }
  return Boolean(
    exposure.ingress_mode
      || exposure.host?.trim()
      || exposure.listen_port
      || exposure.sni_name?.trim()
      || exposure.path_prefix?.trim()
      || exposure.allow_export,
  );
}

function exposureToDraft(
  protocol: string,
  exposure?: ServiceExposureInput | null,
): Pick<
  ServiceCreateDraft,
  | "exposureEnabled"
  | "exposureMode"
  | "exposureHost"
  | "exposureListenPortText"
  | "exposureSniName"
  | "exposurePathPrefix"
  | "exposureAllowExport"
> {
  const enabled = hasServiceExposure(exposure);
  return {
    exposureEnabled: enabled,
    exposureMode: normalizeExposureModeForProtocol(protocol, exposure?.ingress_mode),
    exposureHost: exposure?.host ?? "",
    exposureListenPortText: exposure?.listen_port ? String(exposure.listen_port) : "",
    exposureSniName: exposure?.sni_name ?? "",
    exposurePathPrefix: exposure?.path_prefix ?? "/",
    exposureAllowExport: exposure?.allow_export ?? false,
  };
}

function formatServiceExposureSummary(protocol: string, exposure?: ServiceExposureInput | null): string {
  if (!hasServiceExposure(exposure)) {
    return protocol === "http" || protocol === "https"
      ? "默认入口策略"
      : "未声明入口暴露";
  }
  const ingressMode = exposure?.ingress_mode ?? normalizeExposureModeForProtocol(protocol);
  switch (ingressMode) {
    case "l7_shared": {
      const hostText = exposure?.host?.trim() ? `host=${exposure.host.trim()}` : "host=自动派生";
      const pathText = `path=${exposure?.path_prefix?.trim() || "/"}`;
      return `${ingressMode} / ${hostText} / ${pathText}`;
    }
    case "tls_sni_shared":
      return `${ingressMode} / sni=${exposure?.sni_name?.trim() || "--"}`;
    case "l4_dedicated_port":
      return `${ingressMode} / port=${exposure?.listen_port ?? "--"}`;
    default:
      return "入口配置已声明";
  }
}

function buildExposureDraftSummary(
  draft: Pick<
    ServiceCreateDraft,
    | "protocol"
    | "exposureEnabled"
    | "exposureMode"
    | "exposureHost"
    | "exposureListenPortText"
    | "exposureSniName"
    | "exposurePathPrefix"
    | "exposureAllowExport"
  >,
): string {
  if (!draft.exposureEnabled) {
    return draft.protocol === "http" || draft.protocol === "https"
      ? "未显式声明 exposure，将沿用 Bridge 默认入口策略"
      : "当前不声明入口暴露";
  }
  switch (draft.exposureMode) {
    case "l7_shared":
      return `l7_shared / host=${draft.exposureHost.trim() || "自动派生"} / path=${draft.exposurePathPrefix.trim() || "/"}`;
    case "tls_sni_shared":
      return `tls_sni_shared / sni=${draft.exposureSniName.trim() || "--"} / port=${draft.exposureListenPortText.trim() || "--"}`;
    case "l4_dedicated_port":
      return `l4_dedicated_port / port=${draft.exposureListenPortText.trim() || "--"}`;
    default:
      return "入口配置已声明";
  }
}

function hasRouteHint(routeHint?: RouteHintInput | null): boolean {
  if (!routeHint) {
    return false;
  }
  return (routeHint.priority ?? 0) > 0
    || (routeHint.match_headers?.length ?? 0) > 0
    || (routeHint.match_queries?.length ?? 0) > 0;
}

function formatRouteHintSummary(routeHint?: RouteHintInput | null): string {
  if (!hasRouteHint(routeHint)) {
    return "默认自动路由";
  }
  const summaryParts: string[] = [];
  const headerCount = routeHint?.match_headers?.length ?? 0;
  const queryCount = routeHint?.match_queries?.length ?? 0;
  const priority = routeHint?.priority ?? 0;
  if (headerCount > 0) {
    summaryParts.push(`Header ${headerCount}`);
  }
  if (queryCount > 0) {
    summaryParts.push(`Query ${queryCount}`);
  }
  if (priority > 0) {
    summaryParts.push(`P${priority}`);
  }
  return summaryParts.join(" / ");
}

function buildRouteHintDraftSummary(draft: Pick<ServiceCreateDraft, "routePriorityText" | "headerMatchers" | "queryMatchers">): string {
  const summaryParts: string[] = [];
  const headerCount = draft.headerMatchers.filter((item) => item.name.trim() !== "").length;
  const queryCount = draft.queryMatchers.filter((item) => item.name.trim() !== "").length;
  const normalizedPriority = draft.routePriorityText.trim();
  if (headerCount > 0) {
    summaryParts.push(`Header ${headerCount}`);
  }
  if (queryCount > 0) {
    summaryParts.push(`Query ${queryCount}`);
  }
  if (normalizedPriority !== "" && normalizedPriority !== "0") {
    summaryParts.push(`P${normalizedPriority}`);
  }
  return summaryParts.length > 0 ? summaryParts.join(" / ") : "当前未配置额外匹配条件";
}

interface HostConfigUpdateInput {
  runtime_program: string;
  runtime_args: string[];
  agent_id: string;
  bridge_addr: string;
  bridge_transport: string;
  bridge_tls_enabled: boolean;
  bridge_tls_root_ca_file: string;
  bridge_tls_server_name: string;
  tunnel_pool_min_idle: number;
  tunnel_pool_max_idle: number;
  tunnel_pool_max_inflight: number;
  tunnel_pool_ttl_ms: number;
  tunnel_pool_open_rate: number;
  tunnel_pool_open_burst: number;
  tunnel_pool_reconcile_gap_ms: number;
  ipc_endpoint: string;
}

interface DiagnoseCategoryFilterUpdateInput {
  diagnose_show_ipc: boolean;
  diagnose_show_bridge: boolean;
  diagnose_show_tunnel: boolean;
}

interface SettingsDraft {
  runtimeProgram: string;
  runtimeArgsText: string;
  agentId: string;
  bridgeAddr: string;
  transport: string;
  bridgeTLSEnabled: boolean;
  bridgeTLSRootCAFile: string;
  bridgeTLSServerName: string;
  authMode: string;
  endpoint: string;
  tunnelPoolMinIdleText: string;
  tunnelPoolMaxIdleText: string;
  tunnelPoolMaxInflightText: string;
  tunnelPoolTtlSecText: string;
  tunnelPoolOpenRateText: string;
  tunnelPoolOpenBurstText: string;
  tunnelPoolReconcileGapMsText: string;
}

interface SettingsFieldHelp {
  usage: string;
  impact: string;
}

interface BridgeTransportGuide {
  title: string;
  lines: string[];
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

const SETTINGS_FIELD_HELP: Record<string, SettingsFieldHelp> = {
  agentId: {
    usage: "用于标识当前 Agent 实例，建议在同一环境内保持唯一且稳定。",
    impact: "会影响 Bridge 侧会话归属和日志检索；频繁修改会导致排障链路断裂。",
  },
  runtimeProgram: {
    usage: "填写 Agent 内核可执行文件的绝对路径，重启 Agent 后生效。",
    impact: "路径错误会导致进程无法拉起，IPC 与 Bridge 功能都会不可用。",
  },
  runtimeArgs: {
    usage: "按空格输入启动参数，例如配置文件路径与运行模式参数。",
    impact: "参数会直接传给内核进程，错误参数可能导致启动失败或行为偏差。",
  },
  ipcEndpoint: {
    usage: "本机 IPC 通道地址。Windows 使用 named pipe，Linux/macOS 使用 uds 路径。",
    impact: "宿主与内核通信完全依赖该端点，配置错误会导致“已启动但无法连通”。",
  },
  bridgeTransport: {
    usage: "选择与 Bridge 建链的传输层实现。切换时会按 transport 自动回填默认地址，若你改过某个 transport 的地址，则会记住并在切回来时恢复。",
    impact: "本地默认端口按 tcp=39081、grpc_h2=39082、quic=39083 回填；quic_native 仍要求 Bridge TLS、QUIC 端口和 Root CA 已就绪。",
  },
  authMode: {
    usage: "当前为 LocalRPC 的固定鉴权方案，保持只读用于展示。",
    impact: "用于防止本地未授权调用；随意改动会破坏宿主与内核的安全握手。",
  },
  bridgeAddr: {
    usage: "填写 Bridge 的 host:port 地址；未手动设置过某个 transport 时，会自动回填对应默认监听地址。",
    impact: "地址会按 transport 分别记忆；quic_native 必须指向 Bridge 的 QUIC 监听地址，否则会持续重连并触发退避。",
  },
  bridgeTLSEnabled: {
    usage: "控制 Agent 连接 Bridge 时是否启用 TLS。quic_native 下必须开启。",
    impact: "关闭后只能使用明文 TCP/gRPC；若 quic_native 仍关闭 TLS，保存时会被拒绝。",
  },
  bridgeTLSRootCAFile: {
    usage: "填写 Bridge TLS Root CA 证书文件路径；启用 TLS 时必填，不要填写私钥路径。",
    impact: "用于校验 Bridge 服务端证书；managed_ca 下通常应指向 Bridge 控制台日志里的 ca_cert_file / root-ca.crt，路径错误会导致握手失败或持续重连。",
  },
  bridgeTLSServerName: {
    usage: "可选覆盖 TLS Server Name；留空时默认回退到 bridge_addr 的 host。",
    impact: "用于证书名称校验；当 Bridge 证书 SAN 与访问地址不一致时可在此显式指定。",
  },
  minIdle: {
    usage: "期望常驻的最小空闲 tunnel 数，建议按并发峰值前置预热。",
    impact: "过低会增加首包等待，过高会增加连接数、内存与 FD 占用。",
  },
  maxIdle: {
    usage: "空闲 tunnel 池上限，必须大于等于“最小空闲数”。",
    impact: "限制预建池规模；过小会频繁补池，过大可能造成资源浪费。",
  },
  maxInflight: {
    usage: "一次补池过程中允许并发打开的 tunnel 数。",
    impact: "过小会导致补池慢，过大可能在网络抖动时放大瞬时连接压力。",
  },
  ttlSeconds: {
    usage: "空闲 tunnel 的生命周期（秒）。设置 0 表示禁用基于 TTL 的回收。",
    impact: "值小会更积极回收，值大会更稳定复用连接但占用资源更久。",
  },
  openRate: {
    usage: "平滑建连速率（每秒）。建议与服务端承载能力匹配。",
    impact: "速率过高可能造成连接突刺，过低会导致补池达标速度变慢。",
  },
  openBurst: {
    usage: "冷启动/补池阶段允许的瞬时建连突发窗口。",
    impact: "突发过大可能导致瞬时抖动，过小会拉长达到目标池容量的时间。",
  },
  reconcileGapMs: {
    usage: "周期性池状态对账间隔（毫秒），用于慢路径纠偏。",
    impact: "间隔过大会延迟异常收敛，间隔过小会增加调度与日志开销。",
  },
};

const SERVICE_FORM_FIELD_HELP: Record<string, SettingsFieldHelp> = {
  serviceName: {
    usage: "填写业务服务名称，建议与真实服务名一致，例如 order-service。",
    impact: "用于服务展示与管理识别；后续排障、编辑、删除都会依赖该名称。",
  },
  instanceId: {
    usage: "可选实例 ID；留空时由运行时生成并在控制面回写，编辑模式固定不可改。",
    impact: "用于复用同一实例记录；修改实例 ID 可能触发新的实例身份。",
  },
  namespace: {
    usage: "必填命名空间，例如 dev / stage / prod。",
    impact: "与 environment 共同组成 PublishService.scope；缺失时当前注册协议会直接拒绝。",
  },
  environment: {
    usage: "必填环境标签，例如 demo / alice / prod。",
    impact: "与 namespace 共同组成 PublishService.scope；缺失时当前注册协议会直接拒绝。",
  },
  protocol: {
    usage: "选择服务协议（tcp / http / https）。",
    impact: "影响 upstream 连接方式以及是否允许填写 route_hint；协议选择错误会导致转发行为异常。",
  },
  host: {
    usage: "填写服务监听地址，例如 127.0.0.1 或内网 IP。",
    impact: "Bridge 会直接连接该地址，错误地址会导致 tunnel 无法建立。",
  },
  port: {
    usage: "填写服务监听端口，范围 1-65535。",
    impact: "与主机地址共同确定上游目标，配置错误会导致连接失败。",
  },
  sniName: {
    usage: "仅 `https` upstream 可选；填写 TLS server_name / SNI，例如 order.dev.example.com。",
    impact: "用于 Agent 发起 TLS 连接时的服务端名称校验；不参与实例身份和 Route 匹配。",
  },
  exposureMode: {
    usage: "声明服务挂载到 Bridge 哪种入口：L7 共享入口、TLS SNI 共享入口、或 L4 专属端口。",
    impact: "决定 Bridge 自动派生 Route 时使用 host/path、sni 还是 listen_port；模式选错会导致入口匹配失效。",
  },
  exposureHost: {
    usage: "L7 共享入口下可选覆盖外部 host；留空则由 Bridge 按 service_name + scope 自动派生。",
    impact: "用于控制外部域名；填写后会覆盖默认派生规则。",
  },
  exposureListenPort: {
    usage: "共享入口或专属端口的监听端口；L4 专属端口模式建议显式填写。",
    impact: "决定外部请求应打到哪个入口端口；端口错误会导致入口不可达。",
  },
  exposurePathPrefix: {
    usage: "L7 共享入口下的路径前缀，例如 /api/order；留空时默认 /。",
    impact: "参与 RouteMatch.path_prefix；不同前缀会影响路由优先级和冲突判定。",
  },
  exposureSniName: {
    usage: "TLS SNI 共享入口下必填，例如 order.dev.example.com。",
    impact: "Bridge 会按该 SNI 匹配入口流量；缺失时该模式无法建立有效匹配。",
  },
  exposureAllowExport: {
    usage: "允许 Bridge 将该入口信息导出给外部发现系统时开启。",
    impact: "会影响 discovery/export 准入，不会直接改变本地 upstream 转发。",
  },
  routePriority: {
    usage: "自动派生 HTTP/HTTPS 路由的优先级；默认 0，数值越大越优先。",
    impact: "只影响 Bridge 上自动生成的 Route 顺序；与现有 Route 精确冲突时仍会被 Admission 拒绝。",
  },
};

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
    bridgeTLSEnabled: snapshot.bridge_tls_enabled,
    bridgeTLSRootCAFile: snapshot.bridge_tls_root_ca_file,
    bridgeTLSServerName: snapshot.bridge_tls_server_name,
    authMode: authModeFromTransport(snapshot.ipc_transport),
    endpoint: snapshot.ipc_endpoint,
    tunnelPoolMinIdleText: String(snapshot.tunnel_pool_min_idle),
    tunnelPoolMaxIdleText: String(snapshot.tunnel_pool_max_idle),
    tunnelPoolMaxInflightText: String(snapshot.tunnel_pool_max_inflight),
    tunnelPoolTtlSecText: formatTTLSecondsText(snapshot.tunnel_pool_ttl_ms),
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

function parseNonNegativeSecondsToMillis(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (normalized.length === 0) {
    throw new Error(`${fieldLabel} 不能为空`);
  }
  const parsedValue = Number.parseFloat(normalized);
  if (!Number.isFinite(parsedValue) || parsedValue < 0) {
    throw new Error(`${fieldLabel} 必须是非负数`);
  }
  return Math.round(parsedValue * 1000);
}

function normalizeOptionalText(value: string): string {
  return value.trim();
}

function formatTTLSecondsText(ttlMs: number): string {
  if (!Number.isFinite(ttlMs) || ttlMs < 0) {
    return "0";
  }
  const seconds = ttlMs / 1000;
  if (Number.isInteger(seconds)) {
    return String(seconds);
  }
  return seconds.toFixed(3).replace(/\.?0+$/, "");
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

function FieldHelpTooltip(props: {
  label: string;
  help: SettingsFieldHelp;
}): JSX.Element {
  const tooltipId = useId();
  const triggerRef = useRef<HTMLButtonElement | null>(null);
  const tooltipRef = useRef<HTMLSpanElement | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [layout, setLayout] = useState<{ left: number; top: number; width: number } | null>(null);

  const openTooltip = useCallback(() => {
    setLayout(null);
    setIsOpen(true);
  }, []);

  const closeTooltip = useCallback(() => {
    setIsOpen(false);
  }, []);

  const updateTooltipLayout = useCallback(() => {
    if (!isOpen || !triggerRef.current) {
      return;
    }
    const viewportPadding = 12;
    const verticalGap = 8;
    const triggerRect = triggerRef.current.getBoundingClientRect();
    const preferredWidth = window.innerWidth >= 1024 ? 280 : 260;
    const tooltipWidth = Math.min(preferredWidth, Math.max(180, window.innerWidth - viewportPadding * 2));
    const centeredLeft = triggerRect.left + triggerRect.width / 2 - tooltipWidth / 2;
    const maxLeft = Math.max(viewportPadding, window.innerWidth - viewportPadding - tooltipWidth);
    const left = Math.min(maxLeft, Math.max(viewportPadding, centeredLeft));

    const measuredHeight = tooltipRef.current?.offsetHeight ?? 132;
    const belowTop = triggerRect.bottom + verticalGap;
    const aboveTop = triggerRect.top - measuredHeight - verticalGap;
    const maxTop = Math.max(viewportPadding, window.innerHeight - viewportPadding - measuredHeight);
    const shouldPlaceAbove = belowTop + measuredHeight > window.innerHeight - viewportPadding && aboveTop >= viewportPadding;
    const preferredTop = shouldPlaceAbove ? aboveTop : belowTop;
    const top = Math.min(maxTop, Math.max(viewportPadding, preferredTop));

    setLayout({ left, top, width: tooltipWidth });
  }, [isOpen]);

  useLayoutEffect(() => {
    if (!isOpen) {
      setLayout(null);
      return;
    }
    updateTooltipLayout();
    const frame = window.requestAnimationFrame(updateTooltipLayout);
    const handleWindowChange = () => updateTooltipLayout();
    window.addEventListener("resize", handleWindowChange);
    window.addEventListener("scroll", handleWindowChange, true);
    return () => {
      window.cancelAnimationFrame(frame);
      window.removeEventListener("resize", handleWindowChange);
      window.removeEventListener("scroll", handleWindowChange, true);
    };
  }, [isOpen, updateTooltipLayout]);

  useEffect(() => {
    if (!isOpen) {
      return;
    }
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setIsOpen(false);
      }
    };
    window.addEventListener("keydown", handleEscape);
    return () => window.removeEventListener("keydown", handleEscape);
  }, [isOpen]);

  return (
    <>
      <span
        className="relative inline-flex items-center"
        onMouseEnter={openTooltip}
        onMouseLeave={closeTooltip}
      >
        <button
          type="button"
          ref={triggerRef}
          aria-label={`${props.label} 配置说明`}
          aria-describedby={tooltipId}
          aria-expanded={isOpen}
          onFocus={openTooltip}
          onClick={() => {
            if (isOpen) {
              closeTooltip();
            } else {
              openTooltip();
            }
          }}
          onBlur={closeTooltip}
          className="inline-flex h-4 w-4 items-center justify-center rounded-full text-[#8a97b0] transition hover:text-[#4a5f86] focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#7ea6f3] focus-visible:ring-offset-1"
        >
          <CircleHelp className="h-3.5 w-3.5" aria-hidden />
        </button>
      </span>
      {typeof document !== "undefined"
        ? createPortal(
            <span
              ref={tooltipRef}
              id={tooltipId}
              role="tooltip"
              style={
                layout
                  ? {
                      left: `${layout.left}px`,
                      top: `${layout.top}px`,
                      width: `${layout.width}px`,
                    }
                  : undefined
              }
              className={cn(
                "pointer-events-none fixed z-[120] rounded-lg border border-[#314469] bg-[#1f2d49] px-3 py-2.5 text-left shadow-xl transition",
                layout ? "" : "left-0 top-0",
                isOpen && layout ? "visible translate-y-0 opacity-100" : "invisible translate-y-0 opacity-0",
              )}
            >
              <span className="block text-[11px] font-semibold uppercase tracking-[0.06em] text-[#9fb7e5]">用法</span>
              <span className="mt-0.5 block text-[11px] leading-[1.5] text-[#edf2ff]">{props.help.usage}</span>
              <span className="mt-2 block text-[11px] font-semibold uppercase tracking-[0.06em] text-[#9fb7e5]">作用</span>
              <span className="mt-0.5 block text-[11px] leading-[1.5] text-[#edf2ff]">{props.help.impact}</span>
            </span>,
            document.body,
          )
        : null}
    </>
  );
}

function SettingsField(props: {
  label: string;
  hint?: string;
  help?: SettingsFieldHelp;
  children: ReactNode;
}): JSX.Element {
  return (
    <label className="block space-y-1.5">
      <div className="flex items-end justify-between gap-3">
        <span className="inline-flex items-center gap-1.5 text-sm font-medium text-[#44516d]">
          {props.label}
          {props.help ? <FieldHelpTooltip label={props.label} help={props.help} /> : null}
        </span>
        {props.hint ? <span className="text-[11px] text-[#8290a8]">{props.hint}</span> : null}
      </div>
      {props.children}
    </label>
  );
}

function ServiceFormSection(props: {
  title: string;
  description: string;
  children: ReactNode;
}): JSX.Element {
  return (
    <section className="rounded-2xl border border-[#dbe4f3] bg-[#f8fbff] p-4">
      <div className="mb-3 flex flex-col gap-1 border-b border-[#e5ecf7] pb-3">
        <h3 className="text-sm font-semibold tracking-[0.01em] text-[#22304a]">{props.title}</h3>
        <p className="text-xs leading-5 text-[#6e7d96]">{props.description}</p>
      </div>
      {props.children}
    </section>
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

function formatTunnelIDForDisplay(rawTunnelID: string): string {
  const normalizedTunnelID = rawTunnelID.trim();
  if (normalizedTunnelID === "") {
    return "--";
  }
  const bridgePrefix = "tcp-bridge-tunnel-";
  if (normalizedTunnelID.startsWith(bridgePrefix)) {
    const suffix = normalizedTunnelID.slice(bridgePrefix.length).trim();
    if (/^\d+$/.test(suffix)) {
      return `tun-${suffix}`;
    }
  }
  return normalizedTunnelID;
}

function formatScopeText(scope?: { namespace?: string; environment?: string } | null): string {
  const namespace = scope?.namespace?.trim() ?? "";
  const environment = scope?.environment?.trim() ?? "";
  if (!namespace && !environment) {
    return "--";
  }
  if (namespace && environment) {
    return `${namespace}/${environment}`;
  }
  return namespace || environment;
}

function parseRouteHintPriority(rawValue: string): number {
  const normalizedValue = rawValue.trim();
  if (normalizedValue === "") {
    return 0;
  }
  if (!/^\d+$/.test(normalizedValue)) {
    throw new Error("路由优先级必须是大于等于 0 的整数");
  }
  return Number.parseInt(normalizedValue, 10);
}

function buildRouteHintMatcherPayloads(
  drafts: RouteHintMatcherDraft[],
  fieldLabel: string,
): RouteHintMatcherInput[] {
  const payloads: RouteHintMatcherInput[] = [];
  drafts.forEach((draft, index) => {
    const normalizedName = draft.name.trim();
    const normalizedValue = draft.value.trim();
    if (!normalizedName && !normalizedValue) {
      return;
    }
    if (!normalizedName) {
      throw new Error(`${fieldLabel}第 ${index + 1} 条缺少名称`);
    }
    switch (draft.mode) {
      case "present":
        payloads.push({ name: normalizedName, present: true });
        return;
      case "exact":
        if (!normalizedValue) {
          throw new Error(`${fieldLabel}第 ${index + 1} 条缺少精确匹配值`);
        }
        payloads.push({ name: normalizedName, exact: normalizedValue });
        return;
      case "prefix":
        if (!normalizedValue) {
          throw new Error(`${fieldLabel}第 ${index + 1} 条缺少前缀匹配值`);
        }
        payloads.push({ name: normalizedName, prefix: normalizedValue });
        return;
      case "regex":
        if (!normalizedValue) {
          throw new Error(`${fieldLabel}第 ${index + 1} 条缺少正则表达式`);
        }
        payloads.push({ name: normalizedName, regex: normalizedValue });
        return;
      default:
        return;
    }
  });
  return payloads;
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

// 将服务状态映射为 UI 颜色语义，优先保证“活跃/异常/中性”可读性。
function serviceVariant(status: string): "success" | "warning" | "danger" | "secondary" {
  const normalized = status.trim().toLowerCase();
  if (
    normalized.includes("unhealthy")
    || normalized.includes("inactive")
    || normalized.includes("down")
    || normalized.includes("fail")
    || normalized.includes("error")
    || normalized.includes("stale")
  ) {
    return "danger";
  }
  if (
    normalized.includes("run")
    || normalized.includes("healthy")
    || normalized.includes("active")
    || normalized.includes("ready")
  ) {
    return "success";
  }
  if (normalized.includes("degraded") || normalized.includes("warn")) {
    return "warning";
  }
  if (normalized.includes("idle") || normalized.includes("pending") || normalized.includes("unknown")) {
    return "secondary";
  }
  // 未识别状态统一作为中性态展示，避免误判为故障。
  return "secondary";
}

// 将协议状态值规范为中文展示文案。
function formatServiceStatus(status: string): string {
  const trimmed = status.trim();
  if (!trimmed) {
    return "未知";
  }
  if (/[\u4e00-\u9fa5]/.test(trimmed)) {
    return trimmed;
  }
  const normalized = trimmed.toLowerCase();
  if (normalized.includes("unhealthy")) {
    return "不健康";
  }
  if (normalized.includes("inactive")) {
    return "未激活";
  }
  if (normalized.includes("healthy")) {
    return "健康";
  }
  if (normalized.includes("active")) {
    return "已发布";
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
  if (normalized.includes("stale")) {
    return "陈旧";
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

function formatTransportLabel(protocol: string): string {
  const normalized = protocol.trim().toLowerCase();
  if (!normalized) {
    return "--";
  }
  if (normalized === "tcp_framed") {
    return "TCP Framed";
  }
  if (normalized === "grpc_h2") {
    return "gRPC h2";
  }
  if (normalized === "quic_native") {
    return "QUIC Native";
  }
  if (normalized === "tcp") {
    return "TCP";
  }
  if (normalized === "http") {
    return "HTTP";
  }
  if (normalized === "https") {
    return "HTTPS";
  }
  return protocol;
}

function isQUICNativeTransport(transport: string): boolean {
  return transport.trim().toLowerCase() === "quic_native";
}

function buildBridgeTransportGuide(transport: string, bridgeAddr: string): BridgeTransportGuide | null {
  if (!isQUICNativeTransport(transport)) {
    return null;
  }
  const normalizedBridgeAddr = bridgeAddr.trim();
  const targetText = normalizedBridgeAddr ? `当前目标：${normalizedBridgeAddr}` : "当前目标：请填写 Bridge 的 QUIC 监听地址";
  return {
    title: "QUIC 联调提示",
    lines: [
      `${targetText}，应与 Bridge 后台里的 control_plane.quic_listen_addr 保持一致。`,
      "Bridge 的 control_plane.tls_mode 不能是 plaintext，否则 QUIC listener 不会启动。",
      "如果 Bridge 使用 external 证书，Agent 所在环境需要预置对应 Root CA 或信任链。",
      "如果 Bridge 使用 managed_ca，Root CA 仍需带外分发到 Agent，本 UI 不会自动下发信任锚。",
    ],
  };
}

const LOG_LEVEL_RANK: Record<string, number> = {
  trace: 10,
  debug: 20,
  info: 30,
  warn: 40,
  warning: 40,
  error: 50,
  fatal: 60,
};

const KNOWN_DIAGNOSE_ERROR_CODES: string[] = [
  "AGENT_EXIT_EXPECTED",
  "AGENT_EXIT_UNEXPECTED",
  "AGENT_START_FAILED",
  "AGENT_STARTED",
  "AGENT_STOPPED",
  "AGENT_STOPPING",
  "AGENT_WAIT_FAILED",
  "APP_BOOTSTRAP_NOOP",
  "APP_BOOTSTRAP_SCHEDULED",
  "AUTH_FAILED",
  "AUTH_FLOW_INVALID",
  "AUTH_REQUIRED",
  "AUTO_RESTART_FAILED",
  "AUTO_RESTART_TRIGGERED",
  "BRIDGE_CONTROL_ERROR",
  "BRIDGE_RECONNECT_ESTABLISHED",
  "BRIDGE_RETRY_SCHEDULED",
  "BRIDGE_STATE_ACTIVE",
  "BRIDGE_STATE_CLOSED",
  "BRIDGE_STATE_CONNECTING",
  "BRIDGE_STATE_DRAINING",
  "BRIDGE_STATE_STALE",
  "BRIDGE_UNAVAILABLE",
  "CRASH_INJECTED",
  "DIAGNOSE_LOGS_METHOD_NOT_READY",
  "DIAGNOSE_LOGS_SNAPSHOT",
  "DIAGNOSE_SNAPSHOT_METHOD_NOT_READY",
  "EVENT_EMIT_FAILED",
  "HOST_CONFIG_DIAGNOSE_FILTER_UPDATED",
  "HOST_CONFIG_UPDATED",
  "IPC_CONNECTED",
  "IPC_CONNECTING",
  "IPC_CONNECT_FAILED",
  "IPC_DISCONNECTED",
  "IPC_DISCONNECTING",
  "IPC_RECONNECTING",
  "IPC_RESYNCING",
  "REFILL_REJECTED",
  "RPC_BOOTSTRAP_FAILED",
  "RPC_CONNECTED",
  "RPC_EVENT_DRAINED",
  "RPC_PING_DEGRADED",
  "RPC_PING_FAILED",
  "RPC_RESYNC_FAILED",
  "RPC_SNAPSHOT_DEGRADED",
  "RUNTIME_EVENT",
  "SERVICE_ADD_METHOD_NOT_READY",
  "SERVICE_ADD_SUCCEEDED",
  "SERVICE_LIST_METHOD_NOT_READY",
  "SERVICE_LIST_SNAPSHOT",
  "SERVICE_SYNC_FAILED",
  "SESSION_DRAIN_TRIGGERED",
  "SESSION_RECONNECT_REQUESTED",
  "SESSION_RECONNECT_TRIGGERED",
  "SESSION_SNAPSHOT_FAILED",
  "SESSION_SNAPSHOT_METHOD_NOT_READY",
  "TRAFFIC_STATS_FALLBACK_HOST",
  "TUNNEL_ACTIVE",
  "TUNNEL_CLEANUP_BROKEN",
  "TUNNEL_CLEANUP_CLOSED",
  "TUNNEL_DIAL_ANNOUNCED",
  "TUNNEL_DIAL_ANNOUNCE_BUILD_FAILED",
  "TUNNEL_DIAL_ANNOUNCE_SEND_FAILED",
  "TUNNEL_IDLE_ACQUIRED",
  "TUNNEL_IDLE_TTL_REAPED",
  "TUNNEL_LIST_METHOD_NOT_READY",
  "TUNNEL_LIST_SNAPSHOT",
  "TUNNEL_POOL_CHANGED",
  "TUNNEL_POOL_EVENT",
  "TUNNEL_POOL_REBUILT",
  "TUNNEL_POOL_REPORT_FAILED",
  "TUNNEL_POOL_REPORT_TRIGGERED",
  "TUNNEL_POOL_SESSION_ACTIVE",
  "TUNNEL_POOL_SESSION_DRAINING",
  "TUNNEL_POOL_SESSION_STALE",
  "TUNNEL_POOL_STARTUP_RECONCILE_FAILED",
  "TUNNEL_REFILL_APPLIED",
  "TUNNEL_REFILL_EXPANSION_CHECK",
  "TUNNEL_REFILL_IGNORED",
  "TUNNEL_REFILL_PAYLOAD_INVALID",
  "TUNNEL_REFILL_REJECTED",
  "TUNNEL_REFILL_REQUESTED",
  "TUNNEL_REFILL_REQUEST_RECEIVED",
  "UPSTREAM_RESET",
];

function logLevelRank(level: string): number {
  const normalized = level.trim().toLowerCase();
  return LOG_LEVEL_RANK[normalized] ?? LOG_LEVEL_RANK.info;
}

function classifyDiagnoseCategory(log: Pick<DiagnoseLogEntry, "module" | "code">): DiagnoseCategory {
  const module = log.module.trim().toLowerCase();
  const code = log.code.trim().toLowerCase();
  if (module.includes("ipc") || code.startsWith("ipc_")) {
    return "ipc";
  }
  if (
    module.includes("tunnel")
    || module.includes("refill")
    || code.startsWith("tunnel_")
    || code.includes("_tunnel_")
  ) {
    return "tunnel";
  }
  if (module.includes("bridge") || code.startsWith("bridge_") || code.startsWith("session_")) {
    return "bridge";
  }
  return "bridge";
}

function diagnoseCategoryFilterFromHostConfig(config: HostConfigSnapshot): DiagnoseCategoryFilterState {
  const nextFilter: DiagnoseCategoryFilterState = {
    ipc: config.diagnose_show_ipc,
    bridge: config.diagnose_show_bridge,
    tunnel: config.diagnose_show_tunnel,
  };
  if (!nextFilter.ipc && !nextFilter.bridge && !nextFilter.tunnel) {
    return { ipc: true, bridge: true, tunnel: true };
  }
  return nextFilter;
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

function InfoRow(props: {
  label: string;
  value: string;
  valueClassName?: string;
  compact?: boolean;
}): JSX.Element {
  return (
    <div
      className={cn(
        "flex items-center gap-3 border-b border-[#ebeff6] py-2.5 last:border-b-0",
        props.compact ? "justify-start" : "justify-between",
      )}
    >
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
  const [diagnoseSnapshot, setDiagnoseSnapshot] = useState<DiagnoseSnapshot | null>(null);
  const [diagnoseLogs, setDiagnoseLogs] = useState<DiagnoseLogEntry[]>([]);
  const [serviceItems, setServiceItems] = useState<ServiceListItem[]>([]);
  const [tunnelItems, setTunnelItems] = useState<TunnelListItem[]>([]);
  const [closeConfirmOpen, setCloseConfirmOpen] = useState(false);
  const [closeActionLoading, setCloseActionLoading] = useState(false);
  const [serviceFormOpen, setServiceFormOpen] = useState(false);
  const [serviceFormMode, setServiceFormMode] = useState<"create" | "edit">("create");
  const [serviceEditingID, setServiceEditingID] = useState<string | null>(null);
  const [creatingService, setCreatingService] = useState(false);
  const [deletingServiceID, setDeletingServiceID] = useState<string | null>(null);
  const [serviceCreateDraft, setServiceCreateDraft] = useState<ServiceCreateDraft>(DEFAULT_SERVICE_CREATE_DRAFT);
  const [diagnoseMinLevel, setDiagnoseMinLevel] = useState("info");
  const [diagnoseCategoryFilter, setDiagnoseCategoryFilter] = useState<DiagnoseCategoryFilterState>({
    ipc: true,
    bridge: true,
    tunnel: true,
  });
  const [diagnoseCodeFilter, setDiagnoseCodeFilter] = useState<string[]>([]);
  const [diagnoseCodeSearchText, setDiagnoseCodeSearchText] = useState("");
  const [diagnoseCodeDropdownOpen, setDiagnoseCodeDropdownOpen] = useState(false);
  const [diagnoseKnownAndObservedCodes, setDiagnoseKnownAndObservedCodes] = useState<string[]>(() =>
    Array.from(new Set(KNOWN_DIAGNOSE_ERROR_CODES)).sort((left, right) => left.localeCompare(right)),
  );
  const [busyCommand, setBusyCommand] = useState<RuntimeCommand | null>(null);
  const [savingSettings, setSavingSettings] = useState(false);
  const [nowTsMs, setNowTsMs] = useState(() => Date.now());
  const [settingsDraft, setSettingsDraft] = useState<SettingsDraft | null>(null);
  const [settingsBridgeAddressMemory, setSettingsBridgeAddressMemory] = useState<BridgeTransportAddressMemory>({});
  const diagnoseFilterSaveSeqRef = useRef(0);
  const diagnoseCodeDropdownRef = useRef<HTMLDivElement | null>(null);
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
    setHostLogs(logs.slice(-128).reverse());
  }, []);

  // 读取 runtime 诊断聚合快照（状态 + 事件计数）。
  const refreshDiagnoseSnapshot = useCallback(async () => {
    const snapshot = await invoke<DiagnoseSnapshot>("diagnose_snapshot");
    setDiagnoseSnapshot(snapshot);
  }, []);

  // 读取 runtime 诊断事件流，默认仅保留前端可视窗口大小。
  const refreshDiagnoseLogs = useCallback(async () => {
    const logs = await invoke<DiagnoseLogEntry[]>("diagnose_logs_snapshot");
    setDiagnoseLogs(logs.slice(0, 128));
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
      refreshDiagnoseSnapshot(),
      refreshDiagnoseLogs(),
      refreshSessionSnapshot(),
      refreshServiceList(),
      refreshTunnelList(),
      refreshTrafficStatsSafely(),
      refreshSystemResourceStatsSafely(),
    ]);
  }, [
    refreshDiagnoseLogs,
    refreshDiagnoseSnapshot,
    refreshHostLogs,
    refreshServiceList,
    refreshSessionSnapshot,
    refreshSystemResourceStatsSafely,
    refreshTrafficStatsSafely,
    refreshTunnelList,
  ]);

  const persistDiagnoseCategoryFilter = useCallback(
    async (nextFilter: DiagnoseCategoryFilterState) => {
      const sequence = diagnoseFilterSaveSeqRef.current + 1;
      diagnoseFilterSaveSeqRef.current = sequence;
      const payload: DiagnoseCategoryFilterUpdateInput = {
        diagnose_show_ipc: nextFilter.ipc,
        diagnose_show_bridge: nextFilter.bridge,
        diagnose_show_tunnel: nextFilter.tunnel,
      };
      try {
        const snapshot = await invoke<HostConfigSnapshot>("host_config_update_diagnose_filter", { input: payload });
        if (diagnoseFilterSaveSeqRef.current !== sequence) {
          return;
        }
        setHostConfig(snapshot);
      } catch (error) {
        if (diagnoseFilterSaveSeqRef.current !== sequence) {
          return;
        }
        notify("warning", "保存日志筛选失败", normalizeErrorMessage(error));
        if (hostConfig) {
          setDiagnoseCategoryFilter(diagnoseCategoryFilterFromHostConfig(hostConfig));
        } else {
          setDiagnoseCategoryFilter({ ipc: true, bridge: true, tunnel: true });
        }
      }
    },
    [hostConfig, notify],
  );

  const applyDiagnoseCategoryFilter = useCallback(
    (updater: (prev: DiagnoseCategoryFilterState) => DiagnoseCategoryFilterState) => {
      setDiagnoseCategoryFilter((prev) => {
        const next = updater(prev);
        if (next.ipc === prev.ipc && next.bridge === prev.bridge && next.tunnel === prev.tunnel) {
          return prev;
        }
        void persistDiagnoseCategoryFilter(next);
        return next;
      });
    },
    [persistDiagnoseCategoryFilter],
  );

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
          refreshDiagnoseSnapshot(),
          refreshDiagnoseLogs(),
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
      refreshDiagnoseLogs,
      refreshDiagnoseSnapshot,
      refreshHostLogs,
      refreshServiceList,
      refreshSessionSnapshot,
      refreshSnapshot,
      refreshSystemResourceStatsSafely,
      refreshTrafficStatsSafely,
      refreshTunnelList,
    ],
  );

  const hideToTray = useCallback(async () => {
    setCloseActionLoading(true);
    try {
      await invoke("app_hide_to_tray");
      setCloseConfirmOpen(false);
    } catch (error) {
      notify("error", "隐藏到托盘失败", normalizeErrorMessage(error));
    } finally {
      setCloseActionLoading(false);
    }
  }, [notify]);

  const confirmExit = useCallback(async () => {
    setCloseActionLoading(true);
    try {
      await invoke("app_confirm_exit");
    } catch (error) {
      notify("error", "退出应用失败", normalizeErrorMessage(error));
      setCloseActionLoading(false);
    }
  }, [notify]);

  useEffect(() => {
    let disposed = false;
    const unlisteners: UnlistenFn[] = [];

    const pollTimer = window.setInterval(() => {
      void refreshSnapshot();
      void refreshSessionSnapshot();
      void refreshHostLogs();
      void refreshDiagnoseSnapshot();
      void refreshDiagnoseLogs();
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
        const runtimeUnlisten = await registerManagedListener<AgentRuntimeChangedEvent>(
          listen,
          "agent-runtime-changed",
          (payload) => {
            setRuntimeSnapshot(payload.snapshot);
          },
          () => disposed,
        );
        if (runtimeUnlisten) {
          unlisteners.push(runtimeUnlisten);
        }
        const closeRequestUnlisten = await registerManagedListener<unknown>(
          listen,
          APP_CLOSE_REQUESTED_EVENT,
          () => {
            setCloseConfirmOpen(true);
          },
          () => disposed,
        );
        if (closeRequestUnlisten) {
          unlisteners.push(closeRequestUnlisten);
        }
      } catch (error) {
        if (!disposed) {
          notify("error", "事件订阅失败", normalizeErrorMessage(error));
        }
      }
    })();

    return () => {
      disposed = true;
      window.clearInterval(pollTimer);
      unlisteners.forEach((unlisten) => {
        void unlisten();
      });
    };
  }, [
    bootstrap,
    refreshDiagnoseLogs,
    refreshDiagnoseSnapshot,
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
    setSettingsBridgeAddressMemory(buildBridgeTransportAddressMemory(hostConfig));
    setDiagnoseCategoryFilter(diagnoseCategoryFilterFromHostConfig(hostConfig));
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
  const filteredTunnels = useMemo(() => {
    const dedup = new Map<string, TunnelListItem>();
    tunnelItems.forEach((item) => {
      const current = dedup.get(item.tunnel_id);
      if (!current || item.updated_at_ms >= current.updated_at_ms) {
        dedup.set(item.tunnel_id, item);
      }
    });
    return Array.from(dedup.values()).sort((left, right) =>
      right.updated_at_ms - left.updated_at_ms || left.tunnel_id.localeCompare(right.tunnel_id),
    );
  }, [tunnelItems]);

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
  const diagnoseCategoryAllEnabled =
    diagnoseCategoryFilter.ipc && diagnoseCategoryFilter.bridge && diagnoseCategoryFilter.tunnel;

  const diagnoseSourceLogs = useMemo(() => {
    const hostDiagnoseLikeLogs: DiagnoseLogEntry[] = hostLogs.map((log) => ({
      ts_ms: log.ts_ms,
      level: log.level,
      module: log.module,
      code: log.code,
      message: log.message,
      session_id: undefined,
      session_epoch: undefined,
      bridge_state: undefined,
      request_id: undefined,
      trigger: undefined,
      reason: undefined,
    }));
    return [...diagnoseLogs, ...hostDiagnoseLikeLogs]
      .sort((left, right) => right.ts_ms - left.ts_ms)
      .slice(0, 160);
  }, [diagnoseLogs, hostLogs]);

  useEffect(() => {
    setDiagnoseKnownAndObservedCodes((previousCodes) => {
      const mergedCodes = new Set(previousCodes.map((item) => item.trim()).filter((item) => item.length > 0));
      let changed = false;
      diagnoseSourceLogs.forEach((log) => {
        const normalizedCode = log.code.trim();
        if (!normalizedCode || mergedCodes.has(normalizedCode)) {
          return;
        }
        mergedCodes.add(normalizedCode);
        changed = true;
      });
      if (!changed) {
        return previousCodes;
      }
      return Array.from(mergedCodes).sort((left, right) => left.localeCompare(right));
    });
  }, [diagnoseSourceLogs]);

  const diagnosePreFilteredLogs = useMemo(() => {
    const source = diagnoseSourceLogs;
    const threshold = logLevelRank(diagnoseMinLevel);
    return source.filter((log) => {
      if (logLevelRank(log.level) < threshold) {
        return false;
      }
      const category = classifyDiagnoseCategory(log);
      return diagnoseCategoryFilter[category];
    });
  }, [diagnoseCategoryFilter, diagnoseMinLevel, diagnoseSourceLogs]);

  const diagnoseCodeOptions = useMemo(() => {
    const countsByCode = new Map<string, number>();
    diagnosePreFilteredLogs.forEach((log) => {
      const normalizedCode = log.code.trim();
      if (!normalizedCode) {
        return;
      }
      countsByCode.set(normalizedCode, (countsByCode.get(normalizedCode) ?? 0) + 1);
    });

    const candidateCodeSet = new Set<string>();
    diagnoseKnownAndObservedCodes.forEach((code) => {
      const normalizedCode = code.trim();
      if (!normalizedCode) {
        return;
      }
      candidateCodeSet.add(normalizedCode);
    });
    diagnoseCodeFilter.forEach((selectedCode) => {
      const normalizedCode = selectedCode.trim();
      if (!normalizedCode) {
        return;
      }
      candidateCodeSet.add(normalizedCode);
    });
    countsByCode.forEach((_count, code) => {
      candidateCodeSet.add(code);
    });

    const selectedCodeSet = new Set(
      diagnoseCodeFilter.map((code) => code.trim()).filter((code) => code.length > 0),
    );
    return Array.from(candidateCodeSet)
      .map((code) => ({ code, count: countsByCode.get(code) ?? 0 }))
      .sort((left, right) => {
        const leftSelected = selectedCodeSet.has(left.code);
        const rightSelected = selectedCodeSet.has(right.code);
        if (leftSelected !== rightSelected) {
          return leftSelected ? -1 : 1;
        }
        if (left.count !== right.count) {
          return right.count - left.count;
        }
        return left.code.localeCompare(right.code);
      });
  }, [diagnoseCodeFilter, diagnoseKnownAndObservedCodes, diagnosePreFilteredLogs]);

  const diagnoseCodeFilterSet = useMemo(() => {
    const filteredCodes = diagnoseCodeFilter
      .map((code) => code.trim())
      .filter((code) => code.length > 0);
    return new Set(filteredCodes);
  }, [diagnoseCodeFilter]);

  const diagnoseCodeSearchNormalized = diagnoseCodeSearchText.trim().toLowerCase();
  const diagnoseVisibleCodeOptions = useMemo(() => {
    if (!diagnoseCodeSearchNormalized) {
      return diagnoseCodeOptions;
    }
    return diagnoseCodeOptions.filter((option) =>
      option.code.toLowerCase().includes(diagnoseCodeSearchNormalized),
    );
  }, [diagnoseCodeOptions, diagnoseCodeSearchNormalized]);

  const diagnoseDisplayLogs = useMemo(() => {
    if (diagnoseCodeFilterSet.size === 0) {
      return diagnosePreFilteredLogs;
    }
    return diagnosePreFilteredLogs.filter((log) => diagnoseCodeFilterSet.has(log.code.trim()));
  }, [diagnoseCodeFilterSet, diagnosePreFilteredLogs]);

  const diagnoseCodeFilterAllEnabled = diagnoseCodeFilterSet.size === 0;
  const diagnoseCodeFilterSummary = useMemo(() => {
    const selectedCodes = Array.from(diagnoseCodeFilterSet);
    if (selectedCodes.length === 0) {
      return "全部错误码";
    }
    if (selectedCodes.length <= 2) {
      return selectedCodes.join(", ");
    }
    return `${selectedCodes.slice(0, 2).join(", ")} +${selectedCodes.length - 2}`;
  }, [diagnoseCodeFilterSet]);

  const toggleDiagnoseCodeFilter = useCallback((code: string) => {
    const normalizedCode = code.trim();
    if (!normalizedCode) {
      return;
    }
    setDiagnoseCodeFilter((prev) => {
      const exists = prev.some((item) => item.trim() === normalizedCode);
      if (exists) {
        return prev.filter((item) => item.trim() !== normalizedCode);
      }
      return [...prev, normalizedCode];
    });
  }, []);

  useEffect(() => {
    if (!diagnoseCodeDropdownOpen) {
      return;
    }
    const handlePointerDown = (event: MouseEvent) => {
      if (!diagnoseCodeDropdownRef.current) {
        return;
      }
      const target = event.target;
      if (target instanceof Node && diagnoseCodeDropdownRef.current.contains(target)) {
        return;
      }
      setDiagnoseCodeDropdownOpen(false);
    };
    const handleEscape = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        setDiagnoseCodeDropdownOpen(false);
      }
    };
    window.addEventListener("mousedown", handlePointerDown);
    window.addEventListener("keydown", handleEscape);
    return () => {
      window.removeEventListener("mousedown", handlePointerDown);
      window.removeEventListener("keydown", handleEscape);
    };
  }, [diagnoseCodeDropdownOpen]);

  useEffect(() => {
    if (activeNav !== "diagnose") {
      setDiagnoseCodeDropdownOpen(false);
    }
  }, [activeNav]);

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
      settingsDraft.bridgeTLSEnabled !== hostConfig.bridge_tls_enabled ||
      settingsDraft.bridgeTLSRootCAFile.trim() !== hostConfig.bridge_tls_root_ca_file ||
      settingsDraft.bridgeTLSServerName.trim() !== hostConfig.bridge_tls_server_name ||
      settingsDraft.endpoint.trim() !== hostConfig.ipc_endpoint ||
      settingsDraft.tunnelPoolMinIdleText.trim() !== String(hostConfig.tunnel_pool_min_idle) ||
      settingsDraft.tunnelPoolMaxIdleText.trim() !== String(hostConfig.tunnel_pool_max_idle) ||
      settingsDraft.tunnelPoolMaxInflightText.trim() !== String(hostConfig.tunnel_pool_max_inflight) ||
      settingsDraft.tunnelPoolTtlSecText.trim() !== formatTTLSecondsText(hostConfig.tunnel_pool_ttl_ms) ||
      settingsDraft.tunnelPoolOpenRateText.trim() !== String(hostConfig.tunnel_pool_open_rate) ||
      settingsDraft.tunnelPoolOpenBurstText.trim() !== String(hostConfig.tunnel_pool_open_burst) ||
      settingsDraft.tunnelPoolReconcileGapMsText.trim() !== String(hostConfig.tunnel_pool_reconcile_gap_ms)
    );
  }, [hostConfig, settingsDraft]);
  const settingsBridgeTransportGuide = useMemo(
    () => buildBridgeTransportGuide(settingsDraft?.transport ?? "", settingsDraft?.bridgeAddr ?? ""),
    [settingsDraft],
  );
  const activeBridgeTransportGuide = useMemo(
    () => buildBridgeTransportGuide(hostConfig?.bridge_transport ?? "", hostConfig?.bridge_addr ?? ""),
    [hostConfig],
  );

  const handleBridgeTransportChange = useCallback((nextTransport: string) => {
    setSettingsDraft((prev) => {
      if (!prev) {
        return prev;
      }
      const nextSelection = applyBridgeTransportSelection({
        currentTransport: prev.transport,
        currentBridgeAddr: prev.bridgeAddr,
        nextTransport,
        memory: settingsBridgeAddressMemory,
      });
      setSettingsBridgeAddressMemory(nextSelection.memory);
      return {
        ...prev,
        transport: nextTransport,
        bridgeAddr: nextSelection.bridgeAddr,
      };
    });
  }, [settingsBridgeAddressMemory]);

  const handleBridgeAddrChange = useCallback((nextBridgeAddr: string) => {
    setSettingsDraft((prev) => (prev ? { ...prev, bridgeAddr: nextBridgeAddr } : prev));
    setSettingsBridgeAddressMemory((prevMemory) =>
      rememberBridgeAddressForTransport(settingsDraft?.transport ?? "", nextBridgeAddr, prevMemory),
    );
  }, [settingsDraft?.transport]);

  const resetSettingsDraft = useCallback(() => {
    if (!hostConfig) {
      return;
    }
    setSettingsDraft(toSettingsDraft(hostConfig));
    setSettingsBridgeAddressMemory(buildBridgeTransportAddressMemory(hostConfig));
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
        bridge_tls_enabled: settingsDraft.bridgeTLSEnabled,
        bridge_tls_root_ca_file: normalizeOptionalText(settingsDraft.bridgeTLSRootCAFile),
        bridge_tls_server_name: normalizeOptionalText(settingsDraft.bridgeTLSServerName),
        tunnel_pool_min_idle: parseNonNegativeInteger(
          settingsDraft.tunnelPoolMinIdleText,
          "tunnel_pool_min_idle",
        ),
        tunnel_pool_max_idle: parsePositiveInteger(settingsDraft.tunnelPoolMaxIdleText, "tunnel_pool_max_idle"),
        tunnel_pool_max_inflight: parsePositiveInteger(
          settingsDraft.tunnelPoolMaxInflightText,
          "tunnel_pool_max_inflight",
        ),
        tunnel_pool_ttl_ms: parseNonNegativeSecondsToMillis(settingsDraft.tunnelPoolTtlSecText, "tunnel_pool_ttl_s"),
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
      setSettingsBridgeAddressMemory(buildBridgeTransportAddressMemory(snapshot));
      notify("success", "配置保存成功", "已写入本地 YAML 文件，重连/重启后生效");
      await refreshHostLogs();
    } catch (error) {
      notify("error", "配置保存失败", normalizeErrorMessage(error));
    } finally {
      setSavingSettings(false);
    }
  }, [hostConfig, notify, refreshHostLogs, settingsDraft]);

  const closeServiceForm = useCallback(() => {
    setServiceFormOpen(false);
    setServiceFormMode("create");
    setServiceEditingID(null);
    setServiceCreateDraft(DEFAULT_SERVICE_CREATE_DRAFT);
  }, []);

  const openCreateServiceForm = useCallback(() => {
    setServiceFormMode("create");
    setServiceEditingID(null);
    setServiceCreateDraft(DEFAULT_SERVICE_CREATE_DRAFT);
    setServiceFormOpen(true);
  }, []);

  const openEditServiceForm = useCallback((item: ServiceListItem) => {
    const routeHintDraft = routeHintToDraft(item.route_hint);
    const exposureDraft = exposureToDraft(item.protocol || "tcp", item.exposure);
    setServiceFormMode("edit");
    setServiceEditingID(item.instance_id);
    setServiceCreateDraft({
      instanceId: item.instance_id,
      serviceName: item.service_name,
      namespace: item.scope?.namespace || "",
      environment: item.scope?.environment || "",
      protocol: item.protocol || "tcp",
      host: item.host || "127.0.0.1",
      portText: String(item.port > 0 ? item.port : 8080),
      sniName: item.sni_name || "",
      exposureEnabled: exposureDraft.exposureEnabled,
      exposureMode: exposureDraft.exposureMode,
      exposureHost: exposureDraft.exposureHost,
      exposureListenPortText: exposureDraft.exposureListenPortText,
      exposureSniName: exposureDraft.exposureSniName,
      exposurePathPrefix: exposureDraft.exposurePathPrefix,
      exposureAllowExport: exposureDraft.exposureAllowExport,
      routePriorityText: routeHintDraft.routePriorityText,
      headerMatchers: routeHintDraft.headerMatchers,
      queryMatchers: routeHintDraft.queryMatchers,
    });
    setServiceFormOpen(true);
  }, []);

  const handleServiceFormOpenChange = useCallback((nextOpen: boolean) => {
    if (!nextOpen && !creatingService) {
      closeServiceForm();
    }
  }, [closeServiceForm, creatingService]);

  const submitServiceForm = useCallback(async () => {
    const serviceName = serviceCreateDraft.serviceName.trim();
    const namespace = serviceCreateDraft.namespace.trim();
    const environment = serviceCreateDraft.environment.trim();
    const sniName = serviceCreateDraft.sniName.trim();
    const normalizedProtocol = serviceCreateDraft.protocol.trim().toLowerCase();
    const normalizedExposureMode = normalizeExposureModeForProtocol(
      normalizedProtocol,
      serviceCreateDraft.exposureMode,
    );
    if (!serviceName) {
      notify("warning", "参数不完整", "请输入服务名称");
      return;
    }
    if (serviceName.includes("/")) {
      notify("warning", "参数不合法", "服务名称不能包含 /");
      return;
    }
    if (!["tcp", "http", "https"].includes(normalizedProtocol)) {
      notify("warning", "参数不合法", "协议仅支持 tcp / http / https");
      return;
    }
    if (!namespace) {
      notify("warning", "参数不完整", "请输入命名空间");
      return;
    }
    if (!environment) {
      notify("warning", "参数不完整", "请输入环境");
      return;
    }
    if (!serviceCreateDraft.host.trim()) {
      notify("warning", "参数不完整", "请输入主机地址");
      return;
    }
    let port: number;
    try {
      port = parsePositiveInteger(serviceCreateDraft.portText, "port");
    } catch (error) {
      notify("warning", "参数不合法", normalizeErrorMessage(error));
      return;
    }
    if (port > 65535) {
      notify("warning", "参数不合法", "port 不能超过 65535");
      return;
    }
    let exposure: ServiceExposureInput | undefined;
    try {
      if (serviceCreateDraft.exposureEnabled) {
        const exposurePayload: ServiceExposureInput = {
          ingress_mode: normalizedExposureMode,
          allow_export: serviceCreateDraft.exposureAllowExport,
        };
        const exposureListenPortText = serviceCreateDraft.exposureListenPortText.trim();
        if (exposureListenPortText) {
          const exposureListenPort = parsePositiveInteger(exposureListenPortText, "exposure.listen_port");
          if (exposureListenPort > 65535) {
            notify("warning", "参数不合法", "exposure.listen_port 不能超过 65535");
            return;
          }
          exposurePayload.listen_port = exposureListenPort;
        }
        if (normalizedExposureMode === "l7_shared") {
          const exposureHost = serviceCreateDraft.exposureHost.trim();
          const exposurePathPrefix = serviceCreateDraft.exposurePathPrefix.trim();
          if (exposureHost) {
            exposurePayload.host = exposureHost;
          }
          if (exposurePathPrefix && !exposurePathPrefix.startsWith("/")) {
            notify("warning", "参数不合法", "入口 path_prefix 必须以 / 开头");
            return;
          }
          exposurePayload.path_prefix = exposurePathPrefix || "/";
        } else if (normalizedExposureMode === "tls_sni_shared") {
          const exposureSniName = serviceCreateDraft.exposureSniName.trim();
          if (!exposureSniName) {
            notify("warning", "参数不完整", "tls_sni_shared 模式需要填写入口 SNI");
            return;
          }
          exposurePayload.sni_name = exposureSniName;
        } else if (normalizedExposureMode === "l4_dedicated_port" && !exposurePayload.listen_port) {
          notify("warning", "参数不完整", "l4_dedicated_port 模式需要填写入口监听端口");
          return;
        }
        exposure = exposurePayload;
      }
    } catch (error) {
      notify("warning", "入口暴露配置不合法", normalizeErrorMessage(error));
      return;
    }
    const supportsRouteHint = (normalizedProtocol === "http" || normalizedProtocol === "https")
      && (!serviceCreateDraft.exposureEnabled || normalizedExposureMode === "l7_shared");
    let routeHint: RouteHintInput | undefined;
    try {
      if (supportsRouteHint) {
        const priority = parseRouteHintPriority(serviceCreateDraft.routePriorityText);
        const matchHeaders = buildRouteHintMatcherPayloads(serviceCreateDraft.headerMatchers, "Header 匹配");
        const matchQueries = buildRouteHintMatcherPayloads(serviceCreateDraft.queryMatchers, "Query 匹配");
        if (priority > 0 || matchHeaders.length > 0 || matchQueries.length > 0) {
          routeHint = {
            priority,
            match_headers: matchHeaders.length > 0 ? matchHeaders : undefined,
            match_queries: matchQueries.length > 0 ? matchQueries : undefined,
          };
        }
      }
    } catch (error) {
      notify("warning", "高级路由配置不合法", normalizeErrorMessage(error));
      return;
    }
    const normalizedInstanceID = serviceFormMode === "edit"
      ? (serviceEditingID?.trim() || serviceCreateDraft.instanceId.trim())
      : serviceCreateDraft.instanceId.trim();
    if (serviceFormMode === "edit" && !normalizedInstanceID) {
      notify("warning", "参数不完整", "编辑模式缺少 instance_id");
      return;
    }
    const payload: ServiceAddInput = {
      instance_id: normalizedInstanceID || undefined,
      scope: {
        namespace: namespace || undefined,
        environment: environment || undefined,
      },
      service_name: serviceName,
      protocol: normalizedProtocol,
      host: serviceCreateDraft.host.trim(),
      port,
      sni_name: normalizedProtocol === "https" ? (sniName || undefined) : undefined,
      exposure,
      route_hint: routeHint,
    };
    setCreatingService(true);
    try {
      const created = await invoke<ServiceListItem>("service_add", { input: payload });
      setServiceItems((prev) => {
        const next = [created, ...prev.filter((item) => item.instance_id !== created.instance_id)];
        return next.sort((left, right) => right.updated_at_ms - left.updated_at_ms);
      });
      await refreshServiceList();
      notify(
        "success",
        serviceFormMode === "edit" ? "服务更新成功" : "新增服务成功",
        `${created.service_name} (${created.protocol})`,
      );
      closeServiceForm();
    } catch (error) {
      notify("error", serviceFormMode === "edit" ? "更新服务失败" : "新增服务失败", normalizeErrorMessage(error));
    } finally {
      setCreatingService(false);
    }
  }, [closeServiceForm, notify, refreshServiceList, serviceCreateDraft, serviceEditingID, serviceFormMode]);

  const deleteService = useCallback(async (item: ServiceListItem) => {
    const confirmed = globalThis.confirm(
      `确认删除服务「${item.service_name}」吗？\ninstance_id=${item.instance_id}`,
    );
    if (!confirmed) {
      return;
    }
    setDeletingServiceID(item.instance_id);
    try {
      const payload: ServiceDeleteInput = {
        logical_service_id: item.logical_service_id || undefined,
        instance_id: item.instance_id || undefined,
        scope: {
          namespace: item.scope?.namespace || undefined,
          environment: item.scope?.environment || undefined,
        },
        service_name: item.service_name || undefined,
      };
      const result = await invoke<ServiceDeleteResult>("service_delete", { input: payload });
      if (result.deleted) {
        setServiceItems((prev) => prev.filter((service) => service.instance_id !== item.instance_id));
        if (serviceEditingID === item.instance_id) {
          closeServiceForm();
        }
        notify("success", "服务删除成功", item.service_name);
      } else {
        notify("warning", "服务不存在或已删除", item.service_name);
      }
      await refreshServiceList();
    } catch (error) {
      notify("error", "删除服务失败", normalizeErrorMessage(error));
    } finally {
      setDeletingServiceID(null);
    }
  }, [closeServiceForm, notify, refreshServiceList, serviceEditingID]);

  const renderServicesTable = (): JSX.Element => (
    <Card className="overflow-hidden">
      <CardHeader className="flex flex-row items-center justify-between pb-3">
        <div>
          <CardTitle className="text-[28px] leading-none tracking-[-0.02em]">服务列表</CardTitle>
          <CardDescription className="mt-1 text-xs">快照来源 `service_list_snapshot`</CardDescription>
        </div>
        <Button
          className="h-9 rounded-lg bg-[#1f67e5] px-4 text-sm font-semibold hover:bg-[#1a58c7]"
          onClick={openCreateServiceForm}
        >
          + 新增服务
        </Button>
      </CardHeader>
      <CardContent className="p-0">
        <div className="overflow-x-auto">
          <table className="min-w-full border-separate border-spacing-0">
            <thead>
              <tr className="bg-[#f4f6fb]">
                <th className={TABLE_HEAD_CLASS}>逻辑服务 ID</th>
                <th className={TABLE_HEAD_CLASS}>实例 ID</th>
                <th className={TABLE_HEAD_CLASS}>Scope</th>
                <th className={TABLE_HEAD_CLASS}>名称</th>
                <th className={TABLE_HEAD_CLASS}>协议</th>
                <th className={TABLE_HEAD_CLASS}>地址</th>
                <th className={TABLE_HEAD_CLASS}>SNI</th>
                <th className={TABLE_HEAD_CLASS}>Endpoint 数</th>
                <th className={TABLE_HEAD_CLASS}>状态</th>
                <th className={TABLE_HEAD_CLASS}>更新时间</th>
                <th className={TABLE_HEAD_CLASS}>操作</th>
              </tr>
            </thead>
            <tbody>
              {filteredServices.length === 0 ? (
                <tr>
                  <td className="px-4 py-8 text-center text-sm text-[#7c879e]" colSpan={11}>
                    当前没有可展示的服务
                  </td>
                </tr>
              ) : null}
              {filteredServices.map((item) => (
                <tr key={item.instance_id} className="border-t border-[#edf1f8]">
                  <td className={TABLE_CELL_CLASS}>{item.logical_service_id || "--"}</td>
                  <td className={TABLE_CELL_CLASS}>{item.instance_id}</td>
                  <td className={TABLE_CELL_CLASS}>{formatScopeText(item.scope)}</td>
                  <td className={TABLE_CELL_CLASS}>
                    <div className="space-y-1">
                      <div>{item.service_name}</div>
                      {(item.protocol === "http" || item.protocol === "https") ? (
                        <div className="text-xs text-[#7b89a1]">{formatRouteHintSummary(item.route_hint)}</div>
                      ) : null}
                    </div>
                  </td>
                  <td className={TABLE_CELL_CLASS}>
                    <div className="space-y-1">
                      <div>{item.protocol || "--"}</div>
                      {(item.protocol === "http" || item.protocol === "https") && hasRouteHint(item.route_hint) ? (
                        <span className="inline-flex rounded-full border border-[#d6e4fb] bg-[#f3f8ff] px-2 py-0.5 text-[11px] font-medium text-[#3365b6]">
                          route_hint
                        </span>
                      ) : null}
                    </div>
                  </td>
                  <td className={TABLE_CELL_CLASS}>
                    <div className="space-y-1">
                      <div>{item.host ? `${item.host}${item.port > 0 ? `:${item.port}` : ""}` : "--"}</div>
                      <div className="text-xs text-[#7b89a1]">
                        {formatServiceExposureSummary(item.protocol, item.exposure)}
                      </div>
                    </div>
                  </td>
                  <td className={TABLE_CELL_CLASS}>{item.sni_name || "--"}</td>
                  <td className={TABLE_CELL_CLASS}>{item.endpoint_count}</td>
                  <td className={TABLE_CELL_CLASS}>
                    <Badge variant={serviceVariant(item.status)} title={item.status}>
                      {formatServiceStatus(item.status)}
                    </Badge>
                  </td>
                  <td className={TABLE_CELL_CLASS}>{formatDateTime(item.updated_at_ms)}</td>
                  <td className={TABLE_CELL_CLASS}>
                    <div className="flex items-center gap-2">
                      <Button
                        variant="outline"
                        className="h-7 rounded-md px-2.5 text-xs"
                        onClick={() => openEditServiceForm(item)}
                        disabled={deletingServiceID === item.instance_id}
                      >
                        编辑
                      </Button>
                      <Button
                        variant="destructive"
                        className="h-7 rounded-md px-2.5 text-xs"
                        disabled={deletingServiceID === item.instance_id}
                        onClick={() => void deleteService(item)}
                      >
                        {deletingServiceID === item.instance_id ? "删除中..." : "删除"}
                      </Button>
                    </div>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </CardContent>
    </Card>
  );

  const renderServiceFormModal = (): JSX.Element => {
    const normalizedExposureMode = normalizeExposureModeForProtocol(
      serviceCreateDraft.protocol,
      serviceCreateDraft.exposureMode,
    );
    const supportsRouteHint = (serviceCreateDraft.protocol === "http" || serviceCreateDraft.protocol === "https")
      && (!serviceCreateDraft.exposureEnabled || normalizedExposureMode === "l7_shared");
    const supportsTLSUpstream = serviceCreateDraft.protocol === "https";
    const exposureModeOptions: Array<{ value: ServiceExposureMode; label: string }> = serviceCreateDraft.protocol === "https"
      ? [
          { value: "l7_shared", label: "L7 共享入口" },
          { value: "tls_sni_shared", label: "TLS SNI 共享入口" },
          { value: "l4_dedicated_port", label: "L4 专属端口" },
        ]
      : serviceCreateDraft.protocol === "http"
        ? [
            { value: "l7_shared", label: "L7 共享入口" },
            { value: "l4_dedicated_port", label: "L4 专属端口" },
          ]
        : [
            { value: "l4_dedicated_port", label: "L4 专属端口" },
          ];
    const updateMatcherDraft = (
      field: "headerMatchers" | "queryMatchers",
      matcherID: string,
      patch: Partial<RouteHintMatcherDraft>,
    ) => {
      setServiceCreateDraft((prev) => ({
        ...prev,
        [field]: prev[field].map((item) => (item.id === matcherID ? { ...item, ...patch } : item)),
      }));
    };
    const removeMatcherDraft = (field: "headerMatchers" | "queryMatchers", matcherID: string) => {
      setServiceCreateDraft((prev) => ({
        ...prev,
        [field]: prev[field].filter((item) => item.id !== matcherID),
      }));
    };
    const addMatcherDraft = (field: "headerMatchers" | "queryMatchers") => {
      setServiceCreateDraft((prev) => ({
        ...prev,
        [field]: [...prev[field], createRouteHintMatcherDraft()],
      }));
    };
    const renderMatcherEditor = (
      title: string,
      drafts: RouteHintMatcherDraft[],
      field: "headerMatchers" | "queryMatchers",
      placeholder: string,
    ): JSX.Element => (
      <div className="rounded-xl border border-[#dbe4f3] bg-white p-3">
        <div className="mb-3 flex items-center justify-between gap-3">
          <div>
            <h4 className="text-sm font-semibold text-[#24324d]">{title}</h4>
            <p className="mt-1 text-xs text-[#7b89a1]">支持 exact / prefix / regex / present 四种匹配方式。</p>
          </div>
          <Button
            type="button"
            variant="outline"
            className="h-8 rounded-lg px-3 text-xs"
            onClick={() => addMatcherDraft(field)}
          >
            + 新增条件
          </Button>
        </div>
        {drafts.length === 0 ? (
          <div className="rounded-lg border border-dashed border-[#dbe4f3] bg-[#fbfdff] px-3 py-4 text-xs text-[#8090aa]">
            暂无条件。仅当你需要更细粒度的 HTTP 匹配时再添加。
          </div>
        ) : null}
        <div className="space-y-3">
          {drafts.map((draft, index) => (
            <div key={draft.id} className="rounded-lg border border-[#e6edf8] bg-[#fbfdff] p-3">
              <div className="mb-2 flex items-center justify-between">
                <span className="text-xs font-medium text-[#61708b]">{title} #{index + 1}</span>
                <button
                  type="button"
                  className="text-xs font-medium text-[#b14d4d]"
                  onClick={() => removeMatcherDraft(field, draft.id)}
                >
                  删除
                </button>
              </div>
              <div className="grid grid-cols-1 gap-3 md:grid-cols-[1.4fr_0.9fr_1.7fr]">
                <Input
                  placeholder={placeholder}
                  value={draft.name}
                  onChange={(event) => updateMatcherDraft(field, draft.id, { name: event.target.value })}
                  className="h-9 rounded-lg bg-white"
                />
                <select
                  value={draft.mode}
                  className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]"
                  onChange={(event) => updateMatcherDraft(field, draft.id, {
                    mode: event.target.value as RouteHintMatcherMode,
                    value: event.target.value === "present" ? "" : draft.value,
                  })}
                >
                  <option value="exact">exact</option>
                  <option value="prefix">prefix</option>
                  <option value="regex">regex</option>
                  <option value="present">present</option>
                </select>
                {draft.mode === "present" ? (
                  <div className="flex h-9 items-center rounded-lg border border-dashed border-[#d8dfeb] bg-[#f8fbff] px-3 text-xs text-[#74839e]">
                    仅要求该项存在，不比较具体值
                  </div>
                ) : (
                  <Input
                    placeholder={draft.mode === "regex" ? "例如 ^/api/" : "填写匹配值"}
                    value={draft.value}
                    onChange={(event) => updateMatcherDraft(field, draft.id, { value: event.target.value })}
                    className="h-9 rounded-lg bg-white"
                  />
                )}
              </div>
            </div>
          ))}
        </div>
      </div>
    );

    return (
      <Modal
        open={serviceFormOpen}
        onOpenChange={handleServiceFormOpenChange}
        title={serviceFormMode === "edit" ? "编辑服务" : "新增服务"}
        description="保存后会立即写入本地服务目录，并在 Bridge 会话可用时自动尝试同步。"
        className="max-w-[980px]"
      >
        <div className="space-y-4">
          {serviceFormMode === "edit" && serviceEditingID ? (
            <div className="inline-flex rounded-md border border-[#d9e2f2] bg-[#f7faff] px-2.5 py-1 text-[11px] text-[#657391]">
              instance_id: {serviceEditingID}
            </div>
          ) : null}
          <form
            className="space-y-4"
            onSubmit={(event) => {
              event.preventDefault();
              void submitServiceForm();
            }}
          >
            <ServiceFormSection
              title="身份信息"
              description="定义这条服务是谁。当前注册协议要求 scope.namespace 和 scope.environment 都明确给出。"
            >
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <SettingsField label="服务名称" hint="必填" help={SERVICE_FORM_FIELD_HELP.serviceName}>
                  <Input
                    placeholder="例如 order-service"
                    value={serviceCreateDraft.serviceName}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, serviceName: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                  />
                </SettingsField>
                <SettingsField
                  label="实例 ID"
                  hint={serviceFormMode === "edit" ? "编辑模式固定" : "可选"}
                  help={SERVICE_FORM_FIELD_HELP.instanceId}
                >
                  <Input
                    placeholder="例如 inst-order"
                    value={serviceCreateDraft.instanceId}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, instanceId: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                    disabled={serviceFormMode === "edit"}
                  />
                </SettingsField>
                <SettingsField label="命名空间" hint="必填" help={SERVICE_FORM_FIELD_HELP.namespace}>
                  <Input
                    placeholder="例如 dev"
                    value={serviceCreateDraft.namespace}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, namespace: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                  />
                </SettingsField>
                <SettingsField label="环境" hint="必填" help={SERVICE_FORM_FIELD_HELP.environment}>
                  <Input
                    placeholder="例如 demo"
                    value={serviceCreateDraft.environment}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, environment: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                  />
                </SettingsField>
              </div>
            </ServiceFormSection>

            <ServiceFormSection
              title="服务配置"
              description="定义本地 upstream 的接入地址和协议。SNI 只在 `https` upstream 下生效。"
            >
              <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                <SettingsField label="协议" hint="tcp / http / https" help={SERVICE_FORM_FIELD_HELP.protocol}>
                  <select
                    value={serviceCreateDraft.protocol}
                    className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]"
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => {
                        const nextProtocol = event.target.value;
                        return {
                          ...prev,
                          protocol: nextProtocol,
                          exposureMode: normalizeExposureModeForProtocol(nextProtocol, prev.exposureMode),
                        };
                      })
                    }
                  >
                    <option value="tcp">tcp</option>
                    <option value="http">http</option>
                    <option value="https">https</option>
                  </select>
                </SettingsField>
                <SettingsField label="主机地址" hint="如 127.0.0.1" help={SERVICE_FORM_FIELD_HELP.host}>
                  <Input
                    placeholder="例如 127.0.0.1"
                    value={serviceCreateDraft.host}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, host: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                  />
                </SettingsField>
                <SettingsField label="端口" hint="1 - 65535" help={SERVICE_FORM_FIELD_HELP.port}>
                  <Input
                    placeholder="例如 8080"
                    value={serviceCreateDraft.portText}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, portText: event.target.value }))
                    }
                    inputMode="numeric"
                    className="h-9 rounded-lg bg-white"
                  />
                </SettingsField>
                <SettingsField
                  label="SNI (可选)"
                  hint={supportsTLSUpstream ? "仅 https 生效" : "当前协议不使用"}
                  help={SERVICE_FORM_FIELD_HELP.sniName}
                >
                  <Input
                    placeholder={supportsTLSUpstream ? "例如 order.dev.example.com" : "切换到 https 后可填写"}
                    value={serviceCreateDraft.sniName}
                    onChange={(event) =>
                      setServiceCreateDraft((prev) => ({ ...prev, sniName: event.target.value }))
                    }
                    className="h-9 rounded-lg bg-white"
                    disabled={!supportsTLSUpstream}
                  />
                </SettingsField>
              </div>
            </ServiceFormSection>

            <ServiceFormSection
              title="入口暴露"
              description="可选声明服务应挂在哪个 Bridge 入口。HTTP/HTTPS 留空时仍可沿用默认自动派生策略。"
            >
              <div className="space-y-3">
                <div className="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    className={cn(
                      "inline-flex h-9 items-center gap-2 rounded-full border px-4 text-sm font-medium transition",
                      serviceCreateDraft.exposureEnabled
                        ? "border-[#2d6be6] bg-[#edf4ff] text-[#2458bf]"
                        : "border-[#d8dfeb] bg-white text-[#5d6a83]",
                    )}
                    onClick={() =>
                      setServiceCreateDraft((prev) => ({
                        ...prev,
                        exposureEnabled: !prev.exposureEnabled,
                        exposureMode: normalizeExposureModeForProtocol(prev.protocol, prev.exposureMode),
                      }))
                    }
                  >
                    {serviceCreateDraft.exposureEnabled ? <Check className="h-4 w-4" /> : null}
                    {serviceCreateDraft.exposureEnabled ? "已声明 exposure" : "启用入口暴露"}
                  </button>
                  <span className="text-xs text-[#7b89a1]">
                    {buildExposureDraftSummary(serviceCreateDraft)}
                  </span>
                </div>
                {serviceCreateDraft.exposureEnabled ? (
                  <div className="space-y-3">
                    <div className="rounded-xl border border-[#dce7f8] bg-[#f7fbff] px-3 py-3 text-xs leading-6 text-[#5e6f8e]">
                      入口模式会决定 Bridge 自动派生 Route 时使用 host/path、sni 还是 listen_port。只有 `l7_shared`
                      模式下，`route_hint` 才会参与路由匹配。
                    </div>
                    <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                      <SettingsField label="入口模式" hint="必填" help={SERVICE_FORM_FIELD_HELP.exposureMode}>
                        <select
                          value={normalizedExposureMode}
                          className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]"
                          onChange={(event) =>
                            setServiceCreateDraft((prev) => ({
                              ...prev,
                              exposureMode: event.target.value as ServiceExposureMode,
                            }))
                          }
                        >
                          {exposureModeOptions.map((option) => (
                            <option key={option.value} value={option.value}>
                              {option.label}
                            </option>
                          ))}
                        </select>
                      </SettingsField>
                      <SettingsField
                        label="允许导出"
                        hint="可选"
                        help={SERVICE_FORM_FIELD_HELP.exposureAllowExport}
                      >
                        <button
                          type="button"
                          className={cn(
                            "inline-flex h-9 items-center gap-2 rounded-full border px-4 text-sm font-medium transition",
                            serviceCreateDraft.exposureAllowExport
                              ? "border-[#2d6be6] bg-[#edf4ff] text-[#2458bf]"
                              : "border-[#d8dfeb] bg-white text-[#5d6a83]",
                          )}
                          onClick={() =>
                            setServiceCreateDraft((prev) => ({
                              ...prev,
                              exposureAllowExport: !prev.exposureAllowExport,
                            }))
                          }
                        >
                          {serviceCreateDraft.exposureAllowExport ? <Check className="h-4 w-4" /> : null}
                          {serviceCreateDraft.exposureAllowExport ? "已开启 allow_export" : "关闭 allow_export"}
                        </button>
                      </SettingsField>
                      {normalizedExposureMode === "l7_shared" ? (
                        <>
                          <SettingsField label="入口 Host" hint="可选" help={SERVICE_FORM_FIELD_HELP.exposureHost}>
                            <Input
                              placeholder="留空则按模板自动派生"
                              value={serviceCreateDraft.exposureHost}
                              onChange={(event) =>
                                setServiceCreateDraft((prev) => ({ ...prev, exposureHost: event.target.value }))
                              }
                              className="h-9 rounded-lg bg-white"
                            />
                          </SettingsField>
                          <SettingsField label="Path Prefix" hint="默认 /" help={SERVICE_FORM_FIELD_HELP.exposurePathPrefix}>
                            <Input
                              placeholder="例如 /api/order"
                              value={serviceCreateDraft.exposurePathPrefix}
                              onChange={(event) =>
                                setServiceCreateDraft((prev) => ({ ...prev, exposurePathPrefix: event.target.value }))
                              }
                              className="h-9 rounded-lg bg-white"
                            />
                          </SettingsField>
                          <SettingsField
                            label="入口监听端口"
                            hint="可选"
                            help={SERVICE_FORM_FIELD_HELP.exposureListenPort}
                          >
                            <Input
                              placeholder="留空则使用网关共享端口"
                              value={serviceCreateDraft.exposureListenPortText}
                              onChange={(event) =>
                                setServiceCreateDraft((prev) => ({
                                  ...prev,
                                  exposureListenPortText: event.target.value,
                                }))
                              }
                              inputMode="numeric"
                              className="h-9 rounded-lg bg-white"
                            />
                          </SettingsField>
                        </>
                      ) : null}
                      {normalizedExposureMode === "tls_sni_shared" ? (
                        <>
                          <SettingsField label="入口 SNI" hint="必填" help={SERVICE_FORM_FIELD_HELP.exposureSniName}>
                            <Input
                              placeholder="例如 order.dev.example.com"
                              value={serviceCreateDraft.exposureSniName}
                              onChange={(event) =>
                                setServiceCreateDraft((prev) => ({ ...prev, exposureSniName: event.target.value }))
                              }
                              className="h-9 rounded-lg bg-white"
                            />
                          </SettingsField>
                          <SettingsField
                            label="入口监听端口"
                            hint="可选"
                            help={SERVICE_FORM_FIELD_HELP.exposureListenPort}
                          >
                            <Input
                              placeholder="留空则使用网关共享端口"
                              value={serviceCreateDraft.exposureListenPortText}
                              onChange={(event) =>
                                setServiceCreateDraft((prev) => ({
                                  ...prev,
                                  exposureListenPortText: event.target.value,
                                }))
                              }
                              inputMode="numeric"
                              className="h-9 rounded-lg bg-white"
                            />
                          </SettingsField>
                        </>
                      ) : null}
                      {normalizedExposureMode === "l4_dedicated_port" ? (
                        <SettingsField
                          label="入口监听端口"
                          hint="必填"
                          help={SERVICE_FORM_FIELD_HELP.exposureListenPort}
                        >
                          <Input
                            placeholder="例如 18080"
                            value={serviceCreateDraft.exposureListenPortText}
                            onChange={(event) =>
                              setServiceCreateDraft((prev) => ({
                                ...prev,
                                exposureListenPortText: event.target.value,
                              }))
                            }
                            inputMode="numeric"
                            className="h-9 rounded-lg bg-white"
                          />
                        </SettingsField>
                      ) : null}
                    </div>
                  </div>
                ) : (
                  <div className="rounded-xl border border-dashed border-[#dbe4f3] bg-[#fbfdff] px-4 py-5 text-sm text-[#7b89a1]">
                    未显式声明 exposure。HTTP/HTTPS 服务仍可由 Bridge 按默认规则自动派生 Host 与基础 Route。
                  </div>
                )}
              </div>
            </ServiceFormSection>

            <ServiceFormSection
              title="高级路由"
              description="仅用于增强 HTTP / HTTPS 自动派生 Route 的匹配条件。纯 TCP 端口转发通常不需要这里的配置。"
            >
              <div className="mb-3 rounded-xl border border-[#e3ebf7] bg-white px-3 py-3 text-xs leading-6 text-[#6c7b95]">
                `labels` 用于实例过滤，`metadata` 用于附加语义与观测，它们不属于本地新增服务的主路径输入。为避免误用，这个入口当前只开放
                `route_hint`。
              </div>
              {supportsRouteHint ? (
                <div className="space-y-3">
                  <div className="rounded-xl border border-[#dce7f8] bg-[#f7fbff] px-3 py-3 text-xs leading-6 text-[#5e6f8e]">
                    当前摘要：{buildRouteHintDraftSummary(serviceCreateDraft)}
                  </div>
                  <SettingsField
                    label="路由优先级"
                    hint="默认 0"
                    help={SERVICE_FORM_FIELD_HELP.routePriority}
                  >
                    <Input
                      placeholder="例如 10"
                      value={serviceCreateDraft.routePriorityText}
                      onChange={(event) =>
                        setServiceCreateDraft((prev) => ({ ...prev, routePriorityText: event.target.value }))
                      }
                      inputMode="numeric"
                      className="h-9 rounded-lg bg-white"
                    />
                  </SettingsField>
                  {renderMatcherEditor("Header 匹配", serviceCreateDraft.headerMatchers, "headerMatchers", "例如 x-tenant")}
                  {renderMatcherEditor("Query 匹配", serviceCreateDraft.queryMatchers, "queryMatchers", "例如 version")}
                </div>
              ) : (
                <div className="rounded-xl border border-dashed border-[#dbe4f3] bg-[#fbfdff] px-4 py-5 text-sm text-[#7b89a1]">
                  {serviceCreateDraft.protocol === "tcp"
                    ? "当前协议为 `tcp`，不会生成基于 Header / Query 的 HTTP 路由增强条件。"
                    : `当前 exposure 模式为 \`${normalizedExposureMode}\`，只有 \`l7_shared\` 模式才会应用 route_hint。`}
                </div>
              )}
            </ServiceFormSection>

            <div className="flex items-end justify-end gap-2 pt-1">
              <Button
                type="button"
                variant="outline"
                className="h-9 rounded-lg text-xs"
                disabled={creatingService}
                onClick={closeServiceForm}
              >
                取消
              </Button>
              <Button
                type="submit"
                className="h-9 rounded-lg bg-[#1f67e5] px-4 text-xs font-semibold hover:bg-[#1a58c7]"
                disabled={creatingService}
              >
                {creatingService ? "提交中..." : serviceFormMode === "edit" ? "保存修改" : "提交新增"}
              </Button>
            </div>
          </form>
        </div>
      </Modal>
    );
  };

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
        <CardDescription className="mt-1 text-xs">
          速率表示当前每秒吞吐（B/s），累计值表示 Agent 进程启动以来的总上传/下载流量。
        </CardDescription>
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
                <th className={TABLE_HEAD_CLASS}>逻辑服务 ID</th>
                <th className={TABLE_HEAD_CLASS}>实例 ID</th>
                <th className={TABLE_HEAD_CLASS}>连接协议</th>
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
                  <td className="px-4 py-8 text-center text-sm text-[#7c879e]" colSpan={9}>
                    当前没有可展示的隧道
                  </td>
                </tr>
              ) : null}
              {filteredTunnels.map((item) => (
                <tr key={item.tunnel_id} className="border-t border-[#edf1f8]">
                  <td className={TABLE_CELL_CLASS}>{formatTunnelIDForDisplay(item.tunnel_id)}</td>
                  <td className={TABLE_CELL_CLASS}>{item.logical_service_id}</td>
                  <td className={TABLE_CELL_CLASS}>{item.instance_id}</td>
                  <td className={TABLE_CELL_CLASS}>
                    {formatTransportLabel(item.protocol || hostConfig?.bridge_transport || "")}
                  </td>
                  <td className={TABLE_CELL_CLASS}>{item.local_addr}</td>
                  <td className={TABLE_CELL_CLASS}>{item.remote_addr}</td>
                  <td className={TABLE_CELL_CLASS}>
                    <Badge variant={tunnelVariant(item.state)} title={item.state}>
                      {formatTunnelState(item.state)}
                    </Badge>
                  </td>
                  <td className={TABLE_CELL_CLASS}>{item.latency_ms > 0 ? `${item.latency_ms} ms` : "--"}</td>
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
              <SettingsField label="Agent ID" hint="必填" help={SETTINGS_FIELD_HELP.agentId}>
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
              <SettingsField label="Runtime 程序路径" hint="Agent 可执行文件路径" help={SETTINGS_FIELD_HELP.runtimeProgram}>
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
              <SettingsField label="Runtime 参数" hint="空格分隔" help={SETTINGS_FIELD_HELP.runtimeArgs}>
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
              <SettingsField label="IPC 端点" hint="按平台规则校验" help={SETTINGS_FIELD_HELP.ipcEndpoint}>
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
              <SettingsField
                label="Bridge 传输方式"
                help={SETTINGS_FIELD_HELP.bridgeTransport}
              >
                <select
                  value={settingsDraft.transport}
                  className="h-9 w-full rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]"
                  onChange={(event) => handleBridgeTransportChange(event.target.value)}
                >
                  <option value="tcp_framed">tcp_framed（已支持）</option>
                  <option value="grpc_h2">grpc_h2（已支持）</option>
                  <option value="quic_native">quic_native（已支持，需 Bridge TLS）</option>
                </select>
              </SettingsField>
              <SettingsField label="认证方式" hint="LocalRPC 握手鉴权" help={SETTINGS_FIELD_HELP.authMode}>
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
              <SettingsField label="Bridge 地址" hint="必填" help={SETTINGS_FIELD_HELP.bridgeAddr}>
                <Input
                  value={settingsDraft.bridgeAddr}
                  onChange={(event) => handleBridgeAddrChange(event.target.value)}
                  placeholder={defaultBridgeAddrForTransport(settingsDraft.transport)}
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <div className="space-y-1.5">
                <div className="flex items-end justify-between gap-3">
                  <span className="inline-flex items-center gap-1.5 text-sm font-medium text-[#44516d]">
                    Bridge TLS
                    <FieldHelpTooltip label="Bridge TLS" help={SETTINGS_FIELD_HELP.bridgeTLSEnabled} />
                  </span>
                  <span className="text-[11px] text-[#8290a8]">quic_native 下必须开启</span>
                </div>
                <label className="flex h-9 items-center justify-between rounded-lg border border-[#d8dfeb] bg-white px-3 text-sm text-[#43506b]">
                  <span>{settingsDraft.bridgeTLSEnabled ? "已启用 TLS" : "明文连接"}</span>
                  <input
                    type="checkbox"
                    checked={settingsDraft.bridgeTLSEnabled}
                    onChange={(event) =>
                      setSettingsDraft((prev) =>
                        prev ? { ...prev, bridgeTLSEnabled: event.target.checked } : prev,
                      )
                    }
                  />
                </label>
              </div>
              <SettingsField label="Bridge TLS Root CA" hint="TLS 开启时必填" help={SETTINGS_FIELD_HELP.bridgeTLSRootCAFile}>
                <Input
                  value={settingsDraft.bridgeTLSRootCAFile}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, bridgeTLSRootCAFile: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：/etc/devbridge/root-ca.crt"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="Bridge TLS Server Name" hint="可选覆盖" help={SETTINGS_FIELD_HELP.bridgeTLSServerName}>
                <Input
                  value={settingsDraft.bridgeTLSServerName}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, bridgeTLSServerName: event.target.value } : prev,
                    )
                  }
                  placeholder="例如：bridge.internal.example"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
            </div>
            {settingsBridgeTransportGuide ? (
              <div className="rounded-2xl border border-[#cfe0ff] bg-[#f4f8ff] px-4 py-3">
                <div className="flex items-center gap-2 text-sm font-semibold text-[#1c4d98]">
                  <ShieldCheck size={16} />
                  <span>{settingsBridgeTransportGuide.title}</span>
                </div>
                <div className="mt-2 space-y-1 text-xs leading-5 text-[#4b5d7f]">
                  {settingsBridgeTransportGuide.lines.map((line) => (
                    <p key={line}>{line}</p>
                  ))}
                </div>
              </div>
            ) : null}
          </section>

          <section className="space-y-3">
            <h4 className="text-sm font-semibold uppercase tracking-[0.08em] text-[#5f6d87]">Tunnel 池参数</h4>
            <div className="grid grid-cols-1 gap-3 lg:grid-cols-2">
              <SettingsField label="最小空闲数" help={SETTINGS_FIELD_HELP.minIdle}>
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
              <SettingsField label="最大空闲数" help={SETTINGS_FIELD_HELP.maxIdle}>
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
              <SettingsField label="最大并发打开数" help={SETTINGS_FIELD_HELP.maxInflight}>
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
              <SettingsField label="TTL（秒）" hint="0 表示禁用 TTL 回收" help={SETTINGS_FIELD_HELP.ttlSeconds}>
                <Input
                  value={settingsDraft.tunnelPoolTtlSecText}
                  onChange={(event) =>
                    setSettingsDraft((prev) =>
                      prev ? { ...prev, tunnelPoolTtlSecText: event.target.value } : prev,
                    )
                  }
                  inputMode="decimal"
                  className="h-9 rounded-lg"
                />
              </SettingsField>
              <SettingsField label="打开速率" help={SETTINGS_FIELD_HELP.openRate}>
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
              <SettingsField label="打开突发值" help={SETTINGS_FIELD_HELP.openBurst}>
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
              <SettingsField label="对账间隔（毫秒）" help={SETTINGS_FIELD_HELP.reconcileGapMs}>
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
    <Card className="flex h-full min-h-0 flex-col">
      <CardHeader>
        <CardTitle className="text-[27px] leading-none tracking-[-0.01em]">日志与诊断</CardTitle>
        <CardDescription className="text-xs">优先展示 runtime 诊断事件，宿主日志作为兜底补充</CardDescription>
      </CardHeader>
      <CardContent className="flex min-h-0 flex-1 flex-col">
        <div className="mb-3 grid grid-cols-1 gap-2 md:grid-cols-4">
          <InfoRow label="诊断状态" value={diagnoseSnapshot?.state ?? "--"} compact />
          <InfoRow label="事件总数" value={String(diagnoseSnapshot?.event_total ?? 0)} compact />
          <InfoRow label="错误事件" value={String(diagnoseSnapshot?.event_error_count ?? 0)} compact />
          <InfoRow label="补池事件" value={String(diagnoseSnapshot?.event_refill_total ?? 0)} compact />
        </div>
        <div className="mb-3 space-y-2">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="text-xs text-[#5d6983]">操作类型</span>
              <button
                type="button"
                className={cn(
                  "h-7 rounded-md border px-2.5 text-xs font-medium transition",
                  diagnoseCategoryAllEnabled
                    ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                    : "border-[#d8dfeb] bg-white text-[#65718c] hover:border-[#bcc9df] hover:text-[#3f4b66]",
                )}
                onClick={() =>
                  applyDiagnoseCategoryFilter(() => ({
                    ipc: true,
                    bridge: true,
                    tunnel: true,
                  }))
                }
              >
                全部
              </button>
              <button
                type="button"
                className={cn(
                  "h-7 rounded-md border px-2.5 text-xs font-medium transition",
                  diagnoseCategoryFilter.ipc
                    ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                    : "border-[#d8dfeb] bg-white text-[#65718c] hover:border-[#bcc9df] hover:text-[#3f4b66]",
                )}
                onClick={() =>
                  applyDiagnoseCategoryFilter((prev) => {
                    const enabledCount = Number(prev.ipc) + Number(prev.bridge) + Number(prev.tunnel);
                    if (prev.ipc && enabledCount === 1) {
                      return prev;
                    }
                    return { ...prev, ipc: !prev.ipc };
                  })
                }
              >
                IPC
              </button>
              <button
                type="button"
                className={cn(
                  "h-7 rounded-md border px-2.5 text-xs font-medium transition",
                  diagnoseCategoryFilter.bridge
                    ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                    : "border-[#d8dfeb] bg-white text-[#65718c] hover:border-[#bcc9df] hover:text-[#3f4b66]",
                )}
                onClick={() =>
                  applyDiagnoseCategoryFilter((prev) => {
                    const enabledCount = Number(prev.ipc) + Number(prev.bridge) + Number(prev.tunnel);
                    if (prev.bridge && enabledCount === 1) {
                      return prev;
                    }
                    return { ...prev, bridge: !prev.bridge };
                  })
                }
              >
                Bridge
              </button>
              <button
                type="button"
                className={cn(
                  "h-7 rounded-md border px-2.5 text-xs font-medium transition",
                  diagnoseCategoryFilter.tunnel
                    ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                    : "border-[#d8dfeb] bg-white text-[#65718c] hover:border-[#bcc9df] hover:text-[#3f4b66]",
                )}
                onClick={() =>
                  applyDiagnoseCategoryFilter((prev) => {
                    const enabledCount = Number(prev.ipc) + Number(prev.bridge) + Number(prev.tunnel);
                    if (prev.tunnel && enabledCount === 1) {
                      return prev;
                    }
                    return { ...prev, tunnel: !prev.tunnel };
                  })
                }
              >
                Tunnel
              </button>
            </div>
            <div className="flex items-center gap-2">
              <span className="text-xs text-[#5d6983]">最小日志级别</span>
              <select
                value={diagnoseMinLevel}
                className="h-8 rounded-lg border border-[#d8dfeb] bg-white px-2.5 text-xs text-[#43506b]"
                onChange={(event) => setDiagnoseMinLevel(event.target.value)}
              >
                <option value="trace">TRACE 及以上</option>
                <option value="debug">DEBUG 及以上</option>
                <option value="info">INFO 及以上</option>
                <option value="warn">WARN 及以上</option>
                <option value="error">ERROR 及以上</option>
              </select>
            </div>
          </div>
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs text-[#5d6983]">错误码过滤</span>
            <div ref={diagnoseCodeDropdownRef} className="relative">
              <button
                type="button"
                className={cn(
                  "flex h-8 w-[360px] max-w-[calc(100vw-72px)] min-w-[220px] items-center justify-between gap-2 rounded-lg border px-2.5 text-xs transition",
                  diagnoseCodeDropdownOpen
                    ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                    : "border-[#d8dfeb] bg-white text-[#43506b] hover:border-[#bcc9df]",
                )}
                onClick={() => setDiagnoseCodeDropdownOpen((prev) => !prev)}
              >
                <span className="truncate text-left">{diagnoseCodeFilterSummary}</span>
                <ChevronDown
                  className={cn("h-3.5 w-3.5 shrink-0 transition-transform", diagnoseCodeDropdownOpen && "rotate-180")}
                  aria-hidden
                />
              </button>
              {diagnoseCodeDropdownOpen ? (
                <div className="absolute left-0 top-[calc(100%+6px)] z-30 w-[360px] max-w-[calc(100vw-72px)] rounded-xl border border-[#d9e2f2] bg-white p-2 shadow-[0_16px_30px_rgba(25,47,89,0.16)]">
                  <div className="flex items-center gap-2">
                    <Input
                      value={diagnoseCodeSearchText}
                      onChange={(event) => setDiagnoseCodeSearchText(event.target.value)}
                      placeholder="搜索错误码，例如 TUNNEL_"
                      className="h-8 rounded-md border-[#d8dfeb] px-2 text-xs"
                    />
                    <button
                      type="button"
                      className="h-8 shrink-0 rounded-md border border-[#d8dfeb] px-2.5 text-xs text-[#5a6783] transition hover:border-[#bcc9df] hover:text-[#34405a]"
                      onClick={() => setDiagnoseCodeFilter([])}
                    >
                      全部
                    </button>
                  </div>
                  <div className="mt-2 flex items-center justify-between px-0.5 text-[11px] text-[#73829f]">
                    <span>已选 {diagnoseCodeFilterSet.size} 项</span>
                    <span>已知 + 动态新增错误码</span>
                  </div>
                  <div className="agent-scroll mt-2 max-h-[220px] overflow-y-auto rounded-lg border border-[#e5eaf4] bg-[#f8fbff] p-1.5">
                    <div className="space-y-1">
                      {diagnoseVisibleCodeOptions.length === 0 ? (
                        <span className="block px-1.5 py-1 text-xs text-[#8190aa]">暂无匹配错误码</span>
                      ) : null}
                      {diagnoseVisibleCodeOptions.map((option) => {
                        const selected = diagnoseCodeFilterSet.has(option.code);
                        return (
                          <button
                            key={option.code}
                            type="button"
                            className={cn(
                              "flex h-8 w-full items-center justify-between rounded-md border px-2 text-xs transition",
                              selected
                                ? "border-[#8fb2f2] bg-[#edf4ff] text-[#244a8f]"
                                : "border-[#d8dfeb] bg-white text-[#5f6c88] hover:border-[#bcc9df] hover:text-[#3f4b66]",
                            )}
                            onClick={() => toggleDiagnoseCodeFilter(option.code)}
                          >
                            <span className="flex min-w-0 items-center gap-2">
                              <span
                                className={cn(
                                  "flex h-4 w-4 shrink-0 items-center justify-center rounded border",
                                  selected
                                    ? "border-[#5f8ce0] bg-[#5f8ce0] text-white"
                                    : "border-[#c8d3e7] bg-white text-transparent",
                                )}
                              >
                                <Check className="h-3 w-3" />
                              </span>
                              <span className="truncate">{option.code}</span>
                            </span>
                            <span className="ml-2 shrink-0 text-[11px] opacity-75">{option.count}</span>
                          </button>
                        );
                      })}
                    </div>
                  </div>
                </div>
              ) : null}
            </div>
            {!diagnoseCodeFilterAllEnabled ? (
              <button
                type="button"
                className="h-7 rounded-md border border-[#d8dfeb] bg-white px-2.5 text-xs text-[#5f6c88] transition hover:border-[#bcc9df] hover:text-[#3f4b66]"
                onClick={() => setDiagnoseCodeFilter([])}
              >
                清空选择
              </button>
            ) : null}
          </div>
        </div>
        <div className="agent-scroll min-h-0 flex-1 overflow-y-auto rounded-xl border border-[#e5eaf4]">
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
              {diagnoseDisplayLogs.length === 0 ? (
                <tr>
                  <td className="px-4 py-8 text-center text-sm text-[#7c879e]" colSpan={5}>
                    当前筛选条件下暂无日志
                  </td>
                </tr>
              ) : null}
              {diagnoseDisplayLogs.map((log, index) => (
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

      <div className="grid grid-cols-1 gap-3.5">
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
            <InfoRow label="连接协议" value={formatTransportLabel(hostConfig?.bridge_transport ?? "")} />
            <InfoRow label="Bridge TLS" value={hostConfig?.bridge_tls_enabled ? "已启用" : "未启用"} />
            <InfoRow label="Root CA" value={hostConfig?.bridge_tls_root_ca_file || "--"} />
            <InfoRow label="TLS Server Name" value={hostConfig?.bridge_tls_server_name || "--"} />
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
          {activeBridgeTransportGuide ? (
            <div className="rounded-lg border border-[#cfe0ff] bg-[#f4f8ff] px-3 py-2.5 text-xs text-[#35517f]">
              <div className="mb-1 inline-flex items-center gap-1.5 font-semibold text-[#1c4d98]">
                <ShieldCheck size={14} />
                <span>{activeBridgeTransportGuide.title}</span>
              </div>
              <div className="space-y-1">
                {activeBridgeTransportGuide.lines.map((line) => (
                  <p key={line}>{line}</p>
                ))}
              </div>
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

          <section
            className={cn(
              "agent-scroll min-h-0 flex-1 px-4 py-3.5 lg:px-5",
              activeNav === "diagnose" ? "flex flex-col overflow-hidden" : "overflow-y-auto",
            )}
          >
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

            <div className={cn(activeNav === "diagnose" && "min-h-0 flex-1")}>{renderPageByNav()}</div>
          </section>
        </main>
      </div>
      {renderServiceFormModal()}
      <AlertDialog
        open={closeConfirmOpen}
        onOpenChange={setCloseConfirmOpen}
        title="关闭 Dev Agent？"
        description="你可以将窗口隐藏到系统托盘继续保持 Agent 进程运行，或直接退出应用并停止宿主。"
        cancelText={closeActionLoading ? "退出中..." : "退出应用"}
        actionText={closeActionLoading ? "处理中..." : "隐藏到托盘"}
        onCancel={() => {
          if (!closeActionLoading) {
            void confirmExit();
          }
        }}
        onAction={() => {
          if (!closeActionLoading) {
            void hideToTray();
          }
        }}
        actionClassName="bg-[#1f67e5] hover:bg-[#1a58c7]"
      />
      <Toaster />
    </div>
  );
}
