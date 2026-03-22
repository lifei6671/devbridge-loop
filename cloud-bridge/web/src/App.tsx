import { Toaster } from "sonner";

import { AdminShell } from "./admin/components/AdminShell";
import { useAdminConsole } from "./admin/hooks/useAdminConsole";

export default function App() {
  const vm = useAdminConsole();

  return (
    <>
      <AdminShell vm={vm} />
      <Toaster richColors position="top-right" />
    </>
  );
}
