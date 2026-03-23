import { useEffect, useRef, useState } from "react";

import type {
  AdminPageKey,
  AdminAuthProvider,
  AdminSessionRecord,
  AuthStatus,
  ApiRecord,
  DetailSelection,
  RealtimeMode,
  SSEConnectionState,
} from "../model";
import {
  defaultAdminPage,
  defaultAutoRefreshIntervalMS,
  resolveAdminPageFromLocation,
} from "../model";

export function useAdminConsoleState() {
  const [activePage, setActivePage] = useState<AdminPageKey>(() => {
    if (typeof window === "undefined") {
      return defaultAdminPage;
    }
    return resolveAdminPageFromLocation(window.location);
  });
  const [authStatus, setAuthStatus] = useState<AuthStatus>("loading");
  const [authProviders, setAuthProviders] = useState<AdminAuthProvider[]>([]);
  const [session, setSession] = useState<AdminSessionRecord | null>(null);
  const [authError, setAuthError] = useState("");
  const [loginUsername, setLoginUsername] = useState("");
  const [loginPassword, setLoginPassword] = useState("");
  const [isAuthenticating, setIsAuthenticating] = useState(false);
  const [isLoading, setIsLoading] = useState(false);
  const [lastSyncMS, setLastSyncMS] = useState(0);
  const [detailSelection, setDetailSelection] = useState<DetailSelection | null>(null);

  const [overview, setOverview] = useState<ApiRecord>({});
  const [routeItems, setRouteItems] = useState<ApiRecord[]>([]);
  const [serviceItems, setServiceItems] = useState<ApiRecord[]>([]);
  const [connectorItems, setConnectorItems] = useState<ApiRecord[]>([]);
  const [sessionItems, setSessionItems] = useState<ApiRecord[]>([]);
  const [tunnelSummary, setTunnelSummary] = useState<ApiRecord>({});
  const [agentPoolSummary, setAgentPoolSummary] = useState<ApiRecord>({});
  const [tunnelItems, setTunnelItems] = useState<ApiRecord[]>([]);
  const [trafficSummary, setTrafficSummary] = useState<ApiRecord>({});
  const [configSnapshot, setConfigSnapshot] = useState<ApiRecord>({});
  const [logItems, setLogItems] = useState<ApiRecord[]>([]);
  const [metricPoints, setMetricPoints] = useState<ApiRecord[]>([]);
  const [diagnoseSummary, setDiagnoseSummary] = useState<ApiRecord>({});

  const [sessionStateFilter, setSessionStateFilter] = useState("ALL");
  const [tunnelStateFilter, setTunnelStateFilter] = useState("ALL");
  const [tunnelConnectorFilter, setTunnelConnectorFilter] = useState("ALL");
  const [timeRangeMinutes, setTimeRangeMinutes] = useState(30);
  const [activeMetricKey, setActiveMetricKey] = useState("open_timeout_total");

  const [drainSessionID, setDrainSessionID] = useState("");
  const [drainConnectorID, setDrainConnectorID] = useState("");
  const [drainReason, setDrainReason] = useState("manual_ops");
  const [trafficLookupID, setTrafficLookupID] = useState("");
  const [trafficOwnership, setTrafficOwnership] = useState<ApiRecord | null>(null);
  const [trafficOwnershipError, setTrafficOwnershipError] = useState("");
  const [isTrafficOwnershipLoading, setIsTrafficOwnershipLoading] = useState(false);
  const [patchKey, setPatchKey] = useState("observability.log_level");
  const [patchValue, setPatchValue] = useState("debug");
  const [exportDownloadURL, setExportDownloadURL] = useState("");
  const autoRefreshEnabled = true;
  const autoRefreshIntervalMS = defaultAutoRefreshIntervalMS;
  const [isAutoRefreshing, setIsAutoRefreshing] = useState(false);
  const [realtimeMode, setRealtimeMode] = useState<RealtimeMode>("off");
  const [sseConnectionState, setSSEConnectionState] = useState<SSEConnectionState>("idle");
  const [sseReconnectTrigger, setSSEReconnectTrigger] = useState(0);
  const isAutoRefreshInFlightRef = useRef(false);
  const sseEventSourceRef = useRef<EventSource | null>(null);

  return {
    activeMetricKey,
    activePage,
    agentPoolSummary,
    authError,
    authProviders,
    authStatus,
    autoRefreshEnabled,
    autoRefreshIntervalMS,
    configSnapshot,
    connectorItems,
    detailSelection,
    diagnoseSummary,
    drainConnectorID,
    drainReason,
    drainSessionID,
    exportDownloadURL,
    isAuthenticating,
    isAutoRefreshInFlightRef,
    isAutoRefreshing,
    isLoading,
    isTrafficOwnershipLoading,
    lastSyncMS,
    loginPassword,
    loginUsername,
    logItems,
    metricPoints,
    overview,
    patchKey,
    patchValue,
    realtimeMode,
    routeItems,
    serviceItems,
    session,
    sessionItems,
    sessionStateFilter,
    setActiveMetricKey,
    setActivePage,
    setAgentPoolSummary,
    setAuthError,
    setAuthProviders,
    setAuthStatus,
    setConfigSnapshot,
    setConnectorItems,
    setDetailSelection,
    setDiagnoseSummary,
    setDrainConnectorID,
    setDrainReason,
    setDrainSessionID,
    setExportDownloadURL,
    setIsAuthenticating,
    setIsAutoRefreshing,
    setIsLoading,
    setIsTrafficOwnershipLoading,
    setLastSyncMS,
    setLoginPassword,
    setLoginUsername,
    setLogItems,
    setMetricPoints,
    setOverview,
    setPatchKey,
    setPatchValue,
    setRealtimeMode,
    setRouteItems,
    setServiceItems,
    setSession,
    setSessionItems,
    setSessionStateFilter,
    setSSEConnectionState,
    setSSEReconnectTrigger,
    setTimeRangeMinutes,
    setTrafficLookupID,
    setTrafficOwnership,
    setTrafficOwnershipError,
    setTrafficSummary,
    setTunnelConnectorFilter,
    setTunnelItems,
    setTunnelStateFilter,
    setTunnelSummary,
    sseConnectionState,
    sseEventSourceRef,
    sseReconnectTrigger,
    timeRangeMinutes,
    trafficLookupID,
    trafficOwnership,
    trafficOwnershipError,
    trafficSummary,
    tunnelConnectorFilter,
    tunnelItems,
    tunnelStateFilter,
    tunnelSummary,
  };
}

export type AdminConsoleState = ReturnType<typeof useAdminConsoleState>;
