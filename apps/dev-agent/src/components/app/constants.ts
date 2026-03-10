import {
  AlertTriangle,
  LayoutDashboard,
  ListTree,
  Settings,
  ShieldCheck
} from "lucide-react";

import type { PageItem } from "@/components/app/types";

export const PAGE_ITEMS: PageItem[] = [
  { key: "dashboard", label: "总览", description: "运行状态与连接看板", icon: LayoutDashboard },
  { key: "services", label: "服务", description: "本地注册服务与详情", icon: ListTree },
  { key: "intercepts", label: "接管", description: "当前生效接管关系", icon: ShieldCheck },
  { key: "logs", label: "日志", description: "错误与请求记录", icon: AlertTriangle },
  { key: "config", label: "配置", description: "桌面端与隧道参数", icon: Settings }
];
