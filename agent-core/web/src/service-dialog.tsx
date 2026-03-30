import type { FormEvent } from "react";

import {
  AlertDialog,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogHeader,
  AlertDialogTitle,
} from "@/components/ui/alert-dialog";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Select } from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { Textarea } from "@/components/ui/textarea";
import {
  executiveFieldClassName,
  formatDateTime,
  formatRelativeTime,
  formatStatusText,
  type ServiceItem,
  statusBadgeVariant,
} from "@/console-shared";
import { Field, FieldErrorText, OverviewKeyValue } from "@/console-kit";
import { cn } from "@/lib/utils";
import type { ServiceFormErrors, ServiceFormState } from "@/service-form";

export type ServiceDialogMode = "create" | "edit" | "detail";

export function ServiceDialog({
  actionPending,
  fieldErrors,
  form,
  mode,
  open,
  service,
  onClose,
  onSubmit,
  onUpdateForm,
}: {
  actionPending: string;
  fieldErrors: ServiceFormErrors;
  form: ServiceFormState;
  mode: ServiceDialogMode;
  open: boolean;
  service: ServiceItem | null;
  onClose: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUpdateForm: <K extends keyof ServiceFormState>(key: K, value: ServiceFormState[K]) => void;
}) {
  const modeTitle = mode === "create" ? "注册服务" : mode === "edit" ? "编辑服务" : "服务详情";
  const modeDescription =
    mode === "create"
      ? "按照当前 Agent HTTP 接口的真实 payload 结构提交服务注册。"
      : mode === "edit"
        ? "编辑现有服务实例，保存后会覆盖当前目录中的同一实例。"
        : "查看当前服务实例的登记信息、暴露配置和健康检查摘要。";

  return (
    <AlertDialog open={open} onOpenChange={(nextOpen) => (!nextOpen ? onClose() : null)}>
      <AlertDialogContent className="w-[min(96vw,70rem)] max-w-none overflow-hidden p-0">
        <div className="flex max-h-[88vh] flex-col">
          <AlertDialogHeader className="border-b border-[rgba(214,218,224,0.45)] px-6 py-5 sm:px-7">
            <div className="flex items-start justify-between gap-4">
              <div className="space-y-2">
                <AlertDialogTitle>{modeTitle}</AlertDialogTitle>
                <AlertDialogDescription>{modeDescription}</AlertDialogDescription>
              </div>
              <div className="flex items-center gap-2">
                {mode !== "create" && service ? (
                  <Badge variant={statusBadgeVariant(service.status)}>{formatStatusText(service.status)}</Badge>
                ) : null}
                <Button type="button" variant="outline" size="sm" onClick={onClose}>
                  关闭
                </Button>
              </div>
            </div>
          </AlertDialogHeader>

          <div className="overflow-y-auto px-6 py-6 sm:px-7">
            {mode === "detail" ? <ServiceDetailContent service={service} /> : null}
            {mode === "create" || mode === "edit" ? (
              <ServiceFormContent
                actionPending={actionPending}
                fieldErrors={fieldErrors}
                form={form}
                mode={mode}
                onClose={onClose}
                onSubmit={onSubmit}
                onUpdateForm={onUpdateForm}
              />
            ) : null}
          </div>
        </div>
      </AlertDialogContent>
    </AlertDialog>
  );
}

