import type { FormEvent, ReactNode } from "react";

import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { Separator } from "@/components/ui/separator";
import {
  ArchitectureGlyph,
  DiagnosePage,
  HeaderIconButton,
  HelpGlyph,
  IconFrame,
  LockGlyph,
  MailGlyph,
  MoonGlyph,
  NavGlyph,
  OverviewPage,
  QuickStatus,
  ServicesPage,
  SettingsPage,
  ShieldGlyph,
  TrafficPage,
  TunnelsPage,
} from "@/console-view";
import {
  executiveInputClassName,
  formatCount,
  formatRelativeTime,
  formatStatusText,
  type ConsoleData,
  navigationItems,
  type PageKey,
  statusBadgeVariant,
  type ServiceItem,
} from "@/console-shared";
import { cn } from "@/lib/utils";
import type { SettingsDraft, SettingsFieldErrors } from "@/settings";

export type ConsoleMetric = {
  label: string;
  value: string;
  help: string;
  tone: "default" | "success" | "warning" | "danger";
};

type LoginScreenProps = {
  actionPending: string;
  authState: "checking" | "anonymous" | "authenticated";
  errorMessage: string;
  loginPassword: string;
  loginUsername: string;
  onPasswordChange: (value: string) => void;
  onSubmit: (event: FormEvent<HTMLFormElement>) => void;
  onUsernameChange: (value: string) => void;
  toastLayer: ReactNode;
};

type ConsoleShellProps = {
  actionPending: string;
  currentNav: (typeof navigationItems)[number];
  data: ConsoleData;
  filteredLogs: ConsoleData["logs"]["items"];
  filteredServices: ConsoleData["services"]["services"];
  filteredTunnels: ConsoleData["tunnels"]["tunnels"];
  filterKeyword: string;
  loading: boolean;
  onDeleteService: (service: ServiceItem) => void;
  onDrainSession: () => void;
  onFilterKeywordChange: (value: string) => void;
  onLogout: () => void;
  onOpenCreateService: () => void;
  onOpenDetailService: (service: ServiceItem) => void;
  onOpenEditService: (service: ServiceItem) => void;
  onReconnectSession: () => void;
  onRefresh: () => void;
  onResetSettings: () => void;
  onSaveSettings: () => Promise<void>;
  onSelectPage: (page: PageKey) => void;
  onUpdateSettingsDraft: <K extends keyof SettingsDraft>(key: K, value: SettingsDraft[K]) => void;
  page: PageKey;
  sessionUsername: string;
  settingsDraft: SettingsDraft | null;
  settingsDirty: boolean;
  settingsFieldErrors: SettingsFieldErrors;
  savingSettings: boolean;
  topMetrics: ConsoleMetric[];
};

