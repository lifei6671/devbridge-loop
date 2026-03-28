import { Badge } from "../../../components/ui/badge";
import {
  Card,
  CardContent,
  CardDescription,
  CardHeader,
  CardTitle,
} from "../../../components/ui/card";
import {
  buildOpsConfigChangePreview,
  formatOpsConfigFieldValue,
  isOpsConfigFieldDirty,
  isOpsConfigFieldVisible,
  readOpsConfigRestorePreview,
  readOpsConfigSource,
  readOpsConfigValue,
  type OpsConfigSection,
} from "../../model/ops-config";
import type { ApiRecord } from "../../model";
import { ConfigFieldRow } from "./ConfigFieldRow";

type ConfigSectionCardProps = {
  draft: Record<string, unknown>;
  editableSource?: string;
  onResetToInherited?: (fieldKey: string) => void;
  onValueChange: (fieldKey: string, value: unknown) => void;
  resettingFieldKey?: string | null;
  section: OpsConfigSection;
  snapshot: ApiRecord;
};

export function ConfigSectionCard(props: ConfigSectionCardProps) {
  const {
    draft,
    editableSource = "user",
    onResetToInherited,
    onValueChange,
    resettingFieldKey = null,
    section,
    snapshot,
  } = props;
  const visibleFields = section.fields.filter((field) => isOpsConfigFieldVisible(draft, field));
  const hiddenFields = section.fields.filter((field) => isOpsConfigFieldVisible(draft, field) === false);
  const visibleDirtyCount = visibleFields.filter((field) => isOpsConfigFieldDirty(snapshot, draft, field)).length;
  const hiddenDirtyCount = hiddenFields.filter((field) => isOpsConfigFieldDirty(snapshot, draft, field)).length;
  const dirtyCount = visibleDirtyCount + hiddenDirtyCount;
  const tlsCertSource = readTLSCertSource(section.key, draft);
  const tlsSourceSummary = buildTLSSourceSummary(tlsCertSource, hiddenDirtyCount);

  return (
    <Card className="config-section-card">
      <CardHeader>
        <div className="config-section-head">
          <div>
            <CardTitle>{section.title}</CardTitle>
            <CardDescription>{section.description}</CardDescription>
          </div>
          <Badge variant={dirtyCount > 0 ? "warning" : "outline"}>变更 {dirtyCount}</Badge>
        </div>
      </CardHeader>
      <CardContent className="config-section-content">
        {tlsSourceSummary !== null ? (
          <div className={`config-section-mode-note config-section-mode-note-${tlsSourceSummary.tone}`}>
            <div className="config-section-mode-copy">
              <div className="config-section-mode-title-row">
                <strong>{tlsSourceSummary.title}</strong>
                <Badge variant={tlsSourceSummary.badgeVariant}>{tlsSourceSummary.badgeText}</Badge>
                {hiddenDirtyCount > 0 ? <Badge variant="warning">隐藏改动 {hiddenDirtyCount}</Badge> : null}
              </div>
              <p>{tlsSourceSummary.description}</p>
            </div>
          </div>
        ) : null}
        {visibleFields.map((field) => {
          const source = readOpsConfigSource(snapshot, field.key);
          const restorePreview = readOpsConfigRestorePreview(snapshot, field.key);
          const changePreview = buildOpsConfigChangePreview(
            field,
            source,
            editableSource,
            readOpsConfigValue(snapshot, field.key),
            draft[field.key]
          );
          return (
            <ConfigFieldRow
              key={field.key}
              changePreview={changePreview}
              dirty={isOpsConfigFieldDirty(snapshot, draft, field)}
              editableSource={editableSource}
              field={field}
              isRestoringInherited={resettingFieldKey === field.key}
              onResetToInherited={onResetToInherited}
              onValueChange={onValueChange}
              restorePreviewSource={restorePreview?.source ?? "default"}
              restorePreviewText={
                restorePreview === null ? "" : formatOpsConfigFieldValue(field, restorePreview.value)
              }
              source={source}
              value={draft[field.key]}
            />
          );
        })}
      </CardContent>
    </Card>
  );
}

function readTLSCertSource(sectionKey: string, draft: Record<string, unknown>): string {
  if (sectionKey !== "control-plane-tls") {
    return "";
  }
  return String(draft["control_plane.tls_cert_source"] ?? "").trim().toLowerCase();
}

function buildTLSSourceSummary(tlsCertSource: string, hiddenDirtyCount: number): {
  badgeText: string;
  badgeVariant: "outline" | "secondary" | "warning";
  description: string;
  title: string;
  tone: "external" | "managed";
} | null {
  if (tlsCertSource === "managed_ca") {
    return {
      badgeText: "Managed CA",
      badgeVariant: "warning",
      title: "当前聚焦：Bridge 自管 CA 签发",
      description:
        hiddenDirtyCount > 0
          ? "当前只展示自管 CA 相关字段；external 模式的服务端证书与私钥已隐藏。另有隐藏改动未保存，切回 External cert/key 可复核。"
          : "当前只展示自管 CA 相关字段；external 模式的服务端证书与私钥已隐藏。切回 External cert/key 后字段会恢复显示，已填值不会丢失。",
      tone: "managed",
    };
  }
  if (tlsCertSource === "external") {
    return {
      badgeText: "External cert/key",
      badgeVariant: "secondary",
      title: "当前聚焦：外部证书接入",
      description:
        hiddenDirtyCount > 0
          ? "当前只展示已有服务端证书接入所需字段；Managed CA 的 CA、SAN 和续签参数已隐藏。另有隐藏改动未保存，切到 Managed CA 可复核。"
          : "当前只展示已有服务端证书接入所需字段；Managed CA 的 CA、SAN 和续签参数已隐藏。切到 Managed CA 后会恢复显示自管签发参数。",
      tone: "external",
    };
  }
  return null;
}
