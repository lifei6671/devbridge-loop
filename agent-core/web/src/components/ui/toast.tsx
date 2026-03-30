import * as React from "react";
import * as ToastPrimitive from "@radix-ui/react-toast";

import { cn } from "@/lib/utils";

type ToastVariant = "success" | "warning" | "danger";

const toastVariantClassName: Record<ToastVariant, string> = {
  success:
    "border-[rgba(13,148,136,0.24)] bg-[linear-gradient(135deg,rgba(240,253,250,0.96),rgba(204,251,241,0.88))] text-[rgb(17,94,89)] shadow-[0_22px_48px_rgba(13,148,136,0.12)]",
  warning:
    "border-[rgba(245,158,11,0.24)] bg-[linear-gradient(135deg,rgba(255,251,235,0.97),rgba(254,243,199,0.92))] text-[rgb(146,64,14)] shadow-[0_22px_48px_rgba(245,158,11,0.12)]",
  danger:
    "border-[rgba(239,68,68,0.22)] bg-[linear-gradient(135deg,rgba(254,242,242,0.98),rgba(254,226,226,0.92))] text-[rgb(153,27,27)] shadow-[0_22px_48px_rgba(239,68,68,0.12)]",
};

const ToastProvider = ToastPrimitive.Provider;

const ToastViewport = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Viewport>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Viewport>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Viewport
    ref={ref}
    className={cn(
      "fixed left-1/2 top-4 z-[120] flex w-[min(420px,calc(100vw-2rem))] max-w-full -translate-x-1/2 flex-col gap-3 outline-none sm:top-6",
      className,
    )}
    {...props}
  />
));
ToastViewport.displayName = ToastPrimitive.Viewport.displayName;

const Toast = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Root>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Root> & {
    variant?: ToastVariant;
  }
>(({ className, variant = "success", ...props }, ref) => (
  <ToastPrimitive.Root
    ref={ref}
    className={cn(
      "group relative grid overflow-hidden rounded-[24px] border p-4 backdrop-blur supports-[backdrop-filter]:bg-opacity-95",
      "data-[state=open]:animate-in data-[state=closed]:animate-out",
      "data-[state=closed]:fade-out-0 data-[state=open]:fade-in-0",
      "data-[state=closed]:slide-out-to-right-full data-[state=open]:slide-in-from-top-3",
      "data-[swipe=cancel]:translate-x-0 data-[swipe=end]:translate-x-[var(--radix-toast-swipe-end-x)]",
      "data-[swipe=move]:translate-x-[var(--radix-toast-swipe-move-x)] data-[swipe=move]:transition-none",
      toastVariantClassName[variant],
      className,
    )}
    {...props}
  />
));
Toast.displayName = ToastPrimitive.Root.displayName;

const ToastTitle = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Title>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Title>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Title ref={ref} className={cn("text-sm font-semibold tracking-[-0.01em]", className)} {...props} />
));
ToastTitle.displayName = ToastPrimitive.Title.displayName;

const ToastDescription = React.forwardRef<
  React.ElementRef<typeof ToastPrimitive.Description>,
  React.ComponentPropsWithoutRef<typeof ToastPrimitive.Description>
>(({ className, ...props }, ref) => (
  <ToastPrimitive.Description ref={ref} className={cn("mt-1 text-sm leading-6 opacity-90", className)} {...props} />
));
ToastDescription.displayName = ToastPrimitive.Description.displayName;

export { Toast, ToastDescription, ToastProvider, ToastTitle, ToastViewport };
