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
  sessionEpoch: number;
  resourceVersion: number;
  lastHeartbeatAt: string;
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
  healthy: boolean;
  lastHeartbeatTime: string;
  ttlSeconds: number;
  endpoints: LocalEndpoint[];
}

export interface ErrorEntry {
  code: string;
  message: string;
  occurredAt: string;
  context: Record<string, string>;
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