export function LoginScreen({
  actionPending,
  authState,
  errorMessage,
  loginPassword,
  loginUsername,
  onPasswordChange,
  onSubmit,
  onUsernameChange,
  toastLayer,
}: LoginScreenProps) {
  return (
    <div className="flex min-h-screen flex-col bg-[linear-gradient(180deg,#f8f9fa_0%,#f1f3f5_100%)]">
      <header className="flex items-center justify-between bg-[hsl(var(--surface-low))] px-6 py-5 shadow-[inset_0_-1px_0_rgba(193,198,214,0.38)] md:px-8">
        <div className="space-y-1">
          <div className="font-display text-xl font-bold tracking-tight">Agent 控制台</div>
          <div className="text-sm text-[hsl(var(--muted-foreground))]">面向本地 Agent 的结构化管理界面</div>
        </div>
        <div className="flex items-center gap-2">
          <HeaderIconButton label="帮助">
            <HelpGlyph />
          </HeaderIconButton>
          <HeaderIconButton label="外观模式">
            <MoonGlyph />
          </HeaderIconButton>
        </div>
      </header>

      <main className="flex flex-1 items-center justify-center px-4 py-8 md:px-6 md:py-10">
        <div className="executive-shell grid w-full max-w-5xl overflow-hidden md:grid-cols-[1.03fr_0.97fr]">
          <section className="relative hidden overflow-hidden bg-[linear-gradient(180deg,#005bbf_0%,#1a73e8_100%)] p-11 text-white md:flex md:flex-col md:justify-between">
            <div className="absolute right-[-72px] top-[-72px] size-72 rounded-full bg-[rgba(255,255,255,0.14)] blur-3xl" />
            <div className="absolute bottom-[-48px] left-[-36px] size-56 rounded-full bg-[rgba(197,85,0,0.16)] blur-3xl" />
            <div className="relative z-10 space-y-9">
              <div className="inline-flex size-16 items-center justify-center rounded-[18px] bg-[rgba(255,255,255,0.12)]">
                <ArchitectureGlyph />
              </div>
              <div className="space-y-4">
                <p className="label-kicker text-white/70">本地运行控制</p>
                <h1 className="max-w-[13ch] font-display text-[3rem] font-extrabold leading-[1.04] tracking-[-0.05em] text-white lg:text-[3.15rem]">
                  统一掌控
                  <br />
                  本地 Agent 运行状态
                </h1>
                <p className="max-w-[26rem] text-base leading-8 text-white/78">
                  在一个界面里查看运行状态、连接情况与诊断信息，让本地运维更直接、更可控。
                </p>
              </div>
            </div>

            <div className="relative z-10">
              <div className="flex items-center gap-4 rounded-[18px] bg-[rgba(255,255,255,0.08)] px-5 py-4">
                <div className="flex size-12 items-center justify-center rounded-[14px] bg-[rgba(255,255,255,0.12)]">
                  <ShieldGlyph />
                </div>
                <div>
                  <div className="font-display text-lg font-bold">会话边界清晰</div>
                  <div className="text-sm text-white/72">浏览器登录态与本地 IPC 握手彼此独立</div>
                </div>
              </div>
            </div>
          </section>

          <section className="executive-surface flex flex-col justify-center p-7 sm:p-8 md:p-14">
            <div className="mb-8 space-y-2.5 text-center md:text-left">
              <Badge className="mx-auto md:mx-0" variant="outline">
                Agent 登录
              </Badge>
              <h2 className="font-display text-[2.2rem] font-bold tracking-[-0.045em] text-[hsl(var(--foreground))]">欢迎回来</h2>
            </div>

            <form className="space-y-5" onSubmit={onSubmit}>
              <div className="space-y-1.5">
                <label className="executive-field-label">用户名</label>
                <div className="relative">
                  <Input
                    autoComplete="username"
                    placeholder="admin"
                    value={loginUsername}
                    onChange={(event) => onUsernameChange(event.target.value)}
                    className={cn(executiveInputClassName, "pr-11")}
                  />
                  <div className="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-4 text-[hsl(var(--muted-foreground))]">
                    <MailGlyph />
                  </div>
                </div>
              </div>

              <div className="space-y-1.5">
                <div className="ml-1 flex items-center justify-between gap-4">
                  <label className="executive-field-label ml-0">密码</label>
                </div>
                <div className="relative">
                  <Input
                    type="password"
                    autoComplete="current-password"
                    placeholder="••••••••"
                    value={loginPassword}
                    onChange={(event) => onPasswordChange(event.target.value)}
                    className={cn(executiveInputClassName, "pr-11")}
                  />
                  <div className="pointer-events-none absolute inset-y-0 right-0 flex items-center pr-4 text-[hsl(var(--muted-foreground))]">
                    <LockGlyph />
                  </div>
                </div>
                <p className="ml-1 text-xs font-semibold leading-5 text-[hsl(var(--primary))]">密码由 `ui.web.auth` 管理</p>
              </div>

              <div className="executive-note flex items-center gap-3 text-[13px] leading-6 text-[hsl(var(--muted-foreground))]">
                <span className="inline-flex size-2 rounded-full bg-[hsl(var(--primary))]" />
                登录成功后会创建独立的浏览器会话 Cookie。
              </div>

              {errorMessage ? (
                <div className="rounded-xl bg-[rgba(239,68,68,0.08)] px-4 py-3 text-sm text-[rgb(153,27,27)]">{errorMessage}</div>
              ) : null}

              <Button
                type="submit"
                className="h-14 w-full rounded-xl text-base font-bold shadow-[0_18px_36px_rgba(0,91,191,0.18)]"
                size="lg"
                disabled={actionPending === "login" || authState === "checking"}
              >
                {actionPending === "login" || authState === "checking" ? "正在登录..." : "登录"}
              </Button>
            </form>
          </section>
        </div>
      </main>

      <footer className="mt-auto flex flex-col items-center justify-between gap-2 bg-[hsl(var(--surface-low))] px-6 py-5 text-xs text-[hsl(var(--muted-foreground))] shadow-[inset_0_1px_0_rgba(193,198,214,0.38)] md:flex-row md:px-8">
        <span>© 2026 Agent 控制台. 保留所有权利。</span>
        <div className="flex gap-6">
          <span>支持</span>
          <span>条款</span>
          <span>隐私</span>
        </div>
      </footer>
      {toastLayer}
    </div>
  );
}

