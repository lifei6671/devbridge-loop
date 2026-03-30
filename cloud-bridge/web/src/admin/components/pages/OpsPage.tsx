import { type ChangeEvent, type FormEvent, useEffect, useState } from "react";
import { toast } from "sonner";

import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import {
  asRecord,
  buildConfigUpdateYAMLDocument,
  buildOpsConfigDraft,
  buildOpsConfigPatch,
  deriveManagedCAIdentitySuggestion,
  formatConfigSnapshotAsYAML,
  formatOpsConfigSource,
  listOpsConfigFields,
  opsConfigSections,
  readIssuedConnectorTokenRecord,
  readEditableConfigFilePatch,
  readNumber,
  readOpsConfigFileSource,
  readOpsConfigSource,
  readOpsConfigValue,
  readText,
  toIssuedConnectorTokenView,
  validateOpsConfigPatch,
} from "../../model";
import { ConfigSectionCard } from "../ops/ConfigSectionCard";
import { LineNumberCodeBlock } from "../ops/LineNumberCodeBlock";

type OpsPageProps = {
  vm: AdminConsoleViewModel;
};

type OpsDraftState = {
  baseVersion: number;
  touched: boolean;
  values: Record<string, unknown>;
};

type YAMLEditorState = {
  baseVersion: number;
  document: string;
  touched: boolean;
};

