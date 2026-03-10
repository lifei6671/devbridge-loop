import { AlertTriangle } from "lucide-react";
import type { ReactElement } from "react";

import { Card, CardContent } from "@/components/ui/card";

interface StatusAlertsProps {
  actionError: string;
  runtimeLastError: string | null;
}

function AlertCard({ message }: { message: string }): ReactElement {
  return (
    <Card className="mb-4 border-amber-500/40 bg-amber-100/40">
      <CardContent className="flex items-center gap-2 p-4 text-sm text-amber-950">
        <AlertTriangle className="h-4 w-4" />
        {message}
      </CardContent>
    </Card>
  );
}

export function StatusAlerts({ actionError, runtimeLastError }: StatusAlertsProps): ReactElement {
  return (
    <>
      {actionError ? <AlertCard message={actionError} /> : null}
      {runtimeLastError ? <AlertCard message={runtimeLastError} /> : null}
    </>
  );
}
