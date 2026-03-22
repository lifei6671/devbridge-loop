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
  readOpsConfigRestorePreview,
  readOpsConfigSource,
  readOpsConfigValue,
  type OpsConfigSection,
} from "../../model/ops-config";
import type { ApiRecord } from "../../model";
import { ConfigFieldRow } from "./ConfigFieldRow";

type ConfigSectionCardProps = {
  draft: Record<string, unknown>;
  onResetToInherited?: (fieldKey: string) => void;
  onValueChange: (fieldKey: string, value: unknown) => void;
  resettingFieldKey?: string | null;
  section: OpsConfigSection;
  snapshot: ApiRecord;
};

export function ConfigSectionCard(props: ConfigSectionCardProps) {
  const { draft, onResetToInherited, onValueChange, resettingFieldKey = null, section, snapshot } = props;
  const dirtyCount = section.fields.filter((field) => isOpsConfigFieldDirty(snapshot, draft, field)).length;

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
        {section.fields.map((field) => {
          const source = readOpsConfigSource(snapshot, field.key);
          const restorePreview = readOpsConfigRestorePreview(snapshot, field.key);
          const changePreview = buildOpsConfigChangePreview(
            field,
            source,
            readOpsConfigValue(snapshot, field.key),
            draft[field.key]
          );
          return (
            <ConfigFieldRow
              key={field.key}
              changePreview={changePreview}
              dirty={isOpsConfigFieldDirty(snapshot, draft, field)}
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
