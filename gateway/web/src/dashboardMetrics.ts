import type { EChartsOption } from "echarts";
import {
  endpoints,
  endpointGroup,
  summarize,
  count,
  percent,
  type Analytics,
} from "./analytics";

export function endpointTotals(data: Analytics) {
  return endpoints.map((endpoint) => ({
    ...endpoint,
    value: summarize(
      data.traffic.filter((p) => endpointGroup(p.endpoint) === endpoint.id),
    ).total,
  }));
}
export function cacheMetrics(data: Analytics) {
  return data.distributions.reduce(
    (totals, point) => {
      if (point.metric === "cache_hit") totals.hits += point.count;
      if (point.metric === "cache_lookup") totals.lookups += point.count;
      return totals;
    },
    { hits: 0, lookups: 0 },
  );
}
export function latencyBands(data: Analytics) {
  const bands = [
    { label: "≤ 300 ms", upper: 300, count: 0 },
    { label: "300 ms–1 s", upper: 1000, count: 0 },
    { label: "1–3 s", upper: 3000, count: 0 },
    { label: "> 3 s", upper: Infinity, count: 0 },
  ];
  for (const point of data.distributions) {
    if (point.metric !== "review_ms") continue;
    const band = bands.find((b) => point.upper <= b.upper);
    if (band) band.count += point.count;
  }
  return bands;
}
const escapeHTML = (value: unknown) =>
  String(value).replace(
    /[&<>"']/g,
    (char) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        char
      ]!,
  );
export function donutOption(
  parts: { name: string; value: number; color: string }[],
): EChartsOption {
  const total = parts.reduce((n, part) => n + part.value, 0);
  return {
    animationDuration: 350,
    tooltip: {
      trigger: "item",
      backgroundColor: "#fff",
      borderColor: "#e6eaf1",
      padding: 12,
      textStyle: { color: "#253044", fontSize: 12 },
      formatter: ((p: { name: string; value: number }) =>
        `<b>${escapeHTML(p.name)}</b><br/>${count(p.value)} 次 · ${percent(total ? (p.value / total) * 100 : null)}`) as never,
    },
    series: [
      {
        type: "pie",
        radius: ["65%", "84%"],
        center: ["50%", "50%"],
        startAngle: 90,
        label: { show: false },
        labelLine: { show: false },
        stillShowZeroSum: false,
        itemStyle: { borderColor: "#fff", borderWidth: 4, borderRadius: 5 },
        emphasis: { scale: true, scaleSize: 4 },
        data: total
          ? parts.map((p) => ({
              name: p.name,
              value: p.value,
              itemStyle: { color: p.color },
            }))
          : [{ name: "暂无数据", value: 1, itemStyle: { color: "#edf0f5" } }],
        silent: !total,
      },
    ],
  };
}
export type RiskSources = {
  keys: { credential_id: string; masked_key: string; count: number }[];
  ips: { client_ip: string; count: number }[];
};
