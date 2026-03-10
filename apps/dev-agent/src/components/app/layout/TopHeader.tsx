import { Cable, RefreshCcw, RotateCcw } from "lucide-react";
import type { ReactElement } from "react";

import type { PageItem, PageKey, UiPhase } from "@/components/app/types";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

interface TopHeaderProps {
  activePage: PageKey;
  activePageMeta: PageItem;
  manualReconnectPending: boolean;
  manualReconnecting: boolean;
  manualRefreshing: boolean;
  onChangePage: (page: PageKey) => void;
  onReconnect: () => void;
  onRefresh: () => void;
  onRestart: () => void;
  pageItems: PageItem[];
  phaseBusy: boolean;
  uiPhase: UiPhase;
}

export function TopHeader({
  activePage,
  activePageMeta,
  manualReconnectPending,
  manualReconnecting,
  manualRefreshing,
  onChangePage,
  onReconnect,
  onRefresh,
  onRestart,
  pageItems,
  phaseBusy,
  uiPhase
}: TopHeaderProps): ReactElement {
  const actionDisabled =
    manualRefreshing || phaseBusy || manualReconnectPending || manualReconnecting;

  return (
    <header className="border-b border-slate-200/70 bg-white/50 px-4 py-4 backdrop-blur-xl sm:px-6">
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <p className="text-[11px] uppercase tracking-[0.2em] text-slate-500 md:hidden">开发桥接回路</p>
          <h2 className="font-mono text-xl font-semibold tracking-tight text-slate-900">{activePageMeta.label}</h2>
          <p className="mt-1 text-xs text-slate-500">{activePageMeta.description}</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button
            variant="outline"
            onClick={onRefresh}
            disabled={actionDisabled}
          >
            <RefreshCcw className={cn("mr-2 h-4 w-4", manualRefreshing && "animate-spin")} />
            {manualRefreshing ? "刷新中..." : "刷新"}
          </Button>
          <Button
            variant="outline"
            onClick={onRestart}
            disabled={actionDisabled}
          >
            <RotateCcw className={cn("mr-2 h-4 w-4", phaseBusy && "animate-spin")} />
            {uiPhase === "restarting"
              ? "重启中..."
              : uiPhase === "recovering"
                ? "恢复中..."
                : "重启核心进程"}
          </Button>
          <Button
            onClick={onReconnect}
            disabled={actionDisabled}
          >
            <Cable className={cn("mr-2 h-4 w-4", (manualReconnectPending || manualReconnecting) && "animate-pulse")} />
            {manualReconnectPending ? "准备重连..." : manualReconnecting ? "重连中..." : "手动重连"}
          </Button>
        </div>
      </div>

      <nav className="mt-4 flex gap-2 overflow-x-auto pb-1 md:hidden">
        {pageItems.map((item) => {
          const Icon = item.icon;
          return (
            <Button
              key={item.key}
              variant={activePage === item.key ? "default" : "outline"}
              size="sm"
              onClick={() => onChangePage(item.key)}
              className="whitespace-nowrap"
            >
              <Icon className="mr-2 h-4 w-4" />
              {item.label}
            </Button>
          );
        })}
      </nav>
    </header>
  );
}
