import type { ComponentPropsWithoutRef } from "react";

import * as CheckboxPrimitive from "@radix-ui/react-checkbox";

import { cn } from "@/lib/utils";

type CheckboxProps = ComponentPropsWithoutRef<typeof CheckboxPrimitive.Root>;

function CheckIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20" fill="none" className="size-3.5">
      <path d="m5.5 10 3 3 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Checkbox({ className, ...props }: CheckboxProps) {
  return (
    <CheckboxPrimitive.Root
      className={cn(
        "peer inline-flex size-5 shrink-0 items-center justify-center rounded-md border border-[hsl(var(--border)/0.4)] bg-[hsl(var(--card))] text-[hsl(var(--primary-foreground))] shadow-sm outline-none transition focus-visible:ring-2 focus-visible:ring-[hsl(var(--ring)/0.22)] focus-visible:ring-offset-2 focus-visible:ring-offset-[hsl(var(--background))] data-[state=checked]:border-[hsl(var(--primary))] data-[state=checked]:bg-[hsl(var(--primary))] disabled:cursor-not-allowed disabled:opacity-50",
        className,
      )}
      {...props}
    >
      <CheckboxPrimitive.Indicator>
        <CheckIcon />
      </CheckboxPrimitive.Indicator>
    </CheckboxPrimitive.Root>
  );
}
