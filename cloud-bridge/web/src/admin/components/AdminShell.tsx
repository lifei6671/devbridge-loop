import type { AdminConsoleViewModel } from "../hooks/useAdminConsole";
import { Tooltip } from "../../components/ui/tooltip";
import { asPrettyTime, pageSections, pickPageMeta } from "../model";
import { AdminPageContent } from "./AdminPageContent";
import { SideDetailDrawer } from "./AdminVisuals";

type AdminShellProps = {
  vm: AdminConsoleViewModel;
};

export function AdminShell(props: AdminShellProps) {
  const {
    activeMeta,
    activePage,
    closeDetailDrawer,
    currentDetailItems,
    currentDetailRecord,
    currentDetailTitle,
    detailSelection,
    isLoading,
    lastSyncMS,
    moveDetailSelection,
    navigateToPage,
    prefillOpsFromDetail,
    quickDrainFromDetail,
    realtimeSummary,
    refreshPageData,
  } = props.vm;

  return (
    <>
      <div className="admin-shell">
        <aside className="sidebar panel">
          <div className="sidebar-brand">
            <div className="brand-mark" aria-hidden="true">
              <span />
            </div>
            <div className="brand-copy">
              <h1>DevBridge</h1>
              <p>Bridge Admin Console</p>
            </div>
          </div>


          {pageSections.map((section) => (
            <div key={section.title} className="sidebar-section">
              <p className="sidebar-section-label">{section.title}</p>
              <nav className="nav-list">
                {section.items.map((pageKey) => {
                  const item = pickPageMeta(pageKey);
                  return (
                    <button
                      key={pageKey}
                      type="button"
                      className={`nav-btn ${activePage === pageKey ? "active" : ""}`}
                      onClick={() => navigateToPage(pageKey)}
                    >
                      <span>{item.title}</span>
                      <small>{item.subtitle}</small>
                    </button>
                  );
                })}
              </nav>
            </div>
          ))}

          <div className="sidebar-foot">
            <div className="sidebar-foot-grid">
              <div className="sidebar-foot-item">
                <span>Last Sync</span>
                <strong className="sidebar-foot-value">
                  {lastSyncMS > 0 ? asPrettyTime(lastSyncMS) : "--"}
                </strong>
              </div>
              <div className="sidebar-foot-item">
                <span>Realtime</span>
                <strong className={`sidebar-foot-value tone-${realtimeSummary.tone}`}>
                  {realtimeSummary.label}
                </strong>
              </div>
            </div>
          </div>
        </aside>

        <main className={`main-area ${activePage === "traffic" ? "main-area-traffic" : ""}`}>
          <header className="topbar panel">
            <div className="topbar-main topbar-main-compact">
              <div className="topbar-heading-wrap">
                <p className="topbar-breadcrumb">首页 / {activeMeta.title}</p>
                <p className="topbar-title">{activeMeta.title}</p>
                <p className="topbar-sub">{activeMeta.subtitle}</p>
              </div>

              <div className="topbar-actions">
                <div className="topbar-status-strip">
                  <Tooltip
                    align="end"
                    content={realtimeSummary.detail}
                    contentClassName="topbar-status-tooltip"
                  >
                    <button
                      type="button"
                      className="shell-status-trigger"
                      aria-label={`${realtimeSummary.label} 说明`}
                    >
                      <span className={`shell-status-pill tone-${realtimeSummary.tone}`}>
                        {realtimeSummary.label}
                      </span>
                    </button>
                  </Tooltip>
                </div>
                <button
                  type="button"
                  className="solid-btn topbar-refresh-btn"
                  onClick={() => void refreshPageData(activePage)}
                >
                  {isLoading ? "加载中..." : "刷新"}
                </button>
              </div>
            </div>
          </header>

          <AdminPageContent vm={props.vm} />
        </main>
      </div>
      <SideDetailDrawer
        selection={detailSelection}
        record={currentDetailRecord}
        title={currentDetailTitle}
        totalCount={currentDetailItems.length}
        onClose={closeDetailDrawer}
        onMove={moveDetailSelection}
        onQuickDrainSession={(sessionID) => {
          void quickDrainFromDetail("session", sessionID);
        }}
        onQuickDrainConnector={(connectorID) => {
          void quickDrainFromDetail("connector", connectorID);
        }}
        onPrefillOps={(target, targetID) => {
          prefillOpsFromDetail(target, targetID);
          closeDetailDrawer();
        }}
      />
    </>
  );
}
