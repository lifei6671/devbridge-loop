import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import {
  asPrettyTime,
  buildDetailRowElementID,
  readNumber,
  readScopeText,
  readText,
  readTunnelID,
} from "../../model";
import { StatePill } from "../AdminVisuals";

type TrafficPageProps = {
  vm: AdminConsoleViewModel;
};

function getEmptyTunnelMessage(vm: AdminConsoleViewModel): string {
  const { agentPoolSummary, tunnelConnectorFilter } = vm;
  const connectedCount = readNumber(agentPoolSummary, "connected", 0);
  const connectedText = readText(agentPoolSummary, "connected", "0");

  if (tunnelConnectorFilter === "ALL") {
    if (connectedCount > 0) {
      return `Bridge 当前暂无已登记 tunnel（Agent 上报 connected=${connectedText}）。`;
    }
    return "当前没有 tunnel 数据。";
  }

  if (connectedCount > 0) {
    return `Agent ${tunnelConnectorFilter} 已上报 connected=${connectedText}，但 Bridge 侧暂无已登记 tunnel。`;
  }

  return `Agent ${tunnelConnectorFilter} 当前没有 tunnel 数据。`;
}

export function TrafficPage(props: TrafficPageProps) {
  const {
    agentPoolSummary,
    clearTrafficOwnershipLookup,
    handleDetailRowKeyDown,
    isActiveDetailRow,
    isTrafficOwnershipLoading,
    lookupTrafficOwnership,
    openDetailDrawer,
    refreshPageData,
    setTrafficLookupID,
    setTunnelConnectorFilter,
    setTunnelStateFilter,
    trafficConnectorOptions,
    trafficLookupID,
    trafficOwnership,
    trafficOwnershipError,
    trafficSummary,
    tunnelConnectorFilter,
    tunnelItems,
    tunnelStateFilter,
    tunnelSummary,
  } = props.vm;

  return (
    <div className="content-stack traffic-content-stack">
      <section className="panel traffic-summary-panel">
        <header className="panel-head traffic-summary-head">
          <h3>Tunnel Pool 摘要</h3>
          <div className="traffic-summary-meta">
            <span className="traffic-summary-meta-item panel-sub">
              {tunnelConnectorFilter === "ALL"
                ? "Bridge 运行态 + Agent 上报池（全部 Agent）"
                : `已按 Agent 过滤：${tunnelConnectorFilter}`}
            </span>
            <span className="traffic-summary-meta-item panel-sub">
              更新时间 {asPrettyTime(readNumber(tunnelSummary, "updated_at_ms"))}
            </span>
          </div>
        </header>
        <p className="panel-note">说明：Bridge 统计是已登记 tunnel；Agent 统计来自 TunnelPoolReport 上报。</p>
        <div className="kpi-grid traffic-kpi-grid">
          <article className="kpi-card">
            <p className="kpi-label">Idle</p>
            <p className="kpi-value">{readText(tunnelSummary, "idle", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Reserved</p>
            <p className="kpi-value">{readText(tunnelSummary, "reserved", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Active</p>
            <p className="kpi-value">{readText(tunnelSummary, "active", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Broken</p>
            <p className="kpi-value">{readText(tunnelSummary, "broken", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Open Timeout</p>
            <p className="kpi-value">{readText(trafficSummary, "open_timeout_total", "0")}</p>
          </article>
          <article className="kpi-card">
            <p className="kpi-label">Open Ack Late</p>
            <p className="kpi-value">{readText(trafficSummary, "open_ack_late_total", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent Connected</p>
            <p className="kpi-value">{readText(agentPoolSummary, "connected", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent Idle</p>
            <p className="kpi-value">{readText(agentPoolSummary, "idle", "0")}</p>
          </article>
          <article className="kpi-card kpi-card-agent">
            <p className="kpi-label">Agent In Use</p>
            <p className="kpi-value">{readText(agentPoolSummary, "in_use", "0")}</p>
          </article>
        </div>
      </section>

      <section className="panel traffic-list-panel">
        <header className="panel-head">
          <h3>Tunnel 列表</h3>
          <div className="inline-actions">
            <label className="field-inline field-inline-wide">
              <span>Agent</span>
              <select
                value={tunnelConnectorFilter}
                onChange={(event) => setTunnelConnectorFilter(event.target.value)}
              >
                {trafficConnectorOptions.map((connectorID) => (
                  <option key={connectorID} value={connectorID}>
                    {connectorID}
                  </option>
                ))}
              </select>
            </label>
            <label className="field-inline">
              <span>State</span>
              <select
                value={tunnelStateFilter}
                onChange={(event) => setTunnelStateFilter(event.target.value)}
              >
                <option value="ALL">ALL</option>
                <option value="idle">idle</option>
                <option value="reserved">reserved</option>
                <option value="active">active</option>
                <option value="closed">closed</option>
                <option value="broken">broken</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("traffic")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap traffic-table-wrap">
          <table>
            <thead>
              <tr>
                <th>Tunnel ID</th>
                <th>Connector</th>
                <th>Session</th>
                <th>Traffic</th>
                <th>State</th>
                <th>Last Error</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {tunnelItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("tunnel", index)}
                  key={`${readText(item, "tunnel_id", "tunnel")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("tunnel", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("tunnel", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "tunnel", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Tunnel ${readTunnelID(item, "tunnel_id", "unknown")} 详情`}
                >
                  <td>{readTunnelID(item)}</td>
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "session_id")}</td>
                  <td>{readText(item, "traffic_id", "--")}</td>
                  <td>
                    <StatePill label={readText(item, "state", "unknown")} />
                  </td>
                  <td>{readText(item, "last_error", "--")}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        event.stopPropagation();
                        openDetailDrawer("tunnel", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {tunnelItems.length === 0 && (
                <tr>
                  <td colSpan={8} className="empty-cell">
                    {getEmptyTunnelMessage(props.vm)}
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Traffic Ownership</h3>
          <span className="panel-sub">按 traffic_id 反查命中的服务归属与 scope 路径</span>
        </header>
        <p className="panel-note">
          用于确认当前流量最终命中了哪个 logical service / instance，以及是否走了 external
          fallback。
        </p>
        <form
          className="patch-form"
          onSubmit={(event) => {
            event.preventDefault();
            void lookupTrafficOwnership(trafficLookupID);
          }}
        >
          <label>
            <span>Traffic ID</span>
            <input
              value={trafficLookupID}
              onChange={(event) => setTrafficLookupID(event.target.value)}
              placeholder="traffic-xxx"
            />
          </label>
          <div className="inline-actions">
            <button type="submit" className="solid-btn" disabled={isTrafficOwnershipLoading}>
              {isTrafficOwnershipLoading ? "查询中..." : "查询归属"}
            </button>
            <button type="button" className="ghost-btn" onClick={clearTrafficOwnershipLookup}>
              清空
            </button>
          </div>
        </form>
        {trafficOwnershipError !== "" && <p className="listener-empty">{trafficOwnershipError}</p>}
        {trafficOwnership !== null ? (
          <div className="detail-grid">
            <article className="detail-kv">
              <p>Traffic ID</p>
              <strong>{readText(trafficOwnership, "traffic_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Logical Service</p>
              <strong>{readText(trafficOwnership, "logical_service_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Service Name</p>
              <strong>{readText(trafficOwnership, "service_name", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Instance ID</p>
              <strong>{readText(trafficOwnership, "instance_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Connector</p>
              <strong>{readText(trafficOwnership, "connector_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Session</p>
              <strong>{readText(trafficOwnership, "session_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Route ID</p>
              <strong>{readText(trafficOwnership, "route_id", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Target Kind</p>
              <strong>{readText(trafficOwnership, "target_kind", "--")}</strong>
            </article>
            <article className="detail-kv">
              <p>Scope</p>
              <strong>{readScopeText(trafficOwnership)}</strong>
            </article>
            <article className="detail-kv">
              <p>Request Scope</p>
              <strong>{readScopeText(trafficOwnership, "request_scope")}</strong>
            </article>
            <article className="detail-kv">
              <p>Matched Scope</p>
              <strong>{readScopeText(trafficOwnership, "matched_scope")}</strong>
            </article>
            <article className="detail-kv">
              <p>External Fallback</p>
              <strong>{readText(trafficOwnership, "is_external_fallback", "false")}</strong>
            </article>
            <article className="detail-kv">
              <p>Updated</p>
              <strong>{asPrettyTime(readNumber(trafficOwnership, "updated_at_ms"))}</strong>
            </article>
          </div>
        ) : (
          <p className="listener-empty">
            输入 traffic_id 后可查看 `request_scope / matched_scope / is_external_fallback`
            的运行态结果。
          </p>
        )}
      </section>
    </div>
  );
}
