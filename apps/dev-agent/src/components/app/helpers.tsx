import type { ReactElement } from "react";

import { Badge } from "@/components/ui/badge";
import type { DesktopConfigSaveRequest, DesktopConfigView } from "@/types";

export function formatTime(value?: string | null): string {
  if (!value) {
    return "-";
  }

  const unixSeconds = Number(value);
  const date = Number.isFinite(unixSeconds) && /^\d+$/.test(value)
    ? new Date(unixSeconds * 1000)
    : new Date(value);

  if (Number.isNaN(date.getTime())) {
    return value;
  }

  return date.toLocaleString("zh-CN", { hour12: false });
}

export function formatCountdown(remainingMs: number): string {
  if (!Number.isFinite(remainingMs) || remainingMs <= 0) {
    return "0s";
  }

  return `${Math.ceil(remainingMs / 1000)}s`;
}

export function renderHealthBadge(healthy: boolean): ReactElement {
  if (healthy) {
    return <Badge className="border-transparent bg-emerald-600 text-white">健康</Badge>;
  }
  return <Badge variant="secondary">异常</Badge>;
}

export function formatStatusText(status: string | null | undefined): string {
  switch ((status ?? "").trim().toLowerCase()) {
    case "running":
      return "运行中";
    case "stopped":
      return "已停止";
    case "restarting":
      return "重启中";
    case "recovering":
      return "恢复中";
    case "online":
      return "在线";
    case "offline":
      return "离线";
    case "reconnecting":
      return "重连中";
    case "manual-reconnecting":
      return "手动重连中";
    case "connected":
      return "已连接";
    case "disconnected":
      return "未连接";
    default:
      return status && status.trim() !== "" ? status : "未知";
  }
}

export function parseNumberList(input: string): number[] {
  const parsed = input
    .split(",")
    .map((item) => Number(item.trim()))
    .filter((item) => Number.isFinite(item) && item > 0);

  if (parsed.length === 0) {
    return [500, 1000, 2000, 5000];
  }

  return parsed;
}

export function parseStringList(input: string): string[] {
  const parsed = input
    .split(",")
    .map((item) => item.trim())
    .filter((item) => item.length > 0);

  if (parsed.length === 0) {
    return ["requestHeader", "payload", "runtimeDefault", "baseFallback"];
  }

  return parsed;
}

export function delay(ms: number): Promise<void> {
  return new Promise((resolve) => {
    window.setTimeout(resolve, ms);
  });
}

export function buildDesktopConfigDraft(view: DesktopConfigView): DesktopConfigSaveRequest {
  return {
    agentHttpAddr: view.agentHttpAddr,
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
