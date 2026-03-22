import { cn } from "../../lib/utils";

type SwitchProps = {
  checked: boolean;
  className?: string;
  disabled?: boolean;
  id?: string;
  onCheckedChange: (checked: boolean) => void;
};

export function Switch(props: SwitchProps) {
  const { checked, className, disabled = false, id, onCheckedChange } = props;

  return (
    <label className={cn("ui-switch", disabled === true ? "ui-switch-disabled" : "", className)}>
      <input
        id={id}
        type="checkbox"
        className="ui-switch-input"
        checked={checked === true}
        disabled={disabled === true}
        onChange={(event) => onCheckedChange(event.target.checked)}
      />
      <span className="ui-switch-track">
        <span className="ui-switch-thumb" />
      </span>
    </label>
  );
}
