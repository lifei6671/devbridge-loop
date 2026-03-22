import type {
  ChartDatum,
  MultiTrendSeries,
  TrendDatum,
  TunnelRingSegment,
} from "../../model";

type MultiTrendChartProps = {
  series: MultiTrendSeries[];
  emptyText: string;
};

type TunnelStatusRingProps = {
  items: TunnelRingSegment[];
  centerLabel: string;
  centerValue: string;
};

type BarDistributionChartProps = {
  items: ChartDatum[];
  emptyText: string;
};

type TrendLineChartProps = {
  items: TrendDatum[];
  emptyText: string;
};

type ChartPoint = {
  x: number;
  y: number;
  label: string;
};

type TrendPoint = ChartPoint & {
  value: number;
};

function buildMultiTrendPoints(items: TrendDatum[]): ChartPoint[] {
  const width = 640;
  const height = 248;
  const paddingX = 18;
  const paddingY = 20;
  const values = items.map((item) => item.value);
  const minValue = Math.min(...values);
  const maxValue = Math.max(...values);
  const valueRange = maxValue - minValue || 1;

  return items.map((item, index) => {
    let x = width / 2;
    if (items.length > 1) {
      x = paddingX + (index / (items.length - 1)) * (width - paddingX * 2);
    }
    const y =
      height - paddingY - ((item.value - minValue) / valueRange) * (height - paddingY * 2);
    return {
      x,
      y,
      label: item.label,
    };
  });
}

function buildTunnelRingGradient(items: TunnelRingSegment[]): string {
  const totalValue = items.reduce((sum, item) => sum + item.value, 0);
  if (totalValue <= 0) {
    return "conic-gradient(#e6ebfb 0deg 360deg)";
  }

  let currentRatio = 0;
  const stops = items
    .filter((item) => item.value > 0)
    .map((item) => {
      const start = currentRatio * 360;
      currentRatio += item.value / totalValue;
      const end = currentRatio * 360;
      return item.color + " " + start + "deg " + end + "deg";
    })
    .join(", ");
  return "conic-gradient(" + stops + ")";
}

function buildTrendLinePoints(items: TrendDatum[]): TrendPoint[] {
  const width = 520;
  const height = 168;
  const paddingX = 12;
  const paddingY = 18;
  const values = items.map((item) => item.value);
  const minValue = Math.min(...values);
  const maxValue = Math.max(...values);
  const valueRange = maxValue - minValue || 1;

  return items.map((item, index) => {
    let x = width / 2;
    if (items.length > 1) {
      x = paddingX + (index / (items.length - 1)) * (width - paddingX * 2);
    }
    const y =
      height - paddingY - ((item.value - minValue) / valueRange) * (height - paddingY * 2);
    return {
      x,
      y,
      label: item.label,
      value: item.value,
    };
  });
}

/**
 * MultiTrendChart 用多折线展示多个关键计数器的变化趋势。
 */
export function MultiTrendChart(props: MultiTrendChartProps) {
  const usableSeries = props.series.filter((item) => item.items.length > 0);
  if (usableSeries.length === 0) {
    return <p className="chart-empty">{props.emptyText}</p>;
  }

  const width = 640;
  const height = 248;
  const paddingX = 18;
  const paddingY = 20;
  const gridLineCount = 4;
  const firstSeriesPoints = buildMultiTrendPoints(usableSeries[0].items);

  return (
    <div className="multi-trend-chart">
      <div className="multi-trend-legend">
        {usableSeries.map((item) => (
          <div key={item.label} className="multi-trend-legend-item">
            <span className={"legend-dot legend-" + item.tone} aria-hidden="true" />
            <span>{item.label}</span>
            <strong>{item.latestValue}</strong>
          </div>
        ))}
      </div>
      <svg viewBox={["0 0", width, height].join(" ")} preserveAspectRatio="none">
        {Array.from({ length: gridLineCount }, (_, index) => {
          const ratio = index / (gridLineCount - 1);
          const y = paddingY + ratio * (height - paddingY * 2);
          return (
            <line
              key={y}
              className="multi-trend-grid-line"
              x1={paddingX}
              x2={width - paddingX}
              y1={y}
              y2={y}
            />
          );
        })}
        {usableSeries.map((item) => {
          const points = buildMultiTrendPoints(item.items);
          const polylinePoints = points
            .map((point) => String(point.x) + "," + String(point.y))
            .join(" ");
          return (
            <g key={item.label}>
              <polyline className={"multi-trend-line " + item.tone} points={polylinePoints} />
              <circle
                className={"multi-trend-point " + item.tone}
                cx={points[points.length - 1]?.x ?? width / 2}
                cy={points[points.length - 1]?.y ?? height / 2}
                r={4}
              />
            </g>
          );
        })}
      </svg>
      <div className="trend-foot">
        <span>{firstSeriesPoints[0]?.label ?? "--"}</span>
        <span>{firstSeriesPoints[firstSeriesPoints.length - 1]?.label ?? "--"}</span>
      </div>
    </div>
  );
}

