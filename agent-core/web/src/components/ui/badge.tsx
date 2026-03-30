import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  variant?: "default" | "secondary" | "outline" | "success" | "warning" | "danger";
};

const variantClassName: Record<NonNullable<BadgeProps["variant"]>, string> = {
  default:
    "bg-[linear-gradient(135deg,rgba(0,91,191,0.12),rgba(26,115,232,0.18))] text-[hsl(var(--primary))]",
  secondary: "bg-[hsl(var(--secondary))] text-[hsl(var(--secondary-foreground))]",
  outline: "border border-[hsl(var(--border)/0.45)] bg-transparent text-[hsl(var(--muted-foreground))]",
  success: "bg-[rgba(34,197,94,0.12)] text-[rgb(20,116,49)]",
  warning: "bg-[rgba(245,158,11,0.14)] text-[rgb(180,83,9)]",
  danger: "bg-[rgba(239,68,68,0.12)] text-[rgb(185,28,28)]",
};

export function Badge({ children, className, variant = "default", ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2.5 py-1 text-[11px] font-semibold uppercase tracking-[0.16em]",
        variantClassName[variant],
        className,
      )}
      {...props}
    >
      {children}
    </span>
  );
}
