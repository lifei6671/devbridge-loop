import { useCallback } from "react";
import { toast } from "sonner";

import type { AdminPageKey, ApiRecord, RefreshPageOptions } from "../model";
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

export function useAdminDataActions(state: AdminConsoleState) {
  const {
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
    token,
    tunnelConnectorFilter,
    tunnelStateFilter,
  } = state;

  const requestAdmin = useCallback<AdminRequestFn>(
    async (path, init) => {
      const normalizedToken = token.trim();
      if (normalizedToken === "") {
        throw new Error("请先填写 Bearer Token");
      }
      const headers = new Headers(init?.headers);
      headers.set("Accept", "application/json");
      headers.set("Authorization", `Bearer ${normalizedToken}`);
      // 非 FormData 请求统一按 JSON 发送，减少接口层分支判断。
      if (!(init?.body instanceof FormData) && !headers.has("Content-Type")) {
        headers.set("Content-Type", "application/json");
      }
      const response = await fetch(path, {
        ...init,
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
      if (!response.ok) {
        const errorRecord = asRecord(responseRecord.error);
        const errorCode = readText(errorRecord, "code", "REQUEST_FAILED");
        const errorText = readText(errorRecord, "message", `HTTP ${response.status}`);
        throw new Error(`${errorCode}: ${errorText}`);
      }
      return responseRecord;
    },
    [token]
  );

  const refreshPageData = useCallback<RefreshPageDataFn>(
    async (page, options) => {
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
    refreshPageData,
    requestAdmin,
  };
}

export type AdminDataActions = ReturnType<typeof useAdminDataActions>;
