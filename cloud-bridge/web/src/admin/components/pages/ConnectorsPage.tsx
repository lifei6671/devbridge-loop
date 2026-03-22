import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import {
  asPercentText,
  asPrettyTime,
  buildDetailRowElementID,
  readNumber,
  readText,
} from "../../model";
import { StatePill } from "../AdminVisuals";

type ConnectorsPageProps = {
  vm: AdminConsoleViewModel;
};

export function ConnectorsPage(props: ConnectorsPageProps) {
  const {
    connectorItems,
    handleDetailRowKeyDown,
    isActiveDetailRow,
    openDetailDrawer,
    refreshPageData,
    sessionItems,
    sessionStateFilter,
    setSessionStateFilter,
  } = props.vm;

  return (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>Connector 列表</h3>
          <span className="panel-sub">共 {connectorItems.length} 个</span>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Connector ID</th>
                <th>Session</th>
                <th>State</th>
                <th>Service</th>
                <th>Health Rate</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {connectorItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("connector", index)}
                  key={`${readText(item, "connector_id", "connector")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("connector", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("connector", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "connector", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Connector ${readText(item, "connector_id", "unknown")} 详情`}
                >
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "session_id")}</td>
                  <td>
                    <StatePill label={readText(item, "session_state", "UNKNOWN")} />
                  </td>
                  <td>
                    {readText(item, "active_service_count", "0")} / {" "}
                    {readText(item, "service_count", "0")}
                  </td>
                  <td>{asPercentText(readNumber(item, "health_rate"))}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        event.stopPropagation();
                        openDetailDrawer("connector", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {connectorItems.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    当前没有 connector 数据。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Session 列表</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>状态</span>
              <select
                value={sessionStateFilter}
                onChange={(event) => setSessionStateFilter(event.target.value)}
              >
                <option value="ALL">ALL</option>
                <option value="ACTIVE">ACTIVE</option>
                <option value="DRAINING">DRAINING</option>
                <option value="STALE">STALE</option>
                <option value="CLOSED">CLOSED</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("connectors")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Session ID</th>
                <th>Connector ID</th>
                <th>Epoch</th>
                <th>State</th>
                <th>Last Heartbeat</th>
                <th>Updated</th>
                <th>详情</th>
              </tr>
            </thead>
            <tbody>
              {sessionItems.map((item, index) => (
                <tr
                  id={buildDetailRowElementID("session", index)}
                  key={`${readText(item, "session_id", "session")}-${index}`}
                  className={`table-row-clickable ${isActiveDetailRow("session", index) ? "table-row-active" : ""}`}
                  onClick={() => openDetailDrawer("session", index)}
                  onKeyDown={(event) => handleDetailRowKeyDown(event, "session", index)}
                  tabIndex={0}
                  role="button"
                  aria-label={`查看 Session ${readText(item, "session_id", "unknown")} 详情`}
                >
                  <td>{readText(item, "session_id")}</td>
                  <td>{readText(item, "connector_id")}</td>
                  <td>{readText(item, "epoch", "0")}</td>
                  <td>
                    <StatePill label={readText(item, "state", "UNKNOWN")} />
                  </td>
                  <td>{asPrettyTime(readNumber(item, "last_heartbeat_ms"))}</td>
                  <td>{asPrettyTime(readNumber(item, "updated_at_ms"))}</td>
                  <td>
                    <button
                      type="button"
                      className="row-action-btn"
                      onClick={(event) => {
                        event.stopPropagation();
                        openDetailDrawer("session", index);
                      }}
                    >
                      查看详情
                    </button>
                  </td>
                </tr>
              ))}
              {sessionItems.length === 0 && (
                <tr>
                  <td colSpan={7} className="empty-cell">
                    当前没有 session 数据。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>
    </div>
  );
}
