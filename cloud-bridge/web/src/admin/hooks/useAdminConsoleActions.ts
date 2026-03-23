import { type KeyboardEvent as ReactKeyboardEvent, useCallback } from "react";

import type { AdminPageKey, DetailDomain } from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";
import { useAdminDataActions } from "./useAdminDataActions";
import { useAdminOpsActions } from "./useAdminOpsActions";
import { useAdminTrafficActions } from "./useAdminTrafficActions";

type UseAdminConsoleActionsParams = {
  navigateToPage: (page: AdminPageKey, options?: { replace?: boolean }) => void;
  openDetailDrawer: (domain: DetailDomain, index: number) => void;
  state: AdminConsoleState;
};

export function useAdminConsoleActions(params: UseAdminConsoleActionsParams) {
  const { navigateToPage, openDetailDrawer, state } = params;
  const { detailSelection } = state;

  const { applySSESnapshot, login, logout, refreshAuthSession, refreshPageData, requestAdmin } =
    useAdminDataActions(state);
  const opsActions = useAdminOpsActions({
    navigateToPage,
    refreshPageData,
    requestAdmin,
    state,
  });
  const trafficActions = useAdminTrafficActions({
    requestAdmin,
    state,
  });

  const isActiveDetailRow = useCallback(
    (domain: DetailDomain, index: number): boolean => {
      if (detailSelection === null) {
        return false;
      }
      return detailSelection.domain === domain && detailSelection.index === index;
    },
    [detailSelection]
  );

  const handleDetailRowKeyDown = useCallback(
    (event: ReactKeyboardEvent<HTMLTableRowElement>, domain: DetailDomain, index: number) => {
      if (event.key !== "Enter" && event.key !== " ") {
        return;
      }
      // 避免 Space 键触发表格滚动，统一转成“打开详情”动作。
      event.preventDefault();
      openDetailDrawer(domain, index);
    },
    [openDetailDrawer]
  );


  return {
    applySSESnapshot,
    clearTrafficOwnershipLookup: trafficActions.clearTrafficOwnershipLookup,
    handleDetailRowKeyDown,
    isActiveDetailRow,
    login,
    lookupTrafficOwnership: trafficActions.lookupTrafficOwnership,
    logout,
    performConfigPatch: opsActions.performConfigPatch,
    performDrainConnector: opsActions.performDrainConnector,
    performDrainSession: opsActions.performDrainSession,
    performExportDiagnose: opsActions.performExportDiagnose,
    performReload: opsActions.performReload,
    prefillOpsFromDetail: opsActions.prefillOpsFromDetail,
    quickDrainFromDetail: opsActions.quickDrainFromDetail,
    refreshAuthSession,
    refreshPageData,
    submitConfigPatch: opsActions.submitConfigPatch,
    submitConfigPatchDocument: opsActions.submitConfigPatchDocument,
  };
}

export type AdminConsoleActions = ReturnType<typeof useAdminConsoleActions>;
