import { Children, cloneElement, useId, type HTMLAttributes, type ReactElement, type ReactNode } from "react";

import { cn } from "../../lib/utils";

type TooltipAlign = "start" | "center" | "end";
type TooltipTriggerProps = HTMLAttributes<HTMLElement> & {
  "aria-describedby"?: string;
};

type TooltipProps = {
  align?: TooltipAlign;
  children: ReactElement<TooltipTriggerProps>;
  className?: string;
  content: ReactNode;
  contentClassName?: string;
};

export function Tooltip({
  align = "center",
  children,
  className,
  content,
  contentClassName,
}: TooltipProps) {
  const tooltipId = useId();
  const trigger = Children.only(children) as ReactElement<TooltipTriggerProps>;

  return (
    <span className={cn("ui-tooltip", `ui-tooltip-${align}`, className)}>
      {cloneElement(trigger, { "aria-describedby": tooltipId })}
      <span id={tooltipId} role="tooltip" className={cn("ui-tooltip-content", contentClassName)}>
        {content}
      </span>
    </span>
  );
}