function ServiceDetailContent({ service }: { service: ServiceItem | null }) {
  if (!service) {
    return (
      <div className="rounded-[24px] border border-dashed border-[hsl(var(--border)/0.5)] bg-[rgba(255,255,255,0.66)] px-5 py-6 text-sm text-[hsl(var(--muted-foreground))]">
        未找到目标服务，可能已经被删除或当前筛选条件已变化。
      </div>
    );
  }

  return (
    <div className="grid gap-5">
      <div className="grid gap-4 lg:grid-cols-[1.05fr_0.95fr]">
        <section className="executive-section p-4 sm:p-5">
          <div className="mb-5 flex flex-wrap items-start justify-between gap-3">
            <div>
              <div className="label-kicker">基础身份</div>
              <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
                展示服务实例的作用域、协议和主要入口标识。
              </div>
            </div>
            <Badge variant="outline">{service.instance_id || "未命名实例"}</Badge>
          </div>
          <div className="grid gap-4 md:grid-cols-2">
            <OverviewKeyValue label="服务名" value={service.service_name || "未命名"} />
            <OverviewKeyValue label="作用域" value={`${service.scope.namespace || "default"}/${service.scope.environment || "prod"}`} />
            <OverviewKeyValue label="协议" value={service.protocol || "未记录"} />
            <OverviewKeyValue label="更新于" value={`${formatDateTime(service.updated_at_ms)} · ${formatRelativeTime(service.updated_at_ms)}`} />
          </div>
        </section>

        <section className="executive-section p-4 sm:p-5">
          <div className="mb-5">
            <div className="label-kicker">暴露信息</div>
            <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
              说明外部流量如何命中当前实例，以及对外暴露限制。
            </div>
          </div>
          <div className="grid gap-3">
            <OverviewKeyValue label="入口模式" value={service.exposure?.ingress_mode || "未记录"} />
            <OverviewKeyValue label="暴露 Host" value={service.exposure?.host || "未配置"} />
            <OverviewKeyValue label="路径前缀" value={service.exposure?.path_prefix || "未配置"} />
            <OverviewKeyValue label="SNI 名称" value={service.exposure?.sni_name || service.sni_name || "未配置"} />
            <OverviewKeyValue
              label="监听端口"
              value={typeof service.exposure?.listen_port === "number" && service.exposure.listen_port > 0 ? `${service.exposure.listen_port}` : "未配置"}
            />
            <OverviewKeyValue label="允许外部暴露" value={service.exposure?.allow_export ? "是" : "否"} />
          </div>
        </section>
      </div>

      <section className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">健康检查</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            当前实例的探活模式、路径和最近健康状态。
          </div>
        </div>
        <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
          <OverviewKeyValue label="检查模式" value={service.health_check_mode || "未配置"} />
          <OverviewKeyValue
            label="检查间隔"
            value={typeof service.health_check_interval_sec === "number" ? `${service.health_check_interval_sec} 秒` : "未配置"}
          />
          <OverviewKeyValue label="健康路径" value={service.health_check_path || "未配置"} />
          <OverviewKeyValue label="健康状态" value={service.health_status ? formatStatusText(service.health_status) : "未记录"} />
        </div>
      </section>

      <section className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">实例端点</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            当前目录里记录的实例地址和端点元信息。
          </div>
        </div>
        <div className="grid gap-3">
          {service.endpoints.length > 0 ? (
            service.endpoints.map((endpoint) => (
              <div
                key={endpoint.endpoint_id}
                className="rounded-[22px] border border-[rgba(214,218,224,0.38)] bg-[linear-gradient(180deg,rgba(255,255,255,0.9),rgba(247,249,251,0.88))] px-4 py-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.72)]"
              >
                <div className="flex flex-wrap items-start justify-between gap-3">
                  <div>
                    <div className="font-semibold">
                      {endpoint.host}:{endpoint.port}
                    </div>
                    <div className="mt-1 text-xs text-[hsl(var(--muted-foreground))]">{endpoint.endpoint_id}</div>
                  </div>
                  <Badge variant="outline">{endpoint.protocol}</Badge>
                </div>
                {endpoint.sni_name ? <div className="mt-3 text-sm text-[hsl(var(--muted-foreground))]">SNI: {endpoint.sni_name}</div> : null}
              </div>
            ))
          ) : (
            <div className="rounded-[22px] border border-dashed border-[hsl(var(--border)/0.45)] bg-[rgba(255,255,255,0.6)] px-4 py-5 text-sm text-[hsl(var(--muted-foreground))]">
              该服务当前没有登记任何 endpoint。
            </div>
          )}
        </div>
      </section>
    </div>
  );
}

