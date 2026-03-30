import type { ButtonHTMLAttributes } from "react";

import { cn } from "@/lib/utils";

type ButtonProps = ButtonHTMLAttributes<HTMLButtonElement> & {
  variant?: "default" | "secondary" | "outline" | "ghost" | "destructive";
  size?: "sm" | "md" | "lg";
};

const variantClassName: Record<NonNullable<ButtonProps["variant"]>, string> = {
  default:
    "bg-gradient-to-br from-[hsl(var(--primary))] to-[hsl(var(--accent))] text-[hsl(var(--primary-foreground))] shadow-ambient hover:brightness-[1.03]",
  secondary:
    "bg-[hsl(var(--secondary))] text-[hsl(var(--secondary-foreground))] hover:bg-[hsl(var(--secondary)/0.88)]",
  outline:
    "border border-[hsl(var(--border)/0.5)] bg-[hsl(var(--card)/0.78)] text-[hsl(var(--foreground))] hover:bg-[hsl(var(--secondary))]",
  ghost:
    "text-[hsl(var(--foreground))] hover:bg-[hsl(var(--secondary))]",
  destructive:
    "bg-[hsl(var(--destructive))] text-[hsl(var(--destructive-foreground))] hover:brightness-[0.96]",
};

const sizeClassName: Record<NonNullable<ButtonProps["size"]>, string> = {
  sm: "h-9 px-3 text-xs",
  md: "h-11 px-4 text-sm",
  lg: "h-12 px-5 text-sm",
};

export function Button({
  children,
  className,
  size = "md",
  type = "button",
  variant = "default",
  ...props
}: ButtonProps) {
  return (
    <button
      type={type}
      className={cn(
        "inline-flex items-center justify-center gap-2 rounded-xl font-medium transition duration-150 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[hsl(var(--ring)/0.4)] focus-visible:ring-offset-2 focus-visible:ring-offset-[hsl(var(--background))] disabled:pointer-events-none disabled:opacity-50",
        variantClassName[variant],
        sizeClassName[size],
        className,
      )}
      {...props}
    >
      {children}
    </button>
  );
}