/**
 * TunnelStatusRing 用圆环分布表达 tunnel 状态占比。
 */
export function TunnelStatusRing(props: TunnelStatusRingProps) {
  const gradientText = buildTunnelRingGradient(props.items);

  return (
    <div className="tunnel-ring-layout">
      <div className="tunnel-ring" style={{ background: gradientText }}>
        <div className="tunnel-ring-inner">
          <span>{props.centerLabel}</span>
          <strong>{props.centerValue}</strong>
        </div>
      </div>
      <div className="tunnel-ring-legend">
        {props.items.map((item) => (
          <div key={item.label} className="tunnel-ring-legend-item">
            <span
              className="legend-dot"
              style={{ backgroundColor: item.color }}
              aria-hidden="true"
            />
            <span>{item.label}</span>
            <strong>{item.value}</strong>
          </div>
        ))}
      </div>
    </div>
  );
}

/**
 * BarDistributionChart 用横向条展示分布值，适合离散统计结果。
 */
export function BarDistributionChart(props: BarDistributionChartProps) {
  if (props.items.length === 0) {
    return <p className="chart-empty">{props.emptyText}</p>;
  }
  const maxValue = props.items.reduce((max, item) => Math.max(max, item.value), 0);
  return (
    <div className="bar-chart">
      {props.items.map((item) => {
        const ratio = maxValue <= 0 ? 0 : item.value / maxValue;
        const widthText = String(Math.max(ratio * 100, ratio > 0 ? 3 : 0)) + "%";
        return (
          <div key={item.label} className="bar-row">
            <div className="bar-row-head">
              <span>{item.label}</span>
              <strong>{item.value}</strong>
            </div>
            <div className="bar-track">
              <div className={"bar-fill " + (item.tone ?? "normal")} style={{ width: widthText }} />
            </div>
          </div>
        );
      })}
    </div>
  );
}

/**
 * TrendLineChart 用轻量 SVG 绘制折线趋势，避免引入额外图表依赖。
 */
export function TrendLineChart(props: TrendLineChartProps) {
  if (props.items.length === 0) {
    return <p className="chart-empty">{props.emptyText}</p>;
  }

  const width = 520;
  const height = 168;
  const paddingY = 18;
  const points = buildTrendLinePoints(props.items);
  const polylinePoints = points
    .map((point) => String(point.x) + "," + String(point.y))
    .join(" ");
  const areaPath = [
    "M",
    points[0].x,
    points[0].y,
    "L",
    polylinePoints,
    "L",
    points[points.length - 1].x,
    height - paddingY,
    "L",
    points[0].x,
    height - paddingY,
    "Z",
  ].join(" ");

  return (
    <div className="trend-chart">
      <svg viewBox={["0 0", width, height].join(" ")} preserveAspectRatio="none">
        <path className="trend-area" d={areaPath} />
        <polyline className="trend-line" points={polylinePoints} />
        {points.map((point, index) => (
          <circle key={point.label + "-" + index} className="trend-dot" cx={point.x} cy={point.y} r={3} />
        ))}
      </svg>
      <div className="trend-foot">
        <span>{points[0]?.label ?? "--"}</span>
        <span>{points[points.length - 1]?.label ?? "--"}</span>
      </div>
    </div>
  );
}
