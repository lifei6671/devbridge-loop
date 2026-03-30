import { Children, isValidElement, type ReactNode } from "react";

import * as SelectPrimitive from "@radix-ui/react-select";

import { cn } from "@/lib/utils";

type SelectProps = {
  children: ReactNode;
  className?: string;
  disabled?: boolean;
  onValueChange?: (value: string) => void;
  placeholder?: string;
  value?: string;
};

type SelectOption = {
  disabled?: boolean;
  label: string;
  value: string;
};

function ChevronDownIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20" fill="none" className="size-4">
      <path d="m5 7.5 5 5 5-5" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function CheckIcon() {
  return (
    <svg aria-hidden="true" viewBox="0 0 20 20" fill="none" className="size-4">
      <path d="m5.5 10 3 3 6-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function extractOptions(children: ReactNode): SelectOption[] {
  return Children.toArray(children).flatMap((child) => {
    if (!isValidElement(child) || child.type !== "option") {
      return [];
    }

    const { children: labelNode, disabled, value } = child.props as {
      children?: ReactNode;
      disabled?: boolean;
      value?: string;
    };

    if (typeof value !== "string") {
      return [];
    }

    const label = typeof labelNode === "string" ? labelNode : String(labelNode ?? value);
    return [{ disabled, label, value }];
  });
}

export function Select({ children, className, disabled, onValueChange, placeholder, value }: SelectProps) {
  const options = extractOptions(children);

  return (
    <SelectPrimitive.Root disabled={disabled} onValueChange={onValueChange} value={value}>
      <SelectPrimitive.Trigger
        className={cn(
          "flex h-11 w-full items-center justify-between rounded-xl border border-[hsl(var(--border)/0.28)] bg-[hsl(var(--secondary)/0.72)] px-3 py-2 text-sm text-[hsl(var(--foreground))] shadow-none outline-none transition data-[placeholder]:text-[hsl(var(--muted-foreground))] focus:border-[hsl(var(--primary)/0.35)] focus:bg-[hsl(var(--card))] focus:ring-2 focus:ring-[hsl(var(--ring)/0.22)] disabled:cursor-not-allowed disabled:opacity-50",
          className,
        )}
      >
        <SelectPrimitive.Value placeholder={placeholder} />
        <SelectPrimitive.Icon className="text-[hsl(var(--muted-foreground))]">
          <ChevronDownIcon />
        </SelectPrimitive.Icon>
      </SelectPrimitive.Trigger>
      <SelectPrimitive.Portal>
        <SelectPrimitive.Content
          position="popper"
          sideOffset={8}
          className="z-50 min-w-[var(--radix-select-trigger-width)] overflow-hidden rounded-xl border border-[hsl(var(--border)/0.24)] bg-[hsl(var(--popover))] text-[hsl(var(--popover-foreground))] shadow-[0_22px_48px_rgba(25,28,29,0.14)] backdrop-blur"
        >
          <SelectPrimitive.Viewport className="p-1.5">
            {options.map((option) => (
              <SelectPrimitive.Item
                key={option.value}
                value={option.value}
                disabled={option.disabled}
                className="relative flex min-h-10 cursor-default select-none items-center rounded-lg py-2 pl-9 pr-3 text-sm outline-none transition data-[disabled]:pointer-events-none data-[disabled]:opacity-50 data-[highlighted]:bg-[hsl(var(--secondary))] data-[highlighted]:text-[hsl(var(--foreground))]"
              >
                <span className="absolute left-3 inline-flex size-4 items-center justify-center text-[hsl(var(--primary))]">
                  <SelectPrimitive.ItemIndicator>
                    <CheckIcon />
                  </SelectPrimitive.ItemIndicator>
                </span>
                <SelectPrimitive.ItemText>{option.label}</SelectPrimitive.ItemText>
              </SelectPrimitive.Item>
            ))}
          </SelectPrimitive.Viewport>
        </SelectPrimitive.Content>
      </SelectPrimitive.Portal>
    </SelectPrimitive.Root>
  );
}
