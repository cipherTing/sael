import { useEffect } from "react";
import { useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import type { EChartsOption } from "echarts";
import { request } from "../api";
import { Chart, chartColors, barChart, Legend } from "../components/Chart";
import { Panel, Empty, Loading } from "../components/common";
import { RefreshButton } from "../components/RefreshButton";
import { donutOption } from "../dashboardMetrics";
import { TimeRangePicker, rangeFromQuery, rangeQuery, requestRange } from "../TimeRangePicker";
import { count, duration, percent, ratio } from "../analytics";
import { notifyRetry } from "../notifications";
import "../review-api.css";

type NamedCount = { name: string; count: number };
type Trend = {
  time: string; requests: number; hits: number; blocked: number; errors: number;
  duration_ms: number; p50_ms: number; p95_ms: number;
};
type Overview = {
  since: string; until: string; requests: number; rpm: number; allowed: number; hits: number;
  blocked: number; errors: number; cache_hits: number; p50_ms: number; p95_ms: number;
  outcomes: NamedCount[]; scenes: NamedCount[]; keys: NamedCount[]; trend: Trend[];
};

function trendOption(
  data: Trend[],
  fields: { key: keyof Trend; name: string; color: string }[],
  latency = false,
): EChartsOption {
  return {
    animationDuration: 250,
    color: fields.map((x) => x.color),
    tooltip: {
      trigger: "axis",
      valueFormatter: (value) => (latency ? `${value} ms` : `${value} 次`),
    },
    grid: { top: 24, left: 54, right: 20, bottom: 36 },
    xAxis: {
      type: "time",
      axisLabel: { color: "#8a94a6", hideOverlap: true },
      axisLine: { lineStyle: { color: "#e7eaf0" } },
      axisTick: { show: false },
    },
    yAxis: {
      type: "value",
      min: 0,
      minInterval: latency ? undefined : 1,
      axisLabel: {
        color: "#8a94a6",
        formatter: latency ? "{value} ms" : "{value}",
      },
      splitLine: { lineStyle: { color: "#edf0f5", type: "dashed" } },
    },
    series: fields.map((field) => ({
      name: field.name,
      type: "line",
      showSymbol: false,
      connectNulls: false,
      data: data.map((point) => [point.time, point[field.key] as number]),
      lineStyle: { width: 2 },
    })),
  };
}

export default function ReviewAPIStats({ enabled }: { enabled: boolean }) {
  const [params, setParams] = useSearchParams();
  const queryParams = rangeQuery(
    new URLSearchParams(params),
    rangeFromQuery(params),
  ).toString();
  const query = useQuery({
    queryKey: ["review-api-overview", queryParams],
    queryFn: () =>
      request<Overview>(
        `/admin/review-api/overview?${requestRange(new URLSearchParams(queryParams))}`,
      ),
    enabled,
    refetchInterval: enabled ? 30000 : false,
  });
  useEffect(() => {
    if (query.error) notifyRetry(query.error, () => void query.refetch());
  }, [query.error, query.refetch]);
  if (!enabled)
    return (
      <section className="review-api-disabled">
        <h2>HTTP 审查接口未启用</h2>
        <p>
          开启后会使用现有场景和 Jev
          配置，并展示独立的审核统计。已有密钥和统计会保留。
        </p>
      </section>
    );
  const d = query.data;
  const parts = d
    ? [
        { name: "未命中放行", value: d.allowed, color: chartColors.green },
        {
          name: "命中放行",
          value: d.hits - d.blocked,
          color: chartColors.amber,
        },
        { name: "拦截", value: d.blocked, color: chartColors.red },
        { name: "错误", value: d.errors, color: chartColors.gray },
      ]
    : [];
  const requests = [
    { key: "requests", name: "请求", color: chartColors.blue },
    { key: "hits", name: "命中", color: chartColors.amber },
    { key: "blocked", name: "拦截", color: chartColors.red },
  ] satisfies { key: keyof Trend; name: string; color: string }[];
  const latency = [
    { key: "p50_ms", name: "P50", color: chartColors.blue },
    { key: "p95_ms", name: "P95", color: chartColors.purple },
  ] satisfies { key: keyof Trend; name: string; color: string }[];
  return (
    <>
      <div className="review-api-dashboard-heading">
        <div>
          <h2>审核接口统计</h2>
          <p>独立统计 HTTP 审核请求，不计入网关请求。</p>
        </div>
      </div>
      <div className="review-api-toolbar">
        <TimeRangePicker
          value={rangeFromQuery(params)}
          onChange={(value) => setParams(rangeQuery(params, value))}
        />
        <RefreshButton
          label="刷新统计"
          busy={query.isFetching}
          onRefresh={() => query.refetch()}
        />
      </div>
      {query.isPending ? (
        <Loading />
      ) : query.isError ? (
        <Empty text="统计暂时不可用，请刷新重试" />
      ) : (
        d && (
          <>
            <div className="review-api-metrics">
              {[
                ["请求数", count(d.requests)],
                ["最近一分钟 RPM", count(d.rpm)],
                ["放行", count(d.allowed + d.hits - d.blocked)],
                ["命中", count(d.hits)],
                ["拦截", count(d.blocked)],
                ["错误", count(d.errors)],
                ["命中率", percent(ratio(d.hits, d.requests))],
                ["拦截率", percent(ratio(d.blocked, d.requests))],
                ["P50", d.requests ? duration(d.p50_ms) : "—"],
                ["P95", d.requests ? duration(d.p95_ms) : "—"],
                ["缓存命中率", percent(ratio(d.cache_hits, d.requests))],
                ["缓存命中", count(d.cache_hits)],
              ].map(([label, value]) => (
                <div className="review-api-metric" key={label}>
                  <span>{label}</span>
                  <strong>{value}</strong>
                </div>
              ))}
            </div>
            <p className="review-api-scope">
              统计包含无效密钥、参数错误和审核失败；比率按全部请求计算。RPM
              为最近 60 秒请求数，耗时为分桶估计。
            </p>
            <div className="review-api-grid">
              <Panel title="结果分布">
                <Chart
                  option={donutOption(parts)}
                  label="结果分布"
                  height={220}
                />
                <Legend items={parts} />
              </Panel>
              <Panel title="请求与命中趋势">
                <Chart
                  option={trendOption(d.trend, requests)}
                  label="请求与命中趋势"
                />
                <Legend items={requests} />
              </Panel>
              <Panel title="审核耗时趋势">
                <Chart
                  option={trendOption(d.trend, latency, true)}
                  label="审核耗时趋势"
                />
                <Legend items={latency} />
              </Panel>
              <Panel title="场景命中排行">
                <Chart
                  option={barChart(
                    d.scenes.map((x) => x.name),
                    d.scenes.map((x) => x.count),
                    chartColors.amber,
                  )}
                  label="场景命中排行"
                  height={Math.max(220, d.scenes.length * 28)}
                />
                {!d.scenes.length && <Empty />}
              </Panel>
              <Panel title="Key 使用排行" className="review-api-wide">
                <Chart
                  option={barChart(
                    d.keys.map((x) => x.name),
                    d.keys.map((x) => x.count),
                  )}
                  label="Key 使用排行"
                  height={Math.max(180, d.keys.length * 28)}
                />
                {!d.keys.length && <Empty />}
              </Panel>
            </div>
          </>
        )
      )}
    </>
  );
}