export function ConsoleShell({
  actionPending,
  currentNav,
  data,
  filteredLogs,
  filteredServices,
  filteredTunnels,
  filterKeyword,
  loading,
  onDeleteService,
  onDrainSession,
  onFilterKeywordChange,
  onLogout,
  onOpenCreateService,
  onOpenDetailService,
  onOpenEditService,
  onReconnectSession,
  onRefresh,
  onResetSettings,
  onSaveSettings,
  onSelectPage,
  onUpdateSettingsDraft,
  page,
  sessionUsername,
  settingsDraft,
  settingsDirty,
  settingsFieldErrors,
  savingSettings,
  topMetrics,
}: ConsoleShellProps) {
  return (
    <div className="shell-grid">
      <aside className="shell-sidebar">
        <div className="space-y-4 lg:space-y-6">
          <div className="flex items-center gap-3">
            <IconFrame tone="primary">
              <svg className="size-5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round">
                <rect x="4" y="6" width="16" height="12" rx="3" />
                <path d="M8 10h8" />
                <path d="M8 14h5" />
              </svg>
            </IconFrame>
            <div className="space-y-1">
              <p className="label-kicker">DevBridge</p>
              <h1 className="font-display text-2xl font-semibold tracking-[-0.04em]">Agent 控制台</h1>
            </div>
          </div>

          <div className="rounded-[24px] bg-[rgba(255,255,255,0.72)] p-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.8)]">
            <p className="label-kicker">Agent 标识</p>
            <div className="mt-3 space-y-2">
              <div className="text-lg font-semibold">{data.agent.agent_id}</div>
              <div className="text-sm text-[hsl(var(--muted-foreground))]">{data.agent.bridge_addr}</div>
              <Badge variant={statusBadgeVariant(data.session.state)}>{formatStatusText(data.session.state)}</Badge>
            </div>
          </div>
        </div>

        <nav className="grid grid-cols-2 gap-2 lg:grid-cols-1">
          {navigationItems.map((item) => {
            const isActive = item.key === page;
            return (
              <button
                key={item.key}
                type="button"
                aria-current={isActive ? "page" : undefined}
                className={cn("nav-pill", isActive && "nav-pill-active")}
                onClick={() => onSelectPage(item.key)}
              >
                <IconFrame className="size-11 rounded-[18px]" tone={isActive ? "primary" : "muted"}>
                  <NavGlyph page={item.key} />
                </IconFrame>
                <span className="min-w-0">
                  <span className="block text-sm font-semibold">{item.label}</span>
                  <span className="mt-1 hidden text-xs leading-5 text-[hsl(var(--muted-foreground))] md:block">{item.caption}</span>
                </span>
              </button>
            );
          })}
        </nav>

        <div className="mt-4 space-y-4 rounded-[24px] bg-[rgba(255,255,255,0.66)] p-4 lg:mt-auto">
          <div>
            <p className="label-kicker">快速状态</p>
            <div className="mt-3 grid gap-3">
              <QuickStatus label="传输协议" value={data.agent.bridge_transport} />
              <QuickStatus label="重连次数" value={formatCount(data.session.reconnect_total)} />
              <QuickStatus label="最近心跳" value={formatRelativeTime(data.session.last_heartbeat_at_ms)} />
            </div>
          </div>
          <Separator />
          <div className="flex items-center justify-between gap-3">
            <div>
              <div className="text-sm font-semibold">{sessionUsername || "管理员"}</div>
              <div className="text-xs text-[hsl(var(--muted-foreground))]">管理界面账号</div>
            </div>
            <Button variant="outline" size="sm" onClick={onLogout} disabled={actionPending === "logout"}>
              退出
            </Button>
          </div>
        </div>
      </aside>

      <main className="shell-main">
        <section className="shell-topbar">
          <div className="flex flex-col gap-5 xl:flex-row xl:items-start xl:justify-between">
            <div className="space-y-3">
              <p className="label-kicker">Agent 本地工作台</p>
              <div className="space-y-2">
                <h2 className="font-display text-3xl font-semibold tracking-[-0.05em] sm:text-4xl">{currentNav.label}</h2>
                <p className="max-w-3xl text-sm leading-6 text-[hsl(var(--muted-foreground))]">{currentNav.caption}</p>
              </div>
            </div>

            <div className="flex flex-col gap-3 xl:items-end">
              <div className="flex flex-wrap items-center gap-3">
                <div className="min-w-full flex-1 sm:min-w-[260px] xl:min-w-[320px]">
                  <Input
                    placeholder="搜索服务、隧道、日志或协议..."
                    value={filterKeyword}
                    onChange={(event) => onFilterKeywordChange(event.target.value)}
                  />
                </div>
                <Button variant="outline" onClick={onRefresh} disabled={loading}>
                  刷新
                </Button>
                <Button variant="secondary" onClick={onReconnectSession} disabled={actionPending === "/session/reconnect"}>
                  重新连接
                </Button>
                <Button variant="outline" onClick={onDrainSession} disabled={actionPending === "/session/drain"}>
                  排空
                </Button>
              </div>

              <div className="flex flex-wrap items-center gap-3">
                <Badge variant={statusBadgeVariant(data.session.state)}>{formatStatusText(data.session.state)}</Badge>
                <Badge variant="outline">心跳 {formatRelativeTime(data.session.last_heartbeat_at_ms)}</Badge>
                <Badge variant="outline">更新于 {formatRelativeTime(data.agent.updated_at_ms)}</Badge>
              </div>
            </div>
          </div>
        </section>

        {page === "overview" ? (
          <>
            <section className="grid gap-4 md:grid-cols-2 xl:grid-cols-4">
              {topMetrics.map((item) => (
                <div key={item.label} className="panel-soft rounded-[32px] border border-[rgba(255,255,255,0.72)] bg-[rgba(255,255,255,0.94)] shadow-[0_8px_24px_rgba(25,28,29,0.04)]">
                  <div className="flex min-h-[140px] flex-col justify-between px-5 pt-5 sm:min-h-[154px] sm:px-6 sm:pt-6">
                    <div className="flex flex-col items-start gap-4 sm:flex-row sm:justify-between">
                      <div>
                        <p className="label-kicker">{item.label}</p>
                        <div className="mt-3 font-display text-[1.75rem] font-semibold tracking-[-0.04em] sm:mt-4 sm:text-3xl">{item.value}</div>
                      </div>
                      <Badge variant={item.tone}>
                        {item.tone === "default" ? "实时" : item.tone === "success" ? "正常" : item.tone === "warning" ? "注意" : "异常"}
                      </Badge>
                    </div>
                    <p className="mt-5 text-sm leading-6 text-[hsl(var(--muted-foreground))] sm:mt-6">{item.help}</p>
                  </div>
                </div>
              ))}
            </section>
            <OverviewPage data={data} loading={loading} />
          </>
        ) : null}

        {page === "services" ? (
          <ServicesPage
            actionPending={actionPending}
            keyword={filterKeyword.trim().toLowerCase()}
            services={filteredServices}
            onDeleteService={onDeleteService}
            onOpenCreateService={onOpenCreateService}
            onOpenDetailService={onOpenDetailService}
            onOpenEditService={onOpenEditService}
          />
        ) : null}

        {page === "tunnels" ? <TunnelsPage tunnels={filteredTunnels} /> : null}
        {page === "traffic" ? <TrafficPage data={data} /> : null}
        {page === "diagnose" ? <DiagnosePage data={data} logs={filteredLogs} /> : null}

        {page === "settings" ? (
          <SettingsPage
            config={data.config}
            session={data.session}
            draft={settingsDraft}
            dirty={settingsDirty}
            fieldErrors={settingsFieldErrors}
            saving={savingSettings}
            onChange={onUpdateSettingsDraft}
            onReset={onResetSettings}
            onSave={onSaveSettings}
          />
        ) : null}
      </main>
    </div>
  );
}
