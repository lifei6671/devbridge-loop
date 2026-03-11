export interface StateSummary {
  agentStatus: string;
  bridgeStatus: string;
  tunnelStatus: string;
  currentEnv: string;
  rdName: string;
  registrationCount: number;
  activeIntercepts: number;
  lastUpdateAt: string;
  defaultTTLSeconds: number;
  scanIntervalSeconds: number;
}

export interface TunnelState {
  connected: boolean;
  reconnecting: boolean;
  reconnectAttempt: number;
  sessionEpoch: number;
  resourceVersion: number;
  lastHeartbeatAt: string;
  nextReconnectAt: string | null;
  lastReconnectError: string;
  protocol: string;
  bridgeAddress: string;
  reconnectBackoffMs: number[];
}

export interface LocalEndpoint {
  protocol: string;
  targetHost: string;
  targetPort: number;
  status: string;
}

export interface LocalRegistration {
  serviceName: string;
  env: string;
  instanceId: string;
  metadata?: Record<string, string>;
  healthy: boolean;
  registerTime?: string;
  lastHeartbeatTime: string;
  ttlSeconds: number;
  endpoints: LocalEndpoint[];
}

export interface ActiveIntercept {
  env: string;
  serviceName: string;
  protocol: string;
  tunnelId: string;
  instanceId: string;
  targetPort: number;
  status: string;
  updatedAt: string;
}

export interface ErrorEntry {
  code: string;
  message: string;
  occurredAt: string;
  context: Record<string, string>;
}

export interface RequestSummary {
  direction: string;
  protocol: string;
  serviceName: string;
  requestedEnv: string;
  resolvedEnv: string;
  resolution: string;
  upstream: string;
  statusCode: number;
  result: string;
  errorCode?: string;
  message?: string;
  latencyMs: number;
  occurredAt: string;
}

export interface DiagnosticsSnapshot {
  summary: StateSummary;
  recentErrors: ErrorEntry[];
  recentRequests: RequestSummary[];
  generatedAt: string;
}

export interface AgentRuntime {
  status: string;
  pid: number | null;
  command: string;
  startedAt: string | null;
  lastError: string | null;
  autoRestart: boolean;
  restartCount: number;
  restartAttempt: number;
  nextRestartAt: string | null;
}

export interface DesktopConfigView {
  agentHttpAddr: string;
  agentApiBase: string;
  envName: string;
  agentBinary: string | null;
  agentCoreDir: string | null;
  agentAutoRestart: boolean;
  closeToTrayOnClose: boolean;
  closeToTrayOnCloseConfigured: boolean;
  agentRestartBackoffMs: number[];
  envResolveOrder: string[];
  tunnelBridgeAddress: string;
  tunnelBackflowBaseUrl: string;
  tunnelSyncProtocol: string;
  tunnelMasqueAuthMode: string;
  tunnelMasquePsk: string;
  tunnelMasqueProxyUrl: string;
  tunnelMasqueTargetAddr: string;
  platform: string;
  arch: string;
  configDir: string;
  logDir: string;
  configFile: string;
  configLoaded: boolean;
}

export interface DesktopConfigSaveRequest {
  agentHttpAddr: string;
  agentApiBase: string;
  envName: string;
  agentBinary: string | null;
  agentCoreDir: string | null;
  agentAutoRestart: boolean;
  closeToTrayOnClose: boolean;
  agentRestartBackoffMs: number[];
  envResolveOrder: string[];
  tunnelBridgeAddress: string;
  tunnelBackflowBaseUrl: string;
  tunnelSyncProtocol: string;
  tunnelMasqueAuthMode: string;
  tunnelMasquePsk: string;
  tunnelMasqueProxyUrl: string;
  tunnelMasqueTargetAddr: string;
}
