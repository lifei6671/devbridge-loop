import type { ReactElement } from "react";

import { formatStatusText, formatTime } from "@/components/app/helpers";
import type { PageItem, PageKey } from "@/components/app/types";
import { Badge } from "@/components/ui/badge";
import { cn } from "@/lib/utils";

interface SidebarProps {
  activePage: PageKey;
  agentStatus: string;
  bridgeStatus: string;
  currentEnv: string | null;
  lastUpdateAt: string | null;
  onChangePage: (page: PageKey) => void;
  pageItems: PageItem[];
  rdName: string | null;
}

export function Sidebar({
  activePage,
  agentStatus,
  bridgeStatus,
  currentEnv,
  lastUpdateAt,
  onChangePage,
  pageItems,
  rdName
}: SidebarProps): ReactElement {
  return (
    <aside className="hidden border-r border-slate-200/70 bg-white/70 backdrop-blur-xl md:flex md:flex-col">
      <div className="border-b border-slate-200/70 px-5 pb-4 pt-5">
        <p className="text-[11px] uppercase tracking-[0.3em] text-slate-500">开发桥接回路</p>
        <h1 className="mt-2 font-mono text-xl font-semibold tracking-tight text-slate-900">控制中心</h1>
        <p className="mt-2 text-xs leading-5 text-slate-500">核心进程、桥接服务与隧道状态的一体化控制台</p>
      </div>

      <div className="space-y-2 border-b border-slate-200/70 px-4 py-4 text-xs text-slate-600">
        <div className="flex items-center justify-between rounded-lg border border-slate-200/70 bg-white/80 px-3 py-2">
          <span>核心进程</span>
          <Badge variant="secondary">{formatStatusText(agentStatus)}</Badge>
        </div>
        <div className="flex items-center justify-between rounded-lg border border-slate-200/70 bg-white/80 px-3 py-2">
          <span>桥接连接</span>
          <Badge variant="secondary">{formatStatusText(bridgeStatus)}</Badge>
        </div>
        <div className="flex items-center justify-between rounded-lg border border-slate-200/70 bg-white/80 px-3 py-2">
          <span>当前环境</span>
          <span className="font-mono text-[11px]">{currentEnv ?? "-"}</span>
        </div>
      </div>

      <nav className="space-y-1 px-3 py-4">
        {pageItems.map((item) => {
          const Icon = item.icon;
          return (
            <button
              key={item.key}
              type="button"
              onClick={() => onChangePage(item.key)}
              className={cn(
                "flex w-full items-center gap-3 rounded-xl px-3 py-2.5 text-left text-sm transition-all duration-200",
                activePage === item.key
                  ? "bg-slate-900 text-white shadow-lg shadow-slate-900/15"
                  : "text-slate-700 hover:bg-white hover:text-slate-950"
              )}
            >
              <Icon className="h-4 w-4 shrink-0" />
              <div>
                <div className="font-medium">{item.label}</div>
                <div
                  className={cn(
                    "text-[11px]",
                    activePage === item.key ? "text-slate-300" : "text-slate-500"
                  )}
                >
                  {item.description}
                </div>
              </div>
            </button>
          );
        })}
      </nav>

      <div className="mt-auto border-t border-slate-200/70 px-4 py-3 text-xs text-slate-500">
        <div>研发域名：{rdName ?? "-"}</div>
        <div className="mt-1">最后更新：{formatTime(lastUpdateAt)}</div>
      </div>
    </aside>
  );
}
