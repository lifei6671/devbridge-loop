import { useCallback } from "react";

import { asRecord, encodeQuery, normalizeOperationError } from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";
import type { AdminRequestFn } from "./useAdminDataActions";

type UseAdminTrafficActionsParams = {
  requestAdmin: AdminRequestFn;
  state: AdminConsoleState;
};

export function useAdminTrafficActions(params: UseAdminTrafficActionsParams) {
  const { requestAdmin, state } = params;
  const {
    setIsTrafficOwnershipLoading,
    setTrafficLookupID,
    setTrafficOwnership,
    setTrafficOwnershipError,
  } = state;

  const lookupTrafficOwnership = useCallback(
    async (trafficID: string) => {
      const normalizedTrafficID = trafficID.trim();
      if (normalizedTrafficID === "") {
        setTrafficOwnership(null);
        setTrafficOwnershipError("请先输入 Traffic ID");
        return;
      }
      setIsTrafficOwnershipLoading(true);
      setTrafficOwnershipError("");
      try {
        const response = await requestAdmin(
          `/api/admin/traffic/ownership${encodeQuery({ traffic_id: normalizedTrafficID })}`
        );
        setTrafficOwnership(asRecord(response.ownership));
      } catch (error) {
        setTrafficOwnership(null);
        setTrafficOwnershipError(normalizeOperationError(error));
      } finally {
        setIsTrafficOwnershipLoading(false);
      }
    },
    [requestAdmin, setIsTrafficOwnershipLoading, setTrafficOwnership, setTrafficOwnershipError]
  );

  const clearTrafficOwnershipLookup = useCallback(() => {
    setTrafficLookupID("");
    setTrafficOwnership(null);
    setTrafficOwnershipError("");
    setIsTrafficOwnershipLoading(false);
  }, [setIsTrafficOwnershipLoading, setTrafficLookupID, setTrafficOwnership, setTrafficOwnershipError]);

  return {
    clearTrafficOwnershipLookup,
    lookupTrafficOwnership,
  };
}

export type AdminTrafficActions = ReturnType<typeof useAdminTrafficActions>;
