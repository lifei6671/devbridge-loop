import { cn } from "@/lib/utils";
import type { ConfigSnapshot } from "@/settings";

export type PageKey = "overview" | "services" | "tunnels" | "traffic" | "diagnose" | "settings";

export type LoginResponse = {
  authenticated: boolean;
  username?: string;
};

export type AgentSnapshot = {
  agent_id: string;
  bridge_addr: string;
  bridge_transport: string;
  state: string;
  session_id?: string | null;
  session_epoch?: number | null;
  started_at_ms: number;
  updated_at_ms: number;
  last_error?: string | null;
  bridge_unavailable?: string | null;
  tunnel_pool: {
    opening: number;
    idle: number;
    reserved: number;
    active: number;
    closing: number;
    closed: number;
    broken: number;
    total: number;
  };
};

export type SessionSnapshot = {
  bridge_transport: string;
  state: string;
  session_id?: string | null;
  session_epoch?: number | null;
  last_heartbeat_at_ms?: number | null;
  last_heartbeat_sent_at_ms?: number | null;
  reconnect_total?: number | null;
  retry_fail_streak?: number | null;
  retry_backoff_ms?: number | null;
  next_retry_at_ms?: number | null;
  updated_at_ms: number;
  last_error?: string | null;
  unavailable_reason?: string | null;
};

export type ServiceItem = {
  logical_service_id: string;
  instance_id: string;
  scope: {
    namespace?: string;
    environment?: string;
  };
  service_name: string;
  protocol: string;
  exposure?: {
    ingress_mode?: string;
    host?: string;
    listen_port?: number;
    sni_name?: string;
    path_prefix?: string;
    allow_export?: boolean;
  } | null;
  health_check_mode?: string;
  health_check_interval_sec?: number;
  health_check_path?: string;
  route_hint?: {
    priority?: number;
  } | null;
  status: string;
  health_status?: string;
  endpoints: Array<{
    endpoint_id: string;
    protocol: string;
    host: string;
    port: number;
    sni_name?: string;
  }>;
  sni_name?: string;
  endpoint_count: number;
  updated_at_ms: number;
};

export type ServiceListResponse = {
  services: ServiceItem[];
  updated_at_ms: number;
};

export type TunnelItem = {
  tunnel_id: string;
  traffic_id?: string;
  logical_service_id?: string;
  instance_id?: string;
  state: string;
  protocol: string;
  local_addr?: string;
  remote_addr?: string;
  latency_ms?: number;
  upstream_dial_latency_ms?: number;
  last_heartbeat_at_ms?: number | null;
  last_error?: string;
  updated_at_ms: number;
};

export type TunnelListResponse = {
  tunnels: TunnelItem[];
  updated_at_ms: number;
};

export type TrafficSnapshot = {
  upload_bytes_per_sec: number;
  download_bytes_per_sec: number;
  upload_total_bytes: number;
  download_total_bytes: number;
  sample_window_ms: number;
  updated_at_ms: number;
};

export type DiagnoseSummary = {
  state: string;
  last_error?: string | null;
  retry_fail_streak?: number;
  retry_backoff_ms?: number;
  next_retry_at_ms?: number | null;
  tunnel_idle_count: number;
  tunnel_active_count: number;
  event_total: number;
  event_error_count: number;
  event_state_changes: number;
  event_reconnects: number;
  event_refill_total: number;
  last_event_at_ms?: number | null;
  last_event_code?: string | null;
  last_event_message?: string | null;
  updated_at_ms: number;
};

export type DiagnoseLogItem = {
  ts_ms: number;
  level: string;
  module: string;
  code: string;
  message: string;
  bridge_state?: string;
  session_id?: string;
  request_id?: string;
};

export type DiagnoseLogsResponse = {
  items: DiagnoseLogItem[];
  total: number;
  updated_at_ms: number;
};

export type ConsoleData = {
  agent: AgentSnapshot;
  session: SessionSnapshot;
  services: ServiceListResponse;
  tunnels: TunnelListResponse;
  traffic: TrafficSnapshot;
  diagnose: DiagnoseSummary;
  logs: DiagnoseLogsResponse;
  config: ConfigSnapshot;
};

export type SSEEnvelope = {
  version?: string;
  type?: string;
  server_time_ms?: number;
  sequence?: number;
  interval_ms?: number;
  payload?: ConsoleData;
};

