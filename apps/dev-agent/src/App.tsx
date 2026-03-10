import type { ReactElement } from "react";

import { AppPageContent } from "@/components/app/AppPageContent";
import { Sidebar } from "@/components/app/layout/Sidebar";
import { TopHeader } from "@/components/app/layout/TopHeader";
import { CloseDecisionDialog } from "@/components/app/overlays/CloseDecisionDialog";
import { StatusAlerts } from "@/components/app/overlays/StatusAlerts";
import { ToastNotice } from "@/components/app/overlays/ToastNotice";
import { UnregisterConfirmDialog } from "@/components/app/overlays/UnregisterConfirmDialog";
import { useAppController } from "@/components/app/useAppController";

export default function App(): ReactElement {
  const controller = useAppController();

  return (
    <div className="h-screen w-screen overflow-hidden bg-[radial-gradient(circle_at_15%_10%,#f7efe1_0,#f4efea_32%,#eceff4_100%)] text-slate-900">
      <div className="grid h-full md:grid-cols-[252px_minmax(0,1fr)]">
        <Sidebar
          activePage={controller.activePage}
          agentStatus={controller.agentCardStatus}
          bridgeStatus={controller.bridgeCardStatus}
          currentEnv={controller.summary?.currentEnv ?? null}
          lastUpdateAt={controller.summary?.lastUpdateAt ?? null}
          onChangePage={controller.onChangePage}
          pageItems={controller.pageItems}
          rdName={controller.summary?.rdName ?? null}
        />

        <section className="flex min-h-0 flex-col">
          <TopHeader
            activePage={controller.activePage}
            activePageMeta={controller.activePageMeta}
            manualReconnectPending={controller.manualReconnectPending}
            manualReconnecting={controller.manualReconnecting}
            manualRefreshing={controller.manualRefreshing}
            onChangePage={controller.onChangePage}
            onReconnect={controller.onReconnect}
            onRefresh={controller.onRefresh}
            onRestart={controller.onRestartAgent}
            pageItems={controller.pageItems}
            phaseBusy={controller.phaseBusy}
            uiPhase={controller.uiPhase}
          />

          <main className="relative min-h-0 flex-1 overflow-auto px-4 py-5 sm:px-6">
            <StatusAlerts
              actionError={controller.actionError}
              runtimeLastError={controller.runtime?.lastError ?? null}
            />
            <AppPageContent controller={controller} />
          </main>
        </section>
      </div>

      <CloseDecisionDialog
        open={controller.showCloseDecisionDialog}
        resolving={controller.resolvingCloseDecision}
        onResolve={controller.onResolveCloseDecision}
      />

      <UnregisterConfirmDialog
        open={controller.pendingUnregisterRegistration !== null}
        resolving={controller.resolvingUnregisterRegistration}
        target={controller.pendingUnregisterRegistration}
        onCancel={controller.onCancelUnregisterRegistration}
        onConfirm={controller.onConfirmUnregisterRegistration}
      />

      <ToastNotice toast={controller.toast} />
    </div>
  );
}
