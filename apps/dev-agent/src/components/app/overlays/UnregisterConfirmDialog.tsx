import type { ReactElement } from "react";

import type { PendingUnregisterRegistration } from "@/components/app/useAppController";
import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";

interface UnregisterConfirmDialogProps {
  open: boolean;
  resolving: boolean;
  target: PendingUnregisterRegistration | null;
  onCancel: () => void;
  onConfirm: () => void;
}

export function UnregisterConfirmDialog({
  open,
  resolving,
  target,
  onCancel,
  onConfirm
}: UnregisterConfirmDialogProps): ReactElement | null {
  if (!open || !target) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4">
      <Card className="w-full max-w-md border-border/80 shadow-xl">
        <CardHeader>
          <CardTitle>注销服务实例</CardTitle>
          <CardDescription>
            确认注销实例 `{target.instanceId}`（{target.serviceName}/{target.env}）吗？
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap justify-end gap-2">
          <Button
            variant="outline"
            disabled={resolving}
            onClick={onCancel}
          >
            取消
          </Button>
          <Button
            disabled={resolving}
            onClick={onConfirm}
          >
            {resolving ? "注销中..." : "确认注销"}
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
