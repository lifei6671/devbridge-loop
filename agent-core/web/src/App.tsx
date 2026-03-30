import type { FormEvent } from "react";
import { useDeferredValue, useEffect, useRef, useState } from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Toast, ToastDescription, ToastProvider, ToastTitle, ToastViewport } from "@/components/ui/toast";
import {
  ConsoleShell,
  type ConsoleMetric,
  LoginScreen,
} from "@/console-shell";
import {
  type AgentSnapshot,
  type ConsoleData,
  type DiagnoseLogsResponse,
  type DiagnoseSummary,
  emptyConsoleData,
  formatCount,
  type LoginResponse,
  navigationItems,
  normalizeKeyword,
  type PageKey,
  parseSSEEnvelope,
  readPageFromHash,
  type ServiceItem,
  type ServiceListResponse,
  type SessionSnapshot,
  statusBadgeVariant,
  type TrafficSnapshot,
  type TunnelListResponse,
} from "@/console-shared";
import {
  buildServicePayload,
  defaultServiceForm,
  type ServiceFormErrors,
  type ServiceFormState,
  toServiceForm,
  validateServiceForm,
} from "@/service-form";
import { ServiceDialog } from "@/service-dialog";
import {
  buildConfigFromSettingsDraft,
  type ConfigSnapshot,
  type SettingsFieldErrors,
  settingsDraftIsDirty,
  shouldHydrateSettingsDraft,
  type SettingsDraft,
  toSettingsDraft,
  validateSettingsDraft,
} from "@/settings";

type AuthState = "checking" | "anonymous" | "authenticated";
type RealtimeMode = "off" | "connecting" | "polling" | "sse";
type AppToastVariant = "success" | "warning" | "danger";

type AppToast = {
  id: number;
  open: boolean;
  title: string;
  description?: string;
  variant: AppToastVariant;
};

type ServiceDialogState =
  | {
      mode: "create";
      service: null;
    }
  | {
      mode: "edit" | "detail";
      service: ServiceItem;
    };

class APIError extends Error {
  status: number;

  constructor(status: number, message: string) {
    super(message);
    this.status = status;
  }
}

class UnexpectedContentTypeError extends Error {
  contentType: string;

  path: string;

  constructor(path: string, contentType: string) {
    super(`接口 ${path} 返回了非 JSON 响应(${contentType || "unknown"})。`);
    this.path = path;
    this.contentType = contentType;
  }
}

function resolveBasePath() {
  const rawPath = window.location.pathname.replace(/\/+$/, "");
  if (rawPath === "") {
    return "";
  }
  if (rawPath.endsWith("/index.html")) {
    return rawPath.slice(0, -"/index.html".length);
  }
  return rawPath;
}

const appBasePath = resolveBasePath();
const apiBasePath = `${appBasePath}/api`;
const sseReadyEventName = "agent.ready";
const sseSnapshotEventName = "agent.snapshot";
const sseReconnectIntervalMS = 12000;

async function requestJSON<T>(path: string, init?: RequestInit): Promise<T> {
  const response = await fetch(`${apiBasePath}${path}`, {
    credentials: "include",
    headers: {
      "Content-Type": "application/json",
      ...(init?.headers ?? {}),
    },
    ...init,
  });

  if (!response.ok) {
    if (response.status === 401) {
      throw new APIError(401, "登录已失效，请重新登录。");
    }
    let message = `请求失败(${response.status})`;
    try {
      const errorPayload = (await response.json()) as { message?: string };
      if (errorPayload.message) {
        message = errorPayload.message;
      }
    } catch {
      // noop
    }
    throw new APIError(response.status, message);
  }

  const contentType = response.headers.get("content-type") || "";
  if (!contentType.toLowerCase().includes("application/json")) {
    throw new UnexpectedContentTypeError(path, contentType);
  }

  return (await response.json()) as T;
}

async function loadSession(): Promise<LoginResponse> {
  return requestJSON<LoginResponse>("/session");
}