export function OpsPage(props: OpsPageProps) {
  const {
    configSnapshot,
    connectorTokenItems,
    createConnectorToken,
    drainConnectorID,
    drainReason,
    drainSessionID,
    exportDownloadURL,
    performDrainConnector,
    performDrainSession,
    performExportDiagnose,
    performReload,
    refreshPageData,
    revokeConnectorToken,
    rotateConnectorToken,
    setDrainConnectorID,
    setDrainReason,
    setDrainSessionID,
    submitConfigPatch,
    submitConfigPatchDocument,
  } = props.vm;

  const configVersion = readNumber(configSnapshot, "config_version");
  const [draftState, setDraftState] = useState<OpsDraftState>(() => ({
    baseVersion: configVersion,
    touched: false,
    values: buildOpsConfigDraft(configSnapshot),
  }));
  const [yamlEditor, setYAMLEditor] = useState<YAMLEditorState>(() => ({
    baseVersion: configVersion,
    document: buildConfigUpdateYAMLDocument(configVersion, {}),
    touched: false,
  }));
  const [isSavingConfig, setIsSavingConfig] = useState(false);
  const [isSavingYAML, setIsSavingYAML] = useState(false);
  const [restoringFieldKey, setRestoringFieldKey] = useState<string | null>(null);
  const [tokenConnectorID, setTokenConnectorID] = useState("");
  const [tokenNote, setTokenNote] = useState("");
  const [issuedTokenResult, setIssuedTokenResult] = useState<Record<string, unknown> | null>(null);
  const [isIssuedTokenDialogOpen, setIsIssuedTokenDialogOpen] = useState(false);
  const [activeTokenMutationKey, setActiveTokenMutationKey] = useState("");

  useEffect(() => {
    if (configVersion <= 0) {
      return;
    }
    if (draftState.touched === true) {
      return;
    }
    if (draftState.baseVersion === configVersion) {
      return;
    }
    setDraftState({
      baseVersion: configVersion,
      touched: false,
      values: buildOpsConfigDraft(configSnapshot),
    });
  }, [configSnapshot, configVersion, draftState.baseVersion, draftState.touched]);

  const dirtyPatch = buildOpsConfigPatch(configSnapshot, draftState.values);
  const dirtyPatchSignature = JSON.stringify(dirtyPatch);
  const dirtyCount = Object.keys(dirtyPatch).length;
  const editableConfigFilePatch = readEditableConfigFilePatch(configSnapshot);
  const editableConfigFilePatchRootCount = Object.keys(editableConfigFilePatch).length;
  const yamlSnapshot = formatConfigSnapshotAsYAML(configSnapshot);
  const yamlExample = buildConfigUpdateYAMLDocument(configVersion, {
    "admin.base_path": "/console",
    "observability.log_level": "debug",
  });
  const hasRemoteSnapshotChange =
    draftState.touched === true && draftState.baseVersion > 0 && configVersion > 0
      ? draftState.baseVersion === configVersion
        ? false
        : true
      : false;
  const configFilePath = readText(
    configSnapshot,
    "config_file_path",
    "未解析到可写配置文件"
  );
  const configFileSource = readOpsConfigFileSource(configSnapshot);
  const configFileSourceLabel = formatOpsConfigSource(configFileSource);
  const managedCAHint = buildManagedCAHint({
    controlPlaneListenAddr: String(draftState.values["control_plane.listen_addr"] ?? ""),
    controlPlaneGRPCH2ListenAddr: String(draftState.values["control_plane.grpc_h2_listen_addr"] ?? ""),
    controlPlaneQUICListenAddr: String(draftState.values["control_plane.quic_listen_addr"] ?? ""),
    tlsMode: String(draftState.values["control_plane.tls_mode"] ?? ""),
    tlsCertSource: String(draftState.values["control_plane.tls_cert_source"] ?? ""),
    tlsCACertFile: String(draftState.values["control_plane.tls_ca_cert_file"] ?? ""),
    tlsCAKeyFile: String(draftState.values["control_plane.tls_ca_key_file"] ?? ""),
    tlsServerCommonName: String(draftState.values["control_plane.tls_server_common_name"] ?? ""),
    tlsServerSANDNS: String(draftState.values["control_plane.tls_server_san_dns"] ?? ""),
    tlsServerSANIPs: String(draftState.values["control_plane.tls_server_san_ips"] ?? ""),
    configFilePath,
    configFileSourceLabel,
  });
  const baseConfigFilePath = readText(
    configSnapshot,
    "base_config_file_path",
    "未检测到基础配置文件"
  );
  const connectorAuthSnapshot = asRecord(configSnapshot.connector_auth);
  const connectorTokenStoreSnapshot = asRecord(connectorAuthSnapshot.token_store);
  const connectorTokenStoreFileSnapshot = asRecord(connectorTokenStoreSnapshot.file);
  const connectorTokenStoreDriver = readText(connectorTokenStoreSnapshot, "driver", "--");
  const connectorTokenStoreFilePath = readText(connectorTokenStoreFileSnapshot, "path", "");
  const sourceSummary = {
    default: 0,
    env: 0,
    explicit: 0,
    local: 0,
    system: 0,
    user: 0,
  };

  useEffect(() => {
    if (yamlEditor.touched === true) {
      return;
    }
    const nextPatch = dirtyCount > 0 ? dirtyPatch : {};
    const nextDocument = buildConfigUpdateYAMLDocument(configVersion, nextPatch);
    setYAMLEditor((previousState) => {
      if (
        previousState.baseVersion === configVersion &&
        previousState.document === nextDocument &&
        previousState.touched === false
      ) {
        return previousState;
      }
      return {
        baseVersion: configVersion,
        document: nextDocument,
        touched: false,
      };
    });
  }, [configVersion, dirtyCount, dirtyPatchSignature, yamlEditor.touched]);

  for (const field of listOpsConfigFields()) {
    const source = readOpsConfigSource(configSnapshot, field.key);
    if (source === "env") {
      sourceSummary.env += 1;
      continue;
    }
    if (source === "explicit") {
      sourceSummary.explicit += 1;
      continue;
    }
    if (source === "local") {
      sourceSummary.local += 1;
      continue;
    }
    if (source === "user") {
      sourceSummary.user += 1;
      continue;
    }
    if (source === "system") {
      sourceSummary.system += 1;
      continue;
    }
    sourceSummary.default += 1;
  }

  const syncSavedSnapshotToDraft = (savedSnapshot: Record<string, unknown>) => {
    const nextVersion = readNumber(savedSnapshot, "config_version", configVersion);
    setDraftState({
      baseVersion: nextVersion,
      touched: false,
      values: buildOpsConfigDraft(savedSnapshot),
    });
    setYAMLEditor({
      baseVersion: nextVersion,
      document: buildConfigUpdateYAMLDocument(nextVersion, {}),
      touched: false,
    });
  };

  const mergeRestoredFieldIntoDraft = (fieldKey: string, savedSnapshot: Record<string, unknown>) => {
    const nextVersion = readNumber(savedSnapshot, "config_version", configVersion);
    const restoredValue = readOpsConfigValue(savedSnapshot, fieldKey);
    setDraftState((previousState) => {
      const nextValues = {
        ...previousState.values,
        [fieldKey]: restoredValue,
      };
      return {
        baseVersion: nextVersion,
        touched: Object.keys(buildOpsConfigPatch(savedSnapshot, nextValues)).length > 0,
        values: nextValues,
      };
    });
  };

  const handleFieldChange = (fieldKey: string, value: unknown) => {
    setDraftState((previousState) => ({
      baseVersion: previousState.baseVersion > 0 ? previousState.baseVersion : configVersion,
      touched: true,
      values: {
        ...previousState.values,
        [fieldKey]: value,
      },
    }));
  };

  const handleResetDraft = () => {
    setDraftState({
      baseVersion: configVersion,
      touched: false,
      values: buildOpsConfigDraft(configSnapshot),
    });
  };

  const handleSaveDraft = async () => {
    if (configVersion <= 0) {
      toast.error("配置快照尚未加载完成，请先刷新后再试。");
      return;
    }
    if (dirtyCount === 0) {
      toast.info("当前没有需要保存的配置改动。");
      return;
    }
    const validationError = validateOpsConfigPatch(dirtyPatch);
    if (validationError === null) {
    } else {
      toast.error(validationError);
      return;
    }
    setIsSavingConfig(true);
    try {
      const savedSnapshot = await submitConfigPatch(dirtyPatch, {
        successMessage: `已写入${configFileSourceLabel}配置文件，${dirtyCount} 项改动待重启生效。`,
      });
      if (savedSnapshot === null) {
        return;
      }
      syncSavedSnapshotToDraft(savedSnapshot);
    } finally {
      setIsSavingConfig(false);
    }
  };

  const handleRestoreFieldToInherited = async (fieldKey: string) => {
    const fieldLabel = listOpsConfigFields().find((field) => field.key === fieldKey)?.label ?? fieldKey;
    setRestoringFieldKey(fieldKey);
    try {
      const savedSnapshot = await submitConfigPatch(
        {
          [fieldKey]: null,
        },
        {
          successMessage: `已移除 ${fieldLabel} 在${configFileSourceLabel}中的配置。`,
        }
      );
      if (savedSnapshot === null) {
        return;
      }
      mergeRestoredFieldIntoDraft(fieldKey, savedSnapshot);
    } finally {
      setRestoringFieldKey(null);
    }
  };

  const handleYAMLDocumentChange = (event: ChangeEvent<HTMLTextAreaElement>) => {
    const nextDocument = event.target.value;
    setYAMLEditor((previousState) => ({
      baseVersion: previousState.baseVersion > 0 ? previousState.baseVersion : configVersion,
      document: nextDocument,
      touched: true,
    }));
  };

  const handleFillYAMLFromDirtyPatch = () => {
    if (dirtyCount === 0) {
      toast.info("当前没有未保存改动，已保留空 YAML 模板。");
      return;
    }
    setYAMLEditor({
      baseVersion: configVersion,
      document: buildConfigUpdateYAMLDocument(configVersion, dirtyPatch),
      touched: false,
    });
  };

  const handleFillYAMLFromEditableConfigFilePatch = () => {
    if (editableConfigFilePatchRootCount === 0) {
      toast.info(`${configFileSourceLabel}配置文件当前没有可编辑字段。`);
      return;
    }
    setYAMLEditor({
      baseVersion: configVersion,
      document: buildConfigUpdateYAMLDocument(configVersion, editableConfigFilePatch),
      touched: false,
    });
  };

  const handleResetYAMLDocument = () => {
    const seedPatch = dirtyCount > 0 ? dirtyPatch : {};
    setYAMLEditor({
      baseVersion: configVersion,
      document: buildConfigUpdateYAMLDocument(configVersion, seedPatch),
      touched: false,
    });
  };

  const handleSubmitYAMLDocument = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setIsSavingYAML(true);
    try {
      const savedSnapshot = await submitConfigPatchDocument(yamlEditor.document, {
        successMessage: `已按 YAML patch 写入${configFileSourceLabel}配置文件。`,
      });
      if (savedSnapshot === null) {
        return;
      }
      syncSavedSnapshotToDraft(savedSnapshot);
    } finally {
      setIsSavingYAML(false);
    }
  };

  const handleCreateConnectorToken = async (event: FormEvent<HTMLFormElement>) => {
    event.preventDefault();
    setActiveTokenMutationKey("create");
    try {
      const result = await createConnectorToken({
        connectorID: tokenConnectorID,
        note: tokenNote,
      });
      if (result === null) {
        return;
      }
      setIssuedTokenResult(result);
      setIsIssuedTokenDialogOpen(true);
      setTokenConnectorID("");
      setTokenNote("");
    } finally {
      setActiveTokenMutationKey("");
    }
  };

  const handleRotateConnectorToken = async (tokenID: string) => {
    const normalizedTokenID = tokenID.trim();
    if (normalizedTokenID === "") {
      return;
    }
    setActiveTokenMutationKey(`rotate:${normalizedTokenID}`);
    try {
      const result = await rotateConnectorToken(normalizedTokenID);
      if (result !== null) {
        setIssuedTokenResult(result);
        setIsIssuedTokenDialogOpen(true);
      }
    } finally {
      setActiveTokenMutationKey("");
    }
  };

  const handleRevokeConnectorToken = async (tokenID: string) => {
    const normalizedTokenID = tokenID.trim();
    if (normalizedTokenID === "") {
      return;
    }
    if (window.confirm(`确认吊销 token ${normalizedTokenID} 吗？旧 Agent 将无法继续认证。`) === false) {
      return;
    }
    setActiveTokenMutationKey(`revoke:${normalizedTokenID}`);
    try {
      await revokeConnectorToken(normalizedTokenID);
      if (readText(asRecord(issuedTokenResult?.record), "token_id") === normalizedTokenID) {
        setIssuedTokenResult(null);
        setIsIssuedTokenDialogOpen(false);
      }
    } finally {
      setActiveTokenMutationKey("");
    }
  };

  const issuedTokenView = toIssuedConnectorTokenView(issuedTokenResult);
  const issuedTokenRecord = readIssuedConnectorTokenRecord(issuedTokenResult);
  const issuedPlainToken = issuedTokenView.plainToken;
  return (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head ops-config-head">
          <div>
            <h3>常用运行配置</h3>
            <span className="panel-sub">
              保存操作会把修改写入当前可编辑配置文件，环境变量仍然会覆盖同字段的生效值。
            </span>
          </div>
          <div className="ops-config-head-badges">
            <Badge variant={dirtyCount > 0 ? "warning" : "outline"}>待保存 {dirtyCount}</Badge>
            <Badge variant="secondary">版本 {readText(configSnapshot, "config_version", "--")}</Badge>
            <Badge variant="outline">显式 -config &gt; 环境变量 &gt; 程序目录 &gt; 用户目录 &gt; 系统目录 &gt; 默认值</Badge>
            {hasRemoteSnapshotChange === true ? (
              <Badge variant="warning">后台快照已更新，当前草稿已保留</Badge>
            ) : null}
          </div>
        </header>

        <div className="ops-config-meta-grid">
          <article className="ops-config-meta-card">
            <span className="ops-config-meta-label">当前写回目标</span>
            <code className="ops-config-path">{configFilePath}</code>
            <p>当前会写回{configFileSourceLabel}；环境变量不会被回写，且仍保持更高优先级。</p>
          </article>

          <article className="ops-config-meta-card">
            <span className="ops-config-meta-label">基础配置层</span>
            <code className="ops-config-path">{baseConfigFilePath}</code>
            <p>这里展示当前最高优先级的基础配置文件路径，可能来自显式 `-config`、程序运行目录或系统目录。</p>
          </article>

          <article className="ops-config-meta-card">
            <span className="ops-config-meta-label">来源概览</span>
            <div className="ops-config-source-row">
              <Badge variant="danger">{formatOpsConfigSource("explicit")} {sourceSummary.explicit}</Badge>
              <Badge variant="warning">{formatOpsConfigSource("env")} {sourceSummary.env}</Badge>
              <Badge variant="secondary">{formatOpsConfigSource("local")} {sourceSummary.local}</Badge>
              <Badge variant="success">{formatOpsConfigSource("user")} {sourceSummary.user}</Badge>
              <Badge variant="secondary">{formatOpsConfigSource("system")} {sourceSummary.system}</Badge>
              <Badge variant="outline">{formatOpsConfigSource("default")} {sourceSummary.default}</Badge>
            </div>
            <p>每个字段都会显示当前生效值来自哪一层，方便定位覆盖关系。</p>
          </article>

          <article className="ops-config-meta-card ops-config-meta-card-actions">
            <span className="ops-config-meta-label">表单操作</span>
            <div className="ops-config-actions">
              <Button onClick={() => void handleSaveDraft()} disabled={isSavingConfig === true || dirtyCount === 0}>
                {isSavingConfig === true ? "写入中..." : `保存到${configFileSourceLabel}`}
              </Button>
              <Button
                variant="outline"
                onClick={handleResetDraft}
                disabled={dirtyCount === 0 && hasRemoteSnapshotChange === false}
              >
                重置未保存改动
              </Button>
            </div>
            <p>所有常用字段都通过组件化表单提交，仍保留高级 YAML patch 作为兜底入口。</p>
          </article>
        </div>

        {managedCAHint !== null ? (
          <article className="ops-config-managed-ca-note">
            <div className="ops-config-managed-ca-head">
              <div>
                <h4>Managed CA 自动补齐提示</h4>
                <p>{managedCAHint.summary}</p>
              </div>
              <Badge variant={managedCAHint.tlsMode === "plaintext" ? "outline" : "warning"}>
                {managedCAHint.tlsMode === "plaintext" ? "当前仅提示" : "保存时会补齐"}
              </Badge>
            </div>

            <div className="ops-config-managed-ca-grid">
              <div className="ops-config-managed-ca-item">
                <span>默认 Root CA 证书</span>
                <code>{managedCAHint.defaultCACertFile}</code>
              </div>
              <div className="ops-config-managed-ca-item">
                <span>默认 Root CA 私钥</span>
                <code>{managedCAHint.defaultCAKeyFile}</code>
              </div>
              <div className="ops-config-managed-ca-item">
                <span>预计自动 SAN DNS</span>
                <code>{managedCAHint.suggestedSANDNS}</code>
              </div>
              <div className="ops-config-managed-ca-item">
                <span>预计自动 SAN IP / CN</span>
                <code>{managedCAHint.suggestedSANIPsAndCN}</code>
              </div>
              <div className="ops-config-managed-ca-item">
                <span>当前表单状态</span>
                <code>{managedCAHint.currentBehavior}</code>
              </div>
            </div>

            <p className="ops-config-managed-ca-footnote">
              {managedCAHint.footnote}
            </p>
          </article>
        ) : null}

        <div className="config-section-grid">
          {opsConfigSections.map((section) => (
            <ConfigSectionCard
              key={section.key}
              section={section}
              snapshot={configSnapshot}
              draft={draftState.values}
              editableSource={configFileSource}
              onResetToInherited={handleRestoreFieldToInherited}
              onValueChange={handleFieldChange}
              resettingFieldKey={restoringFieldKey}
            />
          ))}
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <div>
            <h3>Connector Token 管理</h3>
            <span className="panel-sub">
              Bridge 负责签发并持久化 connector token。明文 token 只会在创建或轮换当次返回一次。
            </span>
          </div>
          <div className="inline-actions">
            <Badge variant={connectorTokenStoreDriver === "file" ? "success" : "warning"}>
              store {connectorTokenStoreDriver}
            </Badge>
            <Badge variant="secondary">共 {connectorTokenItems.length} 条</Badge>
            <Button variant="outline" size="sm" onClick={() => void refreshPageData("ops")}>
              刷新 token
            </Button>
          </div>
        </header>

        <div className="ops-grid">
          <article className="ops-card">
            <h4>签发新 token</h4>
            <p>创建后会立即写入 Bridge token store；请把明文 token 安全同步到对应 Agent 配置。</p>
            <form className="field-stack" onSubmit={(event) => void handleCreateConnectorToken(event)}>
              <label>
                <span>Connector ID</span>
                <Input
                  value={tokenConnectorID}
                  onChange={(event) => setTokenConnectorID(event.target.value)}
                  placeholder="agent-local"
                />
              </label>
              <label>
                <span>备注（可选）</span>
                <Input
                  value={tokenNote}
                  onChange={(event) => setTokenNote(event.target.value)}
                  placeholder="例如：生产机房首批接入"
                />
              </label>
              <div className="inline-actions">
                <Button type="submit" disabled={activeTokenMutationKey === "create"}>
                  {activeTokenMutationKey === "create" ? "签发中..." : "创建 token"}
                </Button>
              </div>
            </form>
          </article>

          <article className="ops-card">
            <h4>最近一次明文 token</h4>
            <p>创建或轮换成功后会自动打开模态窗展示一次；刷新页面后不会再回显。</p>
            {issuedPlainToken !== "" ? (
              <div className="snapshot-box field-stack">
                <p>最近一次签发对象：{issuedTokenView.connectorID}</p>
                <p>Token ID：{issuedTokenView.tokenID}</p>
                <div className="inline-actions">
                  <Button size="sm" variant="outline" onClick={() => setIsIssuedTokenDialogOpen(true)}>
                    重新查看本次明文
                  </Button>
                  <Button
                    size="sm"
                    variant="ghost"
                    onClick={() => {
                      setIssuedTokenResult(null);
                      setIsIssuedTokenDialogOpen(false);
                    }}
                  >
                    清除
                  </Button>
                </div>
              </div>
            ) : (
              <div className="snapshot-box">
                <p>当前还没有新签发的明文 token。创建或轮换后会以模态窗形式展示一次。</p>
              </div>
            )}
            {connectorTokenStoreFilePath !== "" ? (
              <p className="panel-sub">当前 file store 路径：{connectorTokenStoreFilePath}</p>
            ) : null}
          </article>
        </div>

        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Token ID</th>
                <th>Connector</th>
                <th>状态</th>
                <th>签发时间</th>
                <th>轮换时间</th>
                <th>备注</th>
                <th>操作</th>
              </tr>
            </thead>
            <tbody>
              {connectorTokenItems.map((item, index) => {
                const tokenID = readText(item, "token_id", `token-${index}`);
                const metadataRecord = asRecord(item.metadata);
                const tokenStatus = readText(item, "status", "unknown");
                const isRotating = activeTokenMutationKey === `rotate:${tokenID}`;
                const isRevoking = activeTokenMutationKey === `revoke:${tokenID}`;
                return (
                  <tr key={`${tokenID}-${index}`}>
                    <td><code>{tokenID}</code></td>
                    <td>{readText(item, "connector_id", "--")}</td>
                    <td>
                      <Badge
                        variant={
                          tokenStatus === "active"
                            ? "success"
                            : tokenStatus === "revoked"
                              ? "danger"
                              : "secondary"
                        }
                      >
                        {tokenStatus}
                      </Badge>
                    </td>
                    <td>{readText(item, "issued_at_ms", "--")}</td>
                    <td>{readText(item, "rotated_at_ms", "--")}</td>
                    <td>{readText(metadataRecord, "note", "--")}</td>
                    <td>
                      <div className="inline-actions">
                        <Button
                          size="sm"
                          variant="outline"
                          onClick={() => void handleRotateConnectorToken(tokenID)}
                          disabled={isRotating || tokenStatus === "revoked"}
                        >
                          {isRotating ? "轮换中..." : "轮换"}
                        </Button>
                        <Button
                          size="sm"
                          variant="destructive"
                          onClick={() => void handleRevokeConnectorToken(tokenID)}
                          disabled={isRevoking || tokenStatus === "revoked"}
                        >
                          {isRevoking ? "吊销中..." : "吊销"}
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
              {connectorTokenItems.length === 0 ? (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    当前还没有 connector token，先为目标 Agent 创建一条即可。
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <div>
            <h3>受控运维命令</h3>
            <span className="panel-sub">写操作将记录审计日志</span>
          </div>
        </header>
        <div className="ops-grid">
          <article className="ops-card">
            <h4>配置重载</h4>
            <p>触发 `POST /api/admin/ops/config/reload`。</p>
            <Button className="ops-action-btn ops-action-reload" onClick={() => void performReload()}>
              触发 reload
            </Button>
          </article>

          <article className="ops-card">
            <h4>Session Drain</h4>
            <p>把指定会话标记为 DRAINING 并收敛 service/tunnel。</p>
            <div className="field-stack">
              <label>
                <span>Session ID</span>
                <Input
                  value={drainSessionID}
                  onChange={(event) => setDrainSessionID(event.target.value)}
                  placeholder="session-xxx"
                />
              </label>
              <label>
                <span>Reason</span>
                <Input
                  value={drainReason}
                  onChange={(event) => setDrainReason(event.target.value)}
                  placeholder="manual_ops"
                />
              </label>
            </div>
            <Button
              variant="destructive"
              className="ops-action-btn ops-action-drain-session"
              onClick={() => void performDrainSession()}
            >
              Drain Session
            </Button>
          </article>

          <article className="ops-card">
            <h4>Connector Drain</h4>
            <p>按 connector 当前会话执行 drain。</p>
            <div className="field-stack">
              <label>
                <span>Connector ID</span>
                <Input
                  value={drainConnectorID}
                  onChange={(event) => setDrainConnectorID(event.target.value)}
                  placeholder="connector-xxx"
                />
              </label>
            </div>
            <Button
              variant="destructive"
              className="ops-action-btn ops-action-drain-connector"
              onClick={() => void performDrainConnector()}
            >
              Drain Connector
            </Button>
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <div>
            <h3>高级 YAML patch / YAML 快照</h3>
            <span className="panel-sub">
              当常用配置项未覆盖目标字段时，可以直接编辑 YAML patch；当前快照也按 YAML 展示。
            </span>
          </div>
        </header>
        <div className="ops-yaml-grid">
          <article className="ops-yaml-card">
            <div className="ops-yaml-toolbar">
              <div>
                <h4>YAML Patch 编辑器</h4>
                <p>
                  提交格式为 `application/yaml`。支持嵌套结构，后端会自动按字段路径展开并写入当前可编辑配置文件。
                </p>
              </div>
              <div className="ops-config-head-badges">
                <Badge variant="secondary">版本 {configVersion > 0 ? configVersion : "--"}</Badge>
                <Badge variant={yamlEditor.touched === true ? "warning" : "outline"}>
                  {yamlEditor.touched === true ? "已修改" : "未修改"}
                </Badge>
              </div>
            </div>

            <div className="ops-config-actions">
              <Button variant="outline" onClick={handleFillYAMLFromDirtyPatch}>
                用当前改动生成 YAML
              </Button>
              <Button
                variant="outline"
                onClick={handleFillYAMLFromEditableConfigFilePatch}
                disabled={editableConfigFilePatchRootCount === 0}
              >
                回填当前文件 patch
              </Button>
              <Button variant="outline" onClick={handleResetYAMLDocument}>
                重置 YAML 模板
              </Button>
            </div>

            <form className="ops-yaml-form" onSubmit={(event) => void handleSubmitYAMLDocument(event)}>
              <label className="ops-yaml-label">
                <span>YAML Patch</span>
                <textarea
                  className="ops-yaml-editor"
                  value={yamlEditor.document}
                  onChange={handleYAMLDocumentChange}
                  spellCheck={false}
                />
              </label>
              <div className="ops-yaml-submit-row">
                <Badge variant="outline">application/yaml</Badge>
                <Button type="submit" disabled={isSavingYAML === true}>
                  {isSavingYAML === true ? "提交中..." : "提交 YAML patch"}
                </Button>
              </div>
            </form>

            <div className="ops-yaml-example">
              <span className="ops-config-meta-label">示例</span>
              <div className="snapshot-box ops-yaml-preview-box">
                <LineNumberCodeBlock code={yamlExample} />
              </div>
            </div>
          </article>

          <article className="ops-yaml-card">
            <div className="ops-yaml-toolbar">
              <div>
                <h4>当前快照（YAML）</h4>
                <p>这个预览展示的是当前已经合并完成的生效配置快照，不是底层配置文件的逐字节原文。</p>
              </div>
            </div>
            <div className="snapshot-box ops-yaml-preview-box">
              <LineNumberCodeBlock code={yamlSnapshot} />
            </div>
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <div>
            <h3>诊断导出</h3>
            <span className="panel-sub">仅 admin 角色可调用</span>
          </div>
        </header>
        <div className="inline-actions">
          <Button onClick={() => void performExportDiagnose()}>生成导出链接</Button>
          {exportDownloadURL !== "" ? (
            <a className="link-btn" href={exportDownloadURL} target="_blank" rel="noreferrer">
              下载诊断包
            </a>
          ) : null}
        </div>
      </section>

      {isIssuedTokenDialogOpen && issuedPlainToken !== "" ? (
        <div
          className="auth-overlay"
          role="dialog"
          aria-modal="true"
          aria-labelledby="issued-token-dialog-title"
        >
          <div className="auth-backdrop" onClick={() => setIsIssuedTokenDialogOpen(false)} />
          <div className="auth-dialog panel issued-token-dialog">
            <div className="auth-dialog-head">
              <p className="auth-kicker">Connector Token</p>
              <h2 id="issued-token-dialog-title">一次性明文 Token</h2>
              <p className="auth-sub">
                该明文只在本次创建或轮换后展示一次。请立即复制到目标 Agent 的
                <code> session.auth_token </code>
                配置中，关闭或刷新页面后将不再回显。
              </p>
            </div>

            <div className="field-stack">
              <label>
                <span>Connector</span>
                <Input value={readText(issuedTokenRecord, "connector_id", "--")} readOnly />
              </label>
              <label>
                <span>Token ID</span>
                <Input value={readText(issuedTokenRecord, "token_id", "--")} readOnly />
              </label>
              <label>
                <span>明文 token</span>
                <Input value={issuedPlainToken} readOnly />
              </label>
            </div>

            <div className="auth-actions issued-token-actions">
              <Button variant="outline" onClick={() => setIsIssuedTokenDialogOpen(false)}>
                关闭
              </Button>
              <Button
                onClick={async () => {
                  try {
                    await navigator.clipboard.writeText(issuedPlainToken);
                    toast.success("已复制本次签发的明文 token。");
                  } catch {
                    toast.error("复制失败，请手动选择后复制。");
                  }
                }}
              >
                复制 Token
              </Button>
            </div>
          </div>
        </div>
      ) : null}
    </div>
  );
}

type ManagedCAHintInput = {
  configFilePath: string;
  configFileSourceLabel: string;
  controlPlaneGRPCH2ListenAddr: string;
  controlPlaneListenAddr: string;
  controlPlaneQUICListenAddr: string;
  tlsMode: string;
  tlsCertSource: string;
  tlsCACertFile: string;
  tlsCAKeyFile: string;
  tlsServerCommonName: string;
  tlsServerSANDNS: string;
  tlsServerSANIPs: string;
};

type ManagedCAHint = {
  currentBehavior: string;
  defaultCACertFile: string;
  defaultCAKeyFile: string;
  footnote: string;
  suggestedSANDNS: string;
  suggestedSANIPsAndCN: string;
  summary: string;
  tlsMode: string;
};

function buildManagedCAHint(input: ManagedCAHintInput): ManagedCAHint | null {
  const tlsCertSource = input.tlsCertSource.trim().toLowerCase();
  if (tlsCertSource !== "managed_ca") {
    return null;
  }

  const tlsMode = input.tlsMode.trim().toLowerCase();
  const defaultPaths = deriveManagedCAPaths(input.configFilePath);
  const suggestedIdentity = deriveManagedCAIdentitySuggestion({
    controlPlaneListenAddr: input.controlPlaneListenAddr,
    controlPlaneGRPCH2ListenAddr: input.controlPlaneGRPCH2ListenAddr,
    controlPlaneQUICListenAddr: input.controlPlaneQUICListenAddr,
  });
  const hasCACertFile = input.tlsCACertFile.trim() !== "";
  const hasCAKeyFile = input.tlsCAKeyFile.trim() !== "";
  const hasServerIdentity =
    input.tlsServerCommonName.trim() !== "" ||
    input.tlsServerSANDNS.trim() !== "" ||
    input.tlsServerSANIPs.trim() !== "";

  let currentBehavior = "当前会使用你手动填写的 CA 路径和证书标识。";
  if (tlsMode === "" || tlsMode === "plaintext") {
    currentBehavior = "当前 TLS 仍是 plaintext；切到 optional/required 后，Managed CA 才会真正参与校验与签发。";
  } else if (hasCACertFile === false || hasCAKeyFile === false || hasServerIdentity === false) {
    currentBehavior = `留空字段会在保存时自动补齐，并在默认${input.configFileSourceLabel}路径下初始化 Root CA 文件；按当前监听地址预计会写入 ${suggestedIdentity.behaviorSummary}。`;
  } else {
    currentBehavior = `当前会使用你手动填写的 CA 路径和证书标识；若重新留空，会回退到 ${suggestedIdentity.behaviorSummary}。手动指定自定义 CA 路径时，Root CA 初始化仍以 reload 或启动阶段为准。`;
  }

  return {
    currentBehavior,
    defaultCACertFile: defaultPaths.caCertFile,
    defaultCAKeyFile: defaultPaths.caKeyFile,
    footnote:
      "如果 Bridge 需要被其他机器、域名或公网 IP 访问，请让监听地址和 SAN/CN 使用同一组真实标识，再让 Agent 按该标识校验证书。",
    suggestedSANDNS: suggestedIdentity.sanDNS,
    suggestedSANIPsAndCN: suggestedIdentity.sanIPsAndCN,
    summary:
      `首次切到 Managed CA 时，后台会为留空的 Root CA 路径与证书标识自动补默认值，并在保存后为默认${input.configFileSourceLabel}路径初始化 Root CA 文件；现在这组提示会跟着当前监听地址一起推导。`,
    tlsMode,
  };
}

function deriveManagedCAPaths(configFilePath: string) {
  const normalizedPath = configFilePath.trim();
  if (normalizedPath === "" || normalizedPath.includes("未解析到")) {
    return {
      caCertFile: "~/.config/devbridge/root-ca.crt",
      caKeyFile: "~/.config/devbridge/root-ca.key",
    };
  }
  const lastSlashIndex = Math.max(normalizedPath.lastIndexOf("/"), normalizedPath.lastIndexOf("\\"));
  if (lastSlashIndex < 0) {
    return {
      caCertFile: "~/.config/devbridge/root-ca.crt",
      caKeyFile: "~/.config/devbridge/root-ca.key",
    };
  }
  const separator = normalizedPath[lastSlashIndex];
  const configDirectory = normalizedPath.slice(0, lastSlashIndex);
  return {
    caCertFile: `${configDirectory}${separator}root-ca.crt`,
    caKeyFile: `${configDirectory}${separator}root-ca.key`,
  };
}

type ManagedCAIdentitySuggestion = {
  behaviorSummary: string;
  sanDNS: string;
  sanIPsAndCN: string;
};
