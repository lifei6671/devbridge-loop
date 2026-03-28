import {
  Children,
  cloneElement,
  useEffect,
  useId,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type FocusEvent,
  type HTMLAttributes,
  type MouseEvent,
  type ReactElement,
  type ReactNode,
} from "react";
import { createPortal } from "react-dom";

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

const TOOLTIP_GAP_PX = 10;
const TOOLTIP_VIEWPORT_MARGIN_PX = 16;

export function Tooltip({
  align = "center",
  children,
  className,
  content,
  contentClassName,
}: TooltipProps) {
  const tooltipId = useId();
  const trigger = Children.only(children) as ReactElement<TooltipTriggerProps>;
  const wrapperRef = useRef<HTMLSpanElement | null>(null);
  const contentRef = useRef<HTMLSpanElement | null>(null);
  const [isOpen, setIsOpen] = useState(false);
  const [position, setPosition] = useState({ left: 0, top: 0 });
  const [isPositionReady, setIsPositionReady] = useState(false);

  const triggerHandlers = useMemo(
    () => ({
      onBlur: (event: FocusEvent<HTMLElement>) => {
        trigger.props.onBlur?.(event);
        setIsOpen(false);
      },
      onFocus: (event: FocusEvent<HTMLElement>) => {
        trigger.props.onFocus?.(event);
        setIsOpen(true);
      },
      onMouseEnter: (event: MouseEvent<HTMLElement>) => {
        trigger.props.onMouseEnter?.(event);
        setIsOpen(true);
      },
      onMouseLeave: (event: MouseEvent<HTMLElement>) => {
        trigger.props.onMouseLeave?.(event);
        setIsOpen(false);
      },
    }),
    [trigger.props]
  );

  useLayoutEffect(() => {
    if (isOpen !== true) {
      setIsPositionReady(false);
      return;
    }
    const wrapperElement = wrapperRef.current;
    const contentElement = contentRef.current;
    if (wrapperElement === null || contentElement === null) {
      return;
    }
    const triggerRect = wrapperElement.getBoundingClientRect();
    const tooltipRect = contentElement.getBoundingClientRect();
    const nextPosition = calculateTooltipPosition(
      {
        bottom: triggerRect.bottom,
        centerX: triggerRect.left + triggerRect.width / 2,
        left: triggerRect.left,
        right: triggerRect.right,
        top: triggerRect.top,
      },
      { height: tooltipRect.height, width: tooltipRect.width },
      { height: window.innerHeight, width: window.innerWidth },
      align
    );
    setPosition(nextPosition);
    setIsPositionReady(true);
  }, [align, isOpen, content]);

  useEffect(() => {
    if (isOpen !== true) {
      return;
    }
    const handleViewportChange = () => {
      setIsPositionReady(false);
      const wrapperElement = wrapperRef.current;
      const contentElement = contentRef.current;
      if (wrapperElement === null || contentElement === null) {
        return;
      }
      const triggerRect = wrapperElement.getBoundingClientRect();
      const tooltipRect = contentElement.getBoundingClientRect();
      setPosition(
        calculateTooltipPosition(
          {
            bottom: triggerRect.bottom,
            centerX: triggerRect.left + triggerRect.width / 2,
            left: triggerRect.left,
            right: triggerRect.right,
            top: triggerRect.top,
          },
          { height: tooltipRect.height, width: tooltipRect.width },
          { height: window.innerHeight, width: window.innerWidth },
          align
        )
      );
      setIsPositionReady(true);
    };
    window.addEventListener("resize", handleViewportChange);
    window.addEventListener("scroll", handleViewportChange, true);
    return () => {
      window.removeEventListener("resize", handleViewportChange);
      window.removeEventListener("scroll", handleViewportChange, true);
    };
  }, [align, isOpen]);

  return (
    <>
      <span ref={wrapperRef} className={cn("ui-tooltip", `ui-tooltip-${align}`, className)}>
        {cloneElement(trigger, {
          "aria-describedby": tooltipId,
          ...triggerHandlers,
        })}
      </span>
      {typeof document !== "undefined"
        ? createPortal(
            <span
              id={tooltipId}
              ref={contentRef}
              role="tooltip"
              className={cn(
                "ui-tooltip-content",
                isOpen === true ? "ui-tooltip-content-visible" : "ui-tooltip-content-hidden",
                contentClassName
              )}
              style={{
                left: `${position.left}px`,
                top: `${position.top}px`,
                visibility: isPositionReady === true ? "visible" : "hidden",
              }}
            >
              {content}
            </span>,
            document.body
          )
        : null}
    </>
  );
}

type TooltipTriggerRect = {
  bottom: number;
  centerX: number;
  left: number;
  right: number;
  top: number;
};

type TooltipRect = {
  height: number;
  width: number;
};

type TooltipViewport = {
  height: number;
  width: number;
};

function calculateTooltipPosition(
  triggerRect: TooltipTriggerRect,
  tooltipRect: TooltipRect,
  viewport: TooltipViewport,
  align: TooltipAlign
) {
  const preferredTop = triggerRect.bottom + TOOLTIP_GAP_PX;
  const fallbackTop = triggerRect.top - tooltipRect.height - TOOLTIP_GAP_PX;
  const minTop = TOOLTIP_VIEWPORT_MARGIN_PX;
  const maxTop = Math.max(minTop, viewport.height - tooltipRect.height - TOOLTIP_VIEWPORT_MARGIN_PX);
  const shouldPlaceAbove =
    preferredTop + tooltipRect.height + TOOLTIP_VIEWPORT_MARGIN_PX > viewport.height &&
    fallbackTop >= TOOLTIP_VIEWPORT_MARGIN_PX;
  const top = clampNumber(shouldPlaceAbove ? fallbackTop : preferredTop, minTop, maxTop);

  let preferredLeft = triggerRect.centerX - tooltipRect.width / 2;
  if (align === "start") {
    preferredLeft = triggerRect.left;
  } else if (align === "end") {
    preferredLeft = triggerRect.right - tooltipRect.width;
  }
  const minLeft = TOOLTIP_VIEWPORT_MARGIN_PX;
  const maxLeft = Math.max(minLeft, viewport.width - tooltipRect.width - TOOLTIP_VIEWPORT_MARGIN_PX);
  const left = clampNumber(preferredLeft, minLeft, maxLeft);

  return { left, top };
}

function clampNumber(value: number, min: number, max: number) {
  return Math.min(Math.max(value, min), max);
}
