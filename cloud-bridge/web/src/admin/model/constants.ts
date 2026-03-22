import type { AdminPageKey } from "./types";

export const authStorageKey = "bridge.admin.token";
export const autoRefreshEnabledStorageKey = "bridge.admin.auto_refresh.enabled";
export const autoRefreshIntervalStorageKey = "bridge.admin.auto_refresh.interval_ms";
export const autoRefreshIntervalOptions = [3000, 5000, 10000, 30000];
export const defaultAutoRefreshIntervalMS = 5000;
export const minSSEReconnectIntervalMS = 15000;

export const sseSnapshotEventName = "bridge.snapshot";
export const sseReadyEventName = "bridge.ready";
export const sseHeartbeatEventName = "bridge.heartbeat";

export const adminPageQueryKey = "page";
export const defaultAdminPage: AdminPageKey = "dashboard";

export const configPatchKeyOptions = [
  "default_scope.namespace",
  "default_scope.environment",
  "ingress.http_addr",
  "ingress.grpc_addr",
  "ingress.https_addr",
  "ingress.tls_sni_addr",
  "ingress.tcp_port_range",
  "ingress.base_domain",
  "admin.enabled",
  "admin.listen_addr",
  "admin.allow_shared_listener",
  "admin.base_path",
  "admin.ui_enabled",
  "control_plane.listen_addr",
  "control_plane.grpc_h2_listen_addr",
  "control_plane.heartbeat_timeout_ms",
  "observability.log_level",
  "observability.metrics_addr",
] as const;

export const bridgeTunnelIDPrefix = "tcp-bridge-tunnel-";
