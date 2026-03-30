import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import {
  executiveFieldClassName,
  formatBytes,
  formatCount,
  formatDateTime,
  formatLevelText,
  formatRate,
  formatRelativeTime,
  formatStatusText,
  type ConsoleData,
  type DiagnoseLogItem,
  settingsFieldCaptionText,
  settingsFieldHelpText,
  settingsFieldPlaceholderText,
  statusBadgeVariant,
  type ServiceItem,
  type SessionSnapshot,
  type TunnelItem,
  levelBadgeVariant,
} from "@/console-shared";
import {
  EmptyStatePanel,
  ExecutiveMetric,
  Field,
  FieldErrorText,
  NavGlyph,
  OverviewKeyValue,
  PoolBar,
  SettingsDetailRow,
  SettingsSummaryMetric,
  StatColumn,
} from "@/console-kit";
import { bridgeTransportOptions, type ConfigSnapshot, type SettingsDraft, type SettingsFieldErrors } from "@/settings";
export function OverviewPage({ data, loading }: { data: ConsoleData; loading: boolean }) {
  return (
    <section className="grid gap-4 xl:grid-cols-[1.2fr_0.8fr]">
      <Card className="panel-soft overflow-hidden border-0 bg-[linear-gradient(180deg,rgba(255,255,255,0.92),rgba(243,244,245,0.92))] shadow-[0_10px_28px_rgba(25,28,29,0.05)]">
        <CardHeader className="flex-col items-start justify-between gap-3 sm:flex-row sm:gap-4">
          <div>
            <CardTitle>桥接会话矩阵</CardTitle>
            <CardDescription>从会话、隧道池和流量三个维度查看 Agent 当前运行态。</CardDescription>
          </div>
          <Badge variant={statusBadgeVariant(data.session.state)}>{loading ? "同步中" : "实时"}</Badge>
        </CardHeader>
        <CardContent className="grid gap-6">
          <div className="grid gap-4 md:grid-cols-2">
            <ExecutiveMetric label="会话 ID" value={data.session.session_id ?? "未建立"} emphasis="primary" />
            <ExecutiveMetric
              label="下次重试"
              value={data.session.next_retry_at_ms ? formatRelativeTime(data.session.next_retry_at_ms) : "无计划"}
              emphasis="muted"
            />
          </div>

          <div className="grid gap-4 md:grid-cols-3">
            <StatColumn title="上行速率" value={formatRate(data.traffic.upload_bytes_per_sec)} caption={`总量 ${formatBytes(data.traffic.upload_total_bytes)}`} />
            <StatColumn title="下行速率" value={formatRate(data.traffic.download_bytes_per_sec)} caption={`总量 ${formatBytes(data.traffic.download_total_bytes)}`} />
            <StatColumn title="状态变更" value={formatCount(data.diagnose.event_state_changes)} caption="最近状态变化总数" />
          </div>

          <div className="grid gap-3">
            <PoolBar label="建立中" current={data.agent.tunnel_pool.opening} total={Math.max(1, data.agent.tunnel_pool.total)} />
            <PoolBar label="空闲中" current={data.agent.tunnel_pool.idle} total={Math.max(1, data.agent.tunnel_pool.total)} />
            <PoolBar label="使用中" current={data.agent.tunnel_pool.active} total={Math.max(1, data.agent.tunnel_pool.total)} />
          </div>
        </CardContent>
      </Card>

      <div className="grid gap-4">
        <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
          <CardHeader className="flex-row items-center justify-between gap-3">
            <div>
              <CardTitle>运行重点</CardTitle>
              <CardDescription>聚焦当前桥接状态和最近诊断事件。</CardDescription>
            </div>
            <Badge variant="warning">实时</Badge>
          </CardHeader>
          <CardContent className="grid gap-3">
            <OverviewKeyValue label="桥接地址" value={data.agent.bridge_transport + " · " + data.agent.bridge_addr} />
            <OverviewKeyValue label="启动时间" value={formatDateTime(data.agent.started_at_ms)} />
            <OverviewKeyValue label="最近心跳" value={formatRelativeTime(data.session.last_heartbeat_at_ms)} />
            <OverviewKeyValue label="最近事件" value={data.diagnose.last_event_code || "未记录"} />
            <OverviewKeyValue label="最近错误" value={data.diagnose.last_error || "无"} />
          </CardContent>
        </Card>

        <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
          <CardHeader className="flex-row items-center justify-between gap-3">
            <div>
              <CardTitle>服务概览</CardTitle>
              <CardDescription>展示当前目录里最近注册的三个服务。</CardDescription>
            </div>
            <Badge variant="outline">{data.services.services.length} 项</Badge>
          </CardHeader>
          <CardContent className="grid gap-4">
            {data.services.services.slice(0, 3).map((service) => (
              <div key={service.instance_id || service.logical_service_id} className="rounded-[22px] border border-[rgba(214,218,224,0.38)] bg-[linear-gradient(180deg,rgba(255,255,255,0.86),rgba(247,249,251,0.86))] px-4 py-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.72)]">
                <div className="flex items-center justify-between gap-3">
                  <div>
                    <div className="font-medium">{service.service_name}</div>
                    <div className="mt-1 text-xs text-[hsl(var(--muted-foreground))]">
                      {service.scope.namespace}/{service.scope.environment}
                    </div>
                  </div>
                  <Badge variant={statusBadgeVariant(service.status)}>{formatStatusText(service.status)}</Badge>
                </div>
                <div className="mt-4 text-sm text-[hsl(var(--muted-foreground))]">
                  {service.endpoints[0] ? `${service.endpoints[0].host}:${service.endpoints[0].port}` : "未登记 endpoint"}
                </div>
              </div>
            ))}
            {data.services.services.length === 0 ? (
              <EmptyStatePanel
                eyebrow="最近服务"
                icon={<NavGlyph page="services" />}
                note="完成首个服务登记后，这里会自动切换成最近服务概览。"
                title="当前还没有服务"
                description="在右侧完成服务登记后，这里会展示最近注册的服务概览。"
                compact
              />
            ) : null}
          </CardContent>
        </Card>
      </div>
    </section>
  );
}

