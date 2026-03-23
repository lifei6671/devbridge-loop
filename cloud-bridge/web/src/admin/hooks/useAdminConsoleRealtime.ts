import { useEffect } from "react";

import type { AdminPageKey, ApiRecord, RefreshPageOptions } from "../model";
import {
  asRecord,
  encodeQuery,
  minSSEReconnectIntervalMS,
  parseSSEEnvelope,
  pickSSETopicByPage,
  sseHeartbeatEventName,
  sseReadyEventName,
  sseSnapshotEventName,
} from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";

type UseAdminConsoleRealtimeParams = {
  applySSESnapshot: (topic: string, payload: ApiRecord) => void;
  refreshPageData: (page: AdminPageKey, options?: RefreshPageOptions) => Promise<void>;
  state: AdminConsoleState;
};

export function useAdminConsoleRealtime(params: UseAdminConsoleRealtimeParams) {
  const { applySSESnapshot, refreshPageData, state } = params;
  const {
    activePage,
    authStatus,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    isAutoRefreshInFlightRef,
    realtimeMode,
    sessionStateFilter,
    setIsAutoRefreshing,
    setLastSyncMS,
    setRealtimeMode,
    setSSEConnectionState,
    setSSEReconnectTrigger,
    sseEventSourceRef,
    sseReconnectTrigger,
    timeRangeMinutes,
    tunnelConnectorFilter,
    tunnelStateFilter,
  } = state;

  useEffect(() => {
    if (authStatus !== "authenticated") {
      return;
    }
    void refreshPageData(activePage);
  }, [activePage, authStatus, refreshPageData]);

  useEffect(() => {
    if (!autoRefreshEnabled || authStatus !== "authenticated" || realtimeMode !== "polling") {
      return;
    }
    const reconnectIntervalMS = Math.max(autoRefreshIntervalMS * 3, minSSEReconnectIntervalMS);
    const timerID = window.setInterval(() => {
      setSSEReconnectTrigger((previousValue) => previousValue + 1);
    }, reconnectIntervalMS);
    return () => {
      window.clearInterval(timerID);
    };
  }, [authStatus, autoRefreshEnabled, autoRefreshIntervalMS, realtimeMode, setSSEReconnectTrigger]);

  useEffect(() => {
    if (sseEventSourceRef.current !== null) {
      sseEventSourceRef.current.close();
      sseEventSourceRef.current = null;
    }
    if (!autoRefreshEnabled) {
      setRealtimeMode("off");
      setSSEConnectionState("idle");
      setIsAutoRefreshing(false);
      return;
    }
    if (authStatus !== "authenticated") {
      setRealtimeMode("off");
      setSSEConnectionState("idle");
      setIsAutoRefreshing(false);
      return;
    }
    if (typeof window === "undefined" || typeof window.EventSource === "undefined") {
      setRealtimeMode("polling");
      setSSEConnectionState("error");
      return;
    }

    const activeTopic = pickSSETopicByPage(activePage);
    const streamQuery: Record<string, string | number | undefined> = {
      topics: activeTopic,
      interval_ms: autoRefreshIntervalMS,
    };
    if (activeTopic === "connectors" && sessionStateFilter !== "ALL") {
      streamQuery.session_state = sessionStateFilter;
    }
    if (activeTopic === "traffic" && tunnelStateFilter !== "ALL") {
      streamQuery.tunnel_state = tunnelStateFilter.toLowerCase();
    }
    if (activeTopic === "traffic" && tunnelConnectorFilter !== "ALL") {
      streamQuery.connector_id = tunnelConnectorFilter;
    }
    if (activeTopic === "observability") {
      streamQuery.time_range_minutes = timeRangeMinutes;
    }
    const streamURL = `/api/admin/events/stream${encodeQuery(streamQuery)}`;
    const eventSource = new window.EventSource(streamURL);
    sseEventSourceRef.current = eventSource;
    setRealtimeMode("sse");
    setSSEConnectionState("connecting");
    let hasReceivedReadyOrSnapshot = false;

    const handleReady = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null) {
        return;
      }
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      hasReceivedReadyOrSnapshot = true;
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    const handleSnapshot = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null || typeof envelope.topic !== "string" || envelope.topic.trim() === "") {
        return;
      }
      applySSESnapshot(envelope.topic, asRecord(envelope.payload));
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      hasReceivedReadyOrSnapshot = true;
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    const handleHeartbeat = (event: MessageEvent) => {
      const envelope = parseSSEEnvelope(String(event.data ?? ""));
      if (envelope === null) {
        return;
      }
      const serverTimeMS =
        typeof envelope.server_time_ms === "number" && Number.isFinite(envelope.server_time_ms)
          ? envelope.server_time_ms
          : Date.now();
      setLastSyncMS(serverTimeMS);
      setSSEConnectionState("live");
    };

    eventSource.addEventListener(sseReadyEventName, handleReady as EventListener);
    eventSource.addEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
    eventSource.addEventListener(sseHeartbeatEventName, handleHeartbeat as EventListener);
    eventSource.onerror = () => {
      // 首次握手失败时切到轮询，避免页面持续“连接中”无数据。
      if (!hasReceivedReadyOrSnapshot) {
        eventSource.close();
        if (sseEventSourceRef.current === eventSource) {
          sseEventSourceRef.current = null;
        }
        setRealtimeMode("polling");
        setSSEConnectionState("error");
        return;
      }
      setSSEConnectionState("connecting");
    };
    return () => {
      eventSource.removeEventListener(sseReadyEventName, handleReady as EventListener);
      eventSource.removeEventListener(sseSnapshotEventName, handleSnapshot as EventListener);
      eventSource.removeEventListener(sseHeartbeatEventName, handleHeartbeat as EventListener);
      eventSource.close();
      if (sseEventSourceRef.current === eventSource) {
        sseEventSourceRef.current = null;
      }
    };
  }, [
    activePage,
    applySSESnapshot,
    authStatus,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    sessionStateFilter,
    setIsAutoRefreshing,
    setLastSyncMS,
    setRealtimeMode,
    setSSEConnectionState,
    sseEventSourceRef,
    sseReconnectTrigger,
    timeRangeMinutes,
    tunnelConnectorFilter,
    tunnelStateFilter,
  ]);

  useEffect(() => {
    if (!autoRefreshEnabled || authStatus !== "authenticated" || realtimeMode !== "polling") {
      setIsAutoRefreshing(false);
      return;
    }
    const timerID = window.setInterval(() => {
      // 若当前仍在请求中则跳过本轮，下一轮继续尝试。
      if (isAutoRefreshInFlightRef.current) {
        return;
      }
      isAutoRefreshInFlightRef.current = true;
      setIsAutoRefreshing(true);
      void refreshPageData(activePage, { silentError: true }).finally(() => {
        isAutoRefreshInFlightRef.current = false;
        setIsAutoRefreshing(false);
      });
    }, autoRefreshIntervalMS);
    return () => {
      window.clearInterval(timerID);
      isAutoRefreshInFlightRef.current = false;
      setIsAutoRefreshing(false);
    };
  }, [
    activePage,
    authStatus,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    isAutoRefreshInFlightRef,
    realtimeMode,
    refreshPageData,
    setIsAutoRefreshing,
  ]);
}
