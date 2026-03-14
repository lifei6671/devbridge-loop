import * as React from "react";

import { cn } from "@/lib/utils";

/** 卡片容器：用于承载业务区块。 */
export const Card = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => {
    // 使用浅色背景和细边框，匹配控制台白色主题。
    return (
      <div
        ref={ref}
        className={cn("rounded-2xl border border-[#dfe4ee] bg-white shadow-[0_2px_10px_rgba(31,42,68,0.04)]", className)}
        {...props}
      />
    );
  },
);
Card.displayName = "Card";

/** 卡片头部：统一标题区域内边距。 */
export const CardHeader = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => {
    return <div ref={ref} className={cn("px-5 pt-5 pb-3", className)} {...props} />;
  },
);
CardHeader.displayName = "CardHeader";

/** 卡片标题：用于主要语义标题。 */
export const CardTitle = React.forwardRef<HTMLHeadingElement, React.HTMLAttributes<HTMLHeadingElement>>(
  ({ className, ...props }, ref) => {
    return <h3 ref={ref} className={cn("text-[22px] font-semibold text-[#1d2740]", className)} {...props} />;
  },
);
CardTitle.displayName = "CardTitle";

/** 卡片描述：用于标题下的解释文案。 */
export const CardDescription = React.forwardRef<HTMLParagraphElement, React.HTMLAttributes<HTMLParagraphElement>>(
  ({ className, ...props }, ref) => {
    return <p ref={ref} className={cn("text-sm text-[#6c7890]", className)} {...props} />;
  },
);
CardDescription.displayName = "CardDescription";

/** 卡片正文：统一内容区间距。 */
export const CardContent = React.forwardRef<HTMLDivElement, React.HTMLAttributes<HTMLDivElement>>(
  ({ className, ...props }, ref) => {
    return <div ref={ref} className={cn("px-5 pb-5", className)} {...props} />;
  },
);
CardContent.displayName = "CardContent";

