import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import type { ComponentType, ReactElement } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  AlertTriangle,
  Cable,
  CheckCircle2,
  Cloud,
  Eye,
  LayoutDashboard,
  ListTree,
  LoaderCircle,
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
import { cn } from "@/lib/utils";
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
type UiPhase = "ready" | "restarting" | "recovering";
type ToastLevel = "success" | "error";

interface AppToast {
  id: number;
  level: ToastLevel;
  message: string;
}

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

function formatCountdown(remainingMs: number): string {
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
    return "0s";
  }

  return `${Math.ceil(remainingMs / 1000)}s`;
}

function renderHealthBadge(healthy: boolean): ReactElement {
  if (healthy) {
    return <Badge className="border-transparent bg-emerald-600 text-white">healthy</Badge>;
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

function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

function buildDesktopConfigDraft(view: DesktopConfigView): DesktopConfigSaveRequest {
  return {
    agentApiBase: view.agentApiBase,
    agentBinary: view.agentBinary,
    agentCoreDir: view.agentCoreDir,
    agentAutoRestart: view.agentAutoRestart,
    closeToTrayOnClose: view.closeToTrayOnClose,
    agentRestartBackoffMs: view.agentRestartBackoffMs,
    envResolveOrder: view.envResolveOrder,
    tunnelBridgeAddress: view.tunnelBridgeAddress,
    tunnelBackflowBaseUrl: view.tunnelBackflowBaseUrl,
    tunnelSyncProtocol: view.tunnelSyncProtocol,
    tunnelMasqueAuthMode: view.tunnelMasqueAuthMode,
    tunnelMasquePsk: view.tunnelMasquePsk,
    tunnelMasqueProxyUrl: view.tunnelMasqueProxyUrl,
    tunnelMasqueTargetAddr: view.tunnelMasqueTargetAddr
  };
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
  const [desktopConfigDraftDirty, setDesktopConfigDraftDirty] = useState(false);
  const [selectedInstanceId, setSelectedInstanceId] = useState<string | null>(null);
  const [manualRefreshing, setManualRefreshing] = useState(false);
  const [uiPhase, setUiPhase] = useState<UiPhase>("ready");
  const [manualReconnectPending, setManualReconnectPending] = useState(false);
  const [manualReconnecting, setManualReconnecting] = useState(false);
  const [savingConfig, setSavingConfig] = useState(false);
  const [unregisteringId, setUnregisteringId] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string>("");
  const [toast, setToast] = useState<AppToast | null>(null);
  const [showCloseDecisionDialog, setShowCloseDecisionDialog] = useState(false);
  const [resolvingCloseDecision, setResolvingCloseDecision] = useState(false);
  const [clockMs, setClockMs] = useState(() => Date.now());
  const desktopConfigDraftDirtyRef = useRef(false);
  const phaseBusy = uiPhase !== "ready";

  const markDesktopConfigDraftDirty = useCallback((dirty: boolean) => {
    desktopConfigDraftDirtyRef.current = dirty;
    setDesktopConfigDraftDirty(dirty);
  }, []);

  const patchDesktopConfigDraft = useCallback(
    (patch: Partial<DesktopConfigSaveRequest>) => {
      // 配置编辑一旦发生，先锁定草稿，避免后台轮询覆盖用户输入。
      markDesktopConfigDraftDirty(true);
      setDesktopConfigDraft((current) => (current ? { ...current, ...patch } : current));
    },
    [markDesktopConfigDraftDirty]
  );

  const showToast = useCallback((message: string, level: ToastLevel = "success") => {
    setToast({
      id: Date.now(),
      level,
      message
    });
  }, []);

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

  const refresh = useCallback(async (options?: { manual?: boolean }) => {
    const showManualRefreshing = options?.manual ?? false;
    if (showManualRefreshing) {
      setManualRefreshing(true);
    }

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
      const nextDraft = buildDesktopConfigDraft(desktopConfigData);
      if (desktopConfigDraftDirtyRef.current) {
        // 用户正在编辑时，轮询仅更新只读状态，不回写配置草稿。
        setDesktopConfigDraft((current) => current ?? nextDraft);
      } else {
        setDesktopConfigDraft(nextDraft);
        markDesktopConfigDraftDirty(false);
      }

      // 当前选中的实例如果被删除，则回退到列表首项，保证详情页始终有有效目标。
      setSelectedInstanceId((current) => {
        if (current && registrationData.some((item) => item.instanceId === current)) {
          return current;
        }
        return registrationData[0]?.instanceId ?? null;
      });

      if (uiPhase === "recovering") {
        setUiPhase("ready");
      }
    } catch (error) {
      setActionError(String(error));
    } finally {
      if (showManualRefreshing) {
        setManualRefreshing(false);
      }
    }
  }, [loadDiagnostics, markDesktopConfigDraftDirty, uiPhase]);

  const reconnect = useCallback(async () => {
    if (manualReconnectPending || manualReconnecting || phaseBusy) {
      return;
    }

    setManualReconnectPending(true);
    setActionError("");

    try {
      // 手动重连点击后延迟 1s 再进入联动过渡态，避免状态瞬切。
      await delay(1000);
      setManualReconnectPending(false);
      setManualReconnecting(true);

      setSummary((current) =>
        current
          ? {
              ...current,
              bridgeStatus: "offline",
              tunnelStatus: "disconnected",
              registrationCount: 0,
              activeIntercepts: 0,
              lastUpdateAt: new Date().toISOString()
            }
          : current
      );
      setRegistrations([]);
      setIntercepts([]);
      setSelectedInstanceId(null);
      setTunnel((current) => {
        if (!current) {
          return current;
        }
        return {
          ...current,
          connected: false,
          reconnecting: true,
          reconnectAttempt: 0,
          sessionEpoch: 0,
          resourceVersion: 0,
          lastHeartbeatAt: "",
          nextReconnectAt: null,
          lastReconnectError: ""
        };
      });

      await call<TunnelState>("trigger_reconnect");
      await refresh();
    } catch (error) {
      setActionError(String(error));
    } finally {
      setManualReconnectPending(false);
      setManualReconnecting(false);
    }
  }, [manualReconnectPending, manualReconnecting, phaseBusy, refresh]);

  const restartAgent = useCallback(async () => {
    if (phaseBusy) {
      return;
    }

    setUiPhase("restarting");
    setActionError("");
    // 点击重启后立即把联动状态回到“初始态”，避免页面继续展示旧连接快照。
    setSummary((current) =>
      current
        ? {
            ...current,
            agentStatus: "restarting",
            bridgeStatus: "offline",
            tunnelStatus: "disconnected",
            registrationCount: 0,
            activeIntercepts: 0,
            lastUpdateAt: new Date().toISOString()
          }
        : current
    );
    setTunnel((current) =>
      current
        ? {
            ...current,
            connected: false,
            reconnecting: false,
            reconnectAttempt: 0,
            sessionEpoch: 0,
            resourceVersion: 0,
            lastHeartbeatAt: "",
            nextReconnectAt: null,
            lastReconnectError: ""
          }
        : current
    );
    setRegistrations([]);
    setIntercepts([]);
    setSelectedInstanceId(null);

    // 在后端返回前把 Agent 卡片切到重启中，形成明确过渡态。
    setRuntime((current) =>
      current
        ? {
            ...current,
            status: "restarting",
            lastError: null
          }
        : current
    );

    try {
      const runtimeData = await call<AgentRuntime>("restart_agent_process");
      setRuntime(runtimeData);
      setUiPhase("recovering");
      await refresh();
    } catch (error) {
      setActionError(String(error));
      setUiPhase("ready");
    }
  }, [phaseBusy, refresh]);

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
      setDesktopConfigDraft(buildDesktopConfigDraft(saved));
      // 保存成功后解锁草稿覆盖，后续轮询可继续同步后台配置。
      markDesktopConfigDraftDirty(false);
      showToast("配置保存成功，重启桌面端后生效。", "success");
    } catch (error) {
      setActionError(String(error));
      showToast(`配置保存失败：${String(error)}`, "error");
    } finally {
      setSavingConfig(false);
    }
  }, [desktopConfigDraft, markDesktopConfigDraftDirty, showToast]);

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
    if (uiPhase === "restarting") {
      return;
    }

    // 恢复阶段和断线重连阶段都提升轮询频率，尽快收敛到稳定状态。
    const intervalMs = uiPhase === "recovering" || tunnel?.reconnecting ? 1000 : 5000;
    void refresh();
    const timer = window.setInterval(() => {
      void refresh();
    }, intervalMs);
    return () => window.clearInterval(timer);
  }, [refresh, uiPhase, tunnel?.reconnecting]);

  useEffect(() => {
    if (!desktopConfig || !desktopConfigDraft) {
      return;
    }

    // 通过与“后端基线配置”对比自动判定脏状态，用户撤销修改后可自动解锁轮询覆盖。
    const baselineDraft = buildDesktopConfigDraft(desktopConfig);
    const dirty = JSON.stringify(baselineDraft) !== JSON.stringify(desktopConfigDraft);
    if (desktopConfigDraftDirtyRef.current !== dirty) {
      markDesktopConfigDraftDirty(dirty);
    }
  }, [desktopConfig, desktopConfigDraft, markDesktopConfigDraftDirty]);

  useEffect(() => {
    if (!toast) {
      return;
    }

    // Toast 自动消失，避免用户手动关闭带来的额外交互负担。
    const timer = window.setTimeout(() => {
      setToast((current) => (current?.id === toast.id ? null : current));
    }, 2600);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    // 倒计时改为 200ms 粒度，避免 5s -> 4s 的瞬时跳变看起来“闪一下”。
    const timer = window.setInterval(() => {
      setClockMs(Date.now());
    }, 200);
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
      return <Badge className="border-transparent bg-emerald-600 text-white">connected</Badge>;
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

    // 直接返回毫秒剩余值，展示层按秒/小数秒格式化，视觉上更平滑。
    return Math.max(0, targetMs - clockMs);
  }, [clockMs, tunnel]);

  const bridgeCardStatus = useMemo(() => {
    if (uiPhase === "restarting") {
      return "offline";
    }
    if (uiPhase === "recovering") {
      return tunnel?.connected ? "online" : "recovering";
    }
    if (manualReconnecting) {
      return "manual-reconnecting";
    }
    if (tunnel?.connected) {
      return "online";
    }
    if (tunnel?.reconnecting) {
      return "reconnecting";
    }
    return summary?.bridgeStatus ?? "unknown";
  }, [manualReconnecting, summary?.bridgeStatus, tunnel?.connected, tunnel?.reconnecting, uiPhase]);

  const agentCardStatus = useMemo(() => {
    if (uiPhase === "restarting") {
      return "restarting";
    }
    if (uiPhase === "recovering") {
      return "recovering";
    }
    return runtime?.status ?? summary?.agentStatus ?? "unknown";
  }, [uiPhase, runtime?.status, summary?.agentStatus]);

  const refreshProgressLabel = useMemo(() => {
    if (uiPhase === "recovering") {
      return "状态恢复中...";
    }
    if (manualRefreshing) {
      return "手动刷新中...";
    }
    return "空闲";
  }, [manualRefreshing, uiPhase]);

  const bridgeOperationLabel = useMemo(() => {
    if (uiPhase === "restarting") {
      return "重启 Agent Core...";
    }
    if (uiPhase === "recovering") {
      return "恢复连接中...";
    }
    if (manualReconnectPending) {
      return "1s 后手动重连...";
    }
    if (manualReconnecting) {
      return "手动重连中...";
    }
    if (tunnel?.reconnecting) {
      return "自动重连中...";
    }
    return "空闲";
  }, [manualReconnectPending, manualReconnecting, tunnel?.reconnecting, uiPhase]);

  const masqueConfigVisible = useMemo(() => {
    const protocol = desktopConfigDraft?.tunnelSyncProtocol ?? "";
    return protocol.trim().toLowerCase() === "masque";
  }, [desktopConfigDraft?.tunnelSyncProtocol]);

  const masquePskVisible = useMemo(() => {
    const authMode = desktopConfigDraft?.tunnelMasqueAuthMode ?? "";
    return masqueConfigVisible && authMode.trim().toLowerCase() === "psk";
  }, [desktopConfigDraft?.tunnelMasqueAuthMode, masqueConfigVisible]);

  const renderDashboard = (): ReactElement => (
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Card
        className={cn(
          "transition-all duration-300",
          phaseBusy && "border-amber-500/60 shadow-lg shadow-amber-900/10"
        )}
      >
        <CardHeader className="pb-2">
          <CardDescription>Agent</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Server className={cn("h-4 w-4", phaseBusy && "animate-pulse")} />
            {agentCardStatus}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>pid: {runtime?.pid ?? "-"}</div>
          <div>env: {summary?.currentEnv ?? "-"}</div>
          <div>status: {agentCardStatus}</div>
          <div>operation: {uiPhase === "restarting" ? "重启 Agent Core..." : uiPhase === "recovering" ? "恢复状态中..." : "空闲"}</div>
          <div>sync: {refreshProgressLabel}</div>
          <div>restart-count: {runtime?.restartCount ?? 0}</div>
          <div className="whitespace-pre-wrap break-all">
            last-error: {runtime?.lastError ? runtime.lastError : "-"}
          </div>
        </CardContent>
      </Card>

      <Card
        className={cn(
          "transition-all duration-300",
          (manualReconnectPending || manualReconnecting || phaseBusy) && "border-amber-500/60 shadow-lg shadow-amber-900/10"
        )}
      >
        <CardHeader className="pb-2">
          <CardDescription>Bridge</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Cloud className={cn("h-4 w-4", (manualReconnectPending || manualReconnecting || phaseBusy) && "animate-pulse")} />
            {bridgeCardStatus}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>address: {tunnel?.bridgeAddress ?? "-"}</div>
          <div>rdName: {summary?.rdName ?? "-"}</div>
          <div>operation: {bridgeOperationLabel}</div>
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
          <div>protocol: {tunnel?.protocol ?? "-"}</div>
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
          {desktopConfigDraftDirty ? (
            <p className="text-xs text-amber-900">
              检测到未保存的配置修改，后台轮询不会覆盖当前表单。
            </p>
          ) : null}

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentApiBase</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentApiBase ?? ""}
              onChange={(event) => patchDesktopConfigDraft({ agentApiBase: event.target.value })}
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentBinary</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentBinary ?? ""}
              onChange={(event) =>
                patchDesktopConfigDraft({
                  agentBinary: event.target.value.trim() === "" ? null : event.target.value
                })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentCoreDir</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.agentCoreDir ?? ""}
              onChange={(event) =>
                patchDesktopConfigDraft({
                  agentCoreDir: event.target.value.trim() === "" ? null : event.target.value
                })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">agentRestartBackoffMs (csv)</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.agentRestartBackoffMs ?? []).join(",")}
              onChange={(event) =>
                patchDesktopConfigDraft({
                  agentRestartBackoffMs: parseNumberList(event.target.value)
                })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">envResolveOrder (csv)</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={(desktopConfigDraft?.envResolveOrder ?? []).join(",")}
              onChange={(event) =>
                patchDesktopConfigDraft({
                  envResolveOrder: parseStringList(event.target.value)
                })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">tunnelBridgeAddress</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBridgeAddress ?? ""}
              onChange={(event) =>
                patchDesktopConfigDraft({ tunnelBridgeAddress: event.target.value })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">tunnelBackflowBaseUrl</label>
            <input
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelBackflowBaseUrl ?? ""}
              onChange={(event) =>
                patchDesktopConfigDraft({ tunnelBackflowBaseUrl: event.target.value })
              }
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs text-muted-foreground">tunnelSyncProtocol</label>
            <select
              className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
              value={desktopConfigDraft?.tunnelSyncProtocol ?? "http"}
              onChange={(event) =>
                patchDesktopConfigDraft({ tunnelSyncProtocol: event.target.value })
              }
            >
              <option value="http">http</option>
              <option value="masque">masque</option>
            </select>
          </div>

          {/* 仅在 MASQUE 模式展示扩展参数，避免 HTTP 模式下出现无关配置噪音。 */}
          {masqueConfigVisible ? (
            <>
              <div className="space-y-1">
                <label className="text-xs text-muted-foreground">tunnelMasqueAuthMode</label>
                <select
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  value={desktopConfigDraft?.tunnelMasqueAuthMode ?? "psk"}
                  onChange={(event) =>
                    patchDesktopConfigDraft({
                      tunnelMasqueAuthMode: event.target.value
                    })
                  }
                >
                  <option value="psk">psk</option>
                  <option value="ecdh">ecdh</option>
                </select>
              </div>

              {masquePskVisible ? (
                <div className="space-y-1">
                  <label className="text-xs text-muted-foreground">tunnelMasquePsk</label>
                  <input
                    className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                    value={desktopConfigDraft?.tunnelMasquePsk ?? ""}
                    onChange={(event) =>
                      patchDesktopConfigDraft({
                        tunnelMasquePsk: event.target.value
                      })
                    }
                  />
                </div>
              ) : (
                <p className="text-xs text-muted-foreground">
                  当前为 ECDH 模式，`tunnelMasquePsk` 不参与鉴权。
                </p>
              )}

              <div className="space-y-1">
                <label className="text-xs text-muted-foreground">tunnelMasqueProxyUrl</label>
                <input
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  placeholder="留空按 tunnelBridgeAddress 自动推导"
                  value={desktopConfigDraft?.tunnelMasqueProxyUrl ?? ""}
                  onChange={(event) =>
                    patchDesktopConfigDraft({
                      tunnelMasqueProxyUrl: event.target.value
                    })
                  }
                />
              </div>

              <div className="space-y-1">
                <label className="text-xs text-muted-foreground">tunnelMasqueTargetAddr</label>
                <input
                  className="w-full rounded-md border border-border/70 bg-background px-2 py-1 font-mono text-xs"
                  value={desktopConfigDraft?.tunnelMasqueTargetAddr ?? ""}
                  onChange={(event) =>
                    patchDesktopConfigDraft({
                      tunnelMasqueTargetAddr: event.target.value
                    })
                  }
                />
              </div>
            </>
          ) : null}

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.agentAutoRestart ?? true}
              onChange={(event) =>
                patchDesktopConfigDraft({ agentAutoRestart: event.target.checked })
              }
            />
            agentAutoRestart
          </label>

          <label className="flex items-center gap-2 text-xs text-muted-foreground">
            <input
              type="checkbox"
              checked={desktopConfigDraft?.closeToTrayOnClose ?? true}
              onChange={(event) =>
                patchDesktopConfigDraft({ closeToTrayOnClose: event.target.checked })
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
            <LoaderCircle className={cn("mr-2 h-4 w-4", savingConfig && "animate-spin")} />
            {savingConfig ? "保存中..." : "保存配置"}
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
    <div className="h-screen w-screen overflow-hidden bg-app-gradient text-foreground">
      <div className="flex h-full w-full flex-col gap-6 overflow-auto px-4 pb-6 pt-6 sm:px-6 lg:px-8">
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
            <Button
              variant="outline"
              onClick={() => void refresh({ manual: true })}
              disabled={manualRefreshing || phaseBusy || manualReconnectPending || manualReconnecting}
            >
              <RefreshCcw className={cn("mr-2 h-4 w-4", manualRefreshing && "animate-spin")} />
              {manualRefreshing ? "刷新中..." : "刷新"}
            </Button>
            <Button
              variant="outline"
              onClick={() => void restartAgent()}
              disabled={manualRefreshing || phaseBusy || manualReconnectPending || manualReconnecting}
            >
              <RotateCcw className={cn("mr-2 h-4 w-4", phaseBusy && "animate-spin")} />
              {uiPhase === "restarting"
                ? "重启中..."
                : uiPhase === "recovering"
                  ? "恢复中..."
                  : "重启 Agent Core"}
            </Button>
            <Button
              onClick={() => void reconnect()}
              disabled={manualRefreshing || phaseBusy || manualReconnectPending || manualReconnecting}
            >
              <Cable className={cn("mr-2 h-4 w-4", (manualReconnectPending || manualReconnecting) && "animate-pulse")} />
              {manualReconnectPending ? "准备重连..." : manualReconnecting ? "重连中..." : "手动重连"}
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

        {toast ? (
          <div className="fixed bottom-6 right-6 z-50">
            <div
              className={cn(
                "flex max-w-sm items-center gap-2 rounded-md border px-3 py-2 text-sm shadow-xl transition-all duration-300",
                toast.level === "success"
                  ? "border-emerald-500/40 bg-emerald-100/95 text-emerald-950"
                  : "border-amber-500/40 bg-amber-100/95 text-amber-950"
              )}
            >
              {toast.level === "success" ? (
                <CheckCircle2 className="h-4 w-4" />
              ) : (
                <AlertTriangle className="h-4 w-4" />
              )}
              <span>{toast.message}</span>
            </div>
          </div>
        ) : null}

        {renderPage()}
      </div>
    </div>
  );
}
