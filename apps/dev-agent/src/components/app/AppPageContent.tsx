import type { ReactElement } from "react";

import { ConfigPage } from "@/components/app/pages/ConfigPage";
import { DashboardPage } from "@/components/app/pages/DashboardPage";
import { InterceptsPage } from "@/components/app/pages/InterceptsPage";
import { LogsPage } from "@/components/app/pages/LogsPage";
import { ServicesPage } from "@/components/app/pages/ServicesPage";
import type { AppController } from "@/components/app/useAppController";

interface AppPageContentProps {
  controller: AppController;
}

export function AppPageContent({ controller }: AppPageContentProps): ReactElement {
  switch (controller.activePage) {
    case "dashboard":
      return (
        <DashboardPage
          agentCardStatus={controller.agentCardStatus}
          bridgeCardStatus={controller.bridgeCardStatus}
          bridgeOperationLabel={controller.bridgeOperationLabel}
          errorsCount={controller.errors.length}
          manualReconnectPending={controller.manualReconnectPending}
          manualReconnecting={controller.manualReconnecting}
          phaseBusy={controller.phaseBusy}
          reconnectCountdown={controller.reconnectCountdown}
          refreshProgressLabel={controller.refreshProgressLabel}
          requestsCount={controller.requests.length}
          runtime={controller.runtime}
          summary={controller.summary}
          tunnel={controller.tunnel}
          uiPhase={controller.uiPhase}
        />
      );
    case "services":
      return (
        <ServicesPage
          onCloseDetail={controller.onClearSelectedInstance}
          onSelectInstance={controller.onSelectInstance}
          onUnregisterInstance={controller.onUnregisterRegistration}
          registrations={controller.registrations}
          selectedInstanceId={controller.selectedInstanceId}
          selectedRegistration={controller.selectedRegistration}
          unregisteringId={controller.unregisteringId}
        />
      );
    case "intercepts":
      return <InterceptsPage intercepts={controller.intercepts} />;
    case "logs":
      return (
        <LogsPage
          clearingLogs={controller.clearingLogs}
          errors={controller.errors}
          onClearLogs={controller.onClearLogs}
          requests={controller.requests}
        />
      );
    case "config":
      return (
        <ConfigPage
          agentHttpAddrValidationError={controller.agentHttpAddrValidationError}
          desktopConfig={controller.desktopConfig}
          desktopConfigDraft={controller.desktopConfigDraft}
          desktopConfigDraftDirty={controller.desktopConfigDraftDirty}
          masqueConfigVisible={controller.masqueConfigVisible}
          masquePskVisible={controller.masquePskVisible}
          onPatchDraft={controller.onPatchDesktopConfigDraft}
          onSaveConfig={controller.onSaveDesktopConfig}
          savingConfig={controller.savingConfig}
        />
      );
    default:
      return (
        <DashboardPage
          agentCardStatus={controller.agentCardStatus}
          bridgeCardStatus={controller.bridgeCardStatus}
          bridgeOperationLabel={controller.bridgeOperationLabel}
          errorsCount={controller.errors.length}
          manualReconnectPending={controller.manualReconnectPending}
          manualReconnecting={controller.manualReconnecting}
          phaseBusy={controller.phaseBusy}
          reconnectCountdown={controller.reconnectCountdown}
          refreshProgressLabel={controller.refreshProgressLabel}
          requestsCount={controller.requests.length}
          runtime={controller.runtime}
          summary={controller.summary}
          tunnel={controller.tunnel}
          uiPhase={controller.uiPhase}
        />
      );
  }
}
