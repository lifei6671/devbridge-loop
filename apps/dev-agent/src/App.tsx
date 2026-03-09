import { useCallback, useEffect, useMemo, useState } from "react";
import type { ComponentType, ReactElement } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  AlertTriangle,
  Cable,
  Cloud,
  Eye,
  LayoutDashboard,
  ListTree,
  RefreshCcw,
  RotateCcw,
  Server,
  Settings,
  ShieldCheck,
  Trash2,
  Zap
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import type {
  ActiveIntercept,
  AgentRuntime,
  DesktopConfigView,
  DesktopConfigSaveRequest,
  DiagnosticsSnapshot,
  ErrorEntry,
  LocalRegistration,
  RequestSummary,
  StateSummary,
  TunnelState
} from "@/types";

type PageKey = "dashboard" | "services" | "intercepts" | "logs" | "config";

const PAGE_ITEMS: Array<{ key: PageKey; label: string; icon: ComponentType<{ className?: string }> }> = [
  { key: "dashboard", label: "Dashboard", icon: LayoutDashboard },
  { key: "services", label: "Services", icon: ListTree },
  { key: "intercepts", label: "Intercepts", icon: ShieldCheck },
  { key: "logs", label: "Logs", icon: AlertTriangle },
  { key: "config", label: "Config", icon: Settings }
];

async function call<T>(command: string, args?: Record<string, unknown>): Promise<T> {
  return invoke<T>(command, args);
}

function formatTime(value?: string | null): string {
  if (!value) {
    return "-";
  }

  // AgentRuntime 的时间戳是秒级字符串，其他接口通常是 RFC3339，统一在这里兼容。
  const unixSeconds = Number(value);
  const date = Number.isFinite(unixSeconds) && /^\d+$/.test(value)
    ? new Date(unixSeconds * 1000)
    : new Date(value);
  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return date.toLocaleString("zh-CN", { hour12: false });
}

function formatCountdown(seconds: number): string {
  if (!Number.isFinite(seconds) || seconds <= 0) {
    return "0s";
  }
  return `${Math.ceil(seconds)}s`;
}

function renderHealthBadge(healthy: boolean): ReactElement {
  if (healthy) {
    return <Badge>healthy</Badge>;
  }
  return <Badge variant="secondary">unhealthy</Badge>;
}

function parseNumberList(input: string): number[] {
  const parsed = input
    .split(",")
    .map((item) => Number(item.trim()))
    .filter((item) => Number.isFinite(item) && item > 0);
  if (parsed.length === 0) {
    return [500, 1000, 2000, 5000];
  }
  return parsed;
}

function parseStringList(input: string): string[] {
  const parsed = input
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item.length > 0);
  if (parsed.length === 0) {
    return ["requestHeader", "payload", "runtimeDefault", "baseFallback"];
  }
  return parsed;
}