export const emptyConsoleData: ConsoleData = {
  agent: {
    agent_id: "agent-local",
    bridge_addr: "127.0.0.1:39081",
    bridge_transport: "tcp_framed",
    state: "idle",
    started_at_ms: Date.now(),
    updated_at_ms: Date.now(),
    tunnel_pool: {
      opening: 0,
      idle: 0,
      reserved: 0,
      active: 0,
      closing: 0,
      closed: 0,
      broken: 0,
      total: 0,
    },
  },
  session: {
    bridge_transport: "tcp_framed",
    state: "idle",
    updated_at_ms: Date.now(),
  },
  services: {
    services: [],
    updated_at_ms: Date.now(),
  },
  tunnels: {
    tunnels: [],
    updated_at_ms: Date.now(),
  },
  traffic: {
    upload_bytes_per_sec: 0,
    download_bytes_per_sec: 0,
    upload_total_bytes: 0,
    download_total_bytes: 0,
    sample_window_ms: 0,
    updated_at_ms: Date.now(),
  },
  diagnose: {
    state: "idle",
    tunnel_idle_count: 0,
    tunnel_active_count: 0,
    event_total: 0,
    event_error_count: 0,
    event_state_changes: 0,
    event_reconnects: 0,
    event_refill_total: 0,
    updated_at_ms: Date.now(),
  },
  logs: {
    items: [],
    total: 0,
    updated_at_ms: Date.now(),
  },
  config: {
    config_version: 1,
    config_file_path: "",
    config_file_source: "default",
    base_config_file_path: "",
    runtime_config_file_path: "",
    runtime_local_config_path: "",
    runtime_system_config_path: "",
    runtime_explicit_config_path: "",
    reload_required: true,
    applied_to_runtime: false,
    source: "agent.runtime.config.store",
    agent_id: "agent-local",
    bridge_addr: "127.0.0.1:39081",
    bridge_transport: "tcp_framed",
    tunnel_pool_min_idle: 0,
    tunnel_pool_max_idle: 0,
    tunnel_pool_max_inflight: 0,
    tunnel_pool_ttl_ms: 0,
    tunnel_pool_max_reuse: 0,
    tunnel_pool_recycle_ack_ms: 0,
    tunnel_pool_open_rate: 0,
    tunnel_pool_open_burst: 0,
    tunnel_pool_reconcile_gap_ms: 0,
    config: {
      agent_id: "agent-local",
      bridge_addr: "127.0.0.1:39081",
      bridge_transport: "tcp_framed",
      bridge_tls: {
        enabled: false,
        root_ca_file: "",
        server_name: "",
      },
      session: {
        heartbeat_interval: "5s",
        auth_timeout: "5s",
        auth_method: "token",
        auth_token: "",
        client_cap_version: "agent-core/v1",
      },
      tunnel_pool: {
        min_idle: 0,
        max_idle: 0,
        max_inflight: 0,
        ttl: "0s",
        max_reuse: 0,
        recycle_ack_timeout: "0s",
        open_rate: 0,
        open_burst: 0,
        reconcile_gap: "0s",
      },
      observability: {
        metrics_addr: "127.0.0.1:39090",
        log_level: "info",
      },
      control_channel: {
        dial_timeout: "5s",
      },
      ui: {
        web: {
          enabled: true,
          listen_addr: "127.0.0.1:39082",
          base_path: "/agent",
          session_cookie_name: "devbridge_agent_session",
          auth: {
            username: "admin",
            password: "",
          },
        },
      },
    },
    updated_at_ms: Date.now(),
  },
};

export const executiveInputClassName = "executive-input";
export const executiveInputErrorClassName = "executive-input-error";

