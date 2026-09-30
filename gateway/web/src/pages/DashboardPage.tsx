import { useEffect, useState } from "react";
import { Link, useNavigate, useSearchParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import {
  ArrowUpRight,
  ArrowDownRight,
  X,
  Activity,
  ShieldCheck,
  Clock3,
  TriangleAlert,
  ExternalLink,
} from "lucide-react";
import {
  TrafficTrend,
  RiskTrend,
  ReviewTrend,
} from "../components/TrafficTimeline";
import {
  EndpointComposition,
  OutcomeComposition,
  LatencyDistribution,
  CacheEfficiency,
  RiskSourceRanking,
} from "../components/DashboardInstruments";
import { RefreshButton } from "../components/RefreshButton";
import { request } from "../api";
import {
  endpoints,
  errorNames,
  summarize,
  groupTraffic,
  quantile,
  averageReview,
  count,
  percent,
  duration,
  sceneStats,
  drilldownQuery,
  type Analytics,
} from "../analytics";
import { type RiskSources } from "../dashboardMetrics";
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
  Help,
  Loading,
  PageHeading,
  Panel,
} from "../components/common";
import { notifyRetry } from "../notifications";
import "../dashboard.css";

export default function DashboardPage({ enabled }: { enabled: boolean }) {
  const navigate = useNavigate();
  const [params, setParams] = useSearchParams();
  const [dimension, setDimension] = useState<"endpoint" | "model">("endpoint");
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
  const data = result.data;
  // Rankings use the resolved window from analytics so clicks preserve exactly what was measured.
  const sourceQuery = data ? drilldownQuery(params, data, {}) : query;
  sourceQuery.set("timezone", Intl.DateTimeFormat().resolvedOptions().timeZone);
  const sources = useQuery({
    queryKey: ["risk-sources", sourceQuery.toString()],
    queryFn: () => request<RiskSources>(`/admin/risk-sources?${sourceQuery}`),
    enabled: !!data,
    refetchInterval: 30_000,
  });
  useEffect(() => {
    if (result.error) notifyRetry(result.error, () => void result.refetch());
  }, [result.error, result.refetch]);
  useEffect(() => {
    if (sources.error) notifyRetry(sources.error, () => void sources.refetch());
  }, [sources.error, sources.refetch]);
  const endpoint = params.get("endpoint") || "",
    model = params.get("model") || "";
  function filter(key: string, value: string) {
    const next = new URLSearchParams(params);
    if (value) next.set(key, value);
    else next.delete(key);
    setParams(next);
  }
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
  const scenes = data ? sceneStats(data) : [];
  const sceneMaximum = Math.max(1, ...scenes.map((scene) => scene.effective));
  const latency =
    data?.distributions.filter((p) => p.metric === "review_ms") || [];
  const errors = data
    ? Object.entries(
        data.errors.reduce<Record<string, number>>((all, p) => {
          all[p.kind] = (all[p.kind] || 0) + p.count;
          return all;
        }, {}),
      ).sort((a, b) => b[1] - a[1])
    : [];
  function onTrendClick(event: { dataIndex: number; seriesName: string }) {
    const bucket = buckets[event.dataIndex];
    if (!bucket) return;
    const scope = { since: bucket.start, until: bucket.end };
    const ep = endpoints.find((e) => e.short === event.seriesName);
    if (ep) {
      const next = drilldownQuery(params, scope, { endpoint: ep.id });
      next.set("granularity", granularity);
      setParams(next);
    } else if (event.seriesName === "Jev 失败率")
      navigate(`/events?${drilldownQuery(params, scope, { kind: "failure" })}`);
    else if (["命中率", "拦截率"].includes(event.seriesName))
      navigate(
        `/events?${drilldownQuery(params, scope, { kind: "hit", ...(event.seriesName === "拦截率" ? { action: "block" } : {}) })}`,
      );
  }
  return (
    <div className="dashboard-console">
      <PageHeading title="总览">
        <span className="review-state">
          <i className={`state-dot ${enabled ? "on" : ""}`} />
          {enabled ? "审查已开启" : "审查已关闭"}
        </span>
      </PageHeading>
      <div className="filterbar dashboard-filterbar">
        <Choice
          label="端点筛选"
          visibleLabel="端点"
          value={endpoint}
          onChange={(v) => filter("endpoint", v)}
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
          visibleLabel="模型"
          value={model}
          onChange={(v) => filter("model", v)}
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
        {model && (
          <button
            className="filter-chip"
            aria-label="清除模型筛选"
            onClick={() => filter("model", "")}
          >
            {model}
            <X size={12} />
          </button>
        )}
        <div className="filter-spacer" />
        <TimeRangePicker
          value={rangeFromQuery(params)}
          onChange={(value) => setParams(rangeQuery(params, value))}
        />
        <Choice
          label="统计粒度"
          visibleLabel="时间粒度"
          value={granularity}
          onChange={(v) => filter("granularity", v)}
          options={[
            { value: "1m", label: "1 分钟" },
            { value: "5m", label: "5 分钟" },
            { value: "1h", label: "1 小时" },
            { value: "1d", label: "1 天" },
          ]}
        />
        <RefreshButton
          label="刷新统计"
          busy={result.isFetching || sources.isFetching}
          onRefresh={() => Promise.all([result.refetch(), sources.refetch()])}
        />
      </div>
      {result.isError ? (
        <div className="empty-state">
          <Button
            variant="outline"
            size="sm"
            onClick={() => void result.refetch()}
          >
            重新加载
          </Button>
        </div>
      ) : !data || !stats ? (
        <Loading />
      ) : (
        <>
          <div className="dashboard-kpis" role="region" aria-label="核心指标">
            <div className="metric dashboard-kpi">
              <div className="kpi-heading">
                <span>用户输入</span>
                <Activity size={15} />
              </div>
              <div className="metric-value">{count(stats.total)}</div>
              <div className="kpi-support">
                <span
                  className={`kpi-change ${stats.total < (previous?.total || 0) ? "down" : "up"}`}
                >
                  {stats.total < (previous?.total || 0) ? (
                    <ArrowDownRight size={12} />
                  ) : (
                    <ArrowUpRight size={12} />
                  )}
                  {change}
                </span>
                <span>较上一时段</span>
              </div>
              <div className="kpi-footer">
                <div>
                  <span className="metric-label">RPM</span>
                  <b className="metric-value">
                    {data.current_rpm === undefined
                      ? "—"
                      : count(data.current_rpm)}
                  </b>
                  <span>最近一分钟</span>
                </div>
              </div>
            </div>
            <div className="metric dashboard-kpi">
              <div className="kpi-heading">
                <span>命中与拦截</span>
                <ShieldCheck size={15} />
              </div>
              <Link
                className="kpi-primary-link"
                aria-label="查看命中记录"
                to={goRecords({ kind: "hit" })}
              >
                <span className="metric-value">{count(stats.hits)}</span>
                <span className="kpi-rate">{percent(stats.hitRate)}</span>
                <ArrowUpRight size={14} />
              </Link>
              <div className="kpi-support">
                命中 / 已审 {count(stats.checked)}
              </div>
              <div className="kpi-footer">
                <Link
                  aria-label="查看拦截记录"
                  to={goRecords({ kind: "hit", action: "block" })}
                >
                  <span>拦截</span>
                  <b>{count(stats.blocked)}</b>
                  <span>{percent(stats.blockRate)}</span>
                  <ArrowUpRight size={12} />
                </Link>
              </div>
            </div>
            <div className="metric dashboard-kpi">
              <div className="kpi-heading">
                <span>平均审查耗时</span>
                <Clock3 size={15} />
                <Help>
                  真实 Jev
                  调用耗时之和除以调用次数。包含失败与重试，不计缓存，不包含下游响应时间。
                </Help>
              </div>
              <div className="metric-value kpi-duration">
                {duration(averageReview(data.traffic))}
              </div>
              <div className="kpi-support">
                实际送审 {count(stats.classifierCalls)} 次
              </div>
              <div className="kpi-footer">
                <div>
                  <span>慢请求</span>
                  <b>{duration(quantile(latency, 0.95))}</b>
                  <Help>95% 的送审请求在此时间内完成，按耗时分桶估算。</Help>
                </div>
              </div>
            </div>
            <div
              className={`metric dashboard-kpi ${stats.failures ? "kpi-alert" : ""}`}
            >
              <div className="kpi-heading">
                <span>Jev 异常</span>
                <TriangleAlert size={15} />
              </div>
              <Link
                className="kpi-primary-link"
                aria-label="查看 Jev 错误记录"
                to={goRecords({ kind: "failure" })}
              >
                <span className="metric-value">{count(stats.failures)}</span>
                <span className="kpi-rate">{percent(stats.failureRate)}</span>
                <ArrowUpRight size={14} />
              </Link>
              <div className="kpi-support">
                失败 / 调用 {count(stats.classifierCalls)}
              </div>
              <div className="kpi-footer">
                <div>
                  <span>输入超限</span>
                  <Link
                    to={goRecords({
                      kind: "warning",
                      error_kind: "classifier_input_too_long",
                    })}
                  >
                    {count(stats.skipped)}
                  </Link>
                  <span>会话拦截</span>
                  <Link
                    to={goRecords({
                      kind: "warning",
                      error_kind: "session_blocked",
                    })}
                  >
                    {count(stats.frozen)}
                  </Link>
                </div>
              </div>
            </div>
          </div>
          <div className="dashboard-instruments">
            <section
              className="dashboard-section dashboard-primary"
              aria-label="输入趋势"
            >
              <TrafficTrend
                buckets={buckets}
                endpoint={endpoint}
                onClick={onTrendClick}
              />
            </section>
            <section
              className="dashboard-section dashboard-trends"
              aria-label="审查趋势"
            >
              <RiskTrend buckets={buckets} onClick={onTrendClick} />
              <ReviewTrend
                data={data}
                buckets={buckets}
                onClick={onTrendClick}
              />
            </section>
            <section
              className="dashboard-section dashboard-compositions"
              aria-label="构成与分布"
            >
              <EndpointComposition
                data={data}
                onSelect={(v) => filter("endpoint", v)}
              />
              <OutcomeComposition
                data={data}
                link={(action) => goRecords({ kind: "hit", action })}
              />
              <LatencyDistribution data={data} />
            </section>
            <section
              className="dashboard-section dashboard-risk-analysis"
              aria-label="风险分析"
            >
              <Panel
                title="场景效果"
                className="scene-effect-panel"
                extra={
                  <Link className="instrument-panel-link" to="/scenes">
                    场景配置
                    <ExternalLink size={12} />
                  </Link>
                }
              >
                {scenes.length ? (
                  <div className="table-scroll scene-effect-scroll">
                    <table
                      className="data-table scene-effect-table"
                      aria-label="场景效果明细"
                    >
                      <thead>
                        <tr>
                          <th>场景</th>
                          <th className="num">生效</th>
                          <th className="num">拦截</th>
                          <th className="num">
                            条件命中<Help>一条请求可同时命中多个场景。</Help>
                          </th>
                          <th className="num">
                            被覆盖
                            <Help>命中条件，但由优先级更高的场景处理。</Help>
                          </th>
                        </tr>
                      </thead>
                      <tbody>
                        {scenes.map((s) => (
                          <tr key={s.id}>
                            <td>
                              <Link
                                to={`/scenes?scene=${encodeURIComponent(s.id)}`}
                              >
                                {s.name}
                              </Link>
                            </td>
                            <td className="num">
                              <Link
                                className="scene-volume-link"
                                aria-label={`查看 ${s.name} 生效记录`}
                                to={goRecords({ scene: s.id, kind: "hit" })}
                              >
                                <span className="scene-volume-track">
                                  <i
                                    style={{
                                      width: `${(s.effective / sceneMaximum) * 100}%`,
                                    }}
                                  />
                                </span>
                                <span>{count(s.effective)}</span>
                              </Link>
                            </td>
                            <td className="num">
                              <Link
                                to={goRecords({
                                  scene: s.id,
                                  action: "block",
                                  kind: "hit",
                                })}
                              >
                                {count(s.blocked)}
                              </Link>
                            </td>
                            <td className="num">{count(s.matched)}</td>
                            <td className="num">{count(s.shadowed)}</td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </div>
                ) : (
                  <Empty />
                )}
              </Panel>
              <RiskSourceRanking
                data={sources.data}
                loading={sources.isPending}
                error={sources.isError}
                recordLink={goRecords}
              />
            </section>
            <section
              className="dashboard-section dashboard-diagnostics"
              aria-label="审查诊断"
            >
              <CacheEfficiency data={data} />
              <Panel
                title="Jev 异常类型"
                className="classifier-error-panel"
                extra={
                  <Link
                    className="instrument-panel-link"
                    to={goRecords({ kind: "failure" })}
                  >
                    查看记录 <ArrowUpRight size={12} />
                  </Link>
                }
              >
                {errors.length ? (
                  <div className="error-breakdown">
                    {errors.map(([kind, n]) => (
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
                              width: `${stats.failures ? Math.min(100, (n / stats.failures) * 100) : 0}%`,
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
            </section>
            <section
              className="dashboard-section dashboard-details"
              aria-label="流量明细"
            >
              <Panel
                title="流量明细"
                className="traffic-detail-panel"
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
                        <th>{dimension === "endpoint" ? "端点" : "模型"}</th>
                        <th className="num">用户输入</th>
                        <th className="num">占比</th>
                        <th className="num">完成审查</th>
                        <th className="num">命中率</th>
                        <th className="num">拦截率</th>
                        <th className="num">Jev 异常</th>
                        <th className="num">平均耗时</th>
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
                        return (
                          <tr key={id}>
                            <td>
                              <button
                                className="dimension-link"
                                aria-label={
                                  dimension === "endpoint"
                                    ? `查看 ${id} 明细`
                                    : `筛选模型 ${id}`
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
                            </td>
                            <td className="num">
                              {percent(
                                stats.total
                                  ? (s.total / stats.total) * 100
                                  : null,
                              )}
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
                              {duration(averageReview(points))}
                            </td>
                          </tr>
                        );
                      })}
                    </tbody>
                  </table>
                  {dimension === "model" && !data.models.length && <Empty />}
                </div>
              </Panel>
            </section>
          </div>
        </>
      )}
    </div>
  );
}
