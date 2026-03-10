import { X } from "lucide-react";
import type { ReactElement } from "react";
import { useMemo, useState } from "react";

import { formatTime } from "@/components/app/helpers";
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
import type { ActiveIntercept } from "@/types";

interface InterceptsPageProps {
  intercepts: ActiveIntercept[];
}

export function InterceptsPage({ intercepts }: InterceptsPageProps): ReactElement {
  const [selectedInterceptKey, setSelectedInterceptKey] = useState<string | null>(null);

  const selectedIntercept = useMemo(() => {
    if (!selectedInterceptKey) {
      return null;
    }
    return (
      intercepts.find(
        (item) =>
          `${item.env}-${item.serviceName}-${item.protocol}-${item.instanceId}-${item.targetPort}` ===
          selectedInterceptKey
      ) ?? null
    );
  }, [intercepts, selectedInterceptKey]);

  return (
    <>
      <Card>
        <CardHeader>
          <CardTitle>生效接管</CardTitle>
          <CardDescription>读取 `/api/v1/state/intercepts` 的实时接管关系（点击行查看详情）</CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>环境</TableHead>
                <TableHead>服务</TableHead>
                <TableHead>协议</TableHead>
                <TableHead>实例</TableHead>
                <TableHead>隧道ID</TableHead>
                <TableHead>目标端口</TableHead>
                <TableHead>状态</TableHead>
                <TableHead>更新时间</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {intercepts.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={8} className="text-center text-muted-foreground">
                    当前无接管关系
                  </TableCell>
                </TableRow>
              ) : (
                intercepts.map((item) => {
                  const itemKey = `${item.env}-${item.serviceName}-${item.protocol}-${item.instanceId}-${item.targetPort}`;
                  const selected = itemKey === selectedInterceptKey;
                  return (
                    <TableRow
                      key={itemKey}
                      className={[
                        "cursor-pointer hover:bg-muted/40",
                        selected ? "bg-muted/30" : ""
                      ].join(" ")}
                      onClick={() => setSelectedInterceptKey(itemKey)}
                      onKeyDown={(event) => {
                        if (event.key === "Enter" || event.key === " ") {
                          event.preventDefault();
                          setSelectedInterceptKey(itemKey);
                        }
                      }}
                      tabIndex={0}
                    >
                      <TableCell>{item.env}</TableCell>
                      <TableCell>{item.serviceName}</TableCell>
                      <TableCell>{item.protocol}</TableCell>
                      <TableCell className="font-mono text-xs">{item.instanceId}</TableCell>
                      <TableCell className="font-mono text-xs">{item.tunnelId}</TableCell>
                      <TableCell>{item.targetPort}</TableCell>
                      <TableCell>{item.status}</TableCell>
                      <TableCell className="text-xs text-muted-foreground">
                        {formatTime(item.updatedAt)}
                      </TableCell>
                    </TableRow>
                  );
                })
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selectedIntercept ? (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4"
          onClick={() => setSelectedInterceptKey(null)}
          onKeyDown={(event) => {
            if (event.key === "Escape") {
              setSelectedInterceptKey(null);
            }
          }}
          role="presentation"
        >
          <Card
            className="max-h-[85vh] w-full max-w-2xl overflow-hidden"
            onClick={(event) => event.stopPropagation()}
            role="dialog"
            aria-modal="true"
            aria-label="接管详情"
          >
            <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
              <div>
                <CardTitle>接管详情</CardTitle>
                <CardDescription>当前选中接管关系的详细状态</CardDescription>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 w-8 p-0"
                onClick={() => setSelectedInterceptKey(null)}
                aria-label="关闭接管详情"
              >
                <X className="h-4 w-4" />
              </Button>
            </CardHeader>
            <CardContent className="space-y-2 overflow-y-auto text-sm">
              <div className="font-semibold">
                {selectedIntercept.serviceName} / {selectedIntercept.env}
              </div>
              <div>协议: {selectedIntercept.protocol}</div>
              <div className="font-mono text-xs">实例ID: {selectedIntercept.instanceId}</div>
              <div className="font-mono text-xs">隧道ID: {selectedIntercept.tunnelId}</div>
              <div>目标端口: {selectedIntercept.targetPort}</div>
              <div>状态: {selectedIntercept.status}</div>
              <div className="text-muted-foreground">
                更新时间: {formatTime(selectedIntercept.updatedAt)}
              </div>
            </CardContent>
          </Card>
        </div>
      ) : null}
    </>
  );
}
