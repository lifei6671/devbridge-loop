import { Badge } from "../../../components/ui/badge";
import { Button } from "../../../components/ui/button";
import { Input } from "../../../components/ui/input";
import { Select } from "../../../components/ui/select";
import { Switch } from "../../../components/ui/switch";
import { Tooltip } from "../../../components/ui/tooltip";
import {
  formatOpsConfigSource,
  getOpsConfigHelpText,
  getOpsConfigSourceBadgeVariant,
  type OpsConfigChangePreview,
  type OpsConfigField,
} from "../../model";

type ConfigFieldRowProps = {
  changePreview?: OpsConfigChangePreview | null;
  dirty: boolean;
  field: OpsConfigField;
  isRestoringInherited?: boolean;
  onResetToInherited?: (fieldKey: string) => void;
  restorePreviewSource?: string;
  restorePreviewText?: string;
  source: string;
  value: unknown;
  onValueChange: (fieldKey: string, value: unknown) => void;
};

export function ConfigFieldRow(props: ConfigFieldRowProps) {
  const {
    changePreview = null,
    dirty,
    field,
    isRestoringInherited = false,
    onResetToInherited,
    restorePreviewSource = "default",
    restorePreviewText = "",
    source,
    value,
    onValueChange,
  } = props;
  const restartBadgeText = field.restartRequired === true ? "需重启" : "即时生效";
  const restartBadgeVariant = field.restartRequired === true ? "secondary" : "success";
  const canResetToInherited = source === "user" && typeof onResetToInherited === "function";

  return (
    <div className="config-field-row">
      <div className="config-field-head">
        <div className="config-field-copy">
          <div className="config-field-title-row">
            <strong>{field.label}</strong>
            <Tooltip align="start" content={getOpsConfigHelpText(field)} contentClassName="field-help-tooltip">
              <button
                type="button"
                className="field-help-btn"
                aria-label={`${field.label} 说明`}
              >
                <svg viewBox="0 0 20 20" aria-hidden="true">
                  <circle cx="10" cy="10" r="7.25" fill="none" stroke="currentColor" strokeWidth="1.5" />
                  <path d="M10 8.1a1 1 0 1 0 0-2 1 1 0 0 0 0 2Zm1.1 6V9H9v1.2h.9v3.9h1.2Z" fill="currentColor" />
                </svg>
              </button>
            </Tooltip>
          </div>
          <p>{field.description}</p>
          {dirty === true && changePreview !== null ? (
            <div className="config-field-change-preview">
              <div className="config-field-change-values">
                <Badge variant="outline">当前</Badge>
                <code>{changePreview.currentText}</code>
                <span className="config-field-change-arrow" aria-hidden="true">→</span>
                <Badge variant={changePreview.nextLabel === "用户目录" ? "secondary" : "warning"}>
                  {changePreview.nextLabel}
                </Badge>
                <code>{changePreview.nextText}</code>
              </div>
              <span className="config-field-change-note">{changePreview.note}</span>
            </div>
          ) : null}
          {canResetToInherited === true && restorePreviewText !== "" ? (
            <div className="config-field-restore-preview">
              <span>恢复后继承</span>
              <Badge variant={getOpsConfigSourceBadgeVariant(restorePreviewSource)}>
                {formatOpsConfigSource(restorePreviewSource)}
              </Badge>
              <code>{restorePreviewText}</code>
            </div>
          ) : null}
        </div>
        <div className="config-field-meta">
          <Badge variant={getOpsConfigSourceBadgeVariant(source)}>
            来源：{formatOpsConfigSource(source)}
          </Badge>
          <Badge variant={restartBadgeVariant}>{restartBadgeText}</Badge>
          {dirty === true ? <Badge variant="warning">未保存</Badge> : null}
          {canResetToInherited === true ? (
            <Button
              variant="outline"
              size="sm"
              className="config-field-reset-btn"
              onClick={() => onResetToInherited?.(field.key)}
              disabled={isRestoringInherited === true}
            >
              {isRestoringInherited === true ? "恢复中..." : "恢复继承值"}
            </Button>
          ) : null}
        </div>
      </div>
      <div className="config-field-control">{renderFieldControl(field, value, onValueChange)}</div>
    </div>
  );
}

function renderFieldControl(
  field: OpsConfigField,
  value: unknown,
  onValueChange: (fieldKey: string, value: unknown) => void
) {
  if (field.kind === "boolean") {
    return (
      <Switch
        checked={value === true}
        onCheckedChange={(checked) => onValueChange(field.key, checked)}
      />
    );
  }

  if (field.kind === "select") {
    return (
      <Select value={String(value ?? "")} onChange={(event) => onValueChange(field.key, event.target.value)}>
        {(field.options ?? []).map((option) => (
          <option key={option.value} value={option.value}>
            {option.label}
          </option>
        ))}
      </Select>
    );
  }

  if (field.kind === "number") {
    return (
      <div className="config-input-unit-wrap">
        <Input
          type="number"
          min={1}
          value={String(value ?? "")}
          placeholder={field.placeholder}
          onChange={(event) => onValueChange(field.key, event.target.value)}
        />
        <span>{field.unit ?? ""}</span>
      </div>
    );
  }

  return (
    <Input
      value={String(value ?? "")}
      placeholder={field.placeholder}
      onChange={(event) => onValueChange(field.key, event.target.value)}
    />
  );
}
