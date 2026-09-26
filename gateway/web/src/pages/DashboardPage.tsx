import { useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { ArrowUpRight, RefreshCw, X, ExternalLink } from "lucide-react";
import type { EChartsOption } from "echarts";
import { TrafficTimeline } from "../components/TrafficTimeline";
import { request } from "../api";
import {
  endpoints,
  errorNames,
  summarize,
  groupTraffic,
  quantile,
  count,
  percent,
  duration,
  sceneStats,
  drilldownQuery,
  type Analytics,
  type Bucket,
} from "../analytics";
import {
  TimeRangePicker,
  rangeFromQuery,
  rangeQuery,
  requestRange,
} from "../TimeRangePicker";
import { Button } from "../components/ui/button";
import {
  Choice,
  EndpointLabel,
  Empty,
  ErrorState,
  Help,
  Loading,
  PageHeading,
  Panel,
} from "../components/common";
import { Chart, barChart, chartColors } from "../components/Chart";

function Outcome({
  data,
  link,
}: {
  data: Analytics;
  link: (action: string) => string;
}) {
  const s = summarize(data.traffic),
    parts = [
      {
        label: "记录放行",
        count: s.allowed,
        color: chartColors.green,
        action: "allow",
      },
      {
        label: "拦截",
        count: s.blocked,
        color: chartColors.red,
        action: "block",
      },
    ];
  return (
    <div className="outcome-body">
      <div className="outcome-strip">
        {parts
          .filter((p) => p.count > 0)
          .map((p) =>
            p.action ? (
              <Link
                aria-label={`查看${p.label}记录`}
                to={link(p.action)}
                key={p.label}
                style={{
                  width: `${(p.count / s.hits) * 100}%`,
                  background: p.color,
                }}
              />
            ) : (
              <span
                key={p.label}
                style={{
                  width: `${(p.count / s.hits) * 100}%`,
                  background: p.color,
                }}
              />
            ),
          )}
      </div>
      <div className="outcome-legend">
        {parts.map((p) => (
          <span key={p.label}>
            <i style={{ background: p.color }} />
            {p.action ? (
              <Link to={link(p.action)}>{p.label}</Link>
            ) : (
              p.label
            )}{" "}
            <b>{count(p.count)}</b>{" "}
            <span>{percent(s.hits ? (p.count / s.hits) * 100 : null)}</span>
          </span>
        ))}
      </div>
    </div>
  );
}
function LatencyHeatmap({
  data,
  buckets,
}: {
  data: Analytics;
  buckets: Bucket[];
}) {
  const bounds = [100, 300, 1000, 3000, 10000, 30000, Infinity],
    labels = [
      "≤ 100 ms",
      "100–300 ms",
      "300 ms–1 s",
      "1–3 s",
      "3–10 s",
      "10–30 s",
      "> 30 s",
    ];
  const times = new Map(buckets.map((b, i) => [Date.parse(b.time), i])),
    cells = new Map<string, number>();
  for (const p of data.distributions) {
    if (p.metric !== "review_ms") continue;
    const x = times.get(Date.parse(p.time));
    if (x === undefined) continue;
    const y = bounds.findIndex((b) => p.upper <= b),
      key = `${x},${y}`;
    cells.set(key, (cells.get(key) || 0) + p.count);
  }
  if (!cells.size) return <Empty />;
  const points = [...cells].map(([key, n]) => [
    ...key.split(",").map(Number),
    n,
  ]);
  const option: EChartsOption = {
    animation: false,
    grid: { top: 15, left: 86, right: 20, bottom: 130 },
    xAxis: {
      type: "category",
      data: buckets.map((b) => b.label),
      axisLabel: { interval: 0, rotate: 60, fontSize: 10, color: "#8a94a6" },
      axisTick: { show: false },
      axisLine: { show: false },
    },
    yAxis: {
      type: "category",
      data: labels,
      axisLabel: { fontSize: 10, color: "#8a94a6" },
      axisTick: { show: false },
      axisLine: { show: false },
    },
    visualMap: {
      show: false,
      min: 0,
      max: Math.max(...points.map((p) => p[2])),
      inRange: { color: ["#eff3ff", "#98aaec", "#405cc1"] },
    },
    tooltip: {
      appendTo: "body",
      formatter: ((p: { data: number[] }) =>
        `${buckets[p.data[0]].label}<br/>${labels[p.data[1]]} · ${count(p.data[2])} 次`) as never,
    },
    series: [
      {
        type: "heatmap",
        data: points,
        itemStyle: { borderWidth: 2, borderColor: "#fff" },
        emphasis: { itemStyle: { borderColor: "#97a9e3" } },
      },
    ],
  };
  return (
    <Chart
      label="审查耗时热力图"
      ticks={buckets.length}
      height={320}
      option={option}
    />
  );
}
export default function DashboardPage({ enabled }: { enabled: boolean }) {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams(),
    [dimension, setDimension] = useState<"endpoint" | "model">("endpoint");
  const query = new URLSearchParams();
  for (const key of [
    "start",
    "end",
    "minutes",
    "range",
    "endpoint",
    "model",
    "granularity",
  ])
    if (params.get(key)) query.set(key, params.get(key)!);
  if (!query.has("start") && !query.has("minutes"))
    query.set("minutes", "1440");
  const granularity = params.get("granularity") || "1h";
  query.set("granularity", granularity);
  query.set("timezone", Intl.DateTimeFormat().resolvedOptions().timeZone);
  const result = useQuery({
    queryKey: ["analytics", query.toString()],
    queryFn: () =>
      request<Analytics>(`/admin/analytics?${requestRange(query)}`),
    refetchInterval: 30_000,
  });
  const data = result.data,
    endpoint = params.get("endpoint") || "",
    model = params.get("model") || "";
  function filter(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  }
  const range = rangeFromQuery(params);
  const buckets = data ? groupTraffic(data) : [],
    stats = data ? summarize(data.traffic) : null,
    previous = data ? summarize(data.previous) : null;
  const goRecords = (extra: Record<string, string> = {}) =>
    data ? `/events?${drilldownQuery(params, data, extra)}` : "/events";
  const change =
    stats && previous
      ? previous.total
        ? `${stats.total >= previous.total ? "+" : ""}${(((stats.total - previous.total) / previous.total) * 100).toFixed(1)}%`
        : stats.total
          ? `+${count(stats.total)}`
          : "—"
      : "—";
  const latency = data
    ? data.distributions.filter((p) => p.metric === "review_ms")
    : [];
  const scenes = data ? sceneStats(data) : [];
  const errorGroups = data
    ? Object.entries(
        data.errors.reduce<Record<string, number>>(
          (all, p) => ({ ...all, [p.kind]: (all[p.kind] || 0) + p.count }),
          {},
        ),
      ).sort((a, b) => b[1] - a[1])
    : [];
  return (
    <>
      <PageHeading title="总览">
        <span className="review-state">
          <i className={`state-dot ${enabled ? "on" : ""}`} />
          {enabled ? "审查已开启" : "审查已关闭"}
        </span>
      </PageHeading>
      <div className="filterbar">
        <Choice
          label="端点筛选"
          value={endpoint}
          onChange={(value) => filter("endpoint", value)}
          options={[
            { value: "", label: "全部端点" },
            ...endpoints.map((e) => ({
              value: e.id,
              label: <EndpointLabel id={e.id} />,
            })),
          ]}
        />
        <Choice
          label="模型筛选"
          value={model}
          onChange={(value) => filter("model", value)}
          options={[
            { value: "", label: "全部模型" },
            ...[
              ...new Set([...(data?.models || []), ...(model ? [model] : [])]),
            ].map((m) => ({ value: m, label: m })),
          ]}
        />
        {endpoint && (
          <button
            className="filter-chip"
            aria-label="清除端点筛选"
            onClick={() => filter("endpoint", "")}
          >
            <EndpointLabel id={endpoint} compact />
            <X size={12} />
          </button>
        )}
        <div className="filter-spacer" />
        <TimeRangePicker
          value={range}
          onChange={(value) => setParams(rangeQuery(params, value))}
        />
        <Choice
          label="统计粒度"
          value={granularity}
          onChange={(value) => filter("granularity", value)}
          options={[
            { value: "1m", label: "1 分钟" },
            { value: "5m", label: "5 分钟" },
            { value: "1h", label: "1 小时" },
            { value: "1d", label: "1 天" },
          ]}
        />
        <Button
          variant="outline"
          size="icon-sm"
          aria-label="刷新统计"
          onClick={() => void result.refetch()}
        >
          <RefreshCw
            size={14}
            className={result.isFetching ? "animate-spin" : ""}
          />
        </Button>
      </div>
      {result.isError ? (
        <ErrorState error={result.error} retry={() => void result.refetch()} />
      ) : !data || !stats ? (
        <Loading />
      ) : (
        <>
          <div className="metric-strip">
            <div className="metric">
              <div className="metric-label">
                进网请求<Help>所选端点的进网请求；对比上一等长时段。</Help>
              </div>
              <div className="metric-value">{count(stats.total)}</div>
              <div className="metric-detail">
                较上一时段 <span>{change}</span>
              </div>
            </div>
            <div className="metric">
              <div className="metric-label">RPM</div>
              <div className="metric-value">
                {data.current_rpm === undefined ? "—" : count(data.current_rpm)}
              </div>
              <div className="metric-detail">最近一分钟</div>
            </div>
            <Link
              className="metric metric-link"
              aria-label="查看命中记录"
              to={goRecords({ kind: "hit" })}
            >
              <div className="metric-label">
                场景命中率
                <ArrowUpRight size={12} />
              </div>
              <div className="metric-value">{percent(stats.hitRate)}</div>
              <div className="metric-detail">
                命中 {count(stats.hits)} / 已审 {count(stats.checked)}
              </div>
            </Link>
            <Link
              className="metric metric-link"
              aria-label="查看拦截记录"
              to={goRecords({ action: "block", kind: "hit" })}
            >
              <div className="metric-label">
                拦截率
                <ArrowUpRight size={12} />
              </div>
              <div className="metric-value">{percent(stats.blockRate)}</div>
              <div className="metric-detail">
                拦截 {count(stats.blocked)} / 已审 {count(stats.checked)}
              </div>
            </Link>
            <Link
              className="metric metric-link"
              aria-label="查看 Jev 错误记录"
              to={goRecords({ kind: "failure" })}
            >
              <div className="metric-label">
                Jev 失败率
                <ArrowUpRight size={12} />
              </div>
              <div
                className="metric-value"
                style={stats.failures ? { color: chartColors.red } : undefined}
              >
                {percent(stats.failureRate)}
              </div>
              <div className="metric-detail">
                失败 {count(stats.failures)} / 调用{" "}
                {count(stats.checked + stats.failures)}
              </div>
            </Link>
            <div className="metric">
              <div className="metric-label">
                审查耗时 P95
                <Help>
                  由耗时分桶估算的上界；包含分类与场景判定，不包含上游响应时间。
                </Help>
              </div>
              <div className="metric-value" style={{ fontSize: 24 }}>
                {duration(quantile(latency, 0.95))}
              </div>
              <div className="metric-detail">
                P50 {duration(quantile(latency, 0.5))}
              </div>
            </div>
          </div>
          <TrafficTimeline
            data={data}
            buckets={buckets}
            endpoint={endpoint}
            onClick={(event) => {
              const bucket = buckets[event.dataIndex];
              if (!bucket) return;
              const scope = { since: bucket.start, until: bucket.end };
              const ep = endpoints.find((e) => e.short === event.seriesName);
              if (ep) {
                const next = drilldownQuery(params, scope, { endpoint: ep.id });
                next.set("granularity", granularity);
                setParams(next);
                return;
              }
              if (event.seriesName === "Jev 失败率") {
                navigate(
                  `/events?${drilldownQuery(params, scope, { kind: "failure" })}`,
                );
                return;
              }
              if (
                event.seriesName === "命中率" ||
                event.seriesName === "拦截率"
              )
                navigate(
                  `/events?${drilldownQuery(params, scope, { kind: "hit", ...(event.seriesName === "拦截率" ? { action: "block" } : {}) })}`,
                );
            }}
            latencyDetail={
              latency.length > 0 ? (
                <details className="latency-detail">
                  <summary>耗时分布热力图</summary>
                  <LatencyHeatmap data={data} buckets={buckets} />
                </details>
              ) : undefined
            }
          />
          {(stats.skipped > 0 || stats.frozen > 0) && (
            <div className="gateway-notices">
              {stats.skipped > 0 && (
                <Link
                  to={goRecords({
                    kind: "warning",
                    error_kind: "classifier_input_too_long",
                  })}
                >
                  输入超限 <strong>{count(stats.skipped)}</strong>
                  <ArrowUpRight size={13} />
                </Link>
              )}
              {stats.frozen > 0 && (
                <Link
                  to={goRecords({
                    kind: "warning",
                    error_kind: "session_blocked",
                  })}
                >
                  会话拦截 <strong>{count(stats.frozen)}</strong>
                  <ArrowUpRight size={13} />
                </Link>
              )}
            </div>
          )}
          <div className="dashboard-matrix">
            <Panel
              title="流量明细"
              extra={
                <div className="segmented">
                  <button
                    className={dimension === "endpoint" ? "selected" : ""}
                    onClick={() => setDimension("endpoint")}
                  >
                    按端点
                  </button>
                  <button
                    className={dimension === "model" ? "selected" : ""}
                    onClick={() => setDimension("model")}
                  >
                    按模型
                  </button>
                </div>
              }
            >
              <div className="table-scroll">
                <table className="data-table">
                  <thead>
                    <tr>
                      <th>{dimension === "endpoint" ? "端点" : "请求模型"}</th>
                      <th className="num">请求量 / 占比</th>
                      <th className="num">完成审查</th>
                      <th className="num">命中率</th>
                      <th className="num">拦截率</th>
                      <th className="num">Jev 失败</th>
                      <th className="num">审查 P95</th>
                    </tr>
                  </thead>
                  <tbody>
                    {(dimension === "endpoint"
                      ? endpoints
                          .filter((e) => !endpoint || e.id === endpoint)
                          .map((e) => e.id)
                      : data.models.filter((m) => !model || m === model)
                    ).map((id) => {
                      const points = data.traffic.filter(
                          (p) =>
                            (dimension === "endpoint"
                              ? p.endpoint
                              : p.model) === id,
                        ),
                        s = summarize(points);
                      const share = stats.total
                        ? (s.total / stats.total) * 100
                        : null;
                      const name =
                        dimension === "endpoint"
                          ? endpoints.find((e) => e.id === id)!.name
                          : id;
                      return (
                        <tr key={id}>
                          <td>
                            <button
                              className="dimension-link"
                              aria-label={
                                dimension === "endpoint"
                                  ? `筛选 ${name}`
                                  : `筛选模型 ${name}`
                              }
                              onClick={() => filter(dimension, id)}
                            >
                              {dimension === "endpoint" ? (
                                <EndpointLabel id={id} />
                              ) : (
                                id
                              )}
                              <ArrowUpRight size={12} />
                            </button>
                          </td>
                          <td className="num">
                            <b>{count(s.total)}</b>
                            <span className="dimension-share">
                              {percent(share)}
                            </span>
                          </td>
                          <td className="num">{count(s.checked)}</td>
                          <td className="num">{percent(s.hitRate)}</td>
                          <td className="num">{percent(s.blockRate)}</td>
                          <td className="num">
                            <Link
                              className={s.failures ? "error-link" : "muted"}
                              to={goRecords({
                                [dimension]: id,
                                kind: "failure",
                              })}
                            >
                              {count(s.failures)}
                            </Link>
                          </td>
                          <td className="num">
                            {duration(
                              quantile(
                                latency.filter(
                                  (p) =>
                                    (dimension === "endpoint"
                                      ? p.endpoint
                                      : p.model) === id,
                                ),
                                0.95,
                              ),
                            )}
                          </td>
                        </tr>
                      );
                    })}
                  </tbody>
                </table>
                {dimension === "model" && !data.models.length && <Empty />}
              </div>
            </Panel>
          </div>
          <div className="dashboard-findings">
            <Panel
              title="场景生效"
              extra={
                <Link className="small muted" to="/scenes">
                  配置场景{" "}
                  <ExternalLink size={11} style={{ display: "inline" }} />
                </Link>
              }
            >
              <Outcome
                data={data}
                link={(action) => goRecords({ kind: "hit", action })}
              />
              {scenes.length ? (
                <Chart
                  label="场景生效排行"
                  height={Math.max(170, Math.min(scenes.length, 6) * 34 + 24)}
                  option={barChart(
                    scenes.slice(0, 6).map((s) => s.name),
                    scenes.slice(0, 6).map((s) => s.effective),
                  )}
                  onClick={(e) =>
                    navigate(
                      goRecords({ scene: scenes[e.dataIndex].id, kind: "hit" }),
                    )
                  }
                />
              ) : (
                <Empty />
              )}
            </Panel>
            <Panel
              title="Jev 异常"
              extra={
                <Link
                  className="small muted"
                  to={goRecords({ kind: "failure" })}
                >
                  查看记录{" "}
                  <ArrowUpRight size={12} style={{ display: "inline" }} />
                </Link>
              }
            >
              {errorGroups.length ? (
                <div className="error-breakdown">
                  {errorGroups.map(([kind, n]) => (
                    <Link
                      key={kind}
                      className="error-row"
                      to={goRecords({ kind: "failure", error_kind: kind })}
                    >
                      <div>
                        <span>{errorNames[kind] || kind}</span>
                        <b>
                          {count(n)}
                          <small>
                            {percent(
                              stats.failures
                                ? (n / stats.failures) * 100
                                : null,
                            )}
                          </small>
                        </b>
                      </div>
                      <div className="error-meter">
                        <i
                          style={{
                            width: `${stats.failures ? (n / stats.failures) * 100 : 0}%`,
                          }}
                        />
                      </div>
                    </Link>
                  ))}
                </div>
              ) : (
                <Empty />
              )}
            </Panel>
          </div>
        </>
      )}
    </>
  );
}
