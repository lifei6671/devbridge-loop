import { useCallback } from "react";
import { toast } from "sonner";

import type { AdminPageKey, AdminSessionRecord, ApiRecord, RefreshPageOptions } from "../model";
import {
  asRecord,
  asRecordArray,
  encodeQuery,
  normalizeOperationError,
  readText,
} from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";

export type AdminRequestFn = (path: string, init?: RequestInit) => Promise<ApiRecord>;
export type RefreshPageDataFn = (
  page: AdminPageKey,
  options?: RefreshPageOptions
) => Promise<void>;

const defaultCSRFHeaderName = "X-CSRF-Token";

function resolveCSRFHeaderName(session: AdminSessionRecord | null): string {
  if (session === null) {
    return defaultCSRFHeaderName;
  }
  const normalizedHeaderName = session.csrf_header_name.trim();
  if (normalizedHeaderName !== "") {
    return normalizedHeaderName;
  }
  return defaultCSRFHeaderName;
}

function isCSRFMismatchResponse(response: Response, payload: ApiRecord): boolean {
  if (response.status !== 403) {
    return false;
  }
  const errorRecord = asRecord(payload.error);
  const errorText = readText(errorRecord, "message").toLowerCase();
  return errorText.includes("csrf");
}

export function useAdminDataActions(state: AdminConsoleState) {
  const {
    authStatus,
    sessionStateFilter,
    setAuthError,
    setAuthProviders,
    setAuthStatus,
    setAgentPoolSummary,
    setConfigSnapshot,
    setConnectorItems,
    setDiagnoseSummary,
    setIsAuthenticating,
    setIsLoading,
    setLastSyncMS,
    setLoginPassword,
    setLogItems,
    setMetricPoints,
    setOverview,
    setRouteItems,
    setSession,
    setServiceItems,
    setSessionItems,
    setTrafficSummary,
    setTunnelItems,
    setTunnelSummary,
    session,
    timeRangeMinutes,
    tunnelConnectorFilter,
    tunnelStateFilter,
  } = state;

  const applySessionResponse = useCallback(
    (payload: ApiRecord) => {
      const authenticated = payload.authenticated === true;
      setAuthProviders(asRecordArray(payload.providers).map((providerRecord) => ({
        name: readText(providerRecord, "name"),
        type: readText(providerRecord, "type"),
        label: readText(providerRecord, "label"),
        login_flow: readText(providerRecord, "login_flow"),
      })));
      if (!authenticated) {
        setSession(null);
        setAuthStatus("anonymous");
        return null;
      }
      const sessionRecord = asRecord(payload.session);
      const resolvedSession: AdminSessionRecord = {
        username: readText(sessionRecord, "username"),
        display_name: readText(sessionRecord, "display_name"),
        role: readText(sessionRecord, "role"),
        provider: readText(sessionRecord, "provider"),
        csrf_token: readText(sessionRecord, "csrf_token"),
        csrf_header_name: readText(payload, "csrf_header_name", defaultCSRFHeaderName),
        expires_at_ms: Number(sessionRecord.expires_at_ms ?? 0),
      };
      setSession(resolvedSession);
      setAuthStatus("authenticated");
      setAuthError("");
      return resolvedSession;
    },
    [setAuthError, setAuthProviders, setAuthStatus, setSession]
  );

  const refreshAuthSession = useCallback(async () => {
    try {
      const response = await fetch("/api/admin/auth/session", {
        credentials: "same-origin",
        headers: {
          Accept: "application/json",
        },
      });
      const payload = asRecord(await response.json());
      return applySessionResponse(payload);
    } catch (error) {
      setAuthStatus("anonymous");
      setSession(null);
      setAuthError(normalizeOperationError(error));
      return null;
    }
  }, [applySessionResponse, setAuthError, setAuthStatus, setSession]);

  const requestAdmin = useCallback<AdminRequestFn>(
    async (path, init) => {
      if (authStatus !== "authenticated" || session === null) {
        throw new Error("请先登录管理后台");
      }
      const performRequest = async (
        activeSession: AdminSessionRecord,
        allowSessionRefresh: boolean
      ): Promise<ApiRecord> => {
        const headers = new Headers(init?.headers);
        headers.set("Accept", "application/json");
        if (init?.method !== undefined) {
          const normalizedMethod = init.method.toUpperCase();
          if (
            (normalizedMethod === "POST" ||
              normalizedMethod === "PUT" ||
              normalizedMethod === "PATCH" ||
              normalizedMethod === "DELETE") &&
            activeSession.csrf_token.trim() !== ""
          ) {
            headers.set(resolveCSRFHeaderName(activeSession), activeSession.csrf_token);
          }
        }
        // 非 FormData 请求统一按 JSON 发送，减少接口层分支判断。
        if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
          headers.set("Content-Type", "application/json");
        }
        const response = await fetch(path, {
          ...init,
          credentials: "same-origin",
          headers,
        });
        const rawText = await response.text();
        let parsedPayload: unknown = {};
        if (rawText.trim() !== "") {
          try {
            parsedPayload = JSON.parse(rawText);
          } catch {
            parsedPayload = { raw_text: rawText };
          }
        }
        const responseRecord = asRecord(parsedPayload);
        if (response.status === 401) {
          setAuthStatus("anonymous");
          setSession(null);
          setAuthError("登录状态已失效，请重新登录。");
        }
        if (!response.ok && allowSessionRefresh && isCSRFMismatchResponse(response, responseRecord)) {
          const refreshedSession = await refreshAuthSession();
          if (refreshedSession !== null) {
            return performRequest(refreshedSession, false);
          }
        }
        if (!response.ok) {
          const errorRecord = asRecord(responseRecord.error);
          const errorCode = readText(errorRecord, "code", "REQUEST_FAILED");
          const errorText = readText(errorRecord, "message", `HTTP ${response.status}`);
          throw new Error(`${errorCode}: ${errorText}`);
        }
        return responseRecord;
      };
      return performRequest(session, true);
    },
    [authStatus, refreshAuthSession, session, setAuthError, setAuthStatus, setSession]
  );

  const login = useCallback(
    async (username: string, password: string) => {
      setIsAuthenticating(true);
      setAuthError("");
      try {
        const response = await fetch("/api/admin/auth/login", {
          method: "POST",
          credentials: "same-origin",
          headers: {
            Accept: "application/json",
            "Content-Type": "application/json",
          },
          body: JSON.stringify({
            username,
            password,
          }),
        });
        const payload = asRecord(await response.json());
        if (!response.ok) {
          const errorRecord = asRecord(payload.error);
          throw new Error(readText(errorRecord, "message", "登录失败"));
        }
        applySessionResponse(payload);
        setLoginPassword("");
      } catch (error) {
        const errorText = normalizeOperationError(error);
        setAuthError(errorText);
        setAuthStatus("anonymous");
        setSession(null);
        toast.error(errorText);
      } finally {
        setIsAuthenticating(false);
      }
    },
    [
      applySessionResponse,
      setAuthError,
      setAuthStatus,
      setIsAuthenticating,
      setLoginPassword,
      setSession,
    ]
  );

  const logout = useCallback(async () => {
    try {
      if (authStatus === "authenticated" && session !== null) {
        await requestAdmin("/api/admin/auth/logout", {
          method: "POST",
        });
      }
    } finally {
      setAuthStatus("anonymous");
      setSession(null);
      setAuthError("");
    }
  }, [authStatus, requestAdmin, session, setAuthError, setAuthStatus, setSession]);

  const refreshPageData = useCallback<RefreshPageDataFn>(
    async (page, options) => {
      if (authStatus !== "authenticated") {
        return;
      }
      setIsLoading(true);
      try {
        if (page === "dashboard") {
          const nowMS = Date.now();
          const fromMS = nowMS - 30 * 60 * 1000;
          const [
            overviewResponse,
            tunnelSummaryResponse,
            trafficSummaryResponse,
            diagnoseResponse,
            metricsResponse,
          ] = await Promise.all([
            requestAdmin("/api/admin/bridge/overview"),
            requestAdmin("/api/admin/tunnels/summary"),
            requestAdmin("/api/admin/traffic/summary"),
            requestAdmin("/api/admin/diagnose/summary"),
            requestAdmin(
              `/api/admin/metrics/query${encodeQuery({
                from: fromMS,
                to: nowMS,
              })}`
            ),
          ]);
          setOverview(asRecord(overviewResponse.overview));
          setTunnelSummary(asRecord(tunnelSummaryResponse.summary));
          setTrafficSummary(asRecord(trafficSummaryResponse.summary));
          setDiagnoseSummary(asRecord(diagnoseResponse.summary));
          setMetricPoints(asRecordArray(metricsResponse.points));
        }

        if (page === "routes") {
          const response = await requestAdmin(`/api/admin/routes${encodeQuery({ limit: 100 })}`);
          setRouteItems(asRecordArray(response.items));
        }

        if (page === "services") {
          const response = await requestAdmin(`/api/admin/services${encodeQuery({ limit: 120 })}`);
          setServiceItems(asRecordArray(response.items));
        }

        if (page === "connectors") {
          const [connectorResponse, sessionResponse] = await Promise.all([
            requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 100 })}`),
            requestAdmin(
              `/api/admin/sessions${encodeQuery({
                limit: 100,
                state: sessionStateFilter === "ALL" ? undefined : sessionStateFilter,
              })}`
            ),
          ]);
          setConnectorItems(asRecordArray(connectorResponse.items));
          setSessionItems(asRecordArray(sessionResponse.items));
        }

        if (page === "traffic") {
          const connectorFilter = tunnelConnectorFilter === "ALL" ? undefined : tunnelConnectorFilter;
          const [tunnelSummaryResponse, tunnelResponse, trafficResponse, connectorResponse] =
            await Promise.all([
              // 汇总与列表都带 connector 过滤，保证“池子数”和“明细”口径一致。
              requestAdmin(
                `/api/admin/tunnels/summary${encodeQuery({
                  connector_id: connectorFilter,
                })}`
              ),
              requestAdmin(
                `/api/admin/tunnels${encodeQuery({
                  limit: 120,
                  state: tunnelStateFilter === "ALL" ? undefined : tunnelStateFilter.toLowerCase(),
                  connector_id: connectorFilter,
                })}`
              ),
              requestAdmin("/api/admin/traffic/summary"),
              requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 120 })}`),
            ]);
          setTunnelSummary(asRecord(tunnelSummaryResponse.summary));
          setAgentPoolSummary(asRecord(tunnelSummaryResponse.agent_pool_summary));
          setTunnelItems(asRecordArray(tunnelResponse.items));
          setTrafficSummary(asRecord(trafficResponse.summary));
          setConnectorItems(asRecordArray(connectorResponse.items));
        }

        if (page === "ops") {
          const [configResponse, connectorResponse, sessionResponse] = await Promise.all([
            requestAdmin("/api/admin/config/snapshot"),
            requestAdmin(`/api/admin/connectors${encodeQuery({ limit: 100 })}`),
            requestAdmin(`/api/admin/sessions${encodeQuery({ limit: 100 })}`),
          ]);
          setConfigSnapshot(asRecord(configResponse.snapshot));
          setConnectorItems(asRecordArray(connectorResponse.items));
          setSessionItems(asRecordArray(sessionResponse.items));
        }

        if (page === "observability") {
          const nowMS = Date.now();
          const fromMS = nowMS - timeRangeMinutes * 60 * 1000;
          const [logsResponse, metricsResponse, diagnoseResponse] = await Promise.all([
            requestAdmin(
              `/api/admin/logs/search${encodeQuery({
                from: fromMS,
                to: nowMS,
                limit: 80,
              })}`
            ),
            requestAdmin(
              `/api/admin/metrics/query${encodeQuery({
                from: fromMS,
                to: nowMS,
              })}`
            ),
            requestAdmin("/api/admin/diagnose/summary"),
          ]);
          setLogItems(asRecordArray(logsResponse.items));
          setMetricPoints(asRecordArray(metricsResponse.points));
          setDiagnoseSummary(asRecord(diagnoseResponse.summary));
        }
        setLastSyncMS(Date.now());
      } catch (error) {
        if (!options?.silentError) {
          toast.error(normalizeOperationError(error));
        }
      } finally {
        setIsLoading(false);
      }
    },
    [
      authStatus,
      requestAdmin,
      sessionStateFilter,
      setAgentPoolSummary,
      setConfigSnapshot,
      setConnectorItems,
      setDiagnoseSummary,
      setIsLoading,
      setLastSyncMS,
      setLogItems,
      setMetricPoints,
      setOverview,
      setRouteItems,
      setServiceItems,
      setSessionItems,
      setTrafficSummary,
      setTunnelItems,
      setTunnelSummary,
      timeRangeMinutes,
      tunnelConnectorFilter,
      tunnelStateFilter,
    ]
  );

  const applySSESnapshot = useCallback(
    (topic: string, payload: ApiRecord) => {
      if (topic === "dashboard") {
        setOverview(asRecord(payload.overview));
        setTunnelSummary(asRecord(payload.tunnel_summary));
        setTrafficSummary(asRecord(payload.traffic_summary));
        setDiagnoseSummary(asRecord(payload.diagnose_summary));
        return;
      }
      if (topic === "routes") {
        setRouteItems(asRecordArray(payload.items));
        return;
      }
      if (topic === "services") {
        setServiceItems(asRecordArray(payload.items));
        return;
      }
      if (topic === "connectors") {
        setConnectorItems(asRecordArray(payload.connectors));
        setSessionItems(asRecordArray(payload.sessions));
        return;
      }
      if (topic === "traffic") {
        setTunnelSummary(asRecord(payload.tunnel_summary));
        setAgentPoolSummary(asRecord(payload.agent_pool_summary));
        setTunnelItems(asRecordArray(payload.tunnels));
        setConnectorItems(asRecordArray(payload.connectors));
        setTrafficSummary(asRecord(payload.traffic_summary));
        return;
      }
      if (topic === "ops") {
        setConfigSnapshot(asRecord(payload.snapshot));
        setConnectorItems(asRecordArray(payload.connectors));
        setSessionItems(asRecordArray(payload.sessions));
        return;
      }
      if (topic === "observability") {
        setLogItems(asRecordArray(payload.logs));
        setMetricPoints(asRecordArray(payload.metrics));
        setDiagnoseSummary(asRecord(payload.diagnose_summary));
      }
    },
    [
      setAgentPoolSummary,
      setConfigSnapshot,
      setConnectorItems,
      setDiagnoseSummary,
      setLogItems,
      setMetricPoints,
      setOverview,
      setRouteItems,
      setServiceItems,
      setSessionItems,
      setTrafficSummary,
      setTunnelItems,
      setTunnelSummary,
    ]
  );

  return {
    applySSESnapshot,
    login,
    refreshPageData,
    refreshAuthSession,
    requestAdmin,
    logout,
  };
}

export type AdminDataActions = ReturnType<typeof useAdminDataActions>;