function ServiceFormContent({
  actionPending,
  fieldErrors,
  form,
  mode,
  onClose,
  onSubmit,
  onUpdateForm,
}: {
  actionPending: string;
  fieldErrors: ServiceFormErrors;
  form: ServiceFormState;
  mode: Exclude<ServiceDialogMode, "detail">;
  onClose: () => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUpdateForm: <K extends keyof ServiceFormState>(key: K, value: ServiceFormState[K]) => void;
}) {
  const showsPathPrefix = form.ingressMode === "l7_shared";
  const showsSNIName = form.ingressMode === "tls_sni_shared";
  const showsListenPort = form.ingressMode === "l4_dedicated_port";
  const showsHealthPath = form.healthCheckMode === "http" || form.healthCheckMode === "https";
  const ingressModeHint =
    form.ingressMode === "l7_shared"
      ? "当前模式会按路径前缀分发流量，请填写 / 开头的路由前缀。"
      : form.ingressMode === "tls_sni_shared"
        ? "当前模式会按 SNI 名称分流流量，请填写证书访问时使用的主机名。"
        : "当前模式会占用独立端口对外提供服务，请填写监听端口。";

  return (
    <form className="grid gap-5" onSubmit={onSubmit}>
      <div className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">基础信息</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            先定义服务作用域、服务名称和目标地址，这是服务注册的基础身份信息。
          </div>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="命名空间">
            <Input
              value={form.namespace}
              onChange={(event) => onUpdateForm("namespace", event.target.value)}
              className={executiveFieldClassName(Boolean(fieldErrors.namespace))}
            />
            {fieldErrors.namespace ? <FieldErrorText message={fieldErrors.namespace} /> : null}
          </Field>
          <Field label="环境">
            <Input
              value={form.environment}
              onChange={(event) => onUpdateForm("environment", event.target.value)}
              className={executiveFieldClassName(Boolean(fieldErrors.environment))}
            />
            {fieldErrors.environment ? <FieldErrorText message={fieldErrors.environment} /> : null}
          </Field>
        </div>

        <div className="mt-4 grid gap-4 md:grid-cols-[1.1fr_0.9fr]">
          <Field label="服务名">
            <Input
              value={form.serviceName}
              onChange={(event) => onUpdateForm("serviceName", event.target.value)}
              placeholder="order-service"
              className={executiveFieldClassName(Boolean(fieldErrors.serviceName))}
            />
            {fieldErrors.serviceName ? <FieldErrorText message={fieldErrors.serviceName} /> : null}
          </Field>
          <Field label="实例 ID" caption={mode === "edit" ? "编辑模式下实例 ID 固定，用于稳定更新当前目录项。" : "留空时由目录自动生成。"}>
            <Input
              value={form.instanceID}
              onChange={(event) => onUpdateForm("instanceID", event.target.value)}
              placeholder="留空则由目录生成"
              disabled={mode === "edit"}
              className={cn(executiveFieldClassName(Boolean(fieldErrors.instanceID)), mode === "edit" && "cursor-not-allowed opacity-80")}
            />
            {fieldErrors.instanceID ? <FieldErrorText message={fieldErrors.instanceID} /> : null}
          </Field>
        </div>

        <div className="mt-4 grid gap-4 lg:grid-cols-[0.9fr_1.1fr_0.8fr]">
          <Field label="协议">
            <Select
              value={form.protocol}
              onValueChange={(value) => onUpdateForm("protocol", value)}
              className={executiveFieldClassName(Boolean(fieldErrors.protocol))}
            >
              <option value="http">http</option>
              <option value="https">https</option>
              <option value="tcp">tcp</option>
              <option value="grpc">grpc</option>
            </Select>
            {fieldErrors.protocol ? <FieldErrorText message={fieldErrors.protocol} /> : null}
          </Field>
          <Field label="主机">
            <Input
              value={form.host}
              onChange={(event) => onUpdateForm("host", event.target.value)}
              className={executiveFieldClassName(Boolean(fieldErrors.host))}
            />
            {fieldErrors.host ? <FieldErrorText message={fieldErrors.host} /> : null}
          </Field>
          <Field label="端口">
            <Input
              type="number"
              value={form.port}
              onChange={(event) => onUpdateForm("port", event.target.value)}
              className={executiveFieldClassName(Boolean(fieldErrors.port))}
            />
            {fieldErrors.port ? <FieldErrorText message={fieldErrors.port} /> : null}
          </Field>
        </div>
      </div>

      <div className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">暴露配置</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            配置实例标识、入口模式和对外暴露方式，决定流量如何路由到目标服务。
          </div>
        </div>
        <div className="grid gap-4 md:grid-cols-2">
          <Field label="入口模式">
            <Select
              value={form.ingressMode}
              onValueChange={(value) => onUpdateForm("ingressMode", value)}
              className={executiveFieldClassName(Boolean(fieldErrors.ingressMode))}
            >
              <option value="l7_shared">l7_shared</option>
              <option value="tls_sni_shared">tls_sni_shared</option>
              <option value="l4_dedicated_port">l4_dedicated_port</option>
            </Select>
            {fieldErrors.ingressMode ? <FieldErrorText message={fieldErrors.ingressMode} /> : null}
          </Field>
          <Field label="暴露 Host">
            <Input
              value={form.ingressHost}
              onChange={(event) => onUpdateForm("ingressHost", event.target.value)}
              placeholder="可选"
              className={executiveFieldClassName(Boolean(fieldErrors.ingressHost))}
            />
            {fieldErrors.ingressHost ? <FieldErrorText message={fieldErrors.ingressHost} /> : null}
          </Field>
        </div>

        <div className="mt-4 rounded-2xl border border-[rgba(0,91,191,0.08)] bg-[linear-gradient(135deg,rgba(240,247,255,0.95),rgba(248,250,252,0.92))] px-4 py-3 text-sm leading-6 text-[rgb(58,78,109)]">
          {ingressModeHint}
        </div>

        <div className="mt-4 grid gap-4 md:grid-cols-2">
          {showsPathPrefix ? (
            <Field label="路径前缀">
              <Input
                value={form.pathPrefix}
                onChange={(event) => onUpdateForm("pathPrefix", event.target.value)}
                placeholder="/api/orders"
                className={executiveFieldClassName(Boolean(fieldErrors.pathPrefix))}
              />
              {fieldErrors.pathPrefix ? <FieldErrorText message={fieldErrors.pathPrefix} /> : null}
            </Field>
          ) : (
            <div className="rounded-2xl border border-dashed border-[hsl(var(--border)/0.45)] bg-[rgba(255,255,255,0.55)] px-4 py-4 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
              当前入口模式不需要路径前缀，系统会忽略这一项。
            </div>
          )}
        </div>

        {showsSNIName || showsListenPort ? (
          <div className="mt-4 grid gap-4 md:grid-cols-2">
            {showsSNIName ? (
              <Field label="SNI 名称">
                <Input
                  value={form.sniName}
                  onChange={(event) => onUpdateForm("sniName", event.target.value)}
                  placeholder="orders.internal.example"
                  className={executiveFieldClassName(Boolean(fieldErrors.sniName))}
                />
                {fieldErrors.sniName ? <FieldErrorText message={fieldErrors.sniName} /> : null}
              </Field>
            ) : null}
            {showsListenPort ? (
              <Field label="监听端口">
                <Input
                  type="number"
                  value={form.listenPort}
                  onChange={(event) => onUpdateForm("listenPort", event.target.value)}
                  placeholder="例如 9443"
                  className={executiveFieldClassName(Boolean(fieldErrors.listenPort))}
                />
                {fieldErrors.listenPort ? <FieldErrorText message={fieldErrors.listenPort} /> : null}
              </Field>
            ) : null}
          </div>
        ) : null}

        <label className="mt-4 flex items-center gap-3 rounded-2xl bg-[hsl(var(--secondary)/0.68)] px-4 py-3 text-sm">
          <Checkbox checked={form.allowExport} onCheckedChange={(checked) => onUpdateForm("allowExport", checked === true)} />
          <span>允许对外暴露（`allow_export`）</span>
        </label>
      </div>

      <div className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">健康检查</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            用于决定目录如何判定实例是否可用，建议保持与服务真实探活行为一致。
          </div>
        </div>
        <div className="grid gap-4 lg:grid-cols-3">
          <Field label="检查模式">
            <Select
              value={form.healthCheckMode}
              onValueChange={(value) => onUpdateForm("healthCheckMode", value)}
              className={executiveFieldClassName(Boolean(fieldErrors.healthCheckMode))}
            >
              <option value="http">http</option>
              <option value="https">https</option>
              <option value="tcp">tcp</option>
            </Select>
            {fieldErrors.healthCheckMode ? <FieldErrorText message={fieldErrors.healthCheckMode} /> : null}
          </Field>
          {showsHealthPath ? (
            <Field label="健康路径">
              <Input
                value={form.healthCheckPath}
                onChange={(event) => onUpdateForm("healthCheckPath", event.target.value)}
                className={executiveFieldClassName(Boolean(fieldErrors.healthCheckPath))}
              />
              {fieldErrors.healthCheckPath ? <FieldErrorText message={fieldErrors.healthCheckPath} /> : null}
            </Field>
          ) : (
            <div className="rounded-2xl border border-dashed border-[hsl(var(--border)/0.45)] bg-[rgba(255,255,255,0.55)] px-4 py-4 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
              TCP 探活不需要额外路径，系统会直接检查目标端口是否可连通。
            </div>
          )}
          <Field label="检查间隔">
            <Input
              type="number"
              value={form.healthInterval}
              onChange={(event) => onUpdateForm("healthInterval", event.target.value)}
              className={executiveFieldClassName(Boolean(fieldErrors.healthInterval))}
            />
            {fieldErrors.healthInterval ? <FieldErrorText message={fieldErrors.healthInterval} /> : null}
          </Field>
        </div>
      </div>

      <div className="executive-section p-4 sm:p-5">
        <div className="mb-5">
          <div className="label-kicker">登记备注</div>
          <div className="mt-2 text-sm leading-6 text-[hsl(var(--muted-foreground))]">
            这部分仅用于前端填写辅助，不会提交到接口，适合记录本次操作的目的和上下文。
          </div>
        </div>
        <Field label="备注">
          <Textarea
            value={form.notes}
            onChange={(event) => onUpdateForm("notes", event.target.value)}
            placeholder="记录本次注册目的、预期路由、服务负责人等。此字段仅用于前端填写辅助，不会提交给接口。"
            className={executiveFieldClassName(Boolean(fieldErrors.notes))}
          />
          {fieldErrors.notes ? <FieldErrorText message={fieldErrors.notes} /> : null}
        </Field>
      </div>

      <Separator />

      <div className="flex flex-col-reverse gap-3 sm:flex-row sm:justify-end">
        <Button type="button" variant="outline" onClick={onClose}>
          取消
        </Button>
        <Button type="submit" className="min-w-[136px]" disabled={actionPending === "save-service"}>
          {actionPending === "save-service" ? (mode === "create" ? "正在注册..." : "正在保存...") : mode === "create" ? "注册服务" : "保存修改"}
        </Button>
      </div>
    </form>
  );
}
