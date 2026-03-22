import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import { readScopeText, readText } from "../../model";
import { StatePill } from "../AdminVisuals";

type ServicesPageProps = {
  vm: AdminConsoleViewModel;
};

export function ServicesPage(props: ServicesPageProps) {
  const { refreshPageData, serviceItems } = props.vm;

  return (
    <section className="panel">
      <header className="panel-head">
        <h3>服务列表</h3>
        <div className="inline-actions">
          <span className="panel-sub">共 {serviceItems.length} 条</span>
          <button
            type="button"
            className="ghost-btn"
            onClick={() => void refreshPageData("services")}
          >
            刷新
          </button>
        </div>
      </header>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Logical Service ID</th>
              <th>Instance ID</th>
              <th>Scope</th>
              <th>服务名</th>
              <th>Agent</th>
              <th>Session</th>
              <th>Endpoint</th>
              <th>SNI</th>
              <th>状态</th>
              <th>访问方式</th>
            </tr>
          </thead>
          <tbody>
            {serviceItems.map((item, index) => (
              <tr
                key={`${readText(item, "logical_service_id", "service")}-${readText(item, "instance_id", String(index))}`}
              >
                <td>{readText(item, "logical_service_id")}</td>
                <td>{readText(item, "instance_id")}</td>
                <td>{readScopeText(item)}</td>
                <td>{readText(item, "service_name")}</td>
                <td>{readText(item, "connector_id", "--")}</td>
                <td>
                  <div className="service-session-cell">
                    <span>{readText(item, "session_id", "--")}</span>
                    <StatePill label={readText(item, "session_state", "UNKNOWN")} />
                  </div>
                </td>
                <td>
                  <div className="service-endpoint-cell">
                    <span>{readText(item, "endpoint_address", "--")}</span>
                    <small>{readText(item, "service_type", "--")}</small>
                  </div>
                </td>
                <td>{readText(item, "sni_name", "--")}</td>
                <td>
                  <div className="service-status-cell">
                    <StatePill label={readText(item, "status", "UNKNOWN")} />
                    <StatePill label={readText(item, "health_status", "UNKNOWN")} />
                  </div>
                </td>
                <td>
                  <div className="service-access-cell">
                    <code>{readText(item, "route_target", "--")}</code>
                    <small>{readText(item, "access_hint", "--")}</small>
                  </div>
                </td>
              </tr>
            ))}
            {serviceItems.length === 0 && (
              <tr>
                <td colSpan={9} className="empty-cell">
                  当前没有服务数据。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}
