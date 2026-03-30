import type { InputHTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type InputProps = InputHTMLAttributes<HTMLInputElement>;

export function Input({ className, type = "text", ...props }: InputProps) {
  return (
    <input
      type={type}
      className={cn(
        "flex h-11 w-full rounded-xl border border-[hsl(var(--border)/0.28)] bg-[hsl(var(--secondary)/0.72)] px-3 py-2 text-sm text-[hsl(var(--foreground))] shadow-none outline-none transition focus:border-[hsl(var(--primary)/0.35)] focus:bg-[hsl(var(--card))] focus:ring-2 focus:ring-[hsl(var(--ring)/0.22)] placeholder:text-[hsl(var(--muted-foreground))]",
        className,
      )}
      {...props}
    />
  );
}
