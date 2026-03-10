import type { ReactElement } from "react";

import { Button } from "@/components/ui/button";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle
} from "@/components/ui/card";

interface CloseDecisionDialogProps {
  open: boolean;
  resolving: boolean;
  onResolve: (action: "tray" | "exit") => void;
}

export function CloseDecisionDialog({
  open,
  resolving,
  onResolve
}: CloseDecisionDialogProps): ReactElement | null {
  if (!open) {
    return null;
  }

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/45 px-4">
      <Card className="w-full max-w-md border-border/80 shadow-xl">
        <CardHeader>
          <CardTitle>关闭客户端</CardTitle>
          <CardDescription>
            请选择“直接退出”或“最小化到托盘”。
          </CardDescription>
        </CardHeader>
        <CardContent className="flex flex-wrap justify-end gap-2">
          <Button
            variant="outline"
            disabled={resolving}
            onClick={() => onResolve("exit")}
          >
            直接退出
          </Button>
          <Button
            disabled={resolving}
            onClick={() => onResolve("tray")}
          >
            最小化到托盘
          </Button>
        </CardContent>
      </Card>
    </div>
  );
}
