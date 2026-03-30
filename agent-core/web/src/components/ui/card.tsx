import type { HTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type DivProps = HTMLAttributes<HTMLDivElement>;
type HeadingProps = HTMLAttributes<HTMLHeadingElement>;
type ParagraphProps = HTMLAttributes<HTMLParagraphElement>;

export function Card({ children, className, ...props }: DivProps) {
  return (
    <div
      className={cn(
        "rounded-3xl border border-[hsl(var(--border)/0.18)] bg-[hsl(var(--card)/0.82)] shadow-ambient backdrop-blur",
        className,
      )}
      {...props}
    >
      {children}
    </div>
  );
}

export function CardHeader({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("flex flex-col gap-2 px-6 pt-6", className)} {...props}>
      {children}
    </div>
  );
}

export function CardTitle({ children, className, ...props }: HeadingProps) {
  return (
    <h3 className={cn("font-display text-lg font-semibold tracking-tight text-[hsl(var(--foreground))]", className)} {...props}>
      {children}
    </h3>
  );
}

export function CardDescription({ children, className, ...props }: ParagraphProps) {
  return (
    <p className={cn("text-sm text-[hsl(var(--muted-foreground))]", className)} {...props}>
      {children}
    </p>
  );
}

export function CardContent({ children, className, ...props }: DivProps) {
  return (
    <div className={cn("px-6 pb-6", className)} {...props}>
      {children}
    </div>
  );
}
