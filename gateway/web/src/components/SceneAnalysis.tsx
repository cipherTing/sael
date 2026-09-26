import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Chart, barChart, chartColors } from "./Chart";
import { Choice, Empty, ErrorState, Loading, Help } from "./common";
import { endpoints, sceneStats, count, type Analytics } from "../analytics";
import { request } from "../api";
import type { Scene, Question } from "../policy";
import { questionName } from "../questionMeta";
import type { EChartsOption } from "echarts";

export function SceneAnalysis({
  scene,
  questions,
}: {
  scene: Scene;
  questions: Question[];
}) {
  const [scope, setScope] = useState(""),
    [minutes, setMinutes] = useState("10080"),
    [selected, setSelected] = useState(scene.conditions[0]?.question || "gore");
  const applicable = endpoints.filter(
    (e) => !scene.endpoints?.length || scene.endpoints.includes(e.id),
  );
  const endpoint = applicable.some((e) => e.id === scope) ? scope : "";
  const q = useQuery({
    queryKey: ["analytics", "scene", minutes, endpoint],
    queryFn: () =>
      request<Analytics>(
        `/admin/analytics?minutes=${minutes}&endpoint=${endpoint}`,
      ),
  });
  if (q.isError)
    return <ErrorState error={q.error} retry={() => void q.refetch()} />;
  if (!q.data) return <Loading />;
  const data = q.data,
    stats = sceneStats({
      ...data,
      scenes: data.scenes.filter((p) =>
        applicable.some((e) => e.id === p.endpoint),
      ),
    }).find((s) => s.id === scene.id),
    condition =
      scene.conditions.find((c) => c.question === selected) ||
      scene.conditions[0],
    question = questions.find((q) => q.key === condition?.question),
    max = question?.max || 1,
    step = max === 3 ? 0.1 : 0.05;
  const values = Array<number>(Math.round(max / step)).fill(0);
  for (const p of data.distributions) {
    if (
      p.metric === `hit_score:${condition?.question}` &&
      applicable.some((e) => e.id === p.endpoint)
    ) {
      const i = Math.round(p.upper / step) - 1;
      if (i >= 0 && i < values.length) values[i] += p.count;
    }
  }
  const bounds = values.map((_, i) => [
    Number((i * step).toFixed(2)),
    Number(((i + 1) * step).toFixed(2)),
  ]);
  const option: EChartsOption = {
    animationDuration: 200,
    grid: { left: 42, right: 20, top: 28, bottom: 45 },
    tooltip: {
      trigger: "item",
      appendTo: "body",
      formatter: ((p: { dataIndex: number }) =>
        `${bounds[p.dataIndex][0]}–${bounds[p.dataIndex][1]}<br/>${count(values[p.dataIndex])} 次`) as never,
    },
    xAxis: {
      type: "value",
      min: 0,
      max,
      interval: max === 3 ? 0.5 : 0.1,
      axisLabel: { fontSize: 10, color: "#8a94a6" },
      axisTick: { show: false },
      axisLine: { lineStyle: { color: "#e8ecf2" } },
    },
    yAxis: {
      type: "value",
      minInterval: 1,
      axisLabel: { fontSize: 10, color: "#8a94a6" },
      splitLine: { lineStyle: { color: "#eef1f6" } },
    },
    series: [
      {
        type: "bar",
        data: values.map((n, i) => [(bounds[i][0] + bounds[i][1]) / 2, n]),
        barMaxWidth: 20,
        itemStyle: { color: "#a7b5e6", borderRadius: [2, 2, 0, 0] },
        markLine:
          condition && Number.isFinite(condition.threshold)
            ? {
                symbol: "none",
                lineStyle: { color: chartColors.red, type: "dashed" },
                label: {
                  formatter: `阈值 ${condition.threshold}`,
                  fontSize: 10,
                  color: chartColors.red,
                },
                data: [{ xAxis: condition.threshold }],
              }
            : undefined,
      },
    ],
  };
  const winners = Object.values(stats?.winners || {}).sort(
    (a, b) => b.count - a.count,
  );
  return (
    <div className="panel-body">
      <div className="filterbar">
        <Choice
          label="分析时段"
          value={minutes}
          onChange={setMinutes}
          options={[
            { value: "1440", label: "近 24 小时" },
            { value: "10080", label: "近 7 天" },
            { value: "43200", label: "近 30 天" },
          ]}
        />
        <Choice
          label="分析端点"
          value={endpoint}
          onChange={setScope}
          options={[
            { value: "", label: "适用端点" },
            ...applicable.map((e) => ({ value: e.id, label: e.name })),
          ]}
        />
      </div>
      <div className="analysis-metrics">
        <div>
          条件匹配<strong>{count(stats?.matched || 0)}</strong>
        </div>
        <div>
          最终生效<strong>{count(stats?.effective || 0)}</strong>
        </div>
        <div>
          优先级未采用<strong>{count(stats?.shadowed || 0)}</strong>
        </div>
      </div>
      <div className="editor-block-title">
        <h3>
          命中请求分数分布{" "}
          <Help>
            命中过场景的请求分数。阈值线取当前草稿，历史匹配和生效次数不会随草稿重算。
          </Help>
        </h3>
        <Choice
          label="分布审核项"
          value={condition?.question || ""}
          onChange={setSelected}
          options={scene.conditions.map((c) => ({
            value: c.question,
            label: questionName(c.question),
          }))}
        />
      </div>
      {values.some((n) => n > 0) ? (
        <Chart label="审核项分数分布与场景阈值" option={option} height={220} />
      ) : (
        <Empty />
      )}
      {winners.length > 0 && (
        <>
          <div className="section-label" style={{ marginTop: 18 }}>
            优先生效场景
          </div>
          <Chart
            label="优先生效场景排行"
            height={Math.max(95, 30 * winners.length)}
            option={barChart(
              winners.map((w) => w.name),
              winners.map((w) => w.count),
              chartColors.amber,
            )}
          />
        </>
      )}
    </div>
  );
}
