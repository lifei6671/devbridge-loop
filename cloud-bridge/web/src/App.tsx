import { Toaster } from "sonner";

import { AdminAuthDialog } from "./admin/components/AdminAuthDialog";
import { AdminShell } from "./admin/components/AdminShell";
import { useAdminConsole } from "./admin/hooks/useAdminConsole";

export default function App() {
  const vm = useAdminConsole();

  return (
    <>
      <AdminShell vm={vm} />
      <AdminAuthDialog vm={vm} />
      <Toaster richColors position="top-right" />
    </>
  );
}