async function loadConsoleData(): Promise<ConsoleData> {
  const [agent, session, services, tunnels, traffic, diagnose, logs, config] = await Promise.all([
    requestJSON<AgentSnapshot>("/agent/snapshot"),
    requestJSON<SessionSnapshot>("/session/snapshot"),
    requestJSON<ServiceListResponse>("/services"),
    requestJSON<TunnelListResponse>("/tunnels"),
    requestJSON<TrafficSnapshot>("/traffic/stats"),
    requestJSON<DiagnoseSummary>("/diagnose/summary"),
    requestJSON<DiagnoseLogsResponse>("/diagnose/logs"),
    requestJSON<ConfigSnapshot>("/app/config"),
  ]);

  return { agent, session, services, tunnels, traffic, diagnose, logs, config };
}

function isStaticPreviewFallbackError(error: unknown, path: string) {
  return error instanceof UnexpectedContentTypeError && error.path === path && error.contentType.toLowerCase().includes("text/html");
}

function App() {
  const [authState, setAuthState] = useState<AuthState>("checking");
  const [sessionUsername, setSessionUsername] = useState("");
  const [page, setPage] = useState<PageKey>(readPageFromHash());
  const [filterKeyword, setFilterKeyword] = useState("");
  const deferredFilterKeyword = useDeferredValue(filterKeyword);
  const [data, setData] = useState<ConsoleData>(emptyConsoleData);
  const [loading, setLoading] = useState(false);
  const [actionPending, setActionPending] = useState("");
  const [pageError, setPageError] = useState("");
  const [loginUsername, setLoginUsername] = useState("admin");
  const [loginPassword, setLoginPassword] = useState("");
  const [serviceDialog, setServiceDialog] = useState<ServiceDialogState | null>(null);
  const [serviceForm, setServiceForm] = useState<ServiceFormState>(defaultServiceForm);
  const [serviceFieldErrors, setServiceFieldErrors] = useState<ServiceFormErrors>({});
  const [servicePendingDelete, setServicePendingDelete] = useState<ServiceItem | null>(null);
  const [settingsDraft, setSettingsDraft] = useState<SettingsDraft | null>(null);
  const [settingsFieldErrors, setSettingsFieldErrors] = useState<SettingsFieldErrors>({});
  const [savingSettings, setSavingSettings] = useState(false);
  const [toasts, setToasts] = useState<AppToast[]>([]);
  const [realtimeMode, setRealtimeMode] = useState<RealtimeMode>("off");
  const settingsDraftWasEditedRef = useRef(false);
  const settingsWarningShownRef = useRef(false);
  const toastSequenceRef = useRef(0);

  function pushToast(variant: AppToastVariant, title: string, description?: string) {
    const id = toastSequenceRef.current + 1;
    toastSequenceRef.current = id;
    setToasts((current) => [...current, { id, open: true, title, description, variant }].slice(-4));
  }

  function dismissToast(id: number, open: boolean) {
    if (open) {
      return;
    }
    setToasts((current) => current.filter((item) => item.id !== id));
  }

  function getErrorMessage(error: unknown, fallback: string) {
    return error instanceof Error ? error.message : fallback;
  }

  useEffect(() => {
    const syncHash = () => {
      setPage(readPageFromHash());
    };
    window.addEventListener("hashchange", syncHash);
    syncHash();
    return () => {
      window.removeEventListener("hashchange", syncHash);
    };
  }, []);

  useEffect(() => {
    let cancelled = false;

    const bootstrap = async () => {
      try {
        const session = await loadSession();
        if (cancelled) {
          return;
        }
        setAuthState(session.authenticated ? "authenticated" : "anonymous");
        setRealtimeMode(session.authenticated ? "connecting" : "off");
        setSessionUsername(session.username ?? "");
        if (session.authenticated) {
          setLoading(true);
          const nextData = await loadConsoleData();
          if (cancelled) {
            return;
          }
          setData(nextData);
        }
      } catch (error) {
        if (cancelled) {
          return;
        }
        if (error instanceof APIError && error.status === 401) {
          setAuthState("anonymous");
          setRealtimeMode("off");
          return;
        }
        if (isStaticPreviewFallbackError(error, "/session")) {
          setAuthState("anonymous");
          setRealtimeMode("off");
          setPageError("");
          return;
        }
        setAuthState("anonymous");
        setRealtimeMode("off");
        setPageError(error instanceof Error ? error.message : "初始化控制台失败。");
      } finally {
        if (!cancelled) {
          setLoading(false);
        }
      }
    };

    void bootstrap();
    return () => {
      cancelled = true;
    };
  }, []);

  useEffect(() => {
    if (authState !== "authenticated") {
      setRealtimeMode("off");
      return;
    }
    if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
      setRealtimeMode("polling");
      return;
    }

    const eventSource = new window.EventSource(`${apiBasePath}/events/stream`);
    let hasReceivedReadyOrSnapshot = false;
    setRealtimeMode("connecting");

    const handleReady = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null) {
        return;
      }
      hasReceivedReadyOrSnapshot = true;
      setRealtimeMode("sse");
    };

    const handleSnapshot = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null || envelope.payload === undefined) {
        return;
      }
      hasReceivedReadyOrSnapshot = true;
      setPageError("");
      setData(envelope.payload);
      setRealtimeMode("sse");
    };

    eventSource.addEventListener(sseReadyEventName, handleReady as EventListener);
    eventSource.addEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
    eventSource.onerror = () => {
      if (!hasReceivedReadyOrSnapshot) {
        eventSource.close();
        setRealtimeMode("polling");
      }
    };

    return () => {
      eventSource.removeEventListener(sseReadyEventName, handleReady as EventListener);
      eventSource.removeEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
      eventSource.close();
    };
  }, [authState]);

  useEffect(() => {
    if (authState !== "authenticated" || realtimeMode !== "polling") {
      return;
    }
    const intervalID = window.setInterval(() => {
      void refreshConsoleData(true);
    }, sseReconnectIntervalMS);
    return () => {
      window.clearInterval(intervalID);
    };
  }, [authState, realtimeMode]);

  const settingsDirty = settingsDraft ? settingsDraftIsDirty(settingsDraft, data.config) : false;

  useEffect(() => {
    setSettingsDraft((currentDraft) => {
      if (!shouldHydrateSettingsDraft(currentDraft, data.config, settingsDraftWasEditedRef.current)) {
        return currentDraft;
      }
      settingsDraftWasEditedRef.current = false;
      return toSettingsDraft(data.config);
    });
  }, [data.config.config_version, data.config.updated_at_ms, data.config]);

  useEffect(() => {
    const shouldWarn =
      authState === "authenticated" &&
      page === "settings" &&
      settingsDraft?.transport === "quic_native" &&
      !settingsDraft.bridgeTLSEnabled;
    if (shouldWarn && !settingsWarningShownRef.current) {
      pushToast("warning", "QUIC 需要同时启用 TLS", "请开启 `bridge_tls_enabled`，并继续补齐根证书文件后再保存配置。");
      settingsWarningShownRef.current = true;
      return;
    }
    if (!shouldWarn) {
      settingsWarningShownRef.current = false;
    }
  }, [authState, page, settingsDraft?.bridgeTLSEnabled, settingsDraft?.transport]);

  async function refreshConsoleData(silent = false) {
    if (!silent) {
      setLoading(true);
    }
    if (authState !== "authenticated") {
      setPageError("");
    }
    try {
      const nextData = await loadConsoleData();
      setData(nextData);
    } catch (error) {
      if (error instanceof APIError && error.status === 401) {
        setAuthState("anonymous");
        setRealtimeMode("off");
        setSessionUsername("");
        return;
      }
      const message = getErrorMessage(error, "刷新数据失败。");
      if (!silent && authState === "authenticated") {
        pushToast("danger", "刷新失败", message);
      } else if (authState !== "authenticated") {
        setPageError(message);
      }
    } finally {
      if (!silent) {
        setLoading(false);
      }
    }
  }

  async function handleLogin(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setActionPending("login");
    setPageError("");
    try {
      const session = await requestJSON<LoginResponse>("/login", {
        method: "POST",
        body: JSON.stringify({
          username: loginUsername.trim(),
          password: loginPassword,
        }),
      });
      setAuthState("authenticated");
      setRealtimeMode("connecting");
      setSessionUsername(session.username ?? loginUsername.trim());
      setLoginPassword("");
      await refreshConsoleData();
    } catch (error) {
      if (isStaticPreviewFallbackError(error, "/login")) {
        setPageError("当前是静态预览页面。登录功能需要通过 Agent 内置 HTTP 服务访问。");
        return;
      }
      setPageError(error instanceof Error ? error.message : "登录失败。");
    } finally {
      setActionPending("");
    }
  }

  async function handleLogout() {
    setActionPending("logout");
    try {
      await requestJSON<{ authenticated: boolean }>("/logout", {
        method: "POST",
      });
      setAuthState("anonymous");
      setRealtimeMode("off");
      setSessionUsername("");
      setData(emptyConsoleData);
      setSettingsDraft(null);
      settingsDraftWasEditedRef.current = false;
      pushToast("success", "已退出管理会话", "浏览器登录态已清除，可以使用其他账号重新登录。");
    } catch (error) {
      pushToast("danger", "退出登录失败", getErrorMessage(error, "退出登录失败。"));
    } finally {
      setActionPending("");
    }
  }

  async function handleSessionAction(path: "/session/reconnect" | "/session/drain") {
    setActionPending(path);
    try {
      await requestJSON(path, { method: "POST" });
      await refreshConsoleData();
      pushToast(
        "success",
        path === "/session/reconnect" ? "已触发重新连接" : "已触发排空",
        path === "/session/reconnect" ? "Agent 将按当前配置重新尝试建立桥接会话。" : "Agent 会优先回收空闲隧道并等待池状态稳定。",
      );
    } catch (error) {
      pushToast("danger", "会话操作失败", getErrorMessage(error, "会话操作失败。"));
    } finally {
      setActionPending("");
    }
  }

  function openCreateServiceDialog() {
    setServiceDialog({
      mode: "create",
      service: null,
    });
    setServiceForm(defaultServiceForm);
    setServiceFieldErrors({});
  }

  function openDetailServiceDialog(service: ServiceItem) {
    setServiceDialog({
      mode: "detail",
      service,
    });
    setServiceFieldErrors({});
  }

  function openEditServiceDialog(service: ServiceItem) {
    setServiceDialog({
      mode: "edit",
      service,
    });
    setServiceForm(toServiceForm(service));
    setServiceFieldErrors({});
  }

  function closeServiceDialog() {
    setServiceDialog(null);
    setServiceFieldErrors({});
  }

  async function handleSaveService(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const dialogMode = serviceDialog?.mode;
    if (!dialogMode || dialogMode === "detail") {
      return;
    }
    setActionPending("save-service");
    try {
      const validationErrors = validateServiceForm(serviceForm);
      const firstFieldError = Object.values(validationErrors)[0];
      if (firstFieldError) {
        setServiceFieldErrors(validationErrors);
        pushToast("warning", "服务表单未提交", firstFieldError);
        return;
      }
      setServiceFieldErrors({});
      await requestJSON("/services", {
        method: "POST",
        body: JSON.stringify(buildServicePayload(serviceForm)),
      });
      closeServiceDialog();
      setServiceForm(defaultServiceForm);
      setServiceFieldErrors({});
      await refreshConsoleData();
      window.location.hash = "services";
      pushToast(
        "success",
        dialogMode === "create" ? "服务已注册" : "服务已更新",
        dialogMode === "create" ? "新的服务实例已经写入实时目录，服务表会自动刷新。" : "服务变更已经写入实时目录，当前实例会按最新配置继续提供服务。",
      );
    } catch (error) {
      pushToast("danger", serviceDialog?.mode === "edit" ? "服务更新失败" : "服务注册失败", getErrorMessage(error, "服务保存失败。"));
    } finally {
      setActionPending("");
    }
  }

  async function handleDeleteService(service: ServiceItem) {
    setServicePendingDelete(service);
  }

  async function confirmDeleteService() {
    if (!servicePendingDelete) {
      return;
    }
    const service = servicePendingDelete;
    const serviceLabel = service.service_name || service.instance_id || service.logical_service_id;
    setActionPending(`delete:${service.instance_id}`);
    try {
      const query = service.instance_id
        ? `instance_id=${encodeURIComponent(service.instance_id)}`
        : `logical_service_id=${encodeURIComponent(service.logical_service_id)}`;
      await requestJSON(`/services?${query}`, { method: "DELETE" });
      setServicePendingDelete(null);
      if (serviceDialog?.service?.instance_id === service.instance_id) {
        closeServiceDialog();
      }
      await refreshConsoleData();
      pushToast("success", "服务已删除", `服务「${serviceLabel}」已从当前目录中移除。`);
    } catch (error) {
      pushToast("danger", "服务删除失败", getErrorMessage(error, "服务删除失败。"));
    } finally {
      setActionPending("");
    }
  }

  function updateServiceForm<K extends keyof ServiceFormState>(key: K, value: ServiceFormState[K]) {
    setServiceForm((current) => ({
      ...current,
      [key]: value,
    }));
    setServiceFieldErrors((current) => {
      if (!current[key]) {
        return current;
      }
      return {
        ...current,
        [key]: undefined,
      };
    });
  }

  function updateSettingsDraft<K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) {
    settingsDraftWasEditedRef.current = true;
    setSettingsDraft((current) => (current ? { ...current, [key]: value } : current));
    setSettingsFieldErrors((current) => {
      if (!current[key]) {
        return current;
      }
      return {
        ...current,
        [key]: undefined,
      };
    });
  }

  function resetSettingsDraft() {
    settingsDraftWasEditedRef.current = false;
    setSettingsDraft(toSettingsDraft(data.config));
    setSettingsFieldErrors({});
    pushToast("warning", "已重置未保存修改", "表单已恢复为最近一次从 Agent 读取到的配置内容。");
  }

  async function handleSaveSettings() {
    if (!settingsDraft) {
      return;
    }
    setSavingSettings(true);
    try {
      const validationErrors = validateSettingsDraft(settingsDraft);
      const firstFieldError = Object.values(validationErrors)[0];
      if (firstFieldError) {
        setSettingsFieldErrors(validationErrors);
        pushToast("warning", "配置未保存", firstFieldError);
        return;
      }
      setSettingsFieldErrors({});
      const nextConfigDocument = buildConfigFromSettingsDraft(data.config.config, settingsDraft);
      const nextConfig = await requestJSON<ConfigSnapshot>("/app/config", {
        method: "PUT",
        body: JSON.stringify({
          config: nextConfigDocument,
        }),
      });
      settingsDraftWasEditedRef.current = false;
      setData((current) => ({
        ...current,
        config: nextConfig,
      }));
      setSettingsDraft(toSettingsDraft(nextConfig));
      pushToast(
        "success",
        "运行配置已保存",
        `已写入 ${nextConfig.config_file_path || nextConfig.base_config_file_path || "配置文件"}，重启 Agent 后生效。`,
      );
    } catch (error) {
      pushToast("danger", "保存运行配置失败", getErrorMessage(error, "保存运行配置失败。"));
    } finally {
      setSavingSettings(false);
    }
  }

  const keyword = normalizeKeyword(deferredFilterKeyword);
  const filteredServices = data.services.services.filter((service) => {
    if (!keyword) {
      return true;
    }
    return [
      service.service_name,
      service.instance_id,
      service.logical_service_id,
      service.scope.namespace,
      service.scope.environment,
      service.protocol,
      service.endpoints.map((endpoint) => `${endpoint.host}:${endpoint.port}`).join(" "),
    ]
      .join(" ")
      .toLowerCase()
      .includes(keyword);
  });

  const filteredTunnels = data.tunnels.tunnels.filter((tunnel) => {
    if (!keyword) {
      return true;
    }
    return [
      tunnel.tunnel_id,
      tunnel.instance_id,
      tunnel.logical_service_id,
      tunnel.protocol,
      tunnel.state,
      tunnel.remote_addr,
    ]
      .join(" ")
      .toLowerCase()
      .includes(keyword);
  });

  const filteredLogs = data.logs.items.filter((item) => {
    if (!keyword) {
      return true;
    }
    return [item.level, item.module, item.code, item.message, item.bridge_state, item.request_id]
      .join(" ")
      .toLowerCase()
      .includes(keyword);
  });

  const topMetrics: ConsoleMetric[] = [
    {
      label: "会话状态",
      value: data.session.state || data.agent.state,
      help: data.session.session_id ? `会话 ${data.session.session_id}` : "等待会话建立",
      tone: statusBadgeVariant(data.session.state),
    },
    {
      label: "服务总数",
      value: formatCount(data.services.services.length),
      help: `${data.services.services.filter((item) => normalizeKeyword(item.health_status) === "healthy").length} 个健康`,
      tone: "default" as const,
    },
    {
      label: "活跃隧道",
      value: formatCount(data.agent.tunnel_pool.active),
      help: `${formatCount(data.agent.tunnel_pool.idle)} 条空闲`,
      tone: "default" as const,
    },
    {
      label: "错误事件",
      value: formatCount(data.diagnose.event_error_count),
      help: data.diagnose.last_event_code ?? "最近无错误事件",
      tone: data.diagnose.event_error_count > 0 ? ("danger" as const) : ("success" as const),
    },
  ];

  const currentNav = navigationItems.find((item) => item.key === page) ?? navigationItems[0];
  const pendingDeleteKey = servicePendingDelete ? `delete:${servicePendingDelete.instance_id}` : "";
  const pendingDeleteLabel =
    servicePendingDelete?.service_name || servicePendingDelete?.instance_id || servicePendingDelete?.logical_service_id || "";
  const toastLayer = (
    <>
      {toasts.map((item) => (
        <Toast key={item.id} open={item.open} variant={item.variant} onOpenChange={(open) => dismissToast(item.id, open)}>
          <ToastTitle>{item.title}</ToastTitle>
          {item.description ? <ToastDescription>{item.description}</ToastDescription> : null}
        </Toast>
      ))}
      <ToastViewport />
    </>
  );

  if (authState !== "authenticated") {
    return (
      <ToastProvider swipeDirection="right">
        <LoginScreen
          actionPending={actionPending}
          authState={authState}
          errorMessage={pageError}
          loginPassword={loginPassword}
          loginUsername={loginUsername}
          onPasswordChange={setLoginPassword}
          onSubmit={handleLogin}
          onUsernameChange={setLoginUsername}
          toastLayer={toastLayer}
        />
      </ToastProvider>
    );
  }

  return (
    <ToastProvider swipeDirection="right">
      <ConsoleShell
        actionPending={actionPending}
        currentNav={currentNav}
        data={data}
        filteredLogs={filteredLogs}
        filteredServices={filteredServices}
        filteredTunnels={filteredTunnels}
        filterKeyword={filterKeyword}
        loading={loading}
        onDeleteService={handleDeleteService}
        onDrainSession={() => void handleSessionAction("/session/drain")}
        onFilterKeywordChange={setFilterKeyword}
        onLogout={handleLogout}
        onOpenCreateService={openCreateServiceDialog}
        onOpenDetailService={openDetailServiceDialog}
        onOpenEditService={openEditServiceDialog}
        onReconnectSession={() => void handleSessionAction("/session/reconnect")}
        onRefresh={() => void refreshConsoleData()}
        onResetSettings={resetSettingsDraft}
        onSaveSettings={handleSaveSettings}
        onSelectPage={(nextPage) => {
          window.location.hash = nextPage;
        }}
        onUpdateSettingsDraft={updateSettingsDraft}
        page={page}
        sessionUsername={sessionUsername}
        settingsDraft={settingsDraft}
        settingsDirty={settingsDirty}
        settingsFieldErrors={settingsFieldErrors}
        savingSettings={savingSettings}
        topMetrics={topMetrics}
      />
      <ServiceDialog
        actionPending={actionPending}
        fieldErrors={serviceFieldErrors}
        form={serviceForm}
        mode={serviceDialog?.mode ?? "create"}
        open={Boolean(serviceDialog)}
        service={serviceDialog?.service ?? null}
        onClose={closeServiceDialog}
        onSubmit={handleSaveService}
        onUpdateForm={updateServiceForm}
      />
      <AlertDialog
        open={Boolean(servicePendingDelete)}
        onOpenChange={(open) => {
          if (!open && actionPending !== pendingDeleteKey) {
            setServicePendingDelete(null);
          }
        }}
      >
        <AlertDialogContent>
          <AlertDialogHeader>
            <AlertDialogTitle>确认删除这个服务实例？</AlertDialogTitle>
            <AlertDialogDescription>
              删除后，当前目录中的该实例会立即消失，后续流量也不会再路由到它。目标服务：
              <span className="mt-2 block break-all rounded-2xl bg-[rgba(15,23,42,0.05)] px-3 py-2 font-medium text-[hsl(var(--foreground))]">
                {pendingDeleteLabel || "未命名服务"}
              </span>
            </AlertDialogDescription>
          </AlertDialogHeader>
          <AlertDialogFooter>
            <AlertDialogCancel disabled={actionPending === pendingDeleteKey}>取消</AlertDialogCancel>
            <AlertDialogAction disabled={actionPending === pendingDeleteKey} onClick={() => void confirmDeleteService()}>
              {actionPending === pendingDeleteKey ? "正在删除..." : "确认删除"}
            </AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
      {toastLayer}
    </ToastProvider>
  );
}

export default App;
