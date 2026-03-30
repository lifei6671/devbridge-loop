import * as React from "react";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";

import { cn } from "@/lib/utils";

const TooltipProvider = TooltipPrimitive.Provider;

const Tooltip = TooltipPrimitive.Root;

const TooltipTrigger = TooltipPrimitive.Trigger;

const TooltipContent = React.forwardRef<
  React.ElementRef<typeof TooltipPrimitive.Content>,
  React.ComponentPropsWithoutRef<typeof TooltipPrimitive.Content>
>(({ className, sideOffset = 4, ...props }, ref) => (
  <TooltipPrimitive.Portal>
    <TooltipPrimitive.Content
      ref={ref}
      sideOffset={sideOffset}
      className={cn(
        "z-50 max-w-[280px] overflow-visible rounded-2xl border border-[rgba(73,84,103,0.9)] bg-[rgba(20,27,38,0.96)] px-3 py-2.5 text-sm leading-6 text-[rgba(244,247,251,0.96)] shadow-[0_18px_46px_rgba(15,23,42,0.28)]",
        "after:absolute after:size-3 after:rotate-45 after:bg-[rgba(20,27,38,0.96)] after:content-['']",
        "data-[side=top]:after:left-1/2 data-[side=top]:after:top-full data-[side=top]:after:-mt-[7px] data-[side=top]:after:-translate-x-1/2",
        "data-[side=bottom]:after:left-1/2 data-[side=bottom]:after:bottom-full data-[side=bottom]:after:-mb-[7px] data-[side=bottom]:after:-translate-x-1/2",
        "data-[side=left]:after:left-full data-[side=left]:after:top-1/2 data-[side=left]:after:-ml-[7px] data-[side=left]:after:-translate-y-1/2",
        "data-[side=right]:after:right-full data-[side=right]:after:top-1/2 data-[side=right]:after:-mr-[7px] data-[side=right]:after:-translate-y-1/2",
        "data-[state=delayed-open]:animate-in data-[state=closed]:animate-out",
        "data-[state=closed]:fade-out-0 data-[state=delayed-open]:fade-in-0",
        "data-[state=closed]:zoom-out-95 data-[state=delayed-open]:zoom-in-95",
        "data-[side=bottom]:slide-in-from-top-2 data-[side=left]:slide-in-from-right-2",
        "data-[side=right]:slide-in-from-left-2 data-[side=top]:slide-in-from-bottom-2",
        className,
      )}
      {...props}
    >
      {props.children}
    </TooltipPrimitive.Content>
  </TooltipPrimitive.Portal>
));
TooltipContent.displayName = TooltipPrimitive.Content.displayName;

export { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger };
