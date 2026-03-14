import * as React from "react";

import { cn } from "@/lib/utils";

/** 输入框组件：提供统一边框、聚焦和禁用态样式。 */
export const Input = React.forwardRef<HTMLInputElement, React.InputHTMLAttributes<HTMLInputElement>>(
  ({ className, type = "text", ...props }, ref) => {
    // 统一输入控件视觉，避免页面中出现风格漂移。
    return (
      <input
        ref={ref}
        type={type}
        className={cn(
          "h-10 w-full rounded-xl border border-[#d8dfeb] bg-white px-3 text-sm text-[#1f2a44] outline-none transition-colors placeholder:text-[#9aa6bc] focus:border-[#9fb4e4] focus:ring-2 focus:ring-[#dfe9ff] disabled:cursor-not-allowed disabled:opacity-60",
          className,
        )}
        {...props}
      />
    );
  },
);
Input.displayName = "Input";

