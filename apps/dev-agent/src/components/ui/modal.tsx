import { useEffect, useId, type ReactNode } from "react";
import { createPortal } from "react-dom";

import { cn } from "@/lib/utils";

interface ModalProps {
  open: boolean;
  title: string;
  description?: string;
  onOpenChange?: (nextOpen: boolean) => void;
  children: ReactNode;
  className?: string;
}

/** 轻量通用模态窗：用于承载表单等复杂交互内容。 */
export function Modal({
  open,
  title,
  description,
  onOpenChange,
  children,
  className,
}: ModalProps): JSX.Element | null {
  const headingId = useId();
  const descriptionId = useId();

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
    <div className="fixed inset-0 z-[150] grid place-items-center bg-[#0f1b33]/45 px-4 py-6 backdrop-blur-[2px]">
      <button
        type="button"
        aria-label="关闭弹窗"
        className="fixed inset-0 cursor-default"
        onClick={handleDismiss}
      />
      <section
        role="dialog"
        aria-modal="true"
        aria-labelledby={headingId}
        aria-describedby={description ? descriptionId : undefined}
        className={cn(
          "relative z-[151] w-full max-h-[92vh] max-w-[820px] overflow-hidden rounded-2xl border border-[#dce4f3] bg-white shadow-[0_26px_58px_rgba(20,39,75,0.24)]",
          className,
        )}
      >
        <div className="flex items-start justify-between gap-4 border-b border-[#e5eaf4] px-5 py-4">
          <div className="space-y-1">
            <h3 id={headingId} className="text-[20px] font-semibold leading-tight tracking-[-0.01em] text-[#1f2b40]">
              {title}
            </h3>
            {description ? (
              <p id={descriptionId} className="text-sm text-[#5d6b86]">
                {description}
              </p>
            ) : null}
          </div>
          <button
            type="button"
            aria-label="关闭弹窗"
            className="inline-flex h-8 w-8 items-center justify-center rounded-lg border border-[#d7dfec] text-[18px] leading-none text-[#61708f] transition hover:bg-[#f4f7fd]"
            onClick={handleDismiss}
          >
            ×
          </button>
        </div>
        <div className="max-h-[calc(92vh-86px)] overflow-y-auto px-5 py-4">{children}</div>
      </section>
    </div>,
    document.body,
  );
}
