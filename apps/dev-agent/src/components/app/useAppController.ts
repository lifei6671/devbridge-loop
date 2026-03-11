import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";

import { PAGE_ITEMS } from "@/components/app/constants";
import {
  buildDesktopConfigDraft,
  delay
} from "@/components/app/helpers";
import type { AppToast, PageKey, ToastLevel, UiPhase } from "@/components/app/types";
import type {
  ActiveIntercept,
  AgentRuntime,
  DesktopConfigSaveRequest,
  DesktopConfigView,
  DiagnosticsSnapshot,
  ErrorEntry,
  LocalRegistration,
  RequestSummary,
  StateSummary,
  TunnelState
} from "@/types";

async function call<T>(command: string, args?: Record<string, unknown>): Promise<T> {
  return invoke<T>(command, args);
}

function formatUnknownError(error: unknown): string {
  return error instanceof Error ? error.message : String(error);
}

function normalizeEnvName(value: string): string {
  const trimmed = value.trim();
  if (trimmed === "") {
    return "dev-default";
  }
  return trimmed;
}

function buildDefaultDesktopConfigDraft(): DesktopConfigSaveRequest {
  return {
    agentHttpAddr: "0.0.0.0:39090",
    agentApiBase: "http://127.0.0.1:39090",
    envName: "dev-default",
    agentBinary: null,
    agentCoreDir: null,
    agentAutoRestart: true,
    closeToTrayOnClose: true,
    agentRestartBackoffMs: [500, 1000, 2000, 5000],
    envResolveOrder: ["requestHeader", "payload", "runtimeDefault", "baseFallback"],
    tunnelBridgeAddress: "http://127.0.0.1:38080",
    tunnelBackflowBaseUrl: "http://127.0.0.1:39090",
    tunnelSyncProtocol: "http",
    tunnelMasqueAuthMode: "psk",
    tunnelMasquePsk: "devloop-masque-default-psk",
    tunnelMasqueProxyUrl: "",
    tunnelMasqueTargetAddr: "127.0.0.1:39081"
  };
}

