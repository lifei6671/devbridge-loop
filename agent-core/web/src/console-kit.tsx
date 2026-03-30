import type { ReactNode, SVGProps } from "react";

import { Button } from "@/components/ui/button";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "@/components/ui/tooltip";
import { formatCount, glyphClassName, type PageKey } from "@/console-shared";
import { cn } from "@/lib/utils";

export function IconFrame({
  children,
  className,
  tone,
}: {
  children: ReactNode;
  className?: string;
  tone?: "primary" | "muted" | "danger";
}) {
  return <span className={cn("inline-flex size-10 items-center justify-center rounded-2xl", glyphClassName(tone), className)}>{children}</span>;
}

function MiniIcon(props: SVGProps<SVGSVGElement>) {
  return <svg viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" {...props} />;
}
export function NavGlyph({ page }: { page: PageKey }) {
  const commonProps = { className: "size-4" };
  switch (page) {
    case "overview":
      return (
        <MiniIcon {...commonProps}>
          <rect x="4" y="4" width="7" height="7" rx="1.5" />
          <rect x="13" y="4" width="7" height="7" rx="1.5" />
          <rect x="4" y="13" width="7" height="7" rx="1.5" />
          <rect x="13" y="13" width="7" height="7" rx="1.5" />
        </MiniIcon>
      );
    case "services":
      return (
        <MiniIcon {...commonProps}>
          <path d="M4 7h16" />
          <path d="M7 12h10" />
          <path d="M10 17h4" />
          <rect x="3" y="4" width="18" height="16" rx="3" />
        </MiniIcon>
      );
    case "tunnels":
      return (
        <MiniIcon {...commonProps}>
          <path d="M8 7h8" />
          <path d="M8 17h8" />
          <path d="M6 9v6" />
          <path d="M18 9v6" />
          <path d="M8 12h8" />
        </MiniIcon>
      );
    case "traffic":
      return (
        <MiniIcon {...commonProps}>
          <path d="M4 16l4-4 3 3 6-7 3 3" />
          <path d="M20 8v5h-5" />
        </MiniIcon>
      );
    case "diagnose":
      return (
        <MiniIcon {...commonProps}>
          <circle cx="12" cy="12" r="8" />
          <path d="M12 8v5" />
          <path d="M12 16h.01" />
        </MiniIcon>
      );
    case "settings":
      return (
        <MiniIcon {...commonProps}>
          <circle cx="12" cy="12" r="3" />
          <path d="M19.4 15a1.6 1.6 0 0 0 .3 1.8l.1.1a2 2 0 0 1-2.8 2.8l-.1-.1a1.6 1.6 0 0 0-1.8-.3 1.6 1.6 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.2a1.6 1.6 0 0 0-1-1.5 1.6 1.6 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.6 1.6 0 0 0 .3-1.8 1.6 1.6 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.2a1.6 1.6 0 0 0 1.5-1 1.6 1.6 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.6 1.6 0 0 0 1.8.3h.1a1.6 1.6 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.2a1.6 1.6 0 0 0 1 1.5 1.6 1.6 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.6 1.6 0 0 0-.3 1.8v.1a1.6 1.6 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.2a1.6 1.6 0 0 0-1.4 1Z" />
        </MiniIcon>
      );
  }
}

export function ArchitectureGlyph() {
  return (
    <MiniIcon className="size-8">
      <path d="M4 18V8l8-4 8 4v10" />
      <path d="M8 18v-4h8v4" />
      <path d="M9 10h.01" />
      <path d="M15 10h.01" />
    </MiniIcon>
  );
}

export function ShieldGlyph() {
  return (
    <MiniIcon className="size-5">
      <path d="M12 3l7 3v5c0 4.5-3 8.4-7 10-4-1.6-7-5.5-7-10V6l7-3Z" />
      <path d="m9.5 12 1.8 1.8 3.2-3.6" />
    </MiniIcon>
  );
}

export function MailGlyph() {
  return (
    <MiniIcon className="size-5">
      <rect x="3" y="5" width="18" height="14" rx="2" />
      <path d="m4 7 8 6 8-6" />
    </MiniIcon>
  );
}

export function LockGlyph() {
  return (
    <MiniIcon className="size-5">
      <rect x="5" y="10" width="14" height="10" rx="2" />
      <path d="M8 10V8a4 4 0 1 1 8 0v2" />
    </MiniIcon>
  );
}

