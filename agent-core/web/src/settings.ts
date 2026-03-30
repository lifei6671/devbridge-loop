export type EditableAgentConfigDocument = {
  agent_id: string;
  bridge_addr: string;
  bridge_transport: string;
  bridge_tls: {
    enabled: boolean;
    root_ca_file: string;
    server_name: string;
  };
  session: {
    heartbeat_interval: string;
    auth_timeout: string;
    auth_method: string;
    auth_token?: string;
    client_cap_version: string;
  };
  tunnel_pool: {
    min_idle: number;
    max_idle: number;
    max_inflight: number;
    ttl: string;
    max_reuse: number;
    recycle_ack_timeout: string;
    open_rate: number;
    open_burst: number;
    reconcile_gap: string;
  };
  observability: {
    metrics_addr: string;
    log_level: string;
  };
  control_channel: {
    dial_timeout: string;
  };
  ui: {
    web: {
      enabled: boolean;
      listen_addr: string;
      base_path: string;
      session_cookie_name: string;
      auth: {
        username: string;
        password: string;
      };
    };
  };
};

export type ConfigSnapshot = {
  config_version: number;
  config_file_path?: string;
  config_file_source?: string;
  base_config_file_path?: string;
  runtime_config_file_path?: string;
  runtime_local_config_path?: string;
  runtime_system_config_path?: string;
  runtime_explicit_config_path?: string;
  updated_at_ms: number;
  updated_by?: string;
  reload_required: boolean;
  applied_to_runtime: boolean;
  source: string;
  config: EditableAgentConfigDocument;
  agent_id: string;
  bridge_addr: string;
  bridge_transport: string;
  tunnel_pool_min_idle: number;
  tunnel_pool_max_idle: number;
  tunnel_pool_max_inflight: number;
  tunnel_pool_ttl_ms: number;
  tunnel_pool_max_reuse: number;
  tunnel_pool_recycle_ack_ms: number;
  tunnel_pool_open_rate: number;
  tunnel_pool_open_burst: number;
  tunnel_pool_reconcile_gap_ms: number;
  ipc_transport?: string;
  ipc_endpoint?: string;
};

export type SettingsDraft = {
  agentId: string;
  bridgeAddr: string;
  transport: string;
  bridgeTLSEnabled: boolean;
  bridgeTLSRootCAFile: string;
  bridgeTLSServerName: string;
  sessionAuthToken: string;
  tunnelPoolMinIdleText: string;
  tunnelPoolMaxIdleText: string;
  tunnelPoolMaxInflightText: string;
  tunnelPoolTtlSecText: string;
  tunnelPoolOpenRateText: string;
  tunnelPoolOpenBurstText: string;
  tunnelPoolReconcileGapMsText: string;
};

export type SettingsFieldKey = keyof SettingsDraft;
export type SettingsFieldErrors = Partial<Record<SettingsFieldKey, string>>;

export const bridgeTransportOptions = [
  { value: "tcp_framed", label: "tcp_framed" },
  { value: "grpc_h2", label: "grpc_h2" },
  { value: "quic_native", label: "quic_native" },
] as const;

export function toSettingsDraft(snapshot: ConfigSnapshot): SettingsDraft {
  return {
    agentId: snapshot.agent_id,
    bridgeAddr: snapshot.bridge_addr,
    transport: snapshot.bridge_transport,
    bridgeTLSEnabled: snapshot.config.bridge_tls.enabled,
    bridgeTLSRootCAFile: snapshot.config.bridge_tls.root_ca_file,
    bridgeTLSServerName: snapshot.config.bridge_tls.server_name,
    sessionAuthToken: getSnapshotSessionAuthToken(snapshot),
    tunnelPoolMinIdleText: String(snapshot.tunnel_pool_min_idle),
    tunnelPoolMaxIdleText: String(snapshot.tunnel_pool_max_idle),
    tunnelPoolMaxInflightText: String(snapshot.tunnel_pool_max_inflight),
    tunnelPoolTtlSecText: formatTTLSecondsText(snapshot.tunnel_pool_ttl_ms),
    tunnelPoolOpenRateText: String(snapshot.tunnel_pool_open_rate),
    tunnelPoolOpenBurstText: String(snapshot.tunnel_pool_open_burst),
    tunnelPoolReconcileGapMsText: String(snapshot.tunnel_pool_reconcile_gap_ms),
  };
}