function validateAgentHttpAddr(value: string): string | null {
  const trimmed = value.trim();
  if (trimmed === "") {
    return "核心监听地址不能为空";
  }

  const withoutScheme = trimmed.replace(/^https?:\/\//i, "");
  const hostPort = withoutScheme.split("/")[0]?.trim() ?? "";
  if (hostPort === "") {
    return "核心监听地址格式无效，请使用 host:port";
  }

  let portText = "";
  if (hostPort.startsWith(":")) {
    portText = hostPort.slice(1);
  } else if (hostPort.startsWith("[")) {
    const closeIndex = hostPort.indexOf("]");
    if (closeIndex <= 0) {
      return "IPv6 地址请使用 [::1]:39090 格式";
    }
    if (hostPort.length <= closeIndex + 2 || hostPort[closeIndex + 1] !== ":") {
      return "核心监听地址缺少端口，请使用 host:port";
    }
    portText = hostPort.slice(closeIndex + 2);
  } else {
    const colonIndex = hostPort.lastIndexOf(":");
    if (colonIndex <= 0) {
      return "核心监听地址缺少端口，请使用 host:port";
    }
    const host = hostPort.slice(0, colonIndex).trim();
    if (host === "") {
      return "核心监听地址缺少主机名";
    }
    if (host.includes(":")) {
      return "IPv6 地址请使用 [::1]:39090 格式";
    }
    portText = hostPort.slice(colonIndex + 1);
  }

  if (!/^\d+$/.test(portText)) {
    return "核心监听地址端口必须是数字";
  }
  const port = Number(portText);
  if (!Number.isInteger(port) || port < 1 || port > 65535) {
    return "核心监听地址端口范围必须在 1-65535";
  }
  return null;
}

export interface PendingUnregisterRegistration {
  env: string;
  instanceId: string;
  serviceName: string;
}

export interface AppController {
  actionError: string;
  activePage: PageKey;
  activePageMeta: (typeof PAGE_ITEMS)[number];
  agentCardStatus: string;
  bridgeCardStatus: string;
  bridgeOperationLabel: string;
  clearingLogs: boolean;
  desktopConfig: DesktopConfigView | null;
  desktopConfigDraft: DesktopConfigSaveRequest | null;
  desktopConfigDraftDirty: boolean;
  agentHttpAddrValidationError: string | null;
  errors: ErrorEntry[];
  intercepts: ActiveIntercept[];
  manualReconnectPending: boolean;
  manualReconnecting: boolean;
  manualRefreshing: boolean;
  masqueConfigVisible: boolean;
  masquePskVisible: boolean;
  onChangePage: (page: PageKey) => void;
  onCancelUnregisterRegistration: () => void;
  onClearSelectedInstance: () => void;
  onClearLogs: () => void;
  onConfirmUnregisterRegistration: () => void;
  onPatchDesktopConfigDraft: (patch: Partial<DesktopConfigSaveRequest>) => void;
  onReconnect: () => void;
  onRefresh: () => void;
  onResolveCloseDecision: (action: "tray" | "exit") => void;
  onRestartAgent: () => void;
  onSaveCurrentEnv: (envName: string) => void;
  onSaveDesktopConfig: () => void;
  onSelectInstance: (instanceId: string) => void;
  onUnregisterRegistration: (instanceId: string) => void;
  pageItems: typeof PAGE_ITEMS;
  phaseBusy: boolean;
  pendingUnregisterRegistration: PendingUnregisterRegistration | null;
  reconnectCountdown: number | null;
  refreshProgressLabel: string;
  registrations: LocalRegistration[];
  requests: RequestSummary[];
  resolvingCloseDecision: boolean;
  resolvingUnregisterRegistration: boolean;
  runtime: AgentRuntime | null;
  savingConfig: boolean;
  selectedInstanceId: string | null;
  selectedRegistration: LocalRegistration | null;
  showCloseDecisionDialog: boolean;
  summary: StateSummary | null;
  toast: AppToast | null;
  tunnel: TunnelState | null;
  uiPhase: UiPhase;
  unregisteringId: string | null;
}

export function useAppController(): AppController {
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
  const [clearingLogs, setClearingLogs] = useState(false);
  const [unregisteringId, setUnregisteringId] = useState<string | null>(null);
  const [pendingUnregisterRegistration, setPendingUnregisterRegistration] =
    useState<PendingUnregisterRegistration | null>(null);
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
      markDesktopConfigDraftDirty(true);
      setDesktopConfigDraft((current) => ({
        ...(current ?? buildDefaultDesktopConfigDraft()),
        ...patch
      }));
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

  const syncDesktopConfig = useCallback(
    (desktopConfigData: DesktopConfigView) => {
      setDesktopConfig(desktopConfigData);
      const nextDraft = buildDesktopConfigDraft(desktopConfigData);
      if (desktopConfigDraftDirtyRef.current) {
        setDesktopConfigDraft((current) => current ?? nextDraft);
      } else {
        setDesktopConfigDraft(nextDraft);
        markDesktopConfigDraftDirty(false);
      }
    },
    [markDesktopConfigDraftDirty]
  );

  const agentHttpAddrValidationError = useMemo(() => {
    if (!desktopConfigDraft) {
      return null;
    }
    return validateAgentHttpAddr(desktopConfigDraft.agentHttpAddr);
  }, [desktopConfigDraft?.agentHttpAddr]);

  const loadDiagnostics = useCallback(async (): Promise<DiagnosticsSnapshot> => {
    try {
      return await call<DiagnosticsSnapshot>("get_diagnostics");
    } catch {
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

  const refresh = useCallback(
    async (options?: { manual?: boolean }) => {
      const showManualRefreshing = options?.manual ?? false;
      if (showManualRefreshing) {
        setManualRefreshing(true);
      }

      try {
        const [runtimeResult, desktopConfigResult] = await Promise.allSettled([
          (async () => {
            const [
              diagnosticsData,
              tunnelData,
              registrationData,
              runtimeData,
              interceptData
            ] = await Promise.all([
              loadDiagnostics(),
              call<TunnelState>("get_tunnel_state"),
              call<LocalRegistration[]>("get_registrations"),
              call<AgentRuntime>("agent_runtime"),
              call<ActiveIntercept[]>("get_active_intercepts")
            ]);

            return {
              diagnosticsData,
              tunnelData,
              registrationData,
              runtimeData,
              interceptData
            };
          })(),
          call<DesktopConfigView>("get_desktop_config")
        ]);

        let nextError = "";

        if (desktopConfigResult.status === "fulfilled") {
          syncDesktopConfig(desktopConfigResult.value);
        } else {
          nextError = formatUnknownError(desktopConfigResult.reason);
        }

        if (runtimeResult.status === "fulfilled") {
          const {
            diagnosticsData,
            tunnelData,
            registrationData,
            runtimeData,
            interceptData
          } = runtimeResult.value;

          setSummary(diagnosticsData.summary);
          setTunnel(tunnelData);
          setRegistrations(registrationData);
          setErrors(diagnosticsData.recentErrors);
          setRequests(diagnosticsData.recentRequests);
          setRuntime(runtimeData);
          setIntercepts(interceptData);

          setSelectedInstanceId((current) => {
            if (current && registrationData.some((item) => item.instanceId === current)) {
              return current;
            }
            return null;
          });

          if (uiPhase === "recovering") {
            setUiPhase("ready");
          }
        } else {
          nextError = formatUnknownError(runtimeResult.reason);
        }

        setActionError(nextError);
      } finally {
        if (showManualRefreshing) {
          setManualRefreshing(false);
        }
      }
    },
    [loadDiagnostics, syncDesktopConfig, uiPhase]
  );

  const reconnect = useCallback(async () => {
    if (manualReconnectPending || manualReconnecting || phaseBusy) {
      return;
    }

    setManualReconnectPending(true);
    setActionError("");

    try {
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
    if (agentHttpAddrValidationError) {
      setActionError(agentHttpAddrValidationError);
      showToast(`配置校验失败：${agentHttpAddrValidationError}`, "error");
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
      markDesktopConfigDraftDirty(false);
      showToast("配置保存成功，重启核心进程后生效。", "success");
    } catch (error) {
      setActionError(String(error));
      showToast(`配置保存失败：${String(error)}`, "error");
    } finally {
      setSavingConfig(false);
    }
  }, [agentHttpAddrValidationError, desktopConfigDraft, markDesktopConfigDraftDirty, showToast]);

  const saveCurrentEnv = useCallback(async (envNameInput: string) => {
    const normalizedEnvName = normalizeEnvName(envNameInput);
    const baseDraft = desktopConfig
      ? buildDesktopConfigDraft(desktopConfig)
      : desktopConfigDraft ?? buildDefaultDesktopConfigDraft();
    if (baseDraft.envName === normalizedEnvName) {
      return;
    }

    const request: DesktopConfigSaveRequest = {
      ...baseDraft,
      envName: normalizedEnvName
    };

    setSavingConfig(true);
    setActionError("");
    try {
      const saved = await call<DesktopConfigView>("save_desktop_config", { request });
      setDesktopConfig(saved);
      setDesktopConfigDraft((current) => {
        if (!desktopConfigDraftDirtyRef.current || !current) {
          return buildDesktopConfigDraft(saved);
        }
        return {
          ...current,
          envName: saved.envName
        };
      });
      showToast("默认环境已保存，重启核心进程后生效。", "success");
    } catch (error) {
      const message = formatUnknownError(error);
      setActionError(message);
      showToast(`保存默认环境失败：${message}`, "error");
    } finally {
      setSavingConfig(false);
    }
  }, [desktopConfig, desktopConfigDraft, showToast]);

  const clearLogs = useCallback(async () => {
    if (clearingLogs) {
      return;
    }

    setClearingLogs(true);
    setActionError("");
    try {
      await call("clear_recent_logs");
      setErrors([]);
      setRequests([]);
      showToast("运行日志已清空。", "success");
      await refresh();
    } catch (error) {
      const message = formatUnknownError(error);
      setActionError(message);
      showToast(`清空日志失败：${message}`, "error");
    } finally {
      setClearingLogs(false);
    }
  }, [clearingLogs, refresh, showToast]);

  const unregisterRegistration = useCallback((instanceId: string) => {
    const target = registrations.find((item) => item.instanceId === instanceId);
    if (!target) {
      return;
    }
    setPendingUnregisterRegistration({
      env: target.env,
      instanceId: target.instanceId,
      serviceName: target.serviceName
    });
  }, [registrations]);

  const confirmUnregisterRegistration = useCallback(async () => {
    if (!pendingUnregisterRegistration || unregisteringId) {
      return;
    }

    const instanceId = pendingUnregisterRegistration.instanceId;
    setUnregisteringId(instanceId);
    setActionError("");
    try {
      await call("unregister_registration", { instanceId });
      setPendingUnregisterRegistration(null);
      await refresh();
    } catch (error) {
      setActionError(String(error));
    } finally {
      setUnregisteringId(null);
    }
  }, [pendingUnregisterRegistration, refresh, unregisteringId]);

  const resolveWindowCloseAction = useCallback(async (action: "tray" | "exit") => {
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
  }, []);

  useEffect(() => {
    if (uiPhase === "restarting") {
      return;
    }

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

    const baselineDraft = buildDesktopConfigDraft(desktopConfig);
    const dirty = JSON.stringify(baselineDraft) !== JSON.stringify(desktopConfigDraft);
    if (desktopConfigDraftDirtyRef.current !== dirty) {
      markDesktopConfigDraftDirty(dirty);
    }
  }, [desktopConfig, desktopConfigDraft, markDesktopConfigDraftDirty]);

  useEffect(() => {
    if (!pendingUnregisterRegistration) {
      return;
    }
    const exists = registrations.some(
      (item) => item.instanceId === pendingUnregisterRegistration.instanceId
    );
    if (!exists) {
      setPendingUnregisterRegistration(null);
    }
  }, [pendingUnregisterRegistration, registrations]);

  useEffect(() => {
    if (!toast) {
      return;
    }

    const timer = window.setTimeout(() => {
      setToast((current) => (current?.id === toast.id ? null : current));
    }, 2600);
    return () => window.clearTimeout(timer);
  }, [toast]);

  useEffect(() => {
    const timer = window.setInterval(() => {
      setClockMs(Date.now());
    }, 200);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    let unlistenRuntime: (() => void) | null = null;
    let unlistenCloseIntent: (() => void) | null = null;

    const setupListener = async (): Promise<void> => {
      unlistenRuntime = await listen<AgentRuntime>("agent-runtime-changed", (event) => {
        setRuntime(event.payload);
      });
      unlistenCloseIntent = await listen("window-close-intent", () => {
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

  const reconnectCountdown = useMemo(() => {
    if (!tunnel || tunnel.connected || !tunnel.nextReconnectAt) {
      return null;
    }

    const targetMs = Date.parse(tunnel.nextReconnectAt);
    if (Number.isNaN(targetMs)) {
      return null;
    }

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
      return "重启核心进程...";
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

  const activePageMeta = PAGE_ITEMS.find((item) => item.key === activePage) ?? PAGE_ITEMS[0];

  const onRefresh = useCallback(() => {
    void refresh({ manual: true });
  }, [refresh]);

  const onClearLogs = useCallback(() => {
    void clearLogs();
  }, [clearLogs]);

  const onClearSelectedInstance = useCallback(() => {
    setSelectedInstanceId(null);
  }, []);

  const onCancelUnregisterRegistration = useCallback(() => {
    setPendingUnregisterRegistration(null);
  }, []);

  const onConfirmUnregisterRegistration = useCallback(() => {
    void confirmUnregisterRegistration();
  }, [confirmUnregisterRegistration]);

  const onReconnect = useCallback(() => {
    void reconnect();
  }, [reconnect]);

  const onRestartAgent = useCallback(() => {
    void restartAgent();
  }, [restartAgent]);

  const onSaveDesktopConfig = useCallback(() => {
    void saveDesktopConfig();
  }, [saveDesktopConfig]);

  const onSaveCurrentEnv = useCallback((envName: string) => {
    void saveCurrentEnv(envName);
  }, [saveCurrentEnv]);

  const onUnregisterRegistration = useCallback((instanceId: string) => {
    void unregisterRegistration(instanceId);
  }, [unregisterRegistration]);

  const onResolveCloseDecision = useCallback((action: "tray" | "exit") => {
    void resolveWindowCloseAction(action);
  }, [resolveWindowCloseAction]);

  return {
    actionError,
    activePage,
    activePageMeta,
    agentCardStatus,
    bridgeCardStatus,
    bridgeOperationLabel,
    clearingLogs,
    agentHttpAddrValidationError,
    desktopConfig,
    desktopConfigDraft,
    desktopConfigDraftDirty,
    errors,
    intercepts,
    manualReconnectPending,
    manualReconnecting,
    manualRefreshing,
    masqueConfigVisible,
    masquePskVisible,
    onChangePage: setActivePage,
    onCancelUnregisterRegistration,
    onClearSelectedInstance,
    onClearLogs,
    onConfirmUnregisterRegistration,
    onPatchDesktopConfigDraft: patchDesktopConfigDraft,
    onReconnect,
    onRefresh,
    onResolveCloseDecision,
    onRestartAgent,
    onSaveCurrentEnv,
    onSaveDesktopConfig,
    onSelectInstance: setSelectedInstanceId,
    onUnregisterRegistration,
    pageItems: PAGE_ITEMS,
    pendingUnregisterRegistration,
    phaseBusy,
    reconnectCountdown,
    refreshProgressLabel,
    registrations,
    requests,
    resolvingCloseDecision,
    resolvingUnregisterRegistration: unregisteringId !== null,
    runtime,
    savingConfig,
    selectedInstanceId,
    selectedRegistration,
    showCloseDecisionDialog,
    summary,
    toast,
    tunnel,
    uiPhase,
    unregisteringId
  };
}