export function HelpGlyph() {
  return (
    <MiniIcon className="size-5">
      <circle cx="12" cy="12" r="8" />
      <path d="M9.6 9.3a2.8 2.8 0 1 1 4.7 2c-.8.7-1.6 1.2-1.6 2.4" />
      <path d="M12 16.7h.01" />
    </MiniIcon>
  );
}

export function MoonGlyph() {
  return (
    <MiniIcon className="size-5">
      <path d="M19 14.5A7.5 7.5 0 0 1 9.5 5a7.9 7.9 0 1 0 9.5 9.5Z" />
    </MiniIcon>
  );
}

export function HeaderIconButton({ children, label }: { children: ReactNode; label: string }) {
  return (
    <Button
      type="button"
      variant="ghost"
      size="sm"
      aria-label={label}
      title={label}
      className="size-10 rounded-full p-0 text-[hsl(var(--muted-foreground))] hover:bg-[rgba(225,227,228,0.82)] hover:text-[hsl(var(--foreground))]"
    >
      {children}
    </Button>
  );
}

export function Field({
  label,
  caption,
  helpText,
  children,
}: {
  label: string;
  caption?: string;
  helpText?: string;
  children: ReactNode;
}) {
  return (
    <label className="grid executive-field">
      <span className="grid gap-1">
        <span className="executive-field-label inline-flex items-center gap-2">
          <span>{label}</span>
          {helpText ? <FieldHelpTooltip label={label} message={helpText} /> : null}
        </span>
        {caption ? <span className="ml-1 pr-6 text-[12px] leading-5 text-[hsl(var(--muted-foreground)/0.92)]">{caption}</span> : null}
      </span>
      {children}
    </label>
  );
}

function FieldHelpTooltip({ label, message }: { label: string; message: string }) {
  return (
    <TooltipProvider delayDuration={140}>
      <Tooltip>
        <TooltipTrigger asChild>
          <button
            type="button"
            aria-label={`${label} 字段说明`}
            className="inline-flex size-5 -translate-y-[1px] items-center justify-center rounded-full border border-[rgba(210,214,220,0.72)] bg-[rgba(255,255,255,0.92)] text-[hsl(var(--muted-foreground))] transition-colors hover:border-[rgba(0,91,191,0.18)] hover:text-[hsl(var(--primary))]"
          >
            <MiniIcon className="size-3.5">
              <circle cx="12" cy="12" r="8" />
              <path d="M9.6 9.3a2.8 2.8 0 1 1 4.7 2c-.8.7-1.6 1.2-1.6 2.4" />
              <path d="M12 16.7h.01" />
            </MiniIcon>
          </button>
        </TooltipTrigger>
        <TooltipContent side="top" align="center">
          <p>{message}</p>
        </TooltipContent>
      </Tooltip>
    </TooltipProvider>
  );
}

export function QuickStatus({ label, value }: { label: string; value: string }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-xs uppercase tracking-[0.16em] text-[hsl(var(--muted-foreground))]">{label}</span>
      <span className="text-sm font-medium">{value}</span>
    </div>
  );
}


export function FieldErrorText({ message }: { message: string }) {
  return <span className="pl-1 text-xs leading-5 text-[rgb(185,28,28)]">{message}</span>;
}

export function SettingsSummaryMetric({
  label,
  value,
  emphasis,
}: {
  label: string;
  value: string;
  emphasis: "primary" | "muted";
}) {
  const toneClassName =
    emphasis === "primary"
      ? "border-[rgba(0,91,191,0.1)] bg-[linear-gradient(180deg,rgba(240,247,255,0.95),rgba(230,239,251,0.88))]"
      : "border-[rgba(214,218,224,0.4)] bg-[linear-gradient(180deg,rgba(255,255,255,0.86),rgba(247,249,251,0.86))]";

  return (
    <div className={cn("rounded-[22px] border px-4 py-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.68)]", toneClassName)}>
      <div className="label-kicker">{label}</div>
      <div className="mt-3 break-all text-lg font-semibold tracking-[-0.02em] text-[hsl(var(--foreground))]">{value || "未记录"}</div>
    </div>
  );
}

export function SettingsDetailRow({
  label,
  value,
  tone = "default",
}: {
  label: string;
  value: string;
  tone?: "default" | "path";
}) {
  const contentClassName =
    tone === "path"
      ? "rounded-2xl bg-[rgba(15,23,42,0.05)] px-3 py-2 font-mono text-[13px] leading-6 text-[hsl(var(--foreground))]"
      : "text-sm leading-6 text-[hsl(var(--foreground))]";

  return (
    <div className="space-y-2">
      <div className="label-kicker">{label}</div>
      <div className={cn(contentClassName, tone !== "path" && "break-all")}>{value || "未记录"}</div>
    </div>
  );
}