export function ServicesPage({
  actionPending,
  keyword,
  services,
  onDeleteService,
  onOpenCreateService,
  onOpenDetailService,
  onOpenEditService,
}: {
  actionPending: string;
  keyword: string;
  services: ServiceItem[];
  onDeleteService: (service: ServiceItem) => void;
  onOpenCreateService: () => void;
  onOpenDetailService: (service: ServiceItem) => void;
  onOpenEditService: (service: ServiceItem) => void;
}) {
  return (
    <section className="grid gap-4">
      <Card className="panel-soft overflow-hidden border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
        <CardHeader className="flex-col items-start justify-between gap-3 sm:flex-row sm:gap-4">
          <div>
            <CardTitle>服务目录</CardTitle>
            <CardDescription>基于 hostapi 的实时服务目录，支持查看注册详情与删除实例。</CardDescription>
          </div>
          <div className="flex w-full flex-wrap items-center justify-end gap-3 sm:w-auto">
            <Badge variant="outline">{keyword ? `过滤中 ${services.length}` : `${services.length} 项`}</Badge>
            <Button onClick={onOpenCreateService}>注册服务</Button>
          </div>
        </CardHeader>
        <CardContent className="-mx-1 px-1 sm:mx-0 sm:px-0">
          {services.length > 0 ? (
            <div className="overflow-x-auto">
              <Table className="min-w-[900px]">
                <TableHeader>
                  <TableRow>
                    <TableHead>服务</TableHead>
                    <TableHead>作用域</TableHead>
                    <TableHead>入口</TableHead>
                    <TableHead>状态</TableHead>
                    <TableHead>更新于</TableHead>
                    <TableHead className="text-right">操作</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {services.map((service) => (
                    <TableRow key={service.instance_id || service.logical_service_id}>
                      <TableCell>
                        <div className="space-y-1">
                          <div className="font-semibold">{service.service_name}</div>
                          <div className="text-xs text-[hsl(var(--muted-foreground))]">{service.instance_id || service.logical_service_id}</div>
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="text-sm">
                          {service.scope.namespace}/{service.scope.environment}
                        </div>
                        <div className="text-xs text-[hsl(var(--muted-foreground))]">{service.protocol}</div>
                      </TableCell>
                      <TableCell>
                        <div className="space-y-1">
                          {service.endpoints.slice(0, 2).map((endpoint) => (
                            <div key={endpoint.endpoint_id} className="text-sm">
                              {endpoint.host}:{endpoint.port}
                            </div>
                          ))}
                          {service.exposure?.path_prefix ? (
                            <div className="text-xs text-[hsl(var(--muted-foreground))]">路径 {service.exposure.path_prefix}</div>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="flex flex-col items-start gap-2">
                          <Badge variant={statusBadgeVariant(service.status)}>{formatStatusText(service.status)}</Badge>
                          {service.health_status ? (
                            <Badge variant={statusBadgeVariant(service.health_status)}>{formatStatusText(service.health_status)}</Badge>
                          ) : null}
                        </div>
                      </TableCell>
                      <TableCell>
                        <div className="text-sm">{formatDateTime(service.updated_at_ms)}</div>
                        <div className="text-xs text-[hsl(var(--muted-foreground))]">{formatRelativeTime(service.updated_at_ms)}</div>
                      </TableCell>
                      <TableCell className="text-right">
                        <div className="flex justify-end gap-2">
                          <Button variant="ghost" size="sm" onClick={() => onOpenDetailService(service)}>
                            详情
                          </Button>
                          <Button variant="ghost" size="sm" onClick={() => onOpenEditService(service)}>
                            编辑
                          </Button>
                          <Button
                            variant="ghost"
                            size="sm"
                            className="text-[rgb(185,28,28)] hover:bg-[rgba(239,68,68,0.08)]"
                            disabled={actionPending === `delete:${service.instance_id}`}
                            onClick={() => onDeleteService(service)}
                          >
                            删除
                          </Button>
                        </div>
                      </TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
            </div>
          ) : (
            <EmptyStatePanel
              eyebrow="服务目录"
              icon={<NavGlyph page="services" />}
              note="点击右上角“注册服务”即可打开模态窗，提交后会立即同步到这里。"
              title="服务目录还是空的"
              description="没有匹配的服务记录。点击右上角按钮即可打开注册表单，提交后会立即出现在这里。"
            />
          )}
        </CardContent>
      </Card>
    </section>
  );
}

export function TunnelsPage({ tunnels }: { tunnels: TunnelItem[] }) {
  return (
    <Card className="panel-soft overflow-hidden border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
      <CardHeader className="flex-col items-start justify-between gap-3 sm:flex-row sm:gap-4">
        <div>
          <CardTitle>隧道视图</CardTitle>
          <CardDescription>展示最近的隧道记录、链路协议、延迟和上游拨号耗时。</CardDescription>
        </div>
        <Badge variant="outline">{tunnels.length} 条</Badge>
      </CardHeader>
      <CardContent className="-mx-1 overflow-x-auto px-1 sm:mx-0 sm:px-0">
        <Table className="min-w-[780px]">
          <TableHeader>
            <TableRow>
              <TableHead>隧道</TableHead>
              <TableHead>服务绑定</TableHead>
              <TableHead>链路</TableHead>
              <TableHead>延迟</TableHead>
              <TableHead>最近心跳</TableHead>
              <TableHead>状态</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {tunnels.map((tunnel) => (
              <TableRow key={tunnel.tunnel_id}>
                <TableCell>
                  <div className="font-semibold">{tunnel.tunnel_id}</div>
                  <div className="text-xs text-[hsl(var(--muted-foreground))]">{formatDateTime(tunnel.updated_at_ms)}</div>
                </TableCell>
                <TableCell>
                  <div className="text-sm">{tunnel.instance_id || "未绑定"}</div>
                  <div className="text-xs text-[hsl(var(--muted-foreground))]">{tunnel.logical_service_id || "无 logical id"}</div>
                </TableCell>
                <TableCell>
                  <div className="text-sm">{tunnel.protocol}</div>
                  <div className="text-xs text-[hsl(var(--muted-foreground))]">
                    {(tunnel.local_addr || "本地待定") + " → " + (tunnel.remote_addr || "远端待定")}
                  </div>
                </TableCell>
                <TableCell>
                  <div className="text-sm">{typeof tunnel.latency_ms === "number" ? `${tunnel.latency_ms} ms` : "未记录"}</div>
                  <div className="text-xs text-[hsl(var(--muted-foreground))]">
                    上游拨号 {typeof tunnel.upstream_dial_latency_ms === "number" ? `${tunnel.upstream_dial_latency_ms} ms` : "未记录"}
                  </div>
                </TableCell>
                <TableCell>{formatRelativeTime(tunnel.last_heartbeat_at_ms)}</TableCell>
                <TableCell>
                  <div className="flex flex-col items-start gap-2">
                    <Badge variant={statusBadgeVariant(tunnel.state)}>{formatStatusText(tunnel.state)}</Badge>
                    {tunnel.last_error ? (
                      <span className="max-w-[240px] text-xs leading-5 text-[rgb(153,27,27)]">{tunnel.last_error}</span>
                    ) : null}
                  </div>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>

        {tunnels.length === 0 ? (
          <EmptyStatePanel
            eyebrow="隧道视图"
            icon={<NavGlyph page="tunnels" />}
            note="建立会话并分配链路后，这里会开始出现活跃隧道。"
            title="当前没有隧道记录"
            description="隧道建立后，这里会展示链路状态、延迟和最近心跳。"
          />
        ) : null}
      </CardContent>
    </Card>
  );
}

export function TrafficPage({ data }: { data: ConsoleData }) {
  const uploadTotal = data.traffic.upload_total_bytes + data.traffic.download_total_bytes;
  const uploadShare = uploadTotal > 0 ? (data.traffic.upload_total_bytes / uploadTotal) * 100 : 0;
  const downloadShare = uploadTotal > 0 ? (data.traffic.download_total_bytes / uploadTotal) * 100 : 0;

  return (
    <section className="grid gap-4 xl:grid-cols-[1.05fr_0.95fr]">
      <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
        <CardHeader>
          <CardTitle>流量吞吐</CardTitle>
          <CardDescription>展示最近采样窗口的实时吞吐与历史累计流量。</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-6">
          <div className="grid gap-4 md:grid-cols-2">
            <ExecutiveMetric label="上行速率" value={formatRate(data.traffic.upload_bytes_per_sec)} emphasis="primary" />
            <ExecutiveMetric label="下行速率" value={formatRate(data.traffic.download_bytes_per_sec)} emphasis="muted" />
          </div>

          <div className="space-y-4">
            <PoolBar label={`上行占比 ${uploadShare.toFixed(0)}%`} current={data.traffic.upload_total_bytes} total={Math.max(1, uploadTotal)} />
            <PoolBar label={`下行占比 ${downloadShare.toFixed(0)}%`} current={data.traffic.download_total_bytes} total={Math.max(1, uploadTotal)} />
          </div>

          <div className="grid gap-4 md:grid-cols-3">
            <StatColumn title="上行总量" value={formatBytes(data.traffic.upload_total_bytes)} caption="累计上行流量" />
            <StatColumn title="下行总量" value={formatBytes(data.traffic.download_total_bytes)} caption="累计下行流量" />
            <StatColumn title="采样窗口" value={`${data.traffic.sample_window_ms} ms`} caption="最近一次采样窗口" />
          </div>
        </CardContent>
      </Card>

      <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
        <CardHeader>
          <CardTitle>链路负载判断</CardTitle>
          <CardDescription>结合会话与流量速率，快速判断当前链路是否处于高负载阶段。</CardDescription>
        </CardHeader>
        <CardContent className="grid gap-4">
          <OverviewKeyValue label="会话状态" value={formatStatusText(data.session.state)} />
          <OverviewKeyValue label="活跃隧道" value={formatCount(data.agent.tunnel_pool.active)} />
          <OverviewKeyValue label="空闲隧道" value={formatCount(data.agent.tunnel_pool.idle)} />
          <OverviewKeyValue label="异常隧道" value={formatCount(data.agent.tunnel_pool.broken)} />
          <OverviewKeyValue label="最近更新" value={formatDateTime(data.traffic.updated_at_ms)} />
        </CardContent>
      </Card>
    </section>
  );
}

export function DiagnosePage({ data, logs }: { data: ConsoleData; logs: DiagnoseLogItem[] }) {
  return (
    <section className="grid gap-4 xl:grid-cols-[0.95fr_1.05fr]">
      <div className="grid gap-4">
        <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
          <CardHeader>
            <CardTitle>诊断摘要</CardTitle>
            <CardDescription>聚合运行事件，帮助判断是否处于重连或错误状态。</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4 md:grid-cols-2">
            <ExecutiveMetric label="事件总数" value={formatCount(data.diagnose.event_total)} emphasis="muted" />
            <ExecutiveMetric label="错误总数" value={formatCount(data.diagnose.event_error_count)} emphasis="danger" />
            <ExecutiveMetric label="重连次数" value={formatCount(data.diagnose.event_reconnects)} emphasis="primary" />
            <ExecutiveMetric label="补池事件" value={formatCount(data.diagnose.event_refill_total)} emphasis="muted" />
          </CardContent>
        </Card>

        <Card className="panel-soft border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
          <CardHeader>
            <CardTitle>运行上下文</CardTitle>
            <CardDescription>展示当前桥接状态与最近事件摘要。</CardDescription>
          </CardHeader>
          <CardContent className="grid gap-4">
            <OverviewKeyValue label="当前状态" value={formatStatusText(data.diagnose.state)} />
            <OverviewKeyValue label="最近事件" value={data.diagnose.last_event_code ?? "无"} />
            <OverviewKeyValue label="最近消息" value={data.diagnose.last_event_message ?? "无"} />
            <OverviewKeyValue label="下次重试" value={formatRelativeTime(data.diagnose.next_retry_at_ms)} />
            <OverviewKeyValue label="最近错误" value={data.diagnose.last_error ?? "无"} />
          </CardContent>
        </Card>
      </div>

      <Card className="panel-soft overflow-hidden border-0 bg-[rgba(255,255,255,0.9)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
        <CardHeader className="flex-col items-start justify-between gap-3 sm:flex-row sm:gap-4">
          <div>
            <CardTitle>最近运行日志</CardTitle>
            <CardDescription>默认展示最近诊断事件，支持顶部搜索快速过滤。</CardDescription>
          </div>
          <Badge variant="outline">{logs.length} 条</Badge>
        </CardHeader>
        <CardContent className="space-y-3">
          {logs.map((item) => (
            <div key={`${item.ts_ms}-${item.code}-${item.message}`} className="rounded-2xl bg-[hsl(var(--secondary)/0.7)] p-4">
              <div className="flex flex-wrap items-start gap-2.5 sm:items-center sm:gap-3">
                <Badge variant={levelBadgeVariant(item.level)}>{formatLevelText(item.level)}</Badge>
                <span className="break-all text-xs uppercase tracking-[0.16em] text-[hsl(var(--muted-foreground))]">{item.module}</span>
                <span className="text-xs text-[hsl(var(--muted-foreground))]">{formatDateTime(item.ts_ms)}</span>
              </div>
              <div className="mt-3 break-all text-sm font-semibold">{item.code}</div>
              <p className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">{item.message}</p>
              {item.bridge_state || item.request_id ? (
                <div className="mt-3 flex flex-col gap-2 text-xs text-[hsl(var(--muted-foreground))] sm:flex-row sm:flex-wrap sm:gap-3">
                  {item.bridge_state ? <span>状态: {formatStatusText(item.bridge_state)}</span> : null}
                  {item.request_id ? <span className="break-all">请求: {item.request_id}</span> : null}
                </div>
              ) : null}
            </div>
          ))}

          {logs.length === 0 ? (
            <EmptyStatePanel
              eyebrow="运行日志"
              icon={<NavGlyph page="diagnose" />}
              note="日志会随着重连、建链、错误和恢复事件持续追加。"
              title="当前没有诊断日志"
              description="新的运行事件和错误日志会在这里持续追加，顶部搜索也会同步生效。"
            />
          ) : null}
        </CardContent>
      </Card>
    </section>
  );
}

export function SettingsPage({
  config,
  session,
  draft,
  dirty,
  fieldErrors,
  saving,
  onChange,
  onReset,
  onSave,
}: {
  config: ConfigSnapshot;
  session: SessionSnapshot;
  draft: SettingsDraft | null;
  dirty: boolean;
  fieldErrors: SettingsFieldErrors;
  saving: boolean;
  onChange: <K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) => void;
  onReset: () => void;
  onSave: () => void;
}) {
  const groups = [
    {
      title: "当前运行态",
      highlights: [
        { label: "Agent 标识", value: config.agent_id, emphasis: "primary" as const },
        { label: "会话状态", value: formatStatusText(session.state), emphasis: "muted" as const },
        { label: "桥接协议", value: config.bridge_transport, emphasis: "muted" as const },
      ],
      details: [{ label: "桥接地址", value: config.bridge_addr }],
    },
    {
      title: "当前隧道池参数",
      highlights: [
        { label: "最小空闲", value: String(config.tunnel_pool_min_idle), emphasis: "primary" as const },
        { label: "最大空闲", value: String(config.tunnel_pool_max_idle), emphasis: "muted" as const },
        { label: "最大并发预开", value: String(config.tunnel_pool_max_inflight), emphasis: "muted" as const },
      ],
      details: [
        { label: "存活时长", value: `${config.tunnel_pool_ttl_ms} ms` },
        { label: "开启速率", value: String(config.tunnel_pool_open_rate) },
        { label: "开启突发", value: String(config.tunnel_pool_open_burst) },
      ],
    },
    {
      title: "配置来源",
      highlights: [
        { label: "配置来源", value: config.config_file_source || "default", emphasis: "primary" as const },
        { label: "IPC 传输", value: config.ipc_transport || "未配置", emphasis: "muted" as const },
        { label: "IPC 端点", value: config.ipc_endpoint || "未配置", emphasis: "muted" as const },
      ],
      details: [
        { label: "可编辑文件", value: config.config_file_path || "未找到可写配置文件", tone: "path" as const },
        { label: "基础配置", value: config.base_config_file_path || "未命中", tone: "path" as const },
      ],
    },
  ];

  if (!draft) {
    return (
      <section className="grid gap-4">
        <Card className="panel-soft">
          <CardHeader>
            <CardTitle>运行配置</CardTitle>
            <CardDescription>正在准备配置表单。</CardDescription>
          </CardHeader>
        </Card>
      </section>
    );
  }

  return (
    <section className="grid gap-4">
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {groups.map((group) => (
          <Card
            key={group.title}
            className="panel-soft border border-[rgba(255,255,255,0.72)] bg-[linear-gradient(180deg,rgba(255,255,255,0.96),rgba(249,250,251,0.92))] shadow-[0_10px_28px_rgba(25,28,29,0.05)]"
          >
            <CardHeader>
              <CardTitle>{group.title}</CardTitle>
              <CardDescription>上方卡片展示当前运行中的实际值，不会因保存配置立即热更新。</CardDescription>
            </CardHeader>
            <CardContent className="grid gap-5">
              <div className="grid gap-3 sm:grid-cols-2 xl:grid-cols-1 2xl:grid-cols-2">
                {group.highlights.map((item) => (
                  <SettingsSummaryMetric key={item.label} label={item.label} value={item.value} emphasis={item.emphasis} />
                ))}
              </div>
              <div className="grid gap-3">
                {group.details.map((item) => (
                  <SettingsDetailRow key={item.label} label={item.label} value={item.value} tone={item.tone} />
                ))}
              </div>
            </CardContent>
          </Card>
        ))}
      </div>

      <Card className="panel-soft border border-[rgba(255,255,255,0.78)] bg-[linear-gradient(180deg,rgba(255,255,255,0.97),rgba(248,249,251,0.94))] shadow-[0_12px_30px_rgba(25,28,29,0.05)]">
        <CardHeader className="flex-col items-start justify-between gap-3 sm:flex-row sm:gap-4">
          <div>
            <CardTitle>编辑共享运行字段</CardTitle>
            <CardDescription>当前主要开放桥接共享字段与 Bridge 签发认证凭证。保存后会写回 Agent YAML，重启 Agent 后生效。</CardDescription>
            <div className="mt-4 flex flex-wrap gap-2">
              <Badge variant="outline" className="bg-[rgba(255,255,255,0.72)]">
                仅保存共享字段
              </Badge>
              <Badge variant="outline" className="bg-[rgba(255,255,255,0.72)]">
                手工录入认证 Token
              </Badge>
              <Badge variant="outline" className="bg-[rgba(255,255,255,0.72)]">
                不会立即热更新
              </Badge>
            </div>
          </div>
          <Badge variant={dirty ? "warning" : "outline"}>{dirty ? "待保存" : "已同步"}</Badge>
        </CardHeader>
        <CardContent className="space-y-7">
          <form
            className="space-y-7"
            onSubmit={(event) => {
              event.preventDefault();
              void onSave();
            }}
          >
            <div className="executive-section border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.94),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
              <div className="mb-5">
                <div className="label-kicker">连接标识</div>
                <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                  这部分决定 Agent 如何标识自身、如何选择桥接协议，以及如何指向桥接地址。
                </div>
              </div>
              <div className="grid gap-4 lg:grid-cols-3">
                <Field label="agent_id" caption={settingsFieldCaptionText.agent_id} helpText={settingsFieldHelpText.agent_id}>
                  <Input
                    value={draft.agentId}
                    onChange={(event) => onChange("agentId", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.agent_id}
                    className={executiveFieldClassName(Boolean(fieldErrors.agentId))}
                  />
                  {fieldErrors.agentId ? <FieldErrorText message={fieldErrors.agentId} /> : null}
                </Field>
                <Field label="bridge_transport" caption={settingsFieldCaptionText.bridge_transport} helpText={settingsFieldHelpText.bridge_transport}>
                  <Select
                    value={draft.transport}
                    onValueChange={(value) => onChange("transport", value)}
                    className={executiveFieldClassName(Boolean(fieldErrors.transport))}
                  >
                    {bridgeTransportOptions.map((option) => (
                      <option key={option.value} value={option.value}>
                        {option.label}
                      </option>
                    ))}
                  </Select>
                  {fieldErrors.transport ? <FieldErrorText message={fieldErrors.transport} /> : null}
                </Field>
                <Field label="bridge_addr" caption={settingsFieldCaptionText.bridge_addr} helpText={settingsFieldHelpText.bridge_addr}>
                  <Input
                    value={draft.bridgeAddr}
                    onChange={(event) => onChange("bridgeAddr", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.bridge_addr}
                    className={executiveFieldClassName(Boolean(fieldErrors.bridgeAddr))}
                  />
                  {fieldErrors.bridgeAddr ? <FieldErrorText message={fieldErrors.bridgeAddr} /> : null}
                </Field>
              </div>
            </div>

            <Separator className="executive-muted-divider" />

            <div className="executive-section border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.94),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
              <div className="mb-5">
                <div className="label-kicker">Bridge 认证</div>
                <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                  这里填写由 Bridge 后台生成并分发给 Agent 的 token。输入框默认空白，Agent 侧不会回显当前 token；留空保存表示保持当前 token 不变。
                </div>
              </div>
              <div className="grid gap-4">
                <Field
                  label="session.auth_token"
                  caption={settingsFieldCaptionText.session_auth_token}
                  helpText={settingsFieldHelpText.session_auth_token}
                >
                  <Input
                    value={draft.sessionAuthToken}
                    onChange={(event) => onChange("sessionAuthToken", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.session_auth_token}
                    className={executiveFieldClassName(Boolean(fieldErrors.sessionAuthToken))}
                  />
                  {fieldErrors.sessionAuthToken ? <FieldErrorText message={fieldErrors.sessionAuthToken} /> : null}
                </Field>
              </div>
            </div>

            <Separator className="executive-muted-divider" />

            <div className="executive-section border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.94),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
              <div className="mb-5">
                <div className="label-kicker">桥接 TLS</div>
                <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                  证书相关字段只影响下次启动时的桥接链路。`quic_native` 模式下这里必须完整配置。
                </div>
              </div>
              <div className="grid gap-4 lg:grid-cols-3">
                <Field
                  label="bridge_tls_enabled"
                  caption={settingsFieldCaptionText.bridge_tls_enabled}
                  helpText={settingsFieldHelpText.bridge_tls_enabled}
                >
                  <Select
                    value={draft.bridgeTLSEnabled ? "true" : "false"}
                    onValueChange={(value) => onChange("bridgeTLSEnabled", value === "true")}
                    className={executiveFieldClassName(Boolean(fieldErrors.bridgeTLSEnabled))}
                  >
                    <option value="false">关闭</option>
                    <option value="true">启用</option>
                  </Select>
                  {fieldErrors.bridgeTLSEnabled ? <FieldErrorText message={fieldErrors.bridgeTLSEnabled} /> : null}
                </Field>
                <Field
                  label="bridge_tls_root_ca_file"
                  caption={settingsFieldCaptionText.bridge_tls_root_ca_file}
                  helpText={settingsFieldHelpText.bridge_tls_root_ca_file}
                >
                  <Input
                    value={draft.bridgeTLSRootCAFile}
                    onChange={(event) => onChange("bridgeTLSRootCAFile", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.bridge_tls_root_ca_file}
                    className={executiveFieldClassName(Boolean(fieldErrors.bridgeTLSRootCAFile))}
                  />
                  {fieldErrors.bridgeTLSRootCAFile ? <FieldErrorText message={fieldErrors.bridgeTLSRootCAFile} /> : null}
                </Field>
                <Field
                  label="bridge_tls_server_name"
                  caption={settingsFieldCaptionText.bridge_tls_server_name}
                  helpText={settingsFieldHelpText.bridge_tls_server_name}
                >
                  <Input
                    value={draft.bridgeTLSServerName}
                    onChange={(event) => onChange("bridgeTLSServerName", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.bridge_tls_server_name}
                    className={executiveFieldClassName(Boolean(fieldErrors.bridgeTLSServerName))}
                  />
                  {fieldErrors.bridgeTLSServerName ? <FieldErrorText message={fieldErrors.bridgeTLSServerName} /> : null}
                </Field>
              </div>
            </div>

            <Separator className="executive-muted-divider" />

            <div className="executive-section border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.94),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
              <div className="mb-5">
                <div className="label-kicker">隧道池</div>
                <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                  这里控制预热、复用和回收节奏，建议以当前运行态卡片为基线逐步微调。
                </div>
              </div>
              <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
                <Field
                  label="tunnel_pool_min_idle（条）"
                  caption={settingsFieldCaptionText.tunnel_pool_min_idle}
                  helpText={settingsFieldHelpText.tunnel_pool_min_idle}
                >
                  <Input
                    value={draft.tunnelPoolMinIdleText}
                    onChange={(event) => onChange("tunnelPoolMinIdleText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_min_idle}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolMinIdleText))}
                  />
                  {fieldErrors.tunnelPoolMinIdleText ? <FieldErrorText message={fieldErrors.tunnelPoolMinIdleText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_max_idle（条）"
                  caption={settingsFieldCaptionText.tunnel_pool_max_idle}
                  helpText={settingsFieldHelpText.tunnel_pool_max_idle}
                >
                  <Input
                    value={draft.tunnelPoolMaxIdleText}
                    onChange={(event) => onChange("tunnelPoolMaxIdleText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_max_idle}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolMaxIdleText))}
                  />
                  {fieldErrors.tunnelPoolMaxIdleText ? <FieldErrorText message={fieldErrors.tunnelPoolMaxIdleText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_max_inflight（条）"
                  caption={settingsFieldCaptionText.tunnel_pool_max_inflight}
                  helpText={settingsFieldHelpText.tunnel_pool_max_inflight}
                >
                  <Input
                    value={draft.tunnelPoolMaxInflightText}
                    onChange={(event) => onChange("tunnelPoolMaxInflightText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_max_inflight}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolMaxInflightText))}
                  />
                  {fieldErrors.tunnelPoolMaxInflightText ? <FieldErrorText message={fieldErrors.tunnelPoolMaxInflightText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_ttl_s（秒）"
                  caption={settingsFieldCaptionText.tunnel_pool_ttl_s}
                  helpText={settingsFieldHelpText.tunnel_pool_ttl_s}
                >
                  <Input
                    value={draft.tunnelPoolTtlSecText}
                    onChange={(event) => onChange("tunnelPoolTtlSecText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_ttl_s}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolTtlSecText))}
                  />
                  {fieldErrors.tunnelPoolTtlSecText ? <FieldErrorText message={fieldErrors.tunnelPoolTtlSecText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_open_rate（每秒）"
                  caption={settingsFieldCaptionText.tunnel_pool_open_rate}
                  helpText={settingsFieldHelpText.tunnel_pool_open_rate}
                >
                  <Input
                    value={draft.tunnelPoolOpenRateText}
                    onChange={(event) => onChange("tunnelPoolOpenRateText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_open_rate}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolOpenRateText))}
                  />
                  {fieldErrors.tunnelPoolOpenRateText ? <FieldErrorText message={fieldErrors.tunnelPoolOpenRateText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_open_burst（条）"
                  caption={settingsFieldCaptionText.tunnel_pool_open_burst}
                  helpText={settingsFieldHelpText.tunnel_pool_open_burst}
                >
                  <Input
                    value={draft.tunnelPoolOpenBurstText}
                    onChange={(event) => onChange("tunnelPoolOpenBurstText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_open_burst}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolOpenBurstText))}
                  />
                  {fieldErrors.tunnelPoolOpenBurstText ? <FieldErrorText message={fieldErrors.tunnelPoolOpenBurstText} /> : null}
                </Field>
                <Field
                  label="tunnel_pool_reconcile_gap_ms（毫秒）"
                  caption={settingsFieldCaptionText.tunnel_pool_reconcile_gap_ms}
                  helpText={settingsFieldHelpText.tunnel_pool_reconcile_gap_ms}
                >
                  <Input
                    value={draft.tunnelPoolReconcileGapMsText}
                    onChange={(event) => onChange("tunnelPoolReconcileGapMsText", event.target.value)}
                    placeholder={settingsFieldPlaceholderText.tunnel_pool_reconcile_gap_ms}
                    className={executiveFieldClassName(Boolean(fieldErrors.tunnelPoolReconcileGapMsText))}
                  />
                  {fieldErrors.tunnelPoolReconcileGapMsText ? <FieldErrorText message={fieldErrors.tunnelPoolReconcileGapMsText} /> : null}
                </Field>
              </div>
            </div>

            <div className="grid gap-4 xl:grid-cols-[1.05fr_0.95fr]">
              <div className="rounded-[28px] border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.95),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
                <div className="label-kicker">保存策略</div>
                <div className="mt-4 grid gap-3">
                  <OverviewKeyValue label="写入范围" value="只覆盖与 Tauri 共用的 Agent 运行字段" />
                  <OverviewKeyValue label="立即生效" value="不会立即热更新，需重启 Agent" />
                  <OverviewKeyValue label="保存目标" value={config.config_file_path || config.base_config_file_path || "自动选择可写配置文件"} />
                </div>
              </div>

              <div className="rounded-[28px] border border-[rgba(220,223,228,0.48)] bg-[linear-gradient(180deg,rgba(255,255,255,0.95),rgba(248,249,251,0.88))] p-5 shadow-[inset_0_1px_0_rgba(255,255,255,0.76)] sm:p-6">
                <div className="label-kicker">重启引导</div>
                <div className="mt-4 space-y-3 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                  <p>写入完成后，下一次启动 Agent 才会加载新的桥接参数、TLS 设置和隧道池策略。</p>
                  <div className="rounded-2xl bg-[rgba(15,23,42,0.05)] px-4 py-3 break-all text-[13px] leading-6 text-[hsl(var(--foreground))]">
                    Web 模式：`agent-core -config /path/to/agent.yaml`
                    <br />
                    Tauri 模式：`agent-core -tauri -config /path/to/agent.yaml`
                  </div>
                </div>
              </div>
            </div>

            <div className="flex flex-col gap-3 sm:flex-row sm:justify-end">
              <Button variant="outline" onClick={onReset} disabled={!dirty || saving}>
                重置
              </Button>
              <Button type="submit" disabled={!dirty || saving}>
                {saving ? "正在保存..." : "保存运行配置"}
              </Button>
            </div>
          </form>
        </CardContent>
      </Card>
    </section>
  );
}
