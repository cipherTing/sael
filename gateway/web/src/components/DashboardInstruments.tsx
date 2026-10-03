import { useState } from "react";
import { Link, useNavigate } from "react-router";
import { ArrowUpRight, Database, KeyRound, Globe } from "lucide-react";
import type { EChartsOption } from "echarts";
import { Chart, chartColors } from "./Chart";
import { Panel, Empty, Loading, EndpointLabel } from "./common";
import { Button } from "./ui/button";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";
import { count, percent, summarize, type Analytics } from "../analytics";
import {
  cacheMetrics,
  endpointTotals,
  latencyBands,
  donutOption,
  type RiskSources,
} from "../dashboardMetrics";

export function EndpointComposition({
  data,
  onSelect,
}: {
  data: Analytics;
  onSelect: (endpoint: string) => void;
}) {
  const parts = endpointTotals(data),
    total = parts.reduce((n, p) => n + p.value, 0);
  return (
    <Panel title="端点构成" className="composition-panel">
      <div className="composition-body">
        <div className="instrument-ring">
          <Chart
            label="端点构成"
            height={168}
            option={donutOption(
              parts.map((p) => ({
                name: p.name,
                value: p.value,
                color: p.color,
              })),
            )}
            onClick={(e) => {
              if (parts[e.dataIndex]) onSelect(parts[e.dataIndex].id);
            }}
          />
          <div className="ring-center">
            <strong>{count(total)}</strong>
            <span>用户输入</span>
          </div>
        </div>
        <div className="composition-rows">
          {parts.map((p) => (
            <button
              className="composition-row"
              type="button"
              key={p.id}
              onClick={() => onSelect(p.id)}
              aria-label={`筛选 ${p.name}`}
            >
              <i className="composition-dot" style={{ background: p.color }} />
              <EndpointLabel id={p.id} />
              <b>{count(p.value)}</b>
              <span>{percent(total ? (p.value / total) * 100 : null)}</span>
            </button>
          ))}
        </div>
      </div>
    </Panel>
  );
}
export function OutcomeComposition({
  data,
  link,
}: {
  data: Analytics;
  link: (action: string) => string;
}) {
  const navigate = useNavigate();
  const s = summarize(data.traffic),
    parts = [
      {
        name: "拦截",
        value: s.blocked,
        color: chartColors.red,
        action: "block",
      },
      {
        name: "记录放行",
        value: s.allowed,
        color: chartColors.purple,
        action: "allow",
      },
    ];
  return (
    <Panel title="命中处置" className="composition-panel">
      <div className="composition-body">
        <div className="instrument-ring">
          <Chart
            label="命中处置构成"
            height={168}
            option={donutOption(parts)}
            onClick={(e) => {
              if (parts[e.dataIndex]) navigate(link(parts[e.dataIndex].action));
            }}
          />
          <div className="ring-center">
            <strong>{count(s.hits)}</strong>
            <span>命中请求</span>
          </div>
        </div>
        <div className="composition-rows">
          {parts.map((p) => (
            <Link
              key={p.name}
              className="composition-row"
              to={link(p.action)}
              aria-label={`查看${p.name}记录`}
            >
              <i className="composition-dot" style={{ background: p.color }} />
              <span>{p.name}</span>
              <b>{count(p.value)}</b>
              <span>{percent(s.hits ? (p.value / s.hits) * 100 : null)}</span>
              <ArrowUpRight size={12} />
            </Link>
          ))}
        </div>
      </div>
    </Panel>
  );
}
export function LatencyDistribution({ data }: { data: Analytics }) {
  const bands = latencyBands(data),
    total = bands.reduce((n, b) => n + b.count, 0);
  if (!total)
    return (
      <Panel title="耗时分布" className="latency-distribution-panel">
        <Empty />
      </Panel>
    );
  const option: EChartsOption = {
    animationDuration: 200,
    grid: { top: 12, bottom: 28, left: 42, right: 18 },
    xAxis: {
      type: "category",
      data: bands.map((b) => b.label),
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: { color: "#64748b", fontSize: 10, interval: 0 },
    },
    yAxis: {
      type: "value",
      minInterval: 1,
      splitNumber: 2,
      axisLabel: { color: "#8490a4", fontSize: 10 },
      splitLine: { lineStyle: { color: "#edf0f5", type: "dashed" } },
    },
    tooltip: {
      trigger: "axis",
      axisPointer: { type: "shadow" },
      formatter: ((rows: { dataIndex: number }[]) => {
        const b = bands[rows[0].dataIndex];
        return `${b.label}<br/><b>${count(b.count)} 次</b> · ${percent((b.count / total) * 100)}`;
      }) as never,
    },
    series: [
      {
        type: "bar",
        data: bands.map((b, i) => ({
          value: b.count,
          itemStyle: {
            color: ["#657fea", "#8a9dee", "#c3cefa", "#cf626b"][i],
            borderRadius: [4, 4, 0, 0],
          },
        })),
        barMaxWidth: 42,
      },
    ],
  };
  return (
    <Panel
      title="耗时分布"
      className="latency-distribution-panel"
      extra={
        <Popover>
          <PopoverTrigger asChild>
            <Button size="sm" variant="ghost" aria-label="查看耗时区间明细">
              区间明细
            </Button>
          </PopoverTrigger>
          <PopoverContent align="end">
            <div className="latency-sample-count">{count(total)} 次送审</div>
            <table className="latency-band-table" aria-label="耗时区间明细">
              <tbody>
                {bands.map((b) => (
                  <tr key={b.label}>
                    <th>{b.label}</th>
                    <td>{count(b.count)}</td>
                    <td>{percent((b.count / total) * 100)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </PopoverContent>
        </Popover>
      }
    >
      <Chart label="审查耗时分布" height={184} option={option} />
    </Panel>
  );
}
export function CacheEfficiency({ data }: { data: Analytics }) {
  const cache = cacheMetrics(data),
    rate = cache.lookups ? (cache.hits / cache.lookups) * 100 : null;
  return (
    <Panel
      title="缓存效率"
      extra={
        <Link className="instrument-panel-link" to="/settings?tab=data">
          <Database size={13} />
          设置
        </Link>
      }
      className="efficiency-panel"
    >
      <div className="metric">
        <div className="metric-label">缓存命中</div>
        <div className="metric-value">{count(cache.hits)}</div>
        <div className="metric-detail">命中率 {percent(rate)}</div>
      </div>
      <div
        className="cache-meter"
        role="meter"
        aria-label="缓存复用率"
        aria-valuemin={0}
        aria-valuemax={100}
        aria-valuenow={rate ?? undefined}
      >
        <i style={{ width: `${Math.min(100, rate || 0)}%` }} />
      </div>
      <div className="cache-stat-row">
        <span>判定查询</span>
        <b>{count(cache.lookups)}</b>
      </div>
      <div className="cache-stat-row">
        <span>节省送审</span>
        <b>{count(cache.hits)} 次</b>
      </div>
    </Panel>
  );
}
export function RiskSourceRanking({
  data,
  loading,
  error,
  recordLink,
}: {
  data?: RiskSources;
  loading: boolean;
  error: boolean;
  recordLink: (extra: Record<string, string>) => string;
}) {
  const [tab, setTab] = useState<"keys" | "ips">("keys");
  const rows =
    tab === "keys"
      ? (data?.keys || []).map((p) => ({
          id: p.credential_id,
          name: p.masked_key || "—",
          count: p.count,
          extra: { credential_id: p.credential_id },
        }))
      : (data?.ips || []).map((p) => ({
          id: p.client_ip,
          name: p.client_ip,
          count: p.count,
          extra: { client_ip: p.client_ip },
        }));
  const max = Math.max(1, ...rows.map((p) => p.count));
  return (
    <Panel
      title="风险来源"
      extra={
        <div className="segmented" role="group" aria-label="风险来源维度">
          <button
            className={tab === "keys" ? "selected" : ""}
            aria-pressed={tab === "keys"}
            onClick={() => setTab("keys")}
          >
            <KeyRound size={12} />
            密钥
          </button>
          <button
            className={tab === "ips" ? "selected" : ""}
            aria-pressed={tab === "ips"}
            onClick={() => setTab("ips")}
          >
            <Globe size={12} />
            IP
          </button>
        </div>
      }
      className="risk-source-panel"
    >
      {loading ? (
        <Loading />
      ) : error ? (
        <Empty text="加载失败" />
      ) : !rows.length ? (
        <Empty />
      ) : (
        <div className="risk-source-rows">
          {rows.map((p, i) => (
            <Link
              key={p.id}
              to={recordLink({ kind: "hit", ...p.extra })}
              className="risk-source-row"
              aria-label={`查看 ${p.name} 命中记录`}
            >
              <span className="risk-source-rank">{i + 1}</span>
              <span className="risk-source-name" title={p.name}>
                {p.name}
                <i style={{ width: `${(p.count / max) * 100}%` }} />
              </span>
              <b>{count(p.count)}</b>
              <ArrowUpRight size={12} />
            </Link>
          ))}
        </div>
      )}
    </Panel>
  );
}
