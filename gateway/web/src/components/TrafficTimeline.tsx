import { useState, type ReactNode } from "react";
import type {
  EChartsOption,
  LineSeriesOption,
  YAXisComponentOption,
} from "echarts";
import { Chart, timeChart, chartColors, type ChartClick } from "./Chart";
import { Empty, Help, Panel } from "./common";
import {
  endpoints,
  latencySeries,
  averageReviewSeries,
  requestsPerMinute,
  type Analytics,
  type Bucket,
} from "../analytics";

type Series = { name: string; color: string; data: (number | null)[] };
type TrafficMetric = "requests" | "rpm";
type ReviewMetric = "mean" | "slow";
function trendOption(
  buckets: Bucket[],
  series: Series[],
  unit: "count" | "percent" | "ms" | "rpm",
): EChartsOption {
  const option = timeChart(buckets, series, unit);
  return {
    ...option,
    grid: { top: 18, left: 62, right: 28, bottom: 112 },
    yAxis: { ...(option.yAxis as YAXisComponentOption), splitNumber: 4 },
    series: (option.series as LineSeriesOption[]).map((line, index) => ({
      ...line,
      smooth: 0.18,
      smoothMonotone: "x",
      lineStyle: { width: 2.4, type: "solid" },
      areaStyle: { opacity: index === 0 ? 0.08 : 0.025 },
      showSymbol: series[index].data.some(
        (v, i, values) =>
          v !== null && values[i - 1] == null && values[i + 1] == null,
      ),
      symbolSize: 5,
    })),
  };
}
export function reviewTimeline(
  data: Analytics,
  buckets: Bucket[],
  metric: ReviewMetric = "mean",
): EChartsOption {
  return trendOption(
    buckets,
    [
      {
        name: metric === "mean" ? "平均耗时" : "慢请求耗时",
        color: chartColors.blue,
        data:
          metric === "mean"
            ? averageReviewSeries(data, buckets)
            : latencySeries(data, buckets),
      },
    ],
    "ms",
  );
}
export function trafficTimeline(
  buckets: Bucket[],
  endpoint: string,
  metric: TrafficMetric = "requests",
): EChartsOption {
  const option = timeChart(
    buckets,
    endpoints
      .filter((e) => !endpoint || e.id === endpoint)
      .map((e) => ({
        name: e.short,
        color: e.color,
        type: "bar",
        stack: "inputs",
        data: buckets.map((b) =>
          metric === "rpm"
            ? requestsPerMinute(b.endpoints[e.id] || 0, b.start, b.end)
            : b.endpoints[e.id] || 0,
        ),
      })),
    metric === "rpm" ? "rpm" : "count",
  );
  return { ...option, grid: { top: 18, left: 58, right: 28, bottom: 112 } };
}
function TrendPanel({
  title,
  label,
  buckets,
  option,
  hasData = true,
  extra,
  children,
  onClick,
}: {
  title: string;
  label: string;
  buckets: Bucket[];
  option: EChartsOption;
  hasData?: boolean;
  extra?: ReactNode;
  children?: ReactNode;
  onClick: (event: ChartClick) => void;
}) {
  const [hidden, setHidden] = useState<Set<string>>(new Set());
  const series = option.series as LineSeriesOption[];
  return (
    <Panel
      title={title}
      extra={extra}
      className={`trend-panel ${hasData ? "" : "trend-empty"}`}
    >
      {hasData ? (
        <>
          <div className="trend-legend" aria-label={`${title}图例`}>
            {series.map((s) => {
              const name = String(s.name);
              return (
                <button
                  key={name}
                  type="button"
                  aria-pressed={!hidden.has(name)}
                  onClick={() =>
                    setHidden((old) => {
                      const next = new Set(old);
                      if (next.has(name)) next.delete(name);
                      else next.add(name);
                      return next;
                    })
                  }
                >
                  <i style={{ background: String(s.itemStyle?.color) }} />
                  {name}
                </button>
              );
            })}
          </div>
          <Chart
            label={label}
            ticks={buckets.length}
            height={280}
            option={{
              ...option,
              series: series.filter((s) => !hidden.has(String(s.name))),
            }}
            onClick={onClick}
          />
        </>
      ) : (
        <Empty />
      )}
      {children}
    </Panel>
  );
}
type TrendProps = { buckets: Bucket[]; onClick: (event: ChartClick) => void };
export function TrafficTrend({
  buckets,
  endpoint,
  onClick,
}: TrendProps & { endpoint: string }) {
  const [metric, setMetric] = useState<TrafficMetric>("requests");
  return (
    <TrendPanel
      title="用户输入趋势"
      label={metric === "rpm" ? "进网 RPM 趋势" : "进网请求趋势"}
      buckets={buckets}
      option={trafficTimeline(buckets, endpoint, metric)}
      onClick={onClick}
      extra={
        <div className="trend-metric" role="group" aria-label="流量指标">
          <button
            type="button"
            aria-pressed={metric === "requests"}
            onClick={() => setMetric("requests")}
          >
            输入量
          </button>
          <button
            type="button"
            aria-pressed={metric === "rpm"}
            onClick={() => setMetric("rpm")}
          >
            RPM
          </button>
        </div>
      }
    />
  );
}
export function RiskTrend({ buckets, onClick }: TrendProps) {
  const review = [
    {
      name: "命中率",
      color: chartColors.purple,
      data: buckets.map((b) => b.hitRate),
    },
    {
      name: "拦截率",
      color: chartColors.red,
      data: buckets.map((b) => b.blockRate),
    },
    {
      name: "Jev 失败率",
      color: chartColors.amber,
      data: buckets.map((b) => b.failureRate),
    },
  ];
  return (
    <TrendPanel
      title="命中与拦截"
      label="命中与拦截趋势"
      buckets={buckets}
      option={trendOption(buckets, review, "percent")}
      hasData={review.some((s) => s.data.some((v) => v !== null))}
      onClick={onClick}
    />
  );
}
export function ReviewTrend({
  data,
  buckets,
  onClick,
  children,
}: TrendProps & { data: Analytics; children?: ReactNode }) {
  const [metric, setMetric] = useState<ReviewMetric>("mean");
  const option = reviewTimeline(data, buckets, metric);
  return (
    <TrendPanel
      title="审查耗时"
      label="审查耗时趋势"
      buckets={buckets}
      option={option}
      hasData={(option.series as LineSeriesOption[]).some((s) =>
        s.data?.some((v) => v !== null),
      )}
      onClick={onClick}
      extra={
        <div className="trend-metric" role="group" aria-label="耗时指标">
          <button
            type="button"
            aria-pressed={metric === "mean"}
            onClick={() => setMetric("mean")}
          >
            平均耗时
          </button>
          <button
            type="button"
            aria-pressed={metric === "slow"}
            onClick={() => setMetric("slow")}
          >
            慢请求
          </button>
          <Help>
            慢请求耗时表示 95%
            的送审请求在这个时间内完成，根据耗时分桶估算。不包含上游响应时间和缓存复用。
          </Help>
        </div>
      }
    >
      {children}
    </TrendPanel>
  );
}
