import * as React from "react";

import { cn } from "@/lib/utils";

type BadgeVariant = "default" | "secondary" | "outline" | "success" | "warning" | "danger";

const BADGE_VARIANT_CLASSES: Record<BadgeVariant, string> = {
  default: "border-transparent bg-[#2f6fe8] text-white",
  secondary: "border-transparent bg-[#edf2fa] text-[#2c3a55]",
  outline: "border-[#d7ddea] bg-white text-[#44516c]",
  success: "border-[#bfe8cf] bg-[#effcf4] text-[#0f8d5e]",
  warning: "border-[#f0dfb7] bg-[#fff9ea] text-[#9a6c18]",
  danger: "border-[#f0c8c8] bg-[#fff3f3] text-[#be4343]",
};

export interface BadgeProps extends React.HTMLAttributes<HTMLDivElement> {
  variant?: BadgeVariant;
}

/** 徽章组件：用于状态标签和轻量指标标识。 */
export function Badge({ className, variant = "secondary", ...props }: BadgeProps): JSX.Element {
  // 徽章保持紧凑尺寸，避免在密集布局中抢占主信息层级。
  return (
    <div
      className={cn(
        "inline-flex items-center rounded-full border px-2.5 py-0.5 text-xs font-medium",
        BADGE_VARIANT_CLASSES[variant],
        className,
      )}
      {...props}
    />
  );
}

