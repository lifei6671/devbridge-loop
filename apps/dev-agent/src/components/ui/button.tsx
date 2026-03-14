import * as React from "react";

import { cn } from "@/lib/utils";

type ButtonVariant = "default" | "secondary" | "outline" | "ghost" | "destructive";
type ButtonSize = "default" | "sm" | "lg" | "icon";

const BUTTON_VARIANT_CLASSES: Record<ButtonVariant, string> = {
  default: "bg-[#2f6fe8] text-white hover:bg-[#245fd0]",
  secondary: "bg-[#eef2f8] text-[#1f2a44] hover:bg-[#e4e9f2]",
  outline: "border border-[#d9dfeb] bg-white text-[#1f2a44] hover:bg-[#f6f8fc]",
  ghost: "bg-transparent text-[#4a5770] hover:bg-[#edf2fa]",
  destructive: "bg-[#d54b4b] text-white hover:bg-[#be3f3f]",
};

const BUTTON_SIZE_CLASSES: Record<ButtonSize, string> = {
  default: "h-10 px-4 text-sm",
  sm: "h-8 px-3 text-xs",
  lg: "h-11 px-5 text-sm",
  icon: "h-10 w-10 text-sm",
};

export interface ButtonProps extends React.ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
}

/** 通用按钮组件：提供与 shadcn 风格一致的视觉变体。 */
export const Button = React.forwardRef<HTMLButtonElement, ButtonProps>(
  ({ className, variant = "default", size = "default", ...props }, ref) => {
    // 按变体和尺寸组合基础样式，保证交互风格统一。
    const mergedClassName = cn(
      "inline-flex items-center justify-center rounded-xl font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-[#a6b8e6] disabled:pointer-events-none disabled:opacity-50",
      BUTTON_VARIANT_CLASSES[variant],
      BUTTON_SIZE_CLASSES[size],
      className,
    );
    return <button ref={ref} className={mergedClassName} {...props} />;
  },
);
Button.displayName = "Button";

