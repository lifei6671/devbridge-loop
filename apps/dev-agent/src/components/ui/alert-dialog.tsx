import { useEffect } from "react";
import { createPortal } from "react-dom";

import { cn } from "@/lib/utils";

import { Button } from "./button";

interface AlertDialogProps {
  open: boolean;
  title: string;
  description?: string;
  cancelText?: string;
  actionText?: string;
  onOpenChange?: (nextOpen: boolean) => void;
  onCancel: () => void;
  onAction: () => void;
  actionClassName?: string;
}

/** 轻量 shadcn 风格确认窗：用于宿主关闭前的关键确认。 */
export function AlertDialog({
  open,
  title,
  description,
  cancelText = "取消",
  actionText = "确定",
  onOpenChange,
  onCancel,
  onAction,
  actionClassName,
}: AlertDialogProps): JSX.Element | null {
  useEffect(() => {
    if (!open) {
      return;
    }
    const onKeydown = (event: KeyboardEvent) => {
      if (event.key === "Escape") {
        onOpenChange?.(false);
      }
    };
    window.addEventListener("keydown", onKeydown);
    return () => {
      window.removeEventListener("keydown", onKeydown);
    };
  }, [onOpenChange, open]);

  if (!open) {
    return null;
  }

  const handleDismiss = () => {
    onOpenChange?.(false);
  };

  return createPortal(
    <div className="fixed inset-0 z-[140] grid place-items-center bg-[#0f1b33]/45 px-4 backdrop-blur-[2px]">
      <div
        className="fixed inset-0"
        role="presentation"
        onClick={handleDismiss}
      />
      <div className="relative z-[141] w-full max-w-[420px] rounded-2xl border border-[#dce4f3] bg-white p-5 shadow-[0_22px_52px_rgba(20,39,75,0.22)]">
        <div className="space-y-2">
          <h3 className="text-[20px] font-semibold leading-tight tracking-[-0.01em] text-[#1f2b40]">{title}</h3>
          {description ? <p className="text-sm leading-6 text-[#5d6b86]">{description}</p> : null}
        </div>
        <div className="mt-5 flex items-center justify-end gap-2.5">
          <Button variant="outline" className="h-9 rounded-lg px-4 text-xs" onClick={onCancel}>
            {cancelText}
          </Button>
          <Button
            className={cn("h-9 rounded-lg px-4 text-xs font-semibold", actionClassName)}
            onClick={onAction}
          >
            {actionText}
          </Button>
        </div>
      </div>
    </div>,
    document.body,
  );
}
