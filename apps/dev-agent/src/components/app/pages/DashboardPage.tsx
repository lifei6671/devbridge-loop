import { Cloud, Server, Zap } from "lucide-react";
import type { ReactElement } from "react";

import {
  formatCountdown,
  formatStatusText,
  formatTime
} from "@/components/app/helpers";
import { Badge } from "@/components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { AgentRuntime, StateSummary, TunnelState } from "@/types";

interface DashboardPageProps {
  agentCardStatus: string;
  bridgeCardStatus: string;
  bridgeOperationLabel: string;
  errorsCount: number;
  manualReconnectPending: boolean;
  manualReconnecting: boolean;
  phaseBusy: boolean;
  reconnectCountdown: number | null;
  refreshProgressLabel: string;
  requestsCount: number;
  runtime: AgentRuntime | null;
  summary: StateSummary | null;
  tunnel: TunnelState | null;
  uiPhase: "ready" | "restarting" | "recovering";
}

function renderTunnelBadge(tunnel: TunnelState | null): ReactElement {
  if (!tunnel) {
    return <Badge variant="outline">未知</Badge>;
  }

  if (tunnel.connected) {
    return <Badge className="border-transparent bg-emerald-600 text-white">已连接</Badge>;
  }

  return <Badge variant="secondary">未连接</Badge>;
}

export function DashboardPage({
  agentCardStatus,
  bridgeCardStatus,
  bridgeOperationLabel,
  errorsCount,
  manualReconnectPending,
  manualReconnecting,
  phaseBusy,
  reconnectCountdown,
  refreshProgressLabel,
  requestsCount,
  runtime,
  summary,
  tunnel,
  uiPhase
}: DashboardPageProps): ReactElement {
  return (
    <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
      <Card
        className={cn(
          "transition-all duration-300",
          phaseBusy && "border-amber-500/60 shadow-lg shadow-amber-900/10"
        )}
      >
        <CardHeader className="pb-2">
          <CardDescription>核心进程</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Server className={cn("h-4 w-4", phaseBusy && "animate-pulse")} />
            {formatStatusText(agentCardStatus)}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>进程号: {runtime?.pid ?? "-"}</div>
          <div>环境: {summary?.currentEnv ?? "-"}</div>
          <div>状态: {formatStatusText(agentCardStatus)}</div>
          <div>
            操作阶段:
            {uiPhase === "restarting"
              ? "重启核心进程..."
              : uiPhase === "recovering"
                ? "恢复状态中..."
                : "空闲"}
          </div>
          <div>同步状态: {refreshProgressLabel}</div>
          <div>重启次数: {runtime?.restartCount ?? 0}</div>
          <div className="whitespace-pre-wrap break-all">
            最近错误: {runtime?.lastError ? runtime.lastError : "-"}
          </div>
        </CardContent>
      </Card>

      <Card
        className={cn(
          "transition-all duration-300",
          (manualReconnectPending || manualReconnecting || phaseBusy) &&
            "border-amber-500/60 shadow-lg shadow-amber-900/10"
        )}
      >
        <CardHeader className="pb-2">
          <CardDescription>桥接连接</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Cloud
              className={cn(
                "h-4 w-4",
                (manualReconnectPending || manualReconnecting || phaseBusy) &&
                  "animate-pulse"
              )}
            />
            {formatStatusText(bridgeCardStatus)}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>地址: {tunnel?.bridgeAddress ?? "-"}</div>
          <div>研发域名: {summary?.rdName ?? "-"}</div>
          <div>操作阶段: {bridgeOperationLabel}</div>
          <div>重连次数: {tunnel?.reconnectAttempt ?? 0}</div>
          <div>
            下次重试:
            {reconnectCountdown !== null
              ? ` ${formatCountdown(reconnectCountdown)}`
              : ` ${formatTime(tunnel?.nextReconnectAt)}`}
          </div>
          <div className="whitespace-pre-wrap break-all">
            最近错误: {tunnel?.lastReconnectError ? tunnel.lastReconnectError : "-"}
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardDescription>隧道链路</CardDescription>
          <CardTitle className="flex items-center gap-2 text-base">
            <Zap className="h-4 w-4" />
            {renderTunnelBadge(tunnel)}
          </CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>协议: {tunnel?.protocol ?? "-"}</div>
          <div>会话计数: {tunnel?.sessionEpoch ?? 0}</div>
          <div>资源版本: {tunnel?.resourceVersion ?? 0}</div>
          <div>最近心跳: {formatTime(tunnel?.lastHeartbeatAt)}</div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader className="pb-2">
          <CardDescription>注册信息</CardDescription>
          <CardTitle className="text-base">{summary?.registrationCount ?? 0} 条生效</CardTitle>
        </CardHeader>
        <CardContent className="space-y-1 text-sm text-muted-foreground">
          <div>接管数: {summary?.activeIntercepts ?? 0}</div>
          <div>错误数(50): {errorsCount}</div>
          <div>请求数(200): {requestsCount}</div>
        </CardContent>
      </Card>
    </section>
  );
}
