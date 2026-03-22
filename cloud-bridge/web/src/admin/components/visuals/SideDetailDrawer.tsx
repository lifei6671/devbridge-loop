import { toast } from "sonner";

import type { ApiRecord, DetailSelection } from "../../model";
import {
  buildDetailSummaryRows,
  copyTextToClipboard,
  isUsableID,
  readText,
} from "../../model";
import { StatePill } from "./StatePill";

type SideDetailDrawerProps = {
  selection: DetailSelection | null;
  record: ApiRecord | null;
  title: string;
  totalCount: number;
  onClose: () => void;
  onMove: (offset: number) => void;
  onQuickDrainSession: (sessionID: string) => void;
  onQuickDrainConnector: (connectorID: string) => void;
  onPrefillOps: (target: "session" | "connector", targetID: string) => void;
};

function pickStateText(selection: DetailSelection, record: ApiRecord): string {
  if (selection.domain === "route") {
    return readText(record, "target_type", "unknown");
  }
  if (selection.domain === "connector") {
    return readText(record, "session_state", "unknown");
  }
  return readText(record, "state", "unknown");
}

/**
 * SideDetailDrawer 展示当前选中对象的摘要、快捷动作和原始 JSON。
 */
export function SideDetailDrawer(props: SideDetailDrawerProps) {
  if (props.selection === null || props.record === null) {
    return null;
  }

  const summaryRows = buildDetailSummaryRows(props.selection.domain, props.record);
  const stateText = pickStateText(props.selection, props.record);
  const hasPrevious = props.selection.index > 0;
  const hasNext = props.selection.index < props.totalCount - 1;
  const orderText = String(props.selection.index + 1) + " / " + String(props.totalCount);
  const sessionID = readText(props.record, "session_id", "");
  const connectorID = readText(props.record, "connector_id", "");
  const canUseSessionID = isUsableID(sessionID);
  const canUseConnectorID = isUsableID(connectorID);

  const copyRawJSON = async () => {
    const copySucceeded = await copyTextToClipboard(JSON.stringify(props.record, null, 2));
    if (copySucceeded) {
      toast.success("原始对象 JSON 已复制");
      return;
    }
    toast.error("复制失败，请手动复制");
  };

  return (
    <div className="detail-overlay" onClick={props.onClose}>
      <aside
        className="detail-drawer"
        onClick={(event) => {
          // 阻止冒泡，避免点击抽屉内容时触发遮罩关闭。
          event.stopPropagation();
        }}
      >
        <header className="detail-head">
          <div>
            <p className="detail-eyebrow">{props.selection.domain.toUpperCase()} DETAIL</p>
            <h3>{props.title}</h3>
          </div>
          <button type="button" className="ghost-btn" onClick={props.onClose}>
            关闭
          </button>
        </header>

        <section className="detail-nav">
          <button
            type="button"
            className="ghost-btn"
            disabled={!hasPrevious}
            onClick={() => props.onMove(-1)}
          >
            上一条
          </button>
          <span>{orderText}</span>
          <button
            type="button"
            className="ghost-btn"
            disabled={!hasNext}
            onClick={() => props.onMove(1)}
          >
            下一条
          </button>
        </section>

        <section className="detail-state-row">
          <span>当前状态</span>
          <StatePill label={stateText} />
        </section>

        {(canUseSessionID || canUseConnectorID) && (
          <section className="detail-actions">
            {canUseSessionID && (
              <>
                <button
                  type="button"
                  className="danger-btn detail-action-btn"
                  onClick={() => props.onQuickDrainSession(sessionID)}
                >
                  <span className="detail-action-title">立即 Drain Session</span>
                  <span className="detail-action-help">
                    直接执行：把当前 Session 置为 DRAINING 并收敛关联资源。
                  </span>
                </button>
                <button
                  type="button"
                  className="ghost-btn detail-action-btn"
                  onClick={() => props.onPrefillOps("session", sessionID)}
                >
                  <span className="detail-action-title">填充 Session 到 Ops</span>
                  <span className="detail-action-help">
                    仅预填 Session ID 到 Ops 页面，不会立即执行 Drain。
                  </span>
                </button>
              </>
            )}
            {canUseConnectorID && (
              <>
                <button
                  type="button"
                  className="danger-btn detail-action-btn"
                  onClick={() => props.onQuickDrainConnector(connectorID)}
                >
                  <span className="detail-action-title">立即 Drain Connector</span>
                  <span className="detail-action-help">直接执行：对当前 Connector 发起 Drain 收敛。</span>
                </button>
                <button
                  type="button"
                  className="ghost-btn detail-action-btn"
                  onClick={() => props.onPrefillOps("connector", connectorID)}
                >
                  <span className="detail-action-title">填充 Connector 到 Ops</span>
                  <span className="detail-action-help">
                    仅预填 Connector ID 到 Ops 页面，便于确认后再执行。
                  </span>
                </button>
              </>
            )}
          </section>
        )}

        <section className="detail-grid">
          {summaryRows.map((item) => (
            <article key={item.label} className="detail-kv">
              <p>{item.label}</p>
              <span className="detail-kv-help">{item.hint}</span>
              <strong>{item.value}</strong>
            </article>
          ))}
        </section>

        <section className="detail-json">
          <header>
            <div>
              <h4>原始对象</h4>
              <span>用于快速排障与字段核对</span>
            </div>
            <div className="detail-json-actions">
              <button type="button" className="ghost-btn compact" onClick={() => void copyRawJSON()}>
                复制 JSON
              </button>
            </div>
          </header>
          <pre>{JSON.stringify(props.record, null, 2)}</pre>
        </section>
      </aside>
    </div>
  );
}