export default function App(): ReactElement {
  const [activePage, setActivePage] = useState<PageKey>("dashboard");
  const [summary, setSummary] = useState<StateSummary | null>(null);
  const [tunnel, setTunnel] = useState<TunnelState | null>(null);
  const [registrations, setRegistrations] = useState<LocalRegistration[]>([]);
  const [errors, setErrors] = useState<ErrorEntry[]>([]);
  const [requests, setRequests] = useState<RequestSummary[]>([]);
  const [runtime, setRuntime] = useState<AgentRuntime | null>(null);
  const [intercepts, setIntercepts] = useState<ActiveIntercept[]>([]);
  const [desktopConfig, setDesktopConfig] = useState<DesktopConfigView | null>(null);
  const [desktopConfigDraft, setDesktopConfigDraft] = useState<DesktopConfigSaveRequest | null>(null);
  const [selectedInstanceId, setSelectedInstanceId] = useState<string | null>(null);
  const [loading, setLoading] = useState(false);
  const [savingConfig, setSavingConfig] = useState(false);
  const [unregisteringId, setUnregisteringId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string>("");
  const [showCloseDecisionDialog, setShowCloseDecisionDialog] = useState(false);
  const [resolvingCloseDecision, setResolvingCloseDecision] = useState(false);
  const [clockMs, setClockMs] = useState(() => Date.now());

  const selectedRegistration = useMemo(() => {
    if (!selectedInstanceId) {
      return null;
    }
    return registrations.find((item) => item.instanceId === selectedInstanceId) ?? null;
  }, [registrations, selectedInstanceId]);

  const loadDiagnostics = useCallback(async (): Promise<DiagnosticsSnapshot> => {
    try {
      return await call<DiagnosticsSnapshot>("get_diagnostics");
    } catch {
      // 兼容旧版本 agent-core：若聚合接口不可用，则回退到三个独立状态接口。
      const [summaryData, errorData, requestData] = await Promise.all([
        call<StateSummary>("get_state_summary"),
        call<ErrorEntry[]>("get_recent_errors"),
        call<RequestSummary[]>("get_recent_requests")
      ]);
      return {
        summary: summaryData,
        recentErrors: errorData,
        recentRequests: requestData,
        generatedAt: new Date().toISOString()
      };
    }
  }, []);

  const refresh = useCallback(async () => {
    setLoading(true);
    setActionError("");
    try {
      const [
        diagnosticsData,
        tunnelData,
        registrationData,
        runtimeData,
        interceptData,
        desktopConfigData
      ] = await Promise.all([
        loadDiagnostics(),
        call<TunnelState>("get_tunnel_state"),
        call<LocalRegistration[]>("get_registrations"),
        call<AgentRuntime>("agent_runtime"),
        call<ActiveIntercept[]>("get_active_intercepts"),
        call<DesktopConfigView>("get_desktop_config")
      ]);

      setSummary(diagnosticsData.summary);
      setTunnel(tunnelData);
      setRegistrations(registrationData);
      setErrors(diagnosticsData.recentErrors);
      setRequests(diagnosticsData.recentRequests);
      setRuntime(runtimeData);
      setIntercepts(interceptData);
      setDesktopConfig(desktopConfigData);
      setDesktopConfigDraft({
        agentApiBase: desktopConfigData.agentApiBase,
        agentBinary: desktopConfigData.agentBinary,
        agentCoreDir: desktopConfigData.agentCoreDir,
        agentAutoRestart: desktopConfigData.agentAutoRestart,
        closeToTrayOnClose: desktopConfigData.closeToTrayOnClose,
        agentRestartBackoffMs: desktopConfigData.agentRestartBackoffMs,
        envResolveOrder: desktopConfigData.envResolveOrder,
        tunnelBridgeAddress: desktopConfigData.tunnelBridgeAddress,
        tunnelBackflowBaseUrl: desktopConfigData.tunnelBackflowBaseUrl
      });

      // 当前选中的实例如果被删除，则回退到列表首项，保证详情页始终有有效目标。
      setSelectedInstanceId((current) => {
        if (current && registrationData.some((item) => item.instanceId === current)) {
          return current;
        }
        return registrationData[0]?.instanceId ?? null;
      });
    } catch (error) {
      setActionError(String(error));
    } finally {
      setLoading(false);
    }
  }, [loadDiagnostics]);

  const reconnect = useCallback(async () => {
    setActionError("");
    try {
      await call<TunnelState>("trigger_reconnect");
      await refresh();
    } catch (error) {
      setActionError(String(error));
    }
  }, [refresh]);

  const restartAgent = useCallback(async () => {
    setActionError("");
    try {
      const runtimeData = await call<AgentRuntime>("restart_agent_process");
      setRuntime(runtimeData);
      await refresh();
    } catch (error) {
      setActionError(String(error));
    }
  }, [refresh]);

  const saveDesktopConfig = useCallback(async () => {
    if (!desktopConfigDraft) {
      return;
    }
    setSavingConfig(true);
    setActionError("");
    try {
      const saved = await call<DesktopConfigView>("save_desktop_config", {
        request: desktopConfigDraft
      });
      setDesktopConfig(saved);
      setDesktopConfigDraft({
        agentApiBase: saved.agentApiBase,
        agentBinary: saved.agentBinary,
        agentCoreDir: saved.agentCoreDir,
        agentAutoRestart: saved.agentAutoRestart,
        closeToTrayOnClose: saved.closeToTrayOnClose,
        agentRestartBackoffMs: saved.agentRestartBackoffMs,
        envResolveOrder: saved.envResolveOrder,
        tunnelBridgeAddress: saved.tunnelBridgeAddress,
        tunnelBackflowBaseUrl: saved.tunnelBackflowBaseUrl
      });
    } catch (error) {
      setActionError(String(error));
    } finally {
      setSavingConfig(false);
    }
  }, [desktopConfigDraft]);

  const unregisterRegistration = useCallback(
    async (instanceId: string) => {
      const target = registrations.find((item) => item.instanceId === instanceId);
      if (!target) {
        return;
      }

      const confirmed = window.confirm(
        `确认注销实例 ${target.instanceId}（${target.serviceName}/${target.env}）吗？`
      );
      if (!confirmed) {
        return;
      }

      setUnregisteringId(instanceId);
      setActionError("");
      try {
        await call("unregister_registration", { instanceId });
        await refresh();
      } catch (error) {
        setActionError(String(error));
      } finally {
        setUnregisteringId(null);
      }
    },
    [refresh, registrations]
  );

  const resolveWindowCloseAction = useCallback(
    async (action: "tray" | "exit") => {
      setResolvingCloseDecision(true);
      setActionError("");
      try {
        await call("resolve_window_close_action", { action });
        setShowCloseDecisionDialog(false);
      } catch (error) {
        setActionError(String(error));
      } finally {
        setResolvingCloseDecision(false);
      }
    },
    []
  );

  useEffect(() => {
    // 断线重连阶段提升轮询频率，保证倒计时与状态切换更及时。
    const intervalMs = tunnel?.reconnecting ? 1000 : 5000;
    void refresh();
    const timer = window.setInterval(() => {
      void refresh();
    }, intervalMs);
    return () => window.clearInterval(timer);
  }, [refresh, tunnel?.reconnecting]);

  useEffect(() => {
    // Bridge 重连倒计时按秒更新，不依赖后端轮询频率。
    const timer = window.setInterval(() => {
      setClockMs(Date.now());
    }, 1000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    // 订阅 Rust Host 推送的进程运行态变化事件，前端无需等轮询也能实时更新。
    let unlistenRuntime: (() => void) | null = null;
    let unlistenCloseIntent: (() => void) | null = null;

    const setupListener = async (): Promise<void> => {
      unlistenRuntime = await listen<AgentRuntime>("agent-runtime-changed", (event) => {
        setRuntime(event.payload);
      });
      unlistenCloseIntent = await listen("window-close-intent", () => {
        // 首次点击关闭时由 Rust Host 通知前端展示二选一弹窗。
        setShowCloseDecisionDialog(true);
      });
    };

    void setupListener();

    return () => {
      if (unlistenRuntime) {
        unlistenRuntime();
      }
      if (unlistenCloseIntent) {
        unlistenCloseIntent();
      }
    };
  }, []);

  const tunnelBadge = useMemo(() => {
    if (!tunnel) {
      return <Badge variant="outline">unknown</Badge>;
    }
    if (tunnel.connected) {
      return <Badge>connected</Badge>;
    }
    return <Badge variant="secondary">disconnected</Badge>;
  }, [tunnel]);

  const reconnectCountdown = useMemo(() => {
    if (!tunnel || tunnel.connected || !tunnel.nextReconnectAt) {
      return null;
    }

    const targetMs = Date.parse(tunnel.nextReconnectAt);
    if (Number.isNaN(targetMs)) {
      return null;
    }

    // 允许出现负值并在这里归零，避免时间同步偏差导致 UI 抖动。
    return Math.max(0, Math.ceil((targetMs - clockMs) / 1000));
  }, [clockMs, tunnel]);

  const bridgeCardStatus = useMemo(() => {
    if (tunnel?.connected) {
      return "online";
    }
    if (tunnel?.reconnecting) {
      return "reconnecting";
    }
    return summary?.bridgeStatus ?? "unknown";
  }, [summary?.bridgeStatus, tunnel?.connected, tunnel?.reconnecting]);

  const renderDashboard = (): ReactElement => (
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Card>
        <CardHeader className="pb-2">
          <CardDescription>Agent</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Server className="h-4 w-4" />
            {summary?.agentStatus ?? "unknown"}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>pid: {runtime?.pid ?? "-"}</div>
          <div>env: {summary?.currentEnv ?? "-"}</div>
          <div>status: {runtime?.status ?? "-"}</div>
          <div>restart-count: {runtime?.restartCount ?? 0}</div>
          <div className="whitespace-pre-wrap break-all">
            last-error: {runtime?.lastError ? runtime.lastError : "-"}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardDescription>Bridge</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Cloud className="h-4 w-4" />
            {bridgeCardStatus}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>address: {tunnel?.bridgeAddress ?? "-"}</div>
          <div>rdName: {summary?.rdName ?? "-"}</div>
          <div>reconnect-attempt: {tunnel?.reconnectAttempt ?? 0}</div>
          <div>
            next-retry:
            {reconnectCountdown !== null
              ? ` ${formatCountdown(reconnectCountdown)}`
              : ` ${formatTime(tunnel?.nextReconnectAt)}`}
          </div>
          <div className="whitespace-pre-wrap break-all">
            last-error: {tunnel?.lastReconnectError ? tunnel.lastReconnectError : "-"}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardDescription>Tunnel</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Zap className="h-4 w-4" />
            {tunnelBadge}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>epoch: {tunnel?.sessionEpoch ?? 0}</div>
          <div>resourceVersion: {tunnel?.resourceVersion ?? 0}</div>
          <div>lastHeartbeat: {formatTime(tunnel?.lastHeartbeatAt)}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardDescription>Registration</CardDescription>
          <CardTitle className="text-base">{summary?.registrationCount ?? 0} active</CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>intercepts: {summary?.activeIntercepts ?? 0}</div>
          <div>errors(50): {errors.length}</div>
          <div>requests(200): {requests.length}</div>
        </CardContent>
      </Card>
    </section>
  );

  const renderServices = (): ReactElement => (
    <section className="grid gap-4 xl:grid-cols-3">
      <Card className="xl:col-span-2">
        <CardHeader>
          <CardTitle>Local Services</CardTitle>
          <CardDescription>
            实时读取 `/api/v1/registrations`，支持手动注销与详情查看。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>Service</TableHead>
                <TableHead>Env</TableHead>
                <TableHead>Instance</TableHead>
                <TableHead>Endpoints</TableHead>
                <TableHead>Healthy</TableHead>
                <TableHead>Register</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {registrations.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground">
                    暂无本地注册项
                  </TableCell>
                </TableRow>
              ) : (
                registrations.map((item) => (
                  <TableRow
                    key={item.instanceId}
                    className={item.instanceId === selectedInstanceId ? "bg-muted/30" : ""}
                  >
                    <TableCell className="font-semibold">{item.serviceName}</TableCell>
                    <TableCell>{item.env}</TableCell>
                    <TableCell className="font-mono text-xs">{item.instanceId}</TableCell>
                    <TableCell>
                      {item.endpoints.map((endpoint) => (
                        <div key={`${endpoint.protocol}-${endpoint.targetPort}`}>
                          {endpoint.protocol}:{endpoint.targetHost}:{endpoint.targetPort}
                        </div>
                      ))}
                    </TableCell>
                    <TableCell>{renderHealthBadge(item.healthy)}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatTime(item.registerTime)}
                    </TableCell>
                    <TableCell>
                      <div className="flex gap-2">
                        <Button
                          variant="outline"
                          size="sm"
                          onClick={() => setSelectedInstanceId(item.instanceId)}
                        >
                          <Eye className="mr-1 h-3.5 w-3.5" />
                          详情
                        </Button>
                        <Button
                          variant="outline"
                          size="sm"
                          disabled={unregisteringId === item.instanceId}
                          onClick={() => void unregisterRegistration(item.instanceId)}
                        >
                          <Trash2 className="mr-1 h-3.5 w-3.5" />
                          注销
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Service Detail</CardTitle>
          <CardDescription>当前选中实例的详细状态</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          {!selectedRegistration ? (
            <p className="text-muted-foreground">请在左侧选择实例查看详情</p>
          ) : (
            <>
              <div className="font-semibold">
                {selectedRegistration.serviceName} / {selectedRegistration.env}
              </div>
              <div className="text-muted-foreground">instance: {selectedRegistration.instanceId}</div>
              <div>healthy: {selectedRegistration.healthy ? "true" : "false"}</div>
              <div>ttlSeconds: {selectedRegistration.ttlSeconds}</div>
              <div>registerTime: {formatTime(selectedRegistration.registerTime)}</div>
              <div>lastHeartbeat: {formatTime(selectedRegistration.lastHeartbeatTime)}</div>
              <div className="space-y-1 rounded-md border border-border/60 p-2">
                <div className="font-medium">metadata</div>
                <pre className="overflow-x-auto text-xs text-muted-foreground">
                  {JSON.stringify(selectedRegistration.metadata ?? {}, null, 2)}
                </pre>
              </div>
            </>
          )}
        </CardContent>
      </Card>
    </section>
  );

  const renderIntercepts = (): ReactElement => (
    <Card>
      <CardHeader>
        <CardTitle>Active Intercepts</CardTitle>
        <CardDescription>读取 `/api/v1/state/intercepts` 的实时接管关系</CardDescription>
      </CardHeader>
      <CardContent>
        <Table>
          <TableHeader>
            <TableRow>
              <TableHead>Env</TableHead>
              <TableHead>Service</TableHead>
              <TableHead>Protocol</TableHead>
              <TableHead>Instance</TableHead>
              <TableHead>Tunnel</TableHead>
              <TableHead>TargetPort</TableHead>
              <TableHead>Status</TableHead>
              <TableHead>UpdatedAt</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {intercepts.length === 0 ? (
              <TableRow>
                <TableCell colSpan={8} className="text-center text-muted-foreground">
                  当前无接管关系
                </TableCell>
              </TableRow>
            ) : (
              intercepts.map((item) => (
                <TableRow
                  key={`${item.env}-${item.serviceName}-${item.protocol}-${item.instanceId}-${item.targetPort}`}
                >
                  <TableCell>{item.env}</TableCell>
                  <TableCell>{item.serviceName}</TableCell>
                  <TableCell>{item.protocol}</TableCell>
                  <TableCell className="font-mono text-xs">{item.instanceId}</TableCell>
                  <TableCell className="font-mono text-xs">{item.tunnelId}</TableCell>
                  <TableCell>{item.targetPort}</TableCell>
                  <TableCell>{item.status}</TableCell>
                  <TableCell className="text-xs text-muted-foreground">
                    {formatTime(item.updatedAt)}
                  </TableCell>
                </TableRow>
              ))
            )}
          </TableBody>
        </Table>
      </CardContent>
    </Card>
  );

  const renderLogs = (): ReactElement => (
    <Card>
      <CardHeader>
        <CardTitle>Runtime Logs</CardTitle>
        <CardDescription>基于 `/api/v1/state/diagnostics` 与运行态事件的最近日志</CardDescription>
      </CardHeader>
      <CardContent className="space-y-3">
        {errors.length === 0 ? (
          <p className="text-sm text-muted-foreground">当前无错误日志</p>
        ) : (
          errors.map((entry) => (
            <div
              key={`${entry.code}-${entry.occurredAt}-${entry.message}`}
              className="rounded-md border border-border/60 p-3"
            >
              <div className="flex items-center justify-between gap-3">
                <Badge variant="outline">{entry.code}</Badge>
                <span className="text-xs text-muted-foreground">{formatTime(entry.occurredAt)}</span>
              </div>
              <p className="mt-2 text-sm">{entry.message}</p>
              <pre className="mt-2 overflow-x-auto text-xs text-muted-foreground">
                {JSON.stringify(entry.context ?? {}, null, 2)}
              </pre>
            </div>
          ))
        )}

        <div className="h-px w-full bg-border/70" />

        <div className="space-y-2">
          <p className="text-sm font-semibold">Recent Requests</p>
          {requests.length === 0 ? (
            <p className="text-sm text-muted-foreground">当前无请求摘要</p>
          ) : (
            requests.map((entry) => (
              <div
                key={`${entry.direction}-${entry.serviceName}-${entry.occurredAt}-${entry.latencyMs}`}
                className="rounded-md border border-border/60 p-3"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{entry.direction}</Badge>
                  <Badge variant="secondary">{entry.protocol}</Badge>
                  <span className="text-sm font-medium">{entry.serviceName}</span>
                  <span className="text-xs text-muted-foreground">
                    {entry.requestedEnv || "-"} → {entry.resolvedEnv || "-"} ({entry.resolution})
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                  <span>upstream: {entry.upstream || "-"}</span>
                  <span>status: {entry.statusCode}</span>
                  <span>latency: {entry.latencyMs}ms</span>
                  <span>result: {entry.result}</span>
                  <span>{formatTime(entry.occurredAt)}</span>
                </div>
                {entry.errorCode || entry.message ? (
                  <p className="mt-2 text-xs text-amber-900">
                    {entry.errorCode ? `${entry.errorCode}: ` : ""}
                    {entry.message ?? ""}
                  </p>
                ) : null}
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  );

  const renderConfig = (): ReactElement => (
    <section className="grid gap-4 xl:grid-cols-2">
      <Card>
        <CardHeader>
          <CardTitle>Desktop Host Config</CardTitle>
          <CardDescription>本地配置目录与配置文件状态</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div>configLoaded: {desktopConfig?.configLoaded ? "true" : "false"}</div>
          <div>configDir: {desktopConfig?.configDir ?? "-"}</div>
          <div>logDir: {desktopConfig?.logDir ?? "-"}</div>
          <div>configFile: {desktopConfig?.configFile ?? "-"}</div>
          <div className="h-px w-full bg-border/70" />
          <div>platform: {desktopConfig?.platform ?? "-"}</div>
          <div>arch: {desktopConfig?.arch ?? "-"}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Desktop Config Editor</CardTitle>
          <CardDescription>基础配置加载与保存（保存后重启桌面端生效）</CardDescription>
        </CardHeader>
        <CardContent className="space-y-2 text-sm">
          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentApiBase</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentApiBase ?? ""}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current ? { ...current, agentApiBase: event.target.value } : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentBinary</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentBinary ?? ""}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current
                    ? {
                        ...current,
                        agentBinary: event.target.value.trim() === "" ? null : event.target.value
                      }
                    : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentCoreDir</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentCoreDir ?? ""}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current
                    ? {
                        ...current,
                        agentCoreDir: event.target.value.trim() === "" ? null : event.target.value
                      }
                    : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentRestartBackoffMs (csv)</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.agentRestartBackoffMs ?? []).join(",")}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current
                    ? {
                        ...current,
                        agentRestartBackoffMs: parseNumberList(event.target.value)
                      }
                    : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">envResolveOrder (csv)</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.envResolveOrder ?? []).join(",")}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current
                    ? {
                        ...current,
                        envResolveOrder: parseStringList(event.target.value)
                      }
                    : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">tunnelBridgeAddress</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBridgeAddress ?? ""}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current ? { ...current, tunnelBridgeAddress: event.target.value } : current
                )
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">tunnelBackflowBaseUrl</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBackflowBaseUrl ?? ""}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current ? { ...current, tunnelBackflowBaseUrl: event.target.value } : current
                )
              }
            />
          </div>

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.agentAutoRestart ?? true}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current ? { ...current, agentAutoRestart: event.target.checked } : current
                )
              }
            />
            agentAutoRestart
          </label>

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.closeToTrayOnClose ?? true}
              onChange={(event) =>
                setDesktopConfigDraft((current) =>
                  current ? { ...current, closeToTrayOnClose: event.target.checked } : current
                )
              }
            />
            closeToTrayOnClose
          </label>

          {!desktopConfig?.closeToTrayOnCloseConfigured ? (
            <p className="text-xs text-muted-foreground">
              首次点击窗口关闭按钮时会弹窗询问；配置保存后将按该选项直接执行。
            </p>
          ) : null}

          <Button
            variant="outline"
            size="sm"
            disabled={savingConfig || !desktopConfigDraft}
            onClick={() => void saveDesktopConfig()}
          >
            保存配置
          </Button>
        </CardContent>
      </Card>
    </section>
  );

  const renderPage = (): ReactElement => {
    switch (activePage) {
      case "dashboard":
        return renderDashboard();
      case "services":
        return renderServices();
      case "intercepts":
        return renderIntercepts();
      case "logs":
        return renderLogs();
      case "config":
        return renderConfig();
      default:
        return renderDashboard();
    }
  };

  return (
    <div className="min-h-screen bg-app-gradient text-foreground">
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-6 px-4 pb-8 pt-6 sm:px-6 lg:px-8">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.28em] text-muted-foreground">
              DevLoop Phase One
            </p>
            <h1 className="mt-1 font-mono text-2xl font-semibold tracking-tight">
              dev-agent control surface
            </h1>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => void refresh()} disabled={loading}>
              <RefreshCcw className="mr-2 h-4 w-4" />
              刷新
            </Button>
            <Button variant="outline" onClick={() => void restartAgent()}>
              <RotateCcw className="mr-2 h-4 w-4" />
              重启 Agent Core
            </Button>
            <Button onClick={() => void reconnect()}>
              <Cable className="mr-2 h-4 w-4" />
              手动重连
            </Button>
          </div>
        </header>

        <nav className="flex flex-wrap gap-2">
          {PAGE_ITEMS.map((item) => {
            const Icon = item.icon;
            return (
              <Button
                key={item.key}
                variant={activePage === item.key ? "default" : "outline"}
                size="sm"
                onClick={() => setActivePage(item.key)}
              >
                <Icon className="mr-2 h-4 w-4" />
                {item.label}
              </Button>
            );
          })}
        </nav>

        {actionError ? (
          <Card className="border-amber-500/40 bg-amber-100/40">
            <CardContent className="flex items-center gap-2 p-4 text-sm text-amber-950">
              <AlertTriangle className="h-4 w-4" />
              {actionError}
            </CardContent>
          </Card>
        ) : null}

        {runtime?.lastError ? (
          <Card className="border-amber-500/40 bg-amber-100/40">
            <CardContent className="flex items-center gap-2 p-4 text-sm text-amber-950">
              <AlertTriangle className="h-4 w-4" />
              {runtime.lastError}
            </CardContent>
          </Card>
        ) : null}

        {showCloseDecisionDialog ? (
          <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4">
            <Card className="w-full max-w-md border-border/80 shadow-xl">
              <CardHeader>
                <CardTitle>关闭 DevLoop Agent</CardTitle>
                <CardDescription>
                  请选择“直接退出”或“最小化到托盘”。选择最小化后将记住该偏好。
                </CardDescription>
              </CardHeader>
              <CardContent className="flex flex-wrap justify-end gap-2">
                <Button
                  variant="outline"
                  disabled={resolvingCloseDecision}
                  onClick={() => void resolveWindowCloseAction("exit")}
                >
                  直接退出
                </Button>
                <Button
                  disabled={resolvingCloseDecision}
                  onClick={() => void resolveWindowCloseAction("tray")}
                >
                  最小化到托盘
                </Button>
              </CardContent>
            </Card>
          </div>
        ) : null}

        {renderPage()}
      </div>
    </div>
  );
}
