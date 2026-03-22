import type { SelectHTMLAttributes } from "react";

import { cn } from "../../lib/utils";

type SelectProps = SelectHTMLAttributes<HTMLSelectElement>;

export function Select({ children, className, ...props }: SelectProps) {
  return (
    <select className={cn("ui-select", className)} {...props}>
      {children}
    </select>
  );
}