export function settingsDraftIsDirty(draft: SettingsDraft, snapshot: ConfigSnapshot): boolean {
  const snapshotSessionAuthToken = getSnapshotSessionAuthToken(snapshot);
  return (
    draft.agentId.trim() !== snapshot.agent_id ||
    draft.bridgeAddr.trim() !== snapshot.bridge_addr ||
    draft.transport.trim() !== snapshot.bridge_transport ||
    draft.bridgeTLSEnabled !== snapshot.config.bridge_tls.enabled ||
    draft.bridgeTLSRootCAFile.trim() !== snapshot.config.bridge_tls.root_ca_file ||
    draft.bridgeTLSServerName.trim() !== snapshot.config.bridge_tls.server_name ||
    draft.sessionAuthToken.trim() !== snapshotSessionAuthToken ||
    draft.tunnelPoolMinIdleText.trim() !== String(snapshot.tunnel_pool_min_idle) ||
    draft.tunnelPoolMaxIdleText.trim() !== String(snapshot.tunnel_pool_max_idle) ||
    draft.tunnelPoolMaxInflightText.trim() !== String(snapshot.tunnel_pool_max_inflight) ||
    draft.tunnelPoolTtlSecText.trim() !== formatTTLSecondsText(snapshot.tunnel_pool_ttl_ms) ||
    draft.tunnelPoolOpenRateText.trim() !== String(snapshot.tunnel_pool_open_rate) ||
    draft.tunnelPoolOpenBurstText.trim() !== String(snapshot.tunnel_pool_open_burst) ||
    draft.tunnelPoolReconcileGapMsText.trim() !== String(snapshot.tunnel_pool_reconcile_gap_ms)
  );
}

export function shouldHydrateSettingsDraft(
  currentDraft: SettingsDraft | null,
  snapshot: ConfigSnapshot,
  hasUserEdited: boolean,
): boolean {
  if (currentDraft === null) {
    return true;
  }
  if (!hasUserEdited) {
    return true;
  }
  return !settingsDraftIsDirty(currentDraft, snapshot);
}

export function buildConfigFromSettingsDraft(
  baseConfig: EditableAgentConfigDocument,
  draft: SettingsDraft,
): EditableAgentConfigDocument {
  const fieldErrors = validateSettingsDraft(draft);
  const firstError = Object.values(fieldErrors)[0];
  if (firstError) {
    throw new Error(firstError);
  }
  return {
    ...baseConfig,
    agent_id: draft.agentId.trim(),
    bridge_addr: draft.bridgeAddr.trim(),
    bridge_transport: draft.transport.trim(),
    bridge_tls: {
      ...baseConfig.bridge_tls,
      enabled: draft.bridgeTLSEnabled,
      root_ca_file: normalizeOptionalText(draft.bridgeTLSRootCAFile),
      server_name: normalizeOptionalText(draft.bridgeTLSServerName),
    },
    session: {
      ...baseConfig.session,
      ...(normalizeOptionalText(draft.sessionAuthToken) ? { auth_token: draft.sessionAuthToken.trim() } : {}),
    },
    tunnel_pool: {
      ...baseConfig.tunnel_pool,
      min_idle: parseNonNegativeInteger(draft.tunnelPoolMinIdleText, "tunnel_pool_min_idle"),
      max_idle: parsePositiveInteger(draft.tunnelPoolMaxIdleText, "tunnel_pool_max_idle"),
      max_inflight: parsePositiveInteger(draft.tunnelPoolMaxInflightText, "tunnel_pool_max_inflight"),
      ttl: `${formatSecondsValue(parseNonNegativeSecondsToMillis(draft.tunnelPoolTtlSecText, "tunnel_pool_ttl_s") / 1000)}s`,
      open_rate: parsePositiveFloat(draft.tunnelPoolOpenRateText, "tunnel_pool_open_rate"),
      open_burst: parsePositiveInteger(draft.tunnelPoolOpenBurstText, "tunnel_pool_open_burst"),
      reconcile_gap: `${parsePositiveInteger(draft.tunnelPoolReconcileGapMsText, "tunnel_pool_reconcile_gap_ms")}ms`,
    },
  };
}

