import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import { asPrettyTime, pickMetricLabel, readNumber, readText } from "../../model";
import { BarDistributionChart, StatePill, TrendLineChart } from "../AdminVisuals";

type ObservabilityPageProps = {
  vm: AdminConsoleViewModel;
};

export function ObservabilityPage(props: ObservabilityPageProps) {
  const {
    activeMetricKey,
    diagnoseIssues,
    diagnoseSummary,
    logActionBars,
    logItems,
    logResultBars,
    metricKeyOptions,
    metricPoints,
    metricSummaryBars,
    metricTrend,
    refreshPageData,
    setActiveMetricKey,
    setTimeRangeMinutes,
    timeRangeMinutes,
  } = props.vm;

  return (
    <div className="content-stack">
      <section className="panel">
        <header className="panel-head">
          <h3>趋势面板</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>指标</span>
              <select
                value={activeMetricKey}
                onChange={(event) => setActiveMetricKey(event.target.value)}
              >
                {metricKeyOptions.map((key) => (
                  <option key={key} value={key}>
                    {pickMetricLabel(key)}
                  </option>
                ))}
              </select>
            </label>
          </div>
        </header>
        <div className="observ-grid">
          <article className="chart-card">
            <h4>{pickMetricLabel(activeMetricKey)} 趋势</h4>
            <TrendLineChart items={metricTrend} emptyText="当前时间窗口无指标数据" />
          </article>
          <article className="chart-card">
            <h4>最新指标快照</h4>
            <BarDistributionChart items={metricSummaryBars} emptyText="暂无快照点位" />
          </article>
          <article className="chart-card">
            <h4>日志结果分布</h4>
            <BarDistributionChart items={logResultBars} emptyText="暂无日志结果统计" />
          </article>
          <article className="chart-card">
            <h4>操作类型 TOP</h4>
            <BarDistributionChart items={logActionBars} emptyText="暂无操作统计" />
          </article>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>日志检索</h3>
          <div className="inline-actions">
            <label className="field-inline">
              <span>窗口</span>
              <select
                value={String(timeRangeMinutes)}
                onChange={(event) => setTimeRangeMinutes(Number(event.target.value))}
              >
                <option value="10">最近 10 分钟</option>
                <option value="30">最近 30 分钟</option>
                <option value="60">最近 60 分钟</option>
              </select>
            </label>
            <button
              type="button"
              className="ghost-btn"
              onClick={() => void refreshPageData("observability")}
            >
              刷新
            </button>
          </div>
        </header>
        <div className="table-wrap">
          <table>
            <thead>
              <tr>
                <th>Time</th>
                <th>Actor</th>
                <th>Path</th>
                <th>Action</th>
                <th>Status</th>
                <th>Result</th>
              </tr>
            </thead>
            <tbody>
              {logItems.map((item, index) => (
                <tr key={`${readText(item, "ts_ms", String(index))}-${index}`}>
                  <td>{asPrettyTime(readNumber(item, "ts_ms"))}</td>
                  <td>{readText(item, "actor", "--")}</td>
                  <td>{readText(item, "path", "--")}</td>
                  <td>{readText(item, "action", "--")}</td>
                  <td>{readText(item, "status", "--")}</td>
                  <td>{readText(item, "result", "--")}</td>
                </tr>
              ))}
              {logItems.length === 0 && (
                <tr>
                  <td colSpan={6} className="empty-cell">
                    当前时间窗口无日志。
                  </td>
                </tr>
              )}
            </tbody>
          </table>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Metrics 快照</h3>
          <span className="panel-sub">points={metricPoints.length}</span>
        </header>
        <div className="snapshot-box">
          <pre>{JSON.stringify(metricPoints[0] ?? {}, null, 2)}</pre>
        </div>
      </section>

      <section className="panel">
        <header className="panel-head">
          <h3>Diagnose</h3>
          <StatePill label={readText(diagnoseSummary, "health", "unknown")} />
        </header>
        <ul className="issue-list">
          {diagnoseIssues.map((issue, index) => (
            <li key={`${issue}-${index}`}>{issue}</li>
          ))}
          {diagnoseIssues.length === 0 && <li>暂无诊断问题。</li>}
        </ul>
      </section>
    </div>
  );
}
