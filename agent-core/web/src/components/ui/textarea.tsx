import type { TextareaHTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type TextareaProps = TextareaHTMLAttributes<HTMLTextAreaElement>;

export function Textarea({ className, ...props }: TextareaProps) {
  return (
    <textarea
      className={cn(
        "min-h-28 w-full rounded-2xl border border-[hsl(var(--border)/0.24)] bg-[rgba(243,244,245,0.82)] px-4 py-3.5 text-sm text-[hsl(var(--foreground))] shadow-none outline-none transition focus:border-[hsl(var(--primary)/0.35)] focus:bg-[hsl(var(--card))] focus:ring-2 focus:ring-[hsl(var(--ring)/0.22)] placeholder:text-[hsl(var(--muted-foreground))]",
        className,
      )}
      {...props}
    />
  );
}
