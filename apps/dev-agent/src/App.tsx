import { useCallback, useEffect, useMemo, useState } from "react";
import { invoke } from "@tauri-apps/api/core";
import { listen } from "@tauri-apps/api/event";
import {
  AlertTriangle,
  Cable,
  Cloud,
  RefreshCcw,
  RotateCcw,
  Server,
  Zap
} from "lucide-react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow
} from "@/components/ui/table";
import type {
  AgentRuntime,
  ErrorEntry,
  LocalRegistration,
  StateSummary,
  TunnelState
} from "@/types";

async function call<T>(command: string): Promise<T> {
  return invoke<T>(command);
}

export default function App(): React.ReactElement {
  const [summary, setSummary] = useState<StateSummary | null>(null);
  const [tunnel, setTunnel] = useState<TunnelState | null>(null);
  const [registrations, setRegistrations] = useState<LocalRegistration[]>([]);
  const [errors, setErrors] = useState<ErrorEntry[]>([]);
  const [runtime, setRuntime] = useState<AgentRuntime | null>(null);
  const [loading, setLoading] = useState(false);
  const [actionError, setActionError] = useState<string>("");

  const refresh = useCallback(async () => {
    setLoading(true);
    setActionError("");
    try {
      const [summaryData, tunnelData, regData, errorData, runtimeData] = await Promise.all([
        call<StateSummary>("get_state_summary"),
        call<TunnelState>("get_tunnel_state"),
        call<LocalRegistration[]>("get_registrations"),
        call<ErrorEntry[]>("get_recent_errors"),
        call<AgentRuntime>("agent_runtime")
      ]);
      setSummary(summaryData);
      setTunnel(tunnelData);
      setRegistrations(regData);
      setErrors(errorData);
      setRuntime(runtimeData);
    } catch (error) {
      setActionError(String(error));
    } finally {
      setLoading(false);
    }
  }, []);

  const reconnect = useCallback(async () => {
    setActionError("");
    try {
      await call<TunnelState>("trigger_reconnect");
      await refresh();
    } catch (error) {
      setActionError(String(error));
    }
  }, [refresh]);

  const restartAgent = useCallback(async () => {
    setActionError("");
    try {
      const runtimeData = await call<AgentRuntime>("restart_agent_process");
      setRuntime(runtimeData);
      await refresh();
    } catch (error) {
      setActionError(String(error));
    }
  }, [refresh]);

  useEffect(() => {
    void refresh();
    const timer = window.setInterval(() => {
      void refresh();
    }, 5000);
    return () => window.clearInterval(timer);
  }, [refresh]);

  useEffect(() => {
    // 订阅 Rust Host 推送的进程运行态变化事件，前端无需等轮询也能实时更新。
    let unlistenFn: (() => void) | null = null;

    const setupListener = async (): Promise<void> => {
      unlistenFn = await listen<AgentRuntime>("agent-runtime-changed", (event) => {
        setRuntime(event.payload);
      });
    };

    void setupListener();

    return () => {
      if (unlistenFn) {
        unlistenFn();
      }
    };
  }, []);

  const tunnelBadge = useMemo(() => {
    if (!tunnel) {
      return <Badge variant="outline">unknown</Badge>;
    }
    if (tunnel.connected) {
      return <Badge>connected</Badge>;
    }
    return <Badge variant="secondary">disconnected</Badge>;
  }, [tunnel]);

  return (
    <div className="min-h-screen bg-app-gradient text-foreground">
      <div className="mx-auto flex w-full max-w-7xl flex-col gap-6 px-4 pb-8 pt-6 sm:px-6 lg:px-8">
        <header className="flex flex-wrap items-center justify-between gap-4">
          <div>
            <p className="text-xs uppercase tracking-[0.28em] text-muted-foreground">
              DevLoop Phase One
            </p>
            <h1 className="mt-1 font-mono text-2xl font-semibold tracking-tight">
              dev-agent control surface
            </h1>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button variant="outline" onClick={() => void refresh()} disabled={loading}>
              <RefreshCcw className="mr-2 h-4 w-4" />
              刷新
            </Button>
            <Button variant="outline" onClick={() => void restartAgent()}>
              <RotateCcw className="mr-2 h-4 w-4" />
              重启 Agent Core
            </Button>
            <Button onClick={() => void reconnect()}>
              <Cable className="mr-2 h-4 w-4" />
              手动重连
            </Button>
          </div>
        </header>

        {actionError ? (
          <Card className="border-amber-500/40 bg-amber-100/40">
            <CardContent className="flex items-center gap-2 p-4 text-sm text-amber-950">
              <AlertTriangle className="h-4 w-4" />
              {actionError}
            </CardContent>
          </Card>
        ) : null}

        {runtime?.lastError ? (
          <Card className="border-amber-500/40 bg-amber-100/40">
            <CardContent className="flex items-center gap-2 p-4 text-sm text-amber-950">
              <AlertTriangle className="h-4 w-4" />
              {runtime.lastError}
            </CardContent>
          </Card>
        ) : null}

        <section className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
          <Card>
            <CardHeader className="pb-2">
              <CardDescription>Agent</CardDescription>
              <CardTitle className="flex items-center gap-2 text-base">
                <Server className="h-4 w-4" />
                {summary?.agentStatus ?? "unknown"}
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              <div>pid: {runtime?.pid ?? "-"}</div>
              <div>env: {summary?.currentEnv ?? "-"}</div>
              <div>status: {runtime?.status ?? "-"}</div>
              <div>restart-count: {runtime?.restartCount ?? 0}</div>
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardDescription>Bridge</CardDescription>
              <CardTitle className="flex items-center gap-2 text-base">
                <Cloud className="h-4 w-4" />
                {summary?.bridgeStatus ?? "unknown"}
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              {tunnel?.bridgeAddress ?? "bridge.example.internal:443"}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardDescription>Tunnel</CardDescription>
              <CardTitle className="flex items-center gap-2 text-base">
                <Zap className="h-4 w-4" />
                {tunnelBadge}
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              epoch: {tunnel?.sessionEpoch ?? 0} | rv: {tunnel?.resourceVersion ?? 0}
            </CardContent>
          </Card>

          <Card>
            <CardHeader className="pb-2">
              <CardDescription>Registration</CardDescription>
              <CardTitle className="text-base">
                {summary?.registrationCount ?? 0} active
              </CardTitle>
            </CardHeader>
            <CardContent className="text-sm text-muted-foreground">
              intercepts: {summary?.activeIntercepts ?? 0}
            </CardContent>
          </Card>
        </section>

        <section className="grid gap-4 xl:grid-cols-3">
          <Card className="xl:col-span-2">
            <CardHeader>
              <CardTitle>Local Registrations</CardTitle>
              <CardDescription>
                支持多实例多端口。来自 `/api/v1/registrations` 的实时内存态。
              </CardDescription>
            </CardHeader>
            <CardContent>
              <Table>
                <TableHeader>
                  <TableRow>
                    <TableHead>Service</TableHead>
                    <TableHead>Env</TableHead>
                    <TableHead>Instance</TableHead>
                    <TableHead>Endpoints</TableHead>
                    <TableHead>TTL</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {registrations.length === 0 ? (
                    <TableRow>
                      <TableCell colSpan={5} className="text-center text-muted-foreground">
                        暂无本地注册项
                      </TableCell>
                    </TableRow>
                  ) : (
                    registrations.map((item) => (
                      <TableRow key={item.instanceId}>
                        <TableCell className="font-semibold">{item.serviceName}</TableCell>
                        <TableCell>{item.env}</TableCell>
                        <TableCell className="font-mono text-xs">{item.instanceId}</TableCell>
                        <TableCell>
                          {item.endpoints.map((endpoint) => (
                            <div key={`${endpoint.protocol}-${endpoint.targetPort}`}>
                              {endpoint.protocol}:{endpoint.targetHost}:
                              {endpoint.targetPort}
                            </div>
                          ))}
                        </TableCell>
                        <TableCell>{item.ttlSeconds}s</TableCell>
                      </TableRow>
                    ))
                  )}
                </TableBody>
              </Table>
            </CardContent>
          </Card>

          <Card>
            <CardHeader>
              <CardTitle>Recent Errors</CardTitle>
              <CardDescription>统一错误模型（最近 50 条）</CardDescription>
            </CardHeader>
            <CardContent className="space-y-3">
              {errors.length === 0 ? (
                <p className="text-sm text-muted-foreground">当前无错误</p>
              ) : (
                errors.slice(0, 8).map((entry) => (
                  <div
                    key={`${entry.code}-${entry.occurredAt}`}
                    className="rounded-md border border-border/60 p-3"
                  >
                    <p className="font-mono text-xs text-primary">{entry.code}</p>
                    <p className="mt-1 text-sm">{entry.message}</p>
                    <p className="mt-1 text-xs text-muted-foreground">{entry.occurredAt}</p>
                  </div>
                ))
              )}
            </CardContent>
          </Card>
        </section>
      </div>
    </div>
  );
}
