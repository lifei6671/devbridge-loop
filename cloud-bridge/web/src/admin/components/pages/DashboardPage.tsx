import type { AdminConsoleViewModel } from "../../hooks/useAdminConsole";
import { asPrettyTime, readNumber, readText } from "../../model";
import {
  BarDistributionChart,
  MultiTrendChart,
  StatePill,
  TunnelStatusRing,
} from "../AdminVisuals";

type DashboardPageProps = {
  vm: AdminConsoleViewModel;
};

export function DashboardPage(props: DashboardPageProps) {
  const {
    dashboardHealthBars,
    dashboardMetricCards,
    dashboardTopCounters,
    dashboardTrendSeries,
    diagnoseIssues,
    diagnoseSummary,
    exportDownloadURL,
    lastSyncMS,
    metricPoints,
    navigateToPage,
    overviewListeners,
    performExportDiagnose,
    realtimeSummary,
    trafficSummary,
    tunnelRingSegments,
    tunnelSummary,
  } = props.vm;

  return (
    <div className="dashboard-stack">
      <section className="panel hero-panel">
        <div className="hero-copy">
          <p className="hero-eyebrow">Bridge Runtime Dashboard</p>
          <h3 className="hero-title">Bridge 运行总览</h3>
          <p className="hero-sub">
            面向控制面运维与排障，统一查看连接、隧道池、流量退化与诊断信号。
          </p>
          <div className="hero-badges">
            <StatePill label={readText(diagnoseSummary, "health", "unknown")} />
            <span className={`shell-status-pill tone-${realtimeSummary.tone}`}>
              {realtimeSummary.label}
            </span>
            <span className="shell-inline-note">
              最后同步 {lastSyncMS > 0 ? asPrettyTime(lastSyncMS) : "--"}
            </span>
          </div>
        </div>
        <div className="hero-actions">
          <button type="button" className="ghost-btn" onClick={() => void performExportDiagnose()}>
            导出快照
          </button>
          <button
            type="button"
            className="solid-btn"
            onClick={() => navigateToPage("observability")}
          >
            运行诊断
          </button>
          {exportDownloadURL !== "" && (
            <a className="link-btn" href={exportDownloadURL} target="_blank" rel="noreferrer">
              下载诊断包
            </a>
          )}
        </div>
      </section>

      <section className="metric-strip">
        {dashboardMetricCards.map((card) => (
          <article
            key={card.label}
            className={`dashboard-metric-card ${card.tone ? `tone-${card.tone}` : ""}`}
          >
            <p className="dashboard-metric-label">{card.label}</p>
            <strong className="dashboard-metric-value">{card.value}</strong>
            <span className="dashboard-metric-hint">{card.hint}</span>
          </article>
        ))}
      </section>

      <div className="dashboard-core-grid">
        <section className="panel">
          <header className="panel-head">
            <div>
              <h3>关键计数趋势</h3>
              <p className="panel-sub">最近 30 分钟的等待、超时与 fallback 形态</p>
            </div>
            <span className="panel-sub">metrics={metricPoints.length}</span>
          </header>
          <MultiTrendChart
            series={dashboardTrendSeries}
            emptyText="当前尚未采集到趋势点位，请刷新后重试。"
          />
        </section>

        <section className="panel">
          <header className="panel-head">
            <div>
              <h3>Tunnel / Traffic 状态</h3>
              <p className="panel-sub">桥侧池状态、退化计数与风险摘要</p>
            </div>
            <span className="panel-sub">
              更新时间 {asPrettyTime(readNumber(tunnelSummary, "updated_at_ms"))}
            </span>
          </header>
          <TunnelStatusRing
            items={tunnelRingSegments}
            centerLabel="idle"
            centerValue={readText(tunnelSummary, "idle", "0")}
          />
          <div className="signal-grid">
            <article className="signal-card">
              <span className="signal-head">Open Reject</span>
              <strong className="signal-value">
                {readText(trafficSummary, "open_reject_total", "0")}
              </strong>
              <span className="signal-note">连接打开阶段被拒绝</span>
            </article>
            <article className="signal-card">
              <span className="signal-head">Auth Failure</span>
              <strong className="signal-value">
                {readText(trafficSummary, "auth_failure_total", "0")}
              </strong>
              <span className="signal-note">认证失败与权限问题</span>
            </article>
            <article className="signal-card">
              <span className="signal-head">Host Derive Fail</span>
              <strong className="signal-value">
                {readText(trafficSummary, "host_derive_failure_total", "0")}
              </strong>
              <span className="signal-note">Host 推导异常</span>
            </article>
          </div>
        </section>
      </div>

      <div className="dashboard-lower-grid">
        <section className="panel">
          <header className="panel-head">
            <div>
              <h3>监听入口</h3>
              <p className="panel-sub">当前 Bridge 已启用的入口、端口与用途</p>
            </div>
            <span className="panel-sub">listeners={overviewListeners.length}</span>
          </header>
          {overviewListeners.length === 0 ? (
            <p className="listener-empty">当前未发现启用中的监听入口。</p>
          ) : (
            <div className="listener-grid">
              {overviewListeners.map((item, index) => (
                <article
                  key={`${readText(item, "listener_id", "listener")}-${index}`}
                  className="listener-card"
                >
                  <div className="listener-top">
                    <div>
                      <p className="listener-label">{readText(item, "label", "--")}</p>
                      <code className="listener-addr">{readText(item, "listen_addr", "--")}</code>
                    </div>
                    <div className="listener-port-wrap">
                      <span className="listener-port-label">Port</span>
                      <strong className="listener-port">{readText(item, "port", "--")}</strong>
                    </div>
                  </div>
                  <p className="listener-purpose">{readText(item, "purpose", "--")}</p>
                </article>
              ))}
            </div>
          )}
        </section>

        <section className="panel">
          <header className="panel-head">
            <div>
              <h3>Connector / Session 风险</h3>
              <p className="panel-sub">优先关注认证、Ack 延迟、路由冲突与回收失败</p>
            </div>
            <StatePill label={readText(diagnoseSummary, "health", "unknown")} />
          </header>
          <BarDistributionChart items={dashboardHealthBars} emptyText="暂无风险分布。" />
          <ul className="issue-list issue-list-tight">
            {diagnoseIssues.length === 0 && <li>暂无诊断问题，当前运行状态稳定。</li>}
            {diagnoseIssues.slice(0, 4).map((issue, index) => (
              <li key={`${issue}-${index}`}>{issue}</li>
            ))}
          </ul>
        </section>

        <section className="panel">
          <header className="panel-head">
            <div>
              <h3>最近高频错误</h3>
              <p className="panel-sub">聚焦当前最值得优先处理的异常面</p>
            </div>
            <button type="button" className="ghost-btn compact" onClick={() => navigateToPage("ops")}>
              去运维
            </button>
          </header>
          <div className="error-card-list">
            {dashboardTopCounters.map((item) => (
              <article key={item.label} className="error-card">
                <span>{item.label}</span>
                <strong>{item.value}</strong>
              </article>
            ))}
          </div>
          <div className="section-link-row">
            <button type="button" className="ghost-btn compact" onClick={() => navigateToPage("traffic")}>
              查看隧道流量
            </button>
            <button
              type="button"
              className="ghost-btn compact"
              onClick={() => navigateToPage("observability")}
            >
              打开观测面板
            </button>
          </div>
        </section>
      </div>
    </div>
  );
}