export function validateSettingsDraft(draft: SettingsDraft): SettingsFieldErrors {
  const errors: SettingsFieldErrors = {};
  if (!draft.agentId.trim()) {
    errors.agentId = "agent_id 不能为空";
  }
  if (!draft.bridgeAddr.trim()) {
    errors.bridgeAddr = "bridge_addr 不能为空";
  }
  if (!draft.transport.trim()) {
    errors.transport = "bridge_transport 不能为空";
  } else if (!bridgeTransportOptions.some((option) => option.value === draft.transport.trim())) {
    errors.transport = "bridge_transport 仅支持 tcp_framed / grpc_h2 / quic_native";
  }
  if (draft.bridgeTLSEnabled && !draft.bridgeTLSRootCAFile.trim()) {
    errors.bridgeTLSRootCAFile = "启用 TLS 时必须提供 bridge_tls_root_ca_file";
  }
  const minIdle = parseIntegerField(draft.tunnelPoolMinIdleText, "tunnel_pool_min_idle", false);
  if (typeof minIdle === "string") {
    errors.tunnelPoolMinIdleText = minIdle;
  }
  const maxIdle = parseIntegerField(draft.tunnelPoolMaxIdleText, "tunnel_pool_max_idle", true);
  if (typeof maxIdle === "string") {
    errors.tunnelPoolMaxIdleText = maxIdle;
  }
  const maxInflight = parseIntegerField(draft.tunnelPoolMaxInflightText, "tunnel_pool_max_inflight", true);
  if (typeof maxInflight === "string") {
    errors.tunnelPoolMaxInflightText = maxInflight;
  }
  const ttlMs = parseSecondsField(draft.tunnelPoolTtlSecText, "tunnel_pool_ttl_s");
  if (typeof ttlMs === "string") {
    errors.tunnelPoolTtlSecText = ttlMs;
  }
  const openRate = parseFloatField(draft.tunnelPoolOpenRateText, "tunnel_pool_open_rate");
  if (typeof openRate === "string") {
    errors.tunnelPoolOpenRateText = openRate;
  }
  const openBurst = parseIntegerField(draft.tunnelPoolOpenBurstText, "tunnel_pool_open_burst", true);
  if (typeof openBurst === "string") {
    errors.tunnelPoolOpenBurstText = openBurst;
  }
  const reconcileGapMs = parseIntegerField(draft.tunnelPoolReconcileGapMsText, "tunnel_pool_reconcile_gap_ms", true);
  if (typeof reconcileGapMs === "string") {
    errors.tunnelPoolReconcileGapMsText = reconcileGapMs;
  }
  if (typeof minIdle === "number" && typeof maxIdle === "number" && minIdle > maxIdle) {
    errors.tunnelPoolMinIdleText = "tunnel_pool_min_idle 不能大于 tunnel_pool_max_idle";
  }
  if (draft.transport.trim() === "quic_native" && !draft.bridgeTLSEnabled) {
    errors.bridgeTLSEnabled = "quic_native 需要启用 bridge_tls_enabled";
  }
  return errors;
}

function getSnapshotSessionAuthToken(snapshot: ConfigSnapshot): string {
  return snapshot.config.session.auth_token?.trim() ?? "";
}

export function parsePositiveInteger(text: string, fieldLabel: string): number {
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

export function parseNonNegativeInteger(text: string, fieldLabel: string): number {
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

export function parseNonNegativeSecondsToMillis(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (normalized.length === 0) {
    throw new Error(`${fieldLabel} 不能为空`);
  }
  if (!isStrictDecimalText(normalized)) {
    throw new Error(`${fieldLabel} 必须是数字`);
  }
  const parsedValue = Number.parseFloat(normalized);
  if (!Number.isFinite(parsedValue) || parsedValue < 0) {
    throw new Error(`${fieldLabel} 必须是非负数`);
  }
  return Math.round(parsedValue * 1000);
}

export function parsePositiveFloat(text: string, fieldLabel: string): number {
  const normalized = text.trim();
  if (normalized.length === 0) {
    throw new Error(`${fieldLabel} 不能为空`);
  }
  if (!isStrictDecimalText(normalized)) {
    throw new Error(`${fieldLabel} 必须是数字`);
  }
  const parsedValue = Number.parseFloat(normalized);
  if (!Number.isFinite(parsedValue) || parsedValue <= 0) {
    throw new Error(`${fieldLabel} 必须大于 0`);
  }
  return parsedValue;
}

export function normalizeOptionalText(value: string): string {
  return value.trim();
}

export function formatTTLSecondsText(ttlMs: number): string {
  if (!Number.isFinite(ttlMs) || ttlMs < 0) {
    return "0";
  }
  const seconds = ttlMs / 1000;
  return formatSecondsValue(seconds);
}

function formatSecondsValue(seconds: number): string {
  if (Number.isInteger(seconds)) {
    return String(seconds);
  }
  return seconds.toFixed(3).replace(/\.?0+$/, "");
}

function parseIntegerField(text: string, fieldLabel: string, positive: boolean): number | string {
  try {
    return positive ? parsePositiveInteger(text, fieldLabel) : parseNonNegativeInteger(text, fieldLabel);
  } catch (error) {
    return error instanceof Error ? error.message : `${fieldLabel} 非法`;
  }
}

function parseSecondsField(text: string, fieldLabel: string): number | string {
  try {
    return parseNonNegativeSecondsToMillis(text, fieldLabel);
  } catch (error) {
    return error instanceof Error ? error.message : `${fieldLabel} 非法`;
  }
}

function parseFloatField(text: string, fieldLabel: string): number | string {
  try {
    return parsePositiveFloat(text, fieldLabel);
  } catch (error) {
    return error instanceof Error ? error.message : `${fieldLabel} 非法`;
  }
}

function isStrictDecimalText(text: string): boolean {
  return /^\d+(?:\.\d+)?$/.test(text);
}
