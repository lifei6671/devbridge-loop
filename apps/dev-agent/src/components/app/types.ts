import type { ComponentType } from "react";

export type PageKey = "dashboard" | "services" | "intercepts" | "logs" | "config";
export type UiPhase = "ready" | "restarting" | "recovering";
export type ToastLevel = "success" | "error";

export interface AppToast {
  id: number;
  level: ToastLevel;
  message: string;
}

export interface PageItem {
  key: PageKey;
  label: string;
  description: string;
  icon: ComponentType<{ className?: string }>;
}
