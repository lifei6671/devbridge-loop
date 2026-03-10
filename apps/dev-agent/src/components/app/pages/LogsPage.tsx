import { LoaderCircle, Trash2 } from "lucide-react";
import type { ReactElement } from "react";

import { formatTime } from "@/components/app/helpers";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";
import { cn } from "@/lib/utils";
import type { ErrorEntry, RequestSummary } from "@/types";

interface LogsPageProps {
  clearingLogs: boolean;
  errors: ErrorEntry[];
  onClearLogs: () => void;
  requests: RequestSummary[];
}

export function LogsPage({
  clearingLogs,
  errors,
  onClearLogs,
  requests
}: LogsPageProps): ReactElement {
  return (
    <Card>
      <CardHeader>
        <div className="flex items-center justify-between gap-3">
          <div>
            <CardTitle>运行日志</CardTitle>
            <CardDescription>基于 `/api/v1/state/diagnostics` 与运行态事件的最近日志</CardDescription>
          </div>
          <Button
            variant="outline"
            size="sm"
            disabled={clearingLogs || (errors.length === 0 && requests.length === 0)}
            onClick={onClearLogs}
          >
            {clearingLogs ? (
              <LoaderCircle className={cn("mr-2 h-4 w-4 animate-spin")} />
            ) : (
              <Trash2 className="mr-2 h-4 w-4" />
            )}
            {clearingLogs ? "清空中..." : "清空日志"}
          </Button>
        </div>
      </CardHeader>
      <CardContent className="space-y-3">
        {errors.length === 0 ? (
          <p className="text-sm text-muted-foreground">当前无错误日志</p>
        ) : (
          errors.map((entry) => (
            <div
              key={`${entry.code}-${entry.occurredAt}-${entry.message}`}
              className="rounded-md border border-border/60 p-3"
            >
              <div className="flex items-center justify-between gap-3">
                <Badge variant="outline">{entry.code}</Badge>
                <span className="text-xs text-muted-foreground">{formatTime(entry.occurredAt)}</span>
              </div>
              <p className="mt-2 text-sm">{entry.message}</p>
              <pre className="mt-2 overflow-x-auto text-xs text-muted-foreground">
                {JSON.stringify(entry.context ?? {}, null, 2)}
              </pre>
            </div>
          ))
        )}

        <div className="h-px w-full bg-border/70" />

        <div className="space-y-2">
          <p className="text-sm font-semibold">最近请求</p>
          {requests.length === 0 ? (
            <p className="text-sm text-muted-foreground">当前无请求摘要</p>
          ) : (
            requests.map((entry) => (
              <div
                key={`${entry.direction}-${entry.serviceName}-${entry.occurredAt}-${entry.latencyMs}`}
                className="rounded-md border border-border/60 p-3"
              >
                <div className="flex flex-wrap items-center gap-2">
                  <Badge variant="outline">{entry.direction}</Badge>
                  <Badge variant="secondary">{entry.protocol}</Badge>
                  <span className="text-sm font-medium">{entry.serviceName}</span>
                  <span className="text-xs text-muted-foreground">
                    {entry.requestedEnv || "-"} → {entry.resolvedEnv || "-"} ({entry.resolution})
                  </span>
                </div>
                <div className="mt-2 flex flex-wrap items-center gap-3 text-xs text-muted-foreground">
                  <span>上游: {entry.upstream || "-"}</span>
                  <span>状态码: {entry.statusCode}</span>
                  <span>耗时: {entry.latencyMs}ms</span>
                  <span>结果: {entry.result}</span>
                  <span>{formatTime(entry.occurredAt)}</span>
                </div>
                {entry.errorCode || entry.message ? (
                  <p className="mt-2 text-xs text-amber-900">
                    {entry.errorCode ? `${entry.errorCode}: ` : ""}
                    {entry.message ?? ""}
                  </p>
                ) : null}
              </div>
            ))
          )}
        </div>
      </CardContent>
    </Card>
  );
}
