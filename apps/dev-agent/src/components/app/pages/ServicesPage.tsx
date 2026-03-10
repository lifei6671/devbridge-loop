import { Trash2, X } from "lucide-react";
import type { ReactElement } from "react";

import {
  formatTime,
  renderHealthBadge
} from "@/components/app/helpers";
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
import type { LocalRegistration } from "@/types";

interface ServicesPageProps {
  onCloseDetail: () => void;
  onSelectInstance: (instanceId: string) => void;
  onUnregisterInstance: (instanceId: string) => void;
  registrations: LocalRegistration[];
  selectedInstanceId: string | null;
  selectedRegistration: LocalRegistration | null;
  unregisteringId: string | null;
}

export function ServicesPage({
  onCloseDetail,
  onSelectInstance,
  onUnregisterInstance,
  registrations,
  selectedInstanceId,
  selectedRegistration,
  unregisteringId
}: ServicesPageProps): ReactElement {
  return (
    <section>
      <Card>
        <CardHeader>
          <CardTitle>本地服务</CardTitle>
          <CardDescription>
            实时读取 `/api/v1/registrations`，点击服务行可查看详情并支持手动注销。
          </CardDescription>
        </CardHeader>
        <CardContent>
          <Table>
            <TableHeader>
              <TableRow>
                <TableHead>服务名</TableHead>
                <TableHead>环境</TableHead>
                <TableHead>实例</TableHead>
                <TableHead>端点</TableHead>
                <TableHead>健康</TableHead>
                <TableHead>注册时间</TableHead>
                <TableHead>操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {registrations.length === 0 ? (
                <TableRow>
                  <TableCell colSpan={7} className="text-center text-muted-foreground">
                    暂无本地注册项
                  </TableCell>
                </TableRow>
              ) : (
                registrations.map((item) => (
                  <TableRow
                    key={item.instanceId}
                    className={[
                      "cursor-pointer hover:bg-muted/40",
                      item.instanceId === selectedInstanceId ? "bg-muted/30" : ""
                    ].join(" ")}
                    onClick={() => onSelectInstance(item.instanceId)}
                    onKeyDown={(event) => {
                      if (event.key === "Enter" || event.key === " ") {
                        event.preventDefault();
                        onSelectInstance(item.instanceId);
                      }
                    }}
                    tabIndex={0}
                  >
                    <TableCell className="font-semibold">{item.serviceName}</TableCell>
                    <TableCell>{item.env}</TableCell>
                    <TableCell className="font-mono text-xs">{item.instanceId}</TableCell>
                    <TableCell>
                      {item.endpoints.map((endpoint) => (
                        <div key={`${endpoint.protocol}-${endpoint.targetPort}`}>
                          {endpoint.protocol}:{endpoint.targetHost}:{endpoint.targetPort}
                        </div>
                      ))}
                    </TableCell>
                    <TableCell>{renderHealthBadge(item.healthy)}</TableCell>
                    <TableCell className="text-xs text-muted-foreground">
                      {formatTime(item.registerTime)}
                    </TableCell>
                    <TableCell>
                      <Button
                        variant="outline"
                        size="sm"
                        disabled={unregisteringId === item.instanceId}
                        onKeyDown={(event) => {
                          event.stopPropagation();
                        }}
                        onClick={(event) => {
                          event.stopPropagation();
                          onUnregisterInstance(item.instanceId);
                        }}
                      >
                        <Trash2 className="mr-1 h-3.5 w-3.5" />
                        注销
                      </Button>
                    </TableCell>
                  </TableRow>
                ))
              )}
            </TableBody>
          </Table>
        </CardContent>
      </Card>

      {selectedRegistration ? (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 p-4"
          onClick={onCloseDetail}
          role="presentation"
        >
          <Card
            className="max-h-[85vh] w-full max-w-2xl overflow-hidden"
            onClick={(event) => event.stopPropagation()}
            role="dialog"
            aria-modal="true"
            aria-label="服务详情"
          >
            <CardHeader className="flex flex-row items-start justify-between gap-2 space-y-0">
              <div>
                <CardTitle>服务详情</CardTitle>
                <CardDescription>当前选中实例的详细状态</CardDescription>
              </div>
              <Button
                variant="ghost"
                size="sm"
                className="h-8 w-8 p-0"
                onClick={onCloseDetail}
                aria-label="关闭服务详情"
              >
                <X className="h-4 w-4" />
              </Button>
            </CardHeader>
            <CardContent className="space-y-2 overflow-y-auto text-sm">
              <div className="font-semibold">
                {selectedRegistration.serviceName} / {selectedRegistration.env}
              </div>
              <div className="text-muted-foreground">实例ID: {selectedRegistration.instanceId}</div>
              <div>健康状态: {selectedRegistration.healthy ? "是" : "否"}</div>
              <div>TTL 秒数: {selectedRegistration.ttlSeconds}</div>
              <div>注册时间: {formatTime(selectedRegistration.registerTime)}</div>
              <div>最近心跳: {formatTime(selectedRegistration.lastHeartbeatTime)}</div>
              <div className="space-y-1 rounded-md border border-border/60 p-2">
                <div className="font-medium">元数据</div>
                <pre className="overflow-x-auto text-xs text-muted-foreground">
                  {JSON.stringify(selectedRegistration.metadata ?? {}, null, 2)}
                </pre>
              </div>
            </CardContent>
          </Card>
        </div>
      ) : null}
    </section>
  );
}
