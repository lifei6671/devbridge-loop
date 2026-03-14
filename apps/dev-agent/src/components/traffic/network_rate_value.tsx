import { cn } from "@/lib/utils";
import { formatBytesPerSecParts } from "@/features/traffic/format";

interface NetworkRateValueProps {
  bytesPerSec: number;
  className?: string;
  valueClassName?: string;
  unitClassName?: string;
}

/** 网速值展示组件：统一格式化与单位渲染，避免页面内重复逻辑。 */
export function NetworkRateValue(props: NetworkRateValueProps): JSX.Element {
  const parts = formatBytesPerSecParts(props.bytesPerSec);
  return (
    <span className={cn("inline-flex items-baseline gap-1", props.className)}>
      <strong className={cn("font-semibold", props.valueClassName)}>{parts.valueText}</strong>
      <span className={cn("text-xs", props.unitClassName)}>{parts.unitText}</span>
    </span>
  );
}