export function ExecutiveMetric({
  label,
  value,
  emphasis = "muted",
}: {
  label: string;
  value: string;
  emphasis?: "primary" | "muted" | "danger";
}) {
  const toneClassName =
    emphasis === "primary"
      ? "bg-[linear-gradient(180deg,rgba(240,247,255,0.92),rgba(229,238,252,0.86))]"
      : emphasis === "danger"
        ? "bg-[linear-gradient(180deg,rgba(254,242,242,0.92),rgba(254,226,226,0.84))]"
        : "bg-[linear-gradient(180deg,rgba(255,255,255,0.88),rgba(247,248,250,0.88))]";

  return (
    <div className={cn("rounded-[22px] px-4 py-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.72)]", toneClassName)}>
      <div className="label-kicker">{label}</div>
      <div className="mt-3 font-display text-2xl font-semibold tracking-[-0.04em]">{value}</div>
    </div>
  );
}

export function StatColumn({ title, value, caption }: { title: string; value: string; caption: string }) {
  return (
    <div className="rounded-[22px] bg-[linear-gradient(180deg,rgba(255,255,255,0.86),rgba(247,248,250,0.86))] px-4 py-4 shadow-[inset_0_1px_0_rgba(255,255,255,0.72)]">
      <div className="label-kicker">{title}</div>
      <div className="mt-3 text-2xl font-semibold tracking-[-0.03em]">{value}</div>
      <div className="mt-2 text-sm text-[hsl(var(--muted-foreground))]">{caption}</div>
    </div>
  );
}

export function PoolBar({ label, current, total }: { label: string; current: number; total: number }) {
  const percentage = Math.min(100, Math.max(0, total > 0 ? (current / total) * 100 : 0));
  return (
    <div className="space-y-2">
      <div className="flex items-center justify-between text-sm">
        <span>{label}</span>
        <span className="text-[hsl(var(--muted-foreground))]">
          {formatCount(current)} / {formatCount(total)}
        </span>
      </div>
      <div className="h-2 rounded-full bg-[hsl(var(--secondary))]">
        <div className="h-2 rounded-full bg-[linear-gradient(90deg,#005bbf,#1a73e8)]" style={{ width: `${percentage}%` }} />
      </div>
    </div>
  );
}

export function OverviewKeyValue({ label, value }: { label: string; value: string }) {
  return (
    <div className="rounded-[20px] bg-[hsl(var(--secondary)/0.66)] px-4 py-4">
      <div className="label-kicker">{label}</div>
      <div className="mt-3 break-all text-sm leading-6 text-[hsl(var(--foreground))]">{value}</div>
    </div>
  );
}

export function EmptyStatePanel({
  eyebrow,
  icon,
  title,
  description,
  note,
  compact = false,
}: {
  eyebrow: string;
  icon: ReactNode;
  title: string;
  description: string;
  note: string;
  compact?: boolean;
}) {
  return (
    <div
      className={cn(
        "relative overflow-hidden rounded-[28px] border border-[rgba(214,218,224,0.38)] bg-[linear-gradient(180deg,rgba(255,255,255,0.92),rgba(247,249,251,0.9))] px-6 py-7 shadow-[inset_0_1px_0_rgba(255,255,255,0.72)]",
        compact && "px-5 py-6",
      )}
    >
      <div className="absolute inset-x-0 top-0 h-1 bg-[linear-gradient(90deg,rgba(0,91,191,0),rgba(0,91,191,0.42),rgba(0,91,191,0))]" />
      <div className="flex flex-col items-center text-center">
        <IconFrame className="size-12 rounded-[18px]" tone="primary">
          {icon}
        </IconFrame>
        <div className="mt-5 label-kicker">{eyebrow}</div>
        <div className="mt-3 font-display text-[1.35rem] font-semibold tracking-[-0.04em] text-[hsl(var(--foreground))]">{title}</div>
        <p className="mt-3 max-w-[32rem] text-sm leading-7 text-[hsl(var(--muted-foreground))]">{description}</p>
        <div className="mt-4 rounded-full bg-[rgba(15,23,42,0.05)] px-4 py-2 text-xs leading-6 text-[hsl(var(--muted-foreground))]">{note}</div>
      </div>
    </div>
  );
}
