import { AlertTriangle, CheckCircle2 } from "lucide-react";
import type { ReactElement } from "react";

import type { AppToast } from "@/components/app/types";
import { cn } from "@/lib/utils";

interface ToastNoticeProps {
  toast: AppToast | null;
}

export function ToastNotice({ toast }: ToastNoticeProps): ReactElement | null {
  if (!toast) {
    return null;
  }

  return (
    <div className="fixed bottom-6 right-6 z-50">
      <div
        className={cn(
          "flex max-w-sm items-center gap-2 rounded-md border px-3 py-2 text-sm shadow-xl transition-all duration-300",
          toast.level === "success"
            ? "border-emerald-500/40 bg-emerald-100/95 text-emerald-950"
            : "border-amber-500/40 bg-amber-100/95 text-amber-950"
        )}
      >
        {toast.level === "success" ? (
          <CheckCircle2 className="h-4 w-4" />
        ) : (
          <AlertTriangle className="h-4 w-4" />
        )}
        <span>{toast.message}</span>
      </div>
    </div>
  );
}
