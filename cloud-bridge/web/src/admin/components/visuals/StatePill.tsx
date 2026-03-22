import { resolveTone } from "../../model";

type StatePillProps = {
  label: string;
};

/**
 * StatePill 渲染状态徽标，统一不同状态的视觉语义。
 */
export function StatePill(props: StatePillProps) {
  const normalizedLabel = props.label.trim() === "" ? "unknown" : props.label;
  const tone = resolveTone(normalizedLabel);
  return <span className={`state-pill ${tone}`}>{normalizedLabel}</span>;
}
