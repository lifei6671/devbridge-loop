import { type FormEvent, useCallback } from "react";
import { toast } from "sonner";

import type { AdminPageKey, ApiRecord } from "../model";
import {
  asPrettyTime,
  asRecord,
  normalizeOperationError,
  parsePatchValue,
  readNumber,
  readText,
} from "../model";
import type { AdminConsoleState } from "./useAdminConsoleState";
import type { AdminRequestFn, RefreshPageDataFn } from "./useAdminDataActions";

type UseAdminOpsActionsParams = {
  navigateToPage: (page: AdminPageKey, options?: { replace?: boolean }) => void;
  refreshPageData: RefreshPageDataFn;
  requestAdmin: AdminRequestFn;
  state: AdminConsoleState;
};

type SubmitConfigPatchOptions = {
  successMessage?: string;
};

type ConnectorTokenCreateInput = {
  connectorID: string;
  note?: string;
};

export type SubmitConfigPatchFn = (
  patch: Record<string, unknown>,
  options?: SubmitConfigPatchOptions
) => Promise<ApiRecord | null>;

export type SubmitConfigPatchDocumentFn = (
  documentText: string,
  options?: SubmitConfigPatchOptions
) => Promise<ApiRecord | null>;

export function useAdminOpsActions(params: UseAdminOpsActionsParams) {
  const { navigateToPage, refreshPageData, requestAdmin, state } = params;
  const {
    configSnapshot,
    drainConnectorID,
    drainReason,
    drainSessionID,
    patchKey,
    patchValue,
    setDrainConnectorID,
    setDrainSessionID,
    setExportDownloadURL,
  } = state;

  const performReload = useCallback(async () => {
    try {
      const response = await requestAdmin("/api/admin/ops/config/reload", {
        method: "POST",
      });
      const result = asRecord(response.result);
      toast.success(
        `配置已触发重载，版本 ${readNumber(result, "config_version")}，时间 ${asPrettyTime(
          readNumber(result, "reloaded_at_ms")
        )}`
      );
      await refreshPageData("ops");
    } catch (error) {
      toast.error(normalizeOperationError(error));
    }
  }, [refreshPageData, requestAdmin]);

  const requestSessionDrain = useCallback(
    async (sessionID: string, reason: string) => {
      const normalizedSessionID = sessionID.trim();
      if (normalizedSessionID === "") {
        throw new Error("请先输入 Session ID");
      }
      const normalizedReason = reason.trim() || "manual_ops";
      const response = await requestAdmin(`/api/admin/ops/session/${normalizedSessionID}/drain`, {
        method: "POST",
        body: JSON.stringify({
          reason: normalizedReason,
        }),
      });
      const result = asRecord(response.result);
      const message = `Session ${readText(result, "session_id")} -> ${readText(
        result,
        "current_state"
      )}，purged_tunnels=${readText(result, "purged_tunnel_count", "0")}`;
      await refreshPageData("connectors");
      await refreshPageData("traffic");
      return message;
    },
    [refreshPageData, requestAdmin]
  );

  const executeSessionDrain = useCallback(
    async (sessionID: string, reason: string) => {
      try {
        const message = await requestSessionDrain(sessionID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestSessionDrain]
  );

  const performDrainSession = useCallback(async () => {
    await executeSessionDrain(drainSessionID, drainReason);
  }, [drainReason, drainSessionID, executeSessionDrain]);

  const requestConnectorDrain = useCallback(
    async (connectorID: string, reason: string) => {
      const normalizedConnectorID = connectorID.trim();
      if (normalizedConnectorID === "") {
        throw new Error("请先输入 Connector ID");
      }
      const normalizedReason = reason.trim() || "manual_ops";
      const response = await requestAdmin(`/api/admin/ops/connector/${normalizedConnectorID}/drain`, {
        method: "POST",
        body: JSON.stringify({
          reason: normalizedReason,
        }),
      });
      const result = asRecord(response.result);
      const message = `Connector ${readText(result, "connector_id")} drain 完成，session=${readText(
        result,
        "session_id"
      )}，result=${readText(result, "result")}`;
      await refreshPageData("connectors");
      await refreshPageData("traffic");
      return message;
    },
    [refreshPageData, requestAdmin]
  );

  const executeConnectorDrain = useCallback(
    async (connectorID: string, reason: string) => {
      try {
        const message = await requestConnectorDrain(connectorID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestConnectorDrain]
  );

  const performDrainConnector = useCallback(async () => {
    await executeConnectorDrain(drainConnectorID, drainReason);
  }, [drainConnectorID, drainReason, executeConnectorDrain]);

  const prefillOpsFromDetail = useCallback(
    (target: "session" | "connector", targetID: string) => {
      const normalizedTargetID = targetID.trim();
      if (normalizedTargetID === "") {
        return;
      }
      if (target === "session") {
        setDrainSessionID(normalizedTargetID);
      } else {
        setDrainConnectorID(normalizedTargetID);
      }
      navigateToPage("ops");
      toast.info(`已填充 ${target}=${normalizedTargetID} 到 Ops 页面`);
    },
    [navigateToPage, setDrainConnectorID, setDrainSessionID]
  );

  const quickDrainFromDetail = useCallback(
    async (target: "session" | "connector", targetID: string) => {
      const normalizedTargetID = targetID.trim();
      if (normalizedTargetID === "") {
        return;
      }
      const reason = "detail_quick_drain";
      try {
        if (target === "session") {
          setDrainSessionID(normalizedTargetID);
          const message = await requestSessionDrain(normalizedTargetID, reason);
          toast.success(message);
          return;
        }
        setDrainConnectorID(normalizedTargetID);
        const message = await requestConnectorDrain(normalizedTargetID, reason);
        toast.success(message);
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [requestConnectorDrain, requestSessionDrain, setDrainConnectorID, setDrainSessionID]
  );

  const requestConfigPatch = useCallback(
    async (patch: Record<string, unknown>) => {
      const configVersion = readNumber(configSnapshot, "config_version");
      if (configVersion <= 0) {
        throw new Error("未读取到 config_version，请先刷新配置快照");
      }
      if (Object.keys(patch).length === 0) {
        throw new Error("没有需要提交的配置变更");
      }
      const response = await requestAdmin("/api/admin/config", {
        method: "PUT",
        body: JSON.stringify({
          if_match_version: configVersion,
          patch,
        }),
      });
      return asRecord(response.result);
    },
    [configSnapshot, requestAdmin]
  );

  const submitConfigPatch = useCallback<SubmitConfigPatchFn>(
    async (patch, options) => {
      try {
        const result = await requestConfigPatch(patch);
        const snapshot = asRecord(result.snapshot);
        toast.success(
          options?.successMessage ??
            `配置更新成功，new_version=${readText(
              result,
              "config_version",
              "--"
            )}，apply_mode=${readText(result, "apply_mode", "--")}`
        );
        await refreshPageData("ops");
        return snapshot;
      } catch (error) {
        toast.error(normalizeOperationError(error));
        return null;
      }
    },
    [refreshPageData, requestConfigPatch]
  );



  const submitConfigPatchDocument = useCallback<SubmitConfigPatchDocumentFn>(
    async (documentText, options) => {
      const normalizedDocument = documentText.trim();
      if (normalizedDocument === "") {
        toast.error("YAML patch 不能为空");
        return null;
      }
      try {
        const response = await requestAdmin("/api/admin/config", {
          method: "PUT",
          headers: {
            "Content-Type": "application/yaml",
          },
          body: normalizedDocument,
        });
        const result = asRecord(response.result);
        const snapshot = asRecord(result.snapshot);
        toast.success(
          options?.successMessage ??
            `YAML 配置更新成功，new_version=${readText(
              result,
              "config_version",
              "--"
            )}，apply_mode=${readText(result, "apply_mode", "--")}`
        );
        await refreshPageData("ops");
        return snapshot;
      } catch (error) {
        toast.error(normalizeOperationError(error));
        return null;
      }
    },
    [refreshPageData, requestAdmin]
  );

  const performConfigPatch = useCallback(
    async (event: FormEvent<HTMLFormElement>) => {
      event.preventDefault();
      try {
        await submitConfigPatch({
          [patchKey]: parsePatchValue(patchKey, patchValue),
        });
      } catch (error) {
        toast.error(normalizeOperationError(error));
      }
    },
    [patchKey, patchValue, submitConfigPatch]
  );

  const performExportDiagnose = useCallback(async () => {
    setExportDownloadURL("");
    try {
      const response = await requestAdmin("/api/admin/ops/diagnose/export", {
        method: "POST",
      });
      const downloadURL = readText(response, "download_url", "");
      if (downloadURL === "") {
        throw new Error("导出接口未返回 download_url");
      }
      setExportDownloadURL(downloadURL);
      toast.success(`诊断包已生成，过期时间 ${asPrettyTime(readNumber(response, "expires_at_ms"))}`);
    } catch (error) {
      toast.error(normalizeOperationError(error));
    }
  }, [requestAdmin, setExportDownloadURL]);

  const createConnectorToken = useCallback(
    async (input: ConnectorTokenCreateInput) => {
      const normalizedConnectorID = input.connectorID.trim();
      if (normalizedConnectorID === "") {
        toast.error("请先填写 Connector ID");
        return null;
      }
      try {
        const response = await requestAdmin("/api/admin/connector-tokens", {
          method: "POST",
          body: JSON.stringify({
            connector_id: normalizedConnectorID,
            metadata: input.note?.trim()
              ? {
                  note: input.note.trim(),
                }
              : undefined,
          }),
        });
        const result = asRecord(response.result);
        await refreshPageData("ops", { silentError: true });
        toast.success(`已为 ${normalizedConnectorID} 创建 token，明文仅本次可见。`);
        return result;
      } catch (error) {
        toast.error(normalizeOperationError(error));
        return null;
      }
    },
    [refreshPageData, requestAdmin]
  );

  const rotateConnectorToken = useCallback(
    async (tokenID: string) => {
      const normalizedTokenID = tokenID.trim();
      if (normalizedTokenID === "") {
        toast.error("未识别到可轮换的 token_id");
        return null;
      }
      try {
        const response = await requestAdmin(`/api/admin/connector-tokens/${normalizedTokenID}/rotate`, {
          method: "POST",
        });
        const result = asRecord(response.result);
        await refreshPageData("ops", { silentError: true });
        toast.success(`已轮换 token ${normalizedTokenID}，旧 token 已失效。`);
        return result;
      } catch (error) {
        toast.error(normalizeOperationError(error));
        return null;
      }
    },
    [refreshPageData, requestAdmin]
  );

  const revokeConnectorToken = useCallback(
    async (tokenID: string) => {
      const normalizedTokenID = tokenID.trim();
      if (normalizedTokenID === "") {
        toast.error("未识别到可吊销的 token_id");
        return null;
      }
      try {
        const response = await requestAdmin(`/api/admin/connector-tokens/${normalizedTokenID}/revoke`, {
          method: "POST",
        });
        const result = asRecord(response.result);
        await refreshPageData("ops", { silentError: true });
        toast.success(`已吊销 token ${normalizedTokenID}。`);
        return result;
      } catch (error) {
        toast.error(normalizeOperationError(error));
        return null;
      }
    },
    [refreshPageData, requestAdmin]
  );

  return {
    createConnectorToken,
    performConfigPatch,
    performDrainConnector,
    performDrainSession,
    performExportDiagnose,
    performReload,
    prefillOpsFromDetail,
    quickDrainFromDetail,
    revokeConnectorToken,
    rotateConnectorToken,
    submitConfigPatch,
    submitConfigPatchDocument,
  };
}

export type AdminOpsActions = ReturnType<typeof useAdminOpsActions>;