export const settingsFieldHelpText: Record<string, string> = {
  agent_id: "这是 Agent 的身份标识。Bridge、会话和服务注册都会用到它，建议保持稳定且唯一。",
  bridge_transport: "决定 Agent 连接 Bridge 时使用哪种传输方式，需要和 Bridge 当前开放的协议一致。",
  bridge_addr: "Bridge 的连接地址，通常写成 host:port。保存后会在下次启动时作为目标地址使用。",
  bridge_tls_enabled: "控制桥接链路是否启用 TLS。若使用 `quic_native`，通常需要同时开启这一项。",
  bridge_tls_root_ca_file: "用于校验证书链的根证书文件路径。未正确配置时，TLS 握手可能无法建立。",
  bridge_tls_server_name: "TLS 校验时使用的服务端名称，一般需要与证书里的主机名或 SAN 保持一致。",
  session_auth_token:
    "由 Bridge 后台生成并分发，Agent 这里手工粘贴。保存后会写入配置文件，下次启动握手时生效；留空与只写不回显的交互后续任务再处理。",
  tunnel_pool_min_idle: "希望长期保留的最小空闲隧道数，单位为条。值越高，首次请求越容易直接复用现成连接。",
  tunnel_pool_max_idle: "允许保留的最大空闲隧道数，单位为条。超过这个值后，多余的空闲连接会被逐步回收。",
  tunnel_pool_max_inflight: "限制同时处于新建中的隧道数量，单位为条。用来避免补池时一下子并发开太多连接。",
  tunnel_pool_ttl_s: "单条隧道允许存活的最长时间，单位为秒。超过这个时长后，系统会开始安排它退出和回收。",
  tunnel_pool_open_rate: "控制补池时的平均开链速度，单位为每秒。数值越高，补足空闲隧道会更快，但压力也会更大。",
  tunnel_pool_open_burst: "在平均速率之外允许的瞬时突发额度，单位为条，适合应对短时间内的流量高峰。",
  tunnel_pool_reconcile_gap_ms: "后台检查并校正隧道池状态的时间间隔，单位为毫秒。越短越积极，越长越节省资源。",
};

export const settingsFieldCaptionText: Record<string, string> = {
  agent_id: "Agent 身份标识",
  bridge_transport: "桥接传输协议",
  bridge_addr: "Bridge 连接地址",
  bridge_tls_enabled: "是否启用 TLS",
  bridge_tls_root_ca_file: "根证书文件路径",
  bridge_tls_server_name: "TLS 服务端名称",
  session_auth_token: "Bridge 签发 Token",
  tunnel_pool_min_idle: "最小空闲隧道数",
  tunnel_pool_max_idle: "最大空闲隧道数",
  tunnel_pool_max_inflight: "最大建链并发数",
  tunnel_pool_ttl_s: "单条隧道存活时长",
  tunnel_pool_open_rate: "平均开链速率",
  tunnel_pool_open_burst: "瞬时开链突发",
  tunnel_pool_reconcile_gap_ms: "池状态巡检间隔",
};

export const settingsFieldPlaceholderText: Record<string, string> = {
  agent_id: "例如 agent-local",
  bridge_addr: "例如 127.0.0.1:39081",
  bridge_tls_root_ca_file: "例如 /etc/devbridge/root-ca.pem",
  bridge_tls_server_name: "例如 bridge.internal.example",
  session_auth_token: "例如 dbt_agent-local.abcd1234，粘贴 Bridge 分发内容",
  tunnel_pool_min_idle: "推荐 8",
  tunnel_pool_max_idle: "推荐 32",
  tunnel_pool_max_inflight: "推荐 4",
  tunnel_pool_ttl_s: "推荐 600",
  tunnel_pool_open_rate: "推荐 10",
  tunnel_pool_open_burst: "推荐 20",
  tunnel_pool_reconcile_gap_ms: "推荐 1000",
};

export const navigationItems: Array<{ key: PageKey; label: string; caption: string }> = [
  { key: "overview", label: "总览", caption: "运行态与会话健康" },
  { key: "services", label: "服务目录", caption: "注册、暴露与删除服务" },
  { key: "tunnels", label: "隧道池", caption: "活跃链路与延迟" },
  { key: "traffic", label: "流量视图", caption: "实时吞吐与累计字节" },
  { key: "diagnose", label: "诊断中心", caption: "事件摘要与最近日志" },
  { key: "settings", label: "运行配置", caption: "Agent 与 IPC 参数" },
];

export function executiveFieldClassName(hasError?: boolean) {
  return cn(executiveInputClassName, hasError && executiveInputErrorClassName);
}

export function parseSSEEnvelope(raw: string): SSEEnvelope | null {
  const normalized = raw.trim();
  if (!normalized) {
    return null;
  }
  try {
    return JSON.parse(normalized) as SSEEnvelope;
  } catch {
    return null;
  }
}

