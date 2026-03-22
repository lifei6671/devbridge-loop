import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import { buildDetailRowElementID, readText } from "../../model";

type RoutesPageProps = {
  vm: AdminConsoleViewModel;
};

export function RoutesPage(props: RoutesPageProps) {
  const { handleDetailRowKeyDown, isActiveDetailRow, openDetailDrawer, routeItems } = props.vm;

  return (
    <section className="panel">
      <header className="panel-head">
        <h3>Route 列表</h3>
        <span className="panel-sub">共 {routeItems.length} 条</span>
      </header>
      <div className="table-wrap">
        <table>
          <thead>
            <tr>
              <th>Route ID</th>
              <th>Target</th>
              <th>Host</th>
              <th>Path Prefix</th>
              <th>Priority</th>
              <th>Version</th>
              <th>详情</th>
            </tr>
          </thead>
          <tbody>
            {routeItems.map((item, index) => (
              <tr
                id={buildDetailRowElementID("route", index)}
                key={`${readText(item, "route_id", "route")}-${index}`}
                className={`table-row-clickable ${isActiveDetailRow("route", index) ? "table-row-active" : ""}`}
                onClick={() => openDetailDrawer("route", index)}
                onKeyDown={(event) => handleDetailRowKeyDown(event, "route", index)}
                tabIndex={0}
                role="button"
                aria-label={`查看 Route ${readText(item, "route_id", "unknown")} 详情`}
              >
                <td>{readText(item, "route_id")}</td>
                <td>{readText(item, "target_type")}</td>
                <td>{readText(item, "host")}</td>
                <td>{readText(item, "path_prefix")}</td>
                <td>{readText(item, "priority", "0")}</td>
                <td>{readText(item, "resource_version", "0")}</td>
                <td>
                  <button
                    type="button"
                    className="row-action-btn"
                    onClick={(event) => {
                      event.stopPropagation();
                      openDetailDrawer("route", index);
                    }}
                  >
                    查看详情
                  </button>
                </td>
              </tr>
            ))}
            {routeItems.length === 0 && (
              <tr>
                <td colSpan={7} className="empty-cell">
                  当前没有路由数据。
                </td>
              </tr>
            )}
          </tbody>
        </table>
      </div>
    </section>
  );
}
