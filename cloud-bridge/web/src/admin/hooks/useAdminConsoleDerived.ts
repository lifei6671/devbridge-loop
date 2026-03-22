import { useEffect, useMemo } from "react";

import type {
  ChartDatum,
  DashboardMetricCard,
  MultiTrendSeries,
  TunnelRingSegment,
} from "../model";
import {
  asRecord,
  asRecordArray,
  buildMetricTrend,
  describeRealtimeState,
  pickDetailTitle,
  pickPageMeta,
  readNumber,
  readText,
  resolveTone,
  summarizeLogActions,
  summarizeLogResults,
  summarizeMetricBars,
  summarizeNamedCounters,
} from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";

export function useAdminConsoleDerived(state: AdminConsoleState) {
  const {
    activeMetricKey,
    activePage,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    connectorItems,
    detailSelection,
    diagnoseSummary,
    isAutoRefreshing,
    logItems,
    metricPoints,
    overview,
    realtimeMode,
    routeItems,
    sessionItems,
    setActiveMetricKey,
    sseConnectionState,
    trafficSummary,
    tunnelConnectorFilter,
    tunnelItems,
    tunnelSummary,
  } = state;

  const detailDomainItems = useMemo(
    () => ({
      route: routeItems,
      connector: connectorItems,
      session: sessionItems,
      tunnel: tunnelItems,
    }),
    [routeItems, connectorItems, sessionItems, tunnelItems]
  );

  const currentDetailItems = useMemo(() => {
    if (detailSelection === null) {
      return [];
    }
    return detailDomainItems[detailSelection.domain] ?? [];
  }, [detailDomainItems, detailSelection]);

  const currentDetailRecord = useMemo(() => {
    if (detailSelection === null) {
      return null;
    }
    if (detailSelection.index < 0 || detailSelection.index >= currentDetailItems.length) {
      return null;
    }
    return currentDetailItems[detailSelection.index];
  }, [currentDetailItems, detailSelection]);

  const currentDetailTitle = useMemo(() => {
    if (detailSelection === null || currentDetailRecord === null) {
      return "";
    }
    return pickDetailTitle(detailSelection.domain, currentDetailRecord);
  }, [currentDetailRecord, detailSelection]);

  const activeMeta = pickPageMeta(activePage);

  const realtimeSummary = useMemo(
    () =>
      describeRealtimeState(
        realtimeMode,
        sseConnectionState,
        isAutoRefreshing,
        autoRefreshIntervalMS,
        autoRefreshEnabled
      ),
    [autoRefreshEnabled, autoRefreshIntervalMS, isAutoRefreshing, realtimeMode, sseConnectionState]
  );


  const diagnoseIssues = useMemo(() => {
    if (!Array.isArray(diagnoseSummary.issues)) {
      return [] as string[];
    }
    return diagnoseSummary.issues.map((item) => String(item));
  }, [diagnoseSummary]);

  const dashboardMetricCards = useMemo<DashboardMetricCard[]>(
    () => [
      {
        label: "Bridge 状态",
        value: readText(diagnoseSummary, "health", "unknown"),
        hint: diagnoseIssues.length === 0 ? "当前无阻断级问题" : `${diagnoseIssues.length} 条诊断提醒`,
        tone: resolveTone(readText(diagnoseSummary, "health", "unknown")),
      },
      {
        label: "在线 Connector",
        value: readText(overview, "connector_total", "0"),
        hint: `Session Active ${readText(overview, "session_active", "0")}`,
      },
      {
        label: "服务发布",
        value: readText(overview, "service_total", "0"),
        hint: `Route ${readText(overview, "route_total", "0")}`,
      },
      {
        label: "Idle Tunnel",
        value: readText(tunnelSummary, "idle", readText(overview, "tunnel_idle", "0")),
        hint: `Reserved ${readText(tunnelSummary, "reserved", "0")}`,
      },
      {
        label: "Open Timeout",
        value: readText(trafficSummary, "open_timeout_total", "0"),
        hint: `Ack Late ${readText(trafficSummary, "open_ack_late_total", "0")}`,
        tone: readNumber(trafficSummary, "open_timeout_total", 0) > 0 ? "danger" : "ok",
      },
      {
        label: "Fallback",
        value: readText(trafficSummary, "scope_fallback_total", "0"),
        hint: `Acquire Wait ${readText(trafficSummary, "acquire_wait_count", "0")}`,
        tone: readNumber(trafficSummary, "scope_fallback_total", 0) > 0 ? "warn" : "ok",
      },
    ],
    [diagnoseIssues.length, diagnoseSummary, overview, trafficSummary, tunnelSummary]
  );

  const overviewListeners = useMemo(() => asRecordArray(overview.listeners), [overview]);

  const dashboardTrendSeries = useMemo<MultiTrendSeries[]>(
    () => [
      {
        label: "Acquire Wait",
        tone: "blue",
        items: buildMetricTrend(metricPoints, "acquire_wait_count"),
        latestValue: readNumber(metricPoints[metricPoints.length - 1] ?? {}, "acquire_wait_count", 0),
      },
      {
        label: "Open Timeout",
        tone: "green",
        items: buildMetricTrend(metricPoints, "open_timeout_total"),
        latestValue: readNumber(metricPoints[metricPoints.length - 1] ?? {}, "open_timeout_total", 0),
      },
      {
        label: "Fallback",
        tone: "orange",
        items: buildMetricTrend(metricPoints, "scope_fallback_total"),
        latestValue: readNumber(metricPoints[metricPoints.length - 1] ?? {}, "scope_fallback_total", 0),
      },
    ],
    [metricPoints]
  );

  const tunnelRingSegments = useMemo<TunnelRingSegment[]>(
    () => [
      { label: "idle", value: readNumber(tunnelSummary, "idle", 0), color: "#4266ff" },
      { label: "reserved", value: readNumber(tunnelSummary, "reserved", 0), color: "#16a672" },
      { label: "active", value: readNumber(tunnelSummary, "active", 0), color: "#f59e0b" },
      { label: "broken", value: readNumber(tunnelSummary, "broken", 0), color: "#f04461" },
      { label: "closed", value: readNumber(tunnelSummary, "closed", 0), color: "#a7b3d1" },
    ],
    [tunnelSummary]
  );

  const dashboardHealthBars = useMemo<ChartDatum[]>(
    () => [
      {
        label: "Auth Failure",
        value: readNumber(trafficSummary, "auth_failure_total", 0),
        tone: readNumber(trafficSummary, "auth_failure_total", 0) > 0 ? "danger" : "ok",
      },
      {
        label: "Ack Late",
        value: readNumber(trafficSummary, "open_ack_late_total", 0),
        tone: readNumber(trafficSummary, "open_ack_late_total", 0) > 0 ? "warn" : "ok",
      },
      {
        label: "Route Conflict",
        value: readNumber(trafficSummary, "route_conflict_rejection_total", 0),
        tone: readNumber(trafficSummary, "route_conflict_rejection_total", 0) > 0 ? "warn" : "ok",
      },
      {
        label: "Tunnel Recycle Fail",
        value: readNumber(trafficSummary, "tunnel_recycle_failure_total", 0),
        tone: readNumber(trafficSummary, "tunnel_recycle_failure_total", 0) > 0 ? "danger" : "ok",
      },
    ],
    [trafficSummary]
  );

  const dashboardTopCounters = useMemo(() => {
    const namedCounters = [
      ...summarizeNamedCounters(trafficSummary.auth_error_code_totals, 2),
      ...summarizeNamedCounters(trafficSummary.tunnel_recycle_error_code_totals, 2),
    ];
    if (namedCounters.length > 0) {
      return namedCounters.slice(0, 3);
    }
    return [
      {
        label: "open_timeout",
        value: readNumber(trafficSummary, "open_timeout_total", 0),
      },
      {
        label: "scope_fallback",
        value: readNumber(trafficSummary, "scope_fallback_total", 0),
      },
      {
        label: "route_conflict",
        value: readNumber(trafficSummary, "route_conflict_rejection_total", 0),
      },
    ];
  }, [trafficSummary]);

  const metricKeyOptions = useMemo(() => {
    const defaultKeys = [
      "open_timeout_total",
      "open_reject_total",
      "open_ack_late_total",
      "acquire_wait_count",
      "acquire_wait_total_ms",
      "scope_fallback_total",
      "route_conflict_rejection_total",
      "host_derive_success_total",
      "host_derive_failure_total",
      "endpoint_override_total",
    ];
    const keySet = new Set<string>(defaultKeys);
    const firstPoint = metricPoints[0];
    if (firstPoint !== undefined) {
      for (const [key, value] of Object.entries(firstPoint)) {
        if (typeof value === "number" && Number.isFinite(value) && key !== "ts_ms") {
          keySet.add(key);
        }
      }
    }
    return [...keySet];
  }, [metricPoints]);

  useEffect(() => {
    if (metricKeyOptions.includes(activeMetricKey)) {
      return;
    }
    if (metricKeyOptions.length === 0) {
      return;
    }
    setActiveMetricKey(metricKeyOptions[0]);
  }, [activeMetricKey, metricKeyOptions, setActiveMetricKey]);

  const metricSummaryBars = useMemo(() => {
    const latestPoint = metricPoints.length > 0 ? metricPoints[metricPoints.length - 1] : {};
    return summarizeMetricBars(asRecord(latestPoint));
  }, [metricPoints]);

  const metricTrend = useMemo(
    () => buildMetricTrend(metricPoints, activeMetricKey),
    [activeMetricKey, metricPoints]
  );

  const logResultBars = useMemo(() => summarizeLogResults(logItems), [logItems]);

  const logActionBars = useMemo(() => summarizeLogActions(logItems), [logItems]);

  const trafficConnectorOptions = useMemo(() => {
    const connectorIDSet = new Set<string>();
    for (const item of connectorItems) {
      const connectorID = readText(item, "connector_id", "");
      if (connectorID !== "") {
        connectorIDSet.add(connectorID);
      }
    }
    for (const item of tunnelItems) {
      const connectorID = readText(item, "connector_id", "");
      if (connectorID !== "") {
        connectorIDSet.add(connectorID);
      }
    }
    if (tunnelConnectorFilter !== "ALL") {
      connectorIDSet.add(tunnelConnectorFilter);
    }
    return ["ALL", ...Array.from(connectorIDSet).sort((left, right) => left.localeCompare(right))];
  }, [connectorItems, tunnelConnectorFilter, tunnelItems]);

  return {
    activeMeta,
    currentDetailItems,
    currentDetailRecord,
    currentDetailTitle,
    dashboardHealthBars,
    dashboardMetricCards,
    dashboardTopCounters,
    dashboardTrendSeries,
    detailDomainItems,
    diagnoseIssues,
    logActionBars,
    logResultBars,
    metricKeyOptions,
    metricSummaryBars,
    metricTrend,
    overviewListeners,
    realtimeSummary,
    trafficConnectorOptions,
    tunnelRingSegments,
  };
}

export type AdminConsoleDerived = ReturnType<typeof useAdminConsoleDerived>;