export function readPageFromHash(): PageKey {
  const rawHash = window.location.hash.replace(/^#/, "").trim() as PageKey;
  if (navigationItems.some((item) => item.key === rawHash)) {
    return rawHash;
  }
  return "overview";
}

export function formatDateTime(timestamp?: number | null) {
  if (!timestamp) {
    return "未记录";
  }
  return new Intl.DateTimeFormat("zh-CN", {
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
    second: "2-digit",
  }).format(new Date(timestamp));
}

export function formatRelativeTime(timestamp?: number | null) {
  if (!timestamp) {
    return "未记录";
  }
  const deltaSeconds = Math.round((timestamp - Date.now()) / 1000);
  const formatter = new Intl.RelativeTimeFormat("zh-CN", { numeric: "auto" });
  const absoluteSeconds = Math.abs(deltaSeconds);
  if (absoluteSeconds < 60) {
    return formatter.format(deltaSeconds, "second");
  }
  if (absoluteSeconds < 3600) {
    return formatter.format(Math.round(deltaSeconds / 60), "minute");
  }
  if (absoluteSeconds < 86400) {
    return formatter.format(Math.round(deltaSeconds / 3600), "hour");
  }
  return formatter.format(Math.round(deltaSeconds / 86400), "day");
}

export function formatBytes(value: number) {
  if (!Number.isFinite(value) || value <= 0) {
    return "0 B";
  }
  const units = ["B", "KB", "MB", "GB", "TB"];
  let current = value;
  let unitIndex = 0;
  while (current >= 1024 && unitIndex < units.length - 1) {
    current /= 1024;
    unitIndex += 1;
  }
  return `${current.toFixed(current >= 10 || unitIndex === 0 ? 0 : 1)} ${units[unitIndex]}`;
}

export function formatRate(value: number) {
  return `${formatBytes(value)}/s`;
}

export function formatCount(value?: number | null) {
  if (typeof value !== "number") {
    return "0";
  }
  return new Intl.NumberFormat("zh-CN").format(value);
}

export function normalizeKeyword(value: string) {
  return value.trim().toLowerCase();
}

export function formatStatusText(value?: string | null) {
  const keyword = normalizeKeyword(value ?? "");
  switch (keyword) {
    case "idle":
      return "空闲";
    case "active":
      return "活跃";
    case "ready":
      return "就绪";
    case "connected":
      return "已连接";
    case "running":
      return "运行中";
    case "success":
      return "正常";
    case "retrying":
      return "重试中";
    case "reconnecting":
      return "重连中";
    case "maintenance":
      return "维护中";
    case "warming":
      return "预热中";
    case "failed":
      return "失败";
    case "error":
      return "异常";
    case "broken":
      return "损坏";
    case "stopped":
      return "已停止";
    case "drained":
      return "已排空";
    case "healthy":
      return "健康";
    case "unhealthy":
      return "异常";
    case "degraded":
      return "降级";
    case "unknown":
      return "未知";
    default:
      return value || "未记录";
  }
}

export function formatLevelText(value?: string | null) {
  const keyword = normalizeKeyword(value ?? "");
  switch (keyword) {
    case "info":
      return "信息";
    case "warn":
    case "warning":
      return "警告";
    case "error":
      return "错误";
    case "debug":
      return "调试";
    case "trace":
      return "追踪";
    default:
      return value || "未记录";
  }
}

export function statusBadgeVariant(status?: string | null): "default" | "secondary" | "success" | "warning" | "danger" {
  switch (normalizeKeyword(status ?? "")) {
    case "active":
    case "ready":
    case "connected":
    case "running":
    case "success":
      return "success";
    case "retrying":
    case "reconnecting":
    case "maintenance":
    case "warming":
      return "warning";
    case "failed":
    case "error":
    case "broken":
    case "stopped":
    case "drained":
      return "danger";
    case "idle":
      return "secondary";
    default:
      return "default";
  }
}

export function levelBadgeVariant(level?: string | null): "outline" | "warning" | "danger" | "default" {
  switch (normalizeKeyword(level ?? "")) {
    case "warn":
    case "warning":
      return "warning";
    case "error":
      return "danger";
    case "info":
      return "default";
    default:
      return "outline";
  }
}

export function glyphClassName(tone: "primary" | "muted" | "danger" = "muted") {
  switch (tone) {
    case "primary":
      return "bg-[rgba(0,91,191,0.12)] text-[hsl(var(--primary))]";
    case "danger":
      return "bg-[rgba(239,68,68,0.1)] text-[rgb(185,28,28)]";
    default:
      return "bg-[rgba(65,71,84,0.08)] text-[hsl(var(--muted-foreground))]";
  }
}
