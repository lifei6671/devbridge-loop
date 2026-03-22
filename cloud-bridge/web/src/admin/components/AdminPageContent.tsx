import type { AdminConsoleViewModel } from "../hooks/useAdminConsole";
import { ConnectorsPage } from "./pages/ConnectorsPage";
import { DashboardPage } from "./pages/DashboardPage";
import { ObservabilityPage } from "./pages/ObservabilityPage";
import { OpsPage } from "./pages/OpsPage";
import { RoutesPage } from "./pages/RoutesPage";
import { ServicesPage } from "./pages/ServicesPage";
import { TrafficPage } from "./pages/TrafficPage";

type AdminPageContentProps = {
  vm: AdminConsoleViewModel;
};

export function AdminPageContent(props: AdminPageContentProps) {
  switch (props.vm.activePage) {
    case "dashboard":
      return <DashboardPage vm={props.vm} />;
    case "routes":
      return <RoutesPage vm={props.vm} />;
    case "services":
      return <ServicesPage vm={props.vm} />;
    case "connectors":
      return <ConnectorsPage vm={props.vm} />;
    case "traffic":
      return <TrafficPage vm={props.vm} />;
    case "ops":
      return <OpsPage vm={props.vm} />;
    case "observability":
      return <ObservabilityPage vm={props.vm} />;
    default:
      return null;
  }
}
