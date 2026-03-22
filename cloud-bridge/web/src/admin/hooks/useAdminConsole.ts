import { useCallback, useEffect } from "react";

import type { AdminPageKey, DetailDomain } from "../model";
import {
  adminPageQueryKey,
  buildDetailRowElementID,
  resolveAdminPageFromLocation,
} from "../model";
import { useAdminConsoleActions } from "./useAdminConsoleActions";
import { useAdminConsoleDerived } from "./useAdminConsoleDerived";
import { useAdminConsoleRealtime } from "./useAdminConsoleRealtime";
import { useAdminConsoleState } from "./useAdminConsoleState";

export function useAdminConsole() {
  const state = useAdminConsoleState();
  const { activePage, detailSelection, setActivePage, setDetailSelection } = state;

  const syncAdminPageToURL = useCallback((page: AdminPageKey, mode: "push" | "replace" = "replace") => {
    if (typeof window === "undefined") {
      return;
    }
    const url = new URL(window.location.href);
    url.searchParams.set(adminPageQueryKey, page);
    const nextURL = `${url.pathname}${url.search}${url.hash}`;
    const currentURL = `${window.location.pathname}${window.location.search}${window.location.hash}`;
    if (nextURL === currentURL) {
      return;
    }
    if (mode === "push") {
      window.history.pushState({ [adminPageQueryKey]: page }, "", nextURL);
      return;
    }
    window.history.replaceState({ [adminPageQueryKey]: page }, "", nextURL);
  }, []);

  const navigateToPage = useCallback(
    (page: AdminPageKey, options?: { replace?: boolean }) => {
      setActivePage((previousPage) => (previousPage === page ? previousPage : page));
      syncAdminPageToURL(page, options?.replace ? "replace" : "push");
    },
    [setActivePage, syncAdminPageToURL]
  );


  const openDetailDrawer = useCallback(
    (domain: DetailDomain, index: number) => {
      if (index < 0) {
        return;
      }
      setDetailSelection({
        domain,
        index,
      });
    },
    [setDetailSelection]
  );

  const closeDetailDrawer = useCallback(() => {
    setDetailSelection(null);
  }, [setDetailSelection]);

  const derived = useAdminConsoleDerived(state);
  const {
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
  } = derived;

  const moveDetailSelection = useCallback(
    (offset: number) => {
      setDetailSelection((previousSelection) => {
        if (previousSelection === null) {
          return previousSelection;
        }
        const items = detailDomainItems[previousSelection.domain] ?? [];
        const nextIndex = previousSelection.index + offset;
        if (nextIndex < 0 || nextIndex >= items.length) {
          return previousSelection;
        }
        return {
          ...previousSelection,
          index: nextIndex,
        };
      });
    },
    [detailDomainItems, setDetailSelection]
  );

  const actions = useAdminConsoleActions({
    navigateToPage,
    openDetailDrawer,
    state,
  });

  useAdminConsoleRealtime({
    applySSESnapshot: actions.applySSESnapshot,
    refreshPageData: actions.refreshPageData,
    state,
  });

  useEffect(() => {
    syncAdminPageToURL(activePage, "replace");
  }, [activePage, syncAdminPageToURL]);

  useEffect(() => {
    if (typeof window === "undefined") {
      return;
    }
    const handleLocationChange = () => {
      const nextPage = resolveAdminPageFromLocation(window.location);
      setActivePage((previousPage) => (previousPage === nextPage ? previousPage : nextPage));
    };
    window.addEventListener("popstate", handleLocationChange);
    window.addEventListener("hashchange", handleLocationChange);
    return () => {
      window.removeEventListener("popstate", handleLocationChange);
      window.removeEventListener("hashchange", handleLocationChange);
    };
  }, [setActivePage]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    const handleEscapePress = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        closeDetailDrawer();
        return;
      }
      if (event.key === "ArrowLeft") {
        moveDetailSelection(-1);
        return;
      }
      if (event.key === "ArrowRight") {
        moveDetailSelection(1);
      }
    };
    window.addEventListener("keydown", handleEscapePress);
    return () => {
      window.removeEventListener("keydown", handleEscapePress);
    };
  }, [closeDetailDrawer, detailSelection, moveDetailSelection]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    if (currentDetailRecord !== null) {
      return;
    }
    closeDetailDrawer();
  }, [closeDetailDrawer, currentDetailRecord, detailSelection]);

  useEffect(() => {
    if (detailSelection === null) {
      return;
    }
    const rowElementID = buildDetailRowElementID(detailSelection.domain, detailSelection.index);
    const targetRowElement = document.getElementById(rowElementID);
    if (!(targetRowElement instanceof HTMLElement)) {
      return;
    }
    // 抽屉切换记录时把对应行滚动到可视范围，减少上下文丢失。
    targetRowElement.scrollIntoView({
      behavior: "smooth",
      block: "nearest",
      inline: "nearest",
    });
  }, [detailSelection]);

  return {
    activeMeta,
    activeMetricKey: state.activeMetricKey,
    activePage: state.activePage,
    agentPoolSummary: state.agentPoolSummary,
    clearTrafficOwnershipLookup: actions.clearTrafficOwnershipLookup,
    closeDetailDrawer,
    configSnapshot: state.configSnapshot,
    connectorItems: state.connectorItems,
    currentDetailItems,
    currentDetailRecord,
    currentDetailTitle,
    dashboardHealthBars,
    dashboardMetricCards,
    dashboardTopCounters,
    dashboardTrendSeries,
    detailSelection: state.detailSelection,
    diagnoseIssues,
    diagnoseSummary: state.diagnoseSummary,
    drainConnectorID: state.drainConnectorID,
    drainReason: state.drainReason,
    drainSessionID: state.drainSessionID,
    exportDownloadURL: state.exportDownloadURL,
    handleDetailRowKeyDown: actions.handleDetailRowKeyDown,
    isActiveDetailRow: actions.isActiveDetailRow,
    isLoading: state.isLoading,
    isTrafficOwnershipLoading: state.isTrafficOwnershipLoading,
    lastSyncMS: state.lastSyncMS,
    logActionBars,
    logItems: state.logItems,
    logResultBars,
    lookupTrafficOwnership: actions.lookupTrafficOwnership,
    metricKeyOptions,
    metricPoints: state.metricPoints,
    metricSummaryBars,
    metricTrend,
    moveDetailSelection,
    navigateToPage,
    openDetailDrawer,
    overviewListeners,
    patchKey: state.patchKey,
    patchValue: state.patchValue,
    performConfigPatch: actions.performConfigPatch,
    performDrainConnector: actions.performDrainConnector,
    performDrainSession: actions.performDrainSession,
    performExportDiagnose: actions.performExportDiagnose,
    performReload: actions.performReload,
    prefillOpsFromDetail: actions.prefillOpsFromDetail,
    quickDrainFromDetail: actions.quickDrainFromDetail,
    realtimeSummary,
    refreshPageData: actions.refreshPageData,
    routeItems: state.routeItems,
    serviceItems: state.serviceItems,
    sessionItems: state.sessionItems,
    submitConfigPatch: actions.submitConfigPatch,
    submitConfigPatchDocument: actions.submitConfigPatchDocument,
    sessionStateFilter: state.sessionStateFilter,
    setActiveMetricKey: state.setActiveMetricKey,
    setDrainConnectorID: state.setDrainConnectorID,
    setDrainReason: state.setDrainReason,
    setDrainSessionID: state.setDrainSessionID,
    setPatchKey: state.setPatchKey,
    setPatchValue: state.setPatchValue,
    setSessionStateFilter: state.setSessionStateFilter,
    setTimeRangeMinutes: state.setTimeRangeMinutes,
    setTrafficLookupID: state.setTrafficLookupID,
    setTunnelConnectorFilter: state.setTunnelConnectorFilter,
    setTunnelStateFilter: state.setTunnelStateFilter,
    timeRangeMinutes: state.timeRangeMinutes,
    trafficConnectorOptions,
    trafficLookupID: state.trafficLookupID,
    trafficOwnership: state.trafficOwnership,
    trafficOwnershipError: state.trafficOwnershipError,
    trafficSummary: state.trafficSummary,
    tunnelConnectorFilter: state.tunnelConnectorFilter,
    tunnelItems: state.tunnelItems,
    tunnelRingSegments,
    tunnelStateFilter: state.tunnelStateFilter,
    tunnelSummary: state.tunnelSummary,
  };
}

export type AdminConsoleViewModel = ReturnType<typeof useAdminConsole>;
