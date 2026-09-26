import { useState, type ReactNode } from "react";
import type {
  EChartsOption,
  LineSeriesOption,
  YAXisComponentOption,
} from "echarts";
import { Chart, timeChart, chartColors, type ChartClick } from "./Chart";
import { Empty, Panel } from "./common";
import {
  endpoints,
  latencySeries,
  requestsPerMinute,
  type Analytics,
  type Bucket,
} from "../analytics";

type Series = { name: string; color: string; data: (number | null)[] };
type TrafficMetric = "requests" | "rpm";

function trendOption(
  buckets: Bucket[],
  series: Series[],
  unit: "count" | "percent" | "ms" | "rpm",
): EChartsOption {
  const option = timeChart(buckets, series, unit);
  return {
    ...option,
    grid: { top: 24, left: 82, right: 28, bottom: 112 },
    yAxis: { ...(option.yAxis as YAXisComponentOption), splitNumber: 4 },
    series: (option.series as LineSeriesOption[]).map((line, index) => ({
      ...line,
      smooth: 0.25,
      smoothMonotone: "x",
      lineStyle: {
        width: 2.2,
        type: line.name === "Jev P95" ? "dashed" : "solid",
      },
      areaStyle: { opacity: index === 0 ? 0.09 : 0.035 },
      showSymbol: series[index].data.some(
        (v, i, values) =>
          v !== null && values[i - 1] == null && values[i + 1] == null,
      ),
      symbolSize: 5,
    })),
  };
}

export function trafficTimeline(
  buckets: Bucket[],
  endpoint: string,
  metric: TrafficMetric = "requests",
): EChartsOption {
  return trendOption(
    buckets,
    endpoints
      .filter((e) => !endpoint || e.id === endpoint)
      .map((e) => ({
        name: e.short,
        color: e.color,
        data: buckets.map((b) =>
          metric === "rpm"
            ? requestsPerMinute(b.endpoints[e.id] || 0, b.start, b.end)
            : b.endpoints[e.id] || 0,
        ),
      })),
    metric === "rpm" ? "rpm" : "count",
  );
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
            height={336}
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

export function TrafficTimeline({
  data,
  buckets,
  endpoint,
  onClick,
  latencyDetail,
}: {
  data: Analytics;
  buckets: Bucket[];
  endpoint: string;
  onClick: (event: ChartClick) => void;
  latencyDetail?: ReactNode;
}) {
  const [metric, setMetric] = useState<TrafficMetric>("requests");
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
  const latency = [
    {
      name: "审查 P50",
      color: chartColors.green,
      data: latencySeries(data, buckets, "review_ms", 0.5),
    },
    {
      name: "审查 P95",
      color: chartColors.blue,
      data: latencySeries(data, buckets),
    },
    {
      name: "Jev P95",
      color: chartColors.gray,
      data: latencySeries(data, buckets, "jev_ms"),
    },
  ];
  return (
    <div className="dashboard-trends">
      <TrendPanel
        title="进网流量"
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
              请求量
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
      <div className="trend-comparison">
        <TrendPanel
          title="命中与拦截"
          label="命中与拦截趋势"
          buckets={buckets}
          option={trendOption(buckets, review, "percent")}
          hasData={review.some((s) => s.data.some((v) => v !== null))}
          onClick={onClick}
        />
        <TrendPanel
          title="审查耗时"
          label="审查耗时趋势"
          buckets={buckets}
          option={trendOption(buckets, latency, "ms")}
          hasData={latency.some((s) => s.data.some((v) => v !== null))}
          onClick={onClick}
        >
          {latencyDetail}
        </TrendPanel>
      </div>
    </div>
  );
}
