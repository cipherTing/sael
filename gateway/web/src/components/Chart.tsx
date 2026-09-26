import { useEffect, useRef, useState } from "react";
import * as echarts from "echarts/core";
import { BarChart, LineChart, HeatmapChart } from "echarts/charts";
import {
  GridComponent,
  AxisPointerComponent,
  TooltipComponent,
  LegendComponent,
  VisualMapComponent,
  MarkLineComponent,
} from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import type { EChartsOption, TooltipComponentOption } from "echarts";
import { placeTooltip } from "./tooltipPosition";
import {
  fullDate,
  percent,
  count,
  rpm,
  duration,
  type Bucket,
} from "../analytics";

echarts.use([
  BarChart,
  LineChart,
  HeatmapChart,
  GridComponent,
  AxisPointerComponent,
  TooltipComponent,
  LegendComponent,
  VisualMapComponent,
  MarkLineComponent,
  SVGRenderer,
]);
export type ChartClick = {
  dataIndex: number;
  seriesName: string;
  name: string;
};
export function Chart({
  option,
  label,
  height = 270,
  ticks = 0,
  onClick,
}: {
  option: EChartsOption;
  label: string;
  height?: number;
  ticks?: number;
  onClick?: (event: ChartClick) => void;
}) {
  const viewport = useRef<HTMLDivElement>(null),
    host = useRef<HTMLDivElement>(null),
    chart = useRef<echarts.ECharts | null>(null),
    click = useRef(onClick);
  const [width, setWidth] = useState(0);
  click.current = onClick;
  useEffect(() => {
    if (!viewport.current) return;
    const element = viewport.current;
    const resize = () => setWidth(element.clientWidth);
    resize();
    const observer = new ResizeObserver(resize);
    observer.observe(element);
    return () => observer.disconnect();
  }, []);
  const plotWidth = Math.max(width, ticks ? ticks * 22 + 68 : 0);
  useEffect(() => {
    if (!host.current || !width) return;
    const instance = echarts.init(host.current, undefined, {
      renderer: "svg",
      width: plotWidth,
      height,
    });
    chart.current = instance;
    instance.on("click", (event) =>
      click.current?.(event as unknown as ChartClick),
    );
    return () => {
      instance.dispose();
      chart.current = null;
    };
  }, [plotWidth, width, height]);
  useEffect(() => {
    const tooltip = (tip: TooltipComponentOption): TooltipComponentOption => ({
      ...tip,
      confine: false,
      appendTo: "body",
      extraCssText: `${tip.extraCssText || ""};max-width:${Math.max(0, document.documentElement.clientWidth - 24)}px;white-space:normal;overflow-wrap:anywhere;box-sizing:border-box`,
      position: (point, _params, _element, _rect, size) =>
        placeTooltip(
          host.current!.getBoundingClientRect(),
          point,
          size.contentSize,
          [
            document.documentElement.clientWidth,
            document.documentElement.clientHeight,
          ],
        ),
    });
    chart.current?.setOption(
      {
        ...option,
        tooltip: Array.isArray(option.tooltip)
          ? option.tooltip.map(tooltip)
          : option.tooltip
            ? tooltip(option.tooltip)
            : undefined,
      },
      { notMerge: true, lazyUpdate: true },
    );
  }, [option, plotWidth, width, height]);
  return (
    <div
      ref={viewport}
      className="chart-viewport"
      role="img"
      aria-label={label}
    >
      <div ref={host} style={{ width: plotWidth || "100%", height }} />
    </div>
  );
}
export const chartColors = {
  blue: "#516de5",
  purple: "#9271d3",
  green: "#4a9d87",
  red: "#cf626b",
  amber: "#c89642",
  gray: "#c1c9d6",
};
const escapeHTML = (text: unknown) =>
  String(text).replace(
    /[&<>"']/g,
    (c) =>
      ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[
        c
      ]!,
  );
type TooltipItem = {
  axisValue?: string;
  dataIndex: number;
  seriesName: string;
  value: number | null;
  color?: string;
};
export function timeChart(
  buckets: Bucket[],
  series: {
    name: string;
    data: (number | null)[];
    color: string;
    type?: "bar" | "line";
    stack?: string;
  }[],
  unit: "count" | "percent" | "ms" | "rpm" = "count",
): EChartsOption {
  const largestRate = Math.max(
    0,
    ...series.flatMap((s) => s.data.map((value) => value ?? 0)),
  );
  const formatValue = (value: number | null) =>
    value === null
      ? "—"
      : unit === "percent"
        ? percent(value)
        : unit === "rpm"
          ? `${rpm(value)} RPM`
          : unit === "ms"
            ? duration(value)
            : `${count(value)} 次`;
  return {
    animationDuration: 250,
    textStyle: {
      fontFamily:
        'Inter, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
    },
    color: series.map((s) => s.color),
    grid: { top: 14, left: 46, right: 20, bottom: 116 },
    tooltip: {
      trigger: "axis",
      appendTo: "body",
      backgroundColor: "#fff",
      borderColor: "#e4e7ed",
      padding: 12,
      textStyle: { color: "#253044", fontSize: 12 },
      axisPointer: {
        type: series.some((s) => s.type === "bar") ? "shadow" : "line",
      },
      extraCssText:
        "border-radius:9px;box-shadow:0 8px 32px #1e293b20;z-index:1000",
      formatter: ((items: TooltipItem | TooltipItem[]) => {
        const rows = Array.isArray(items) ? items : [items],
          b = buckets[rows[0]?.dataIndex];
        if (!b) return "";
        const title = `${fullDate(b.start)} — ${fullDate(b.end)}`;
        return (
          `<div style="font-weight:600;margin-bottom:9px">${escapeHTML(title)}</div>` +
          rows
            .map(
              (row) =>
                `<div style="display:flex;gap:20px;justify-content:space-between;margin-top:5px"><span><i style="display:inline-block;width:7px;height:7px;border-radius:2px;background:${escapeHTML(row.color)};margin-right:7px"></i>${escapeHTML(row.seriesName)}</span><b>${escapeHTML(formatValue(row.value))}</b></div>`,
            )
            .join("") +
          (unit === "percent"
            ? `<div style="margin-top:8px;color:#667085">已审 ${count(b.checked)} · 命中 ${count(b.hits)} · 拦截 ${count(b.blocked)}</div>`
            : "")
        );
      }) as never,
    },
    xAxis: {
      type: "category",
      data: buckets.map((b) => b.label),
      boundaryGap: true,
      axisTick: { show: false },
      axisLine: { lineStyle: { color: "#e7eaf0" } },
      axisLabel: {
        interval: 0,
        rotate: 60,
        hideOverlap: false,
        color: "#8a94a6",
        fontSize: 10,
        margin: 12,
        showMinLabel: true,
        showMaxLabel: true,
      },
    },
    yAxis: {
      type: "value",
      min: 0,
      max:
        unit === "percent"
          ? Math.min(100, Math.max(5, Math.ceil((largestRate * 1.1) / 5) * 5))
          : undefined,
      minInterval: unit === "count" ? 1 : undefined,
      axisLabel: {
        color: "#8a94a6",
        fontSize: 10,
        formatter:
          unit === "percent"
            ? "{value}%"
            : unit === "ms"
              ? (value: number) => duration(value)
              : unit === "rpm"
                ? (value: number) => rpm(value)
                : undefined,
      },
      splitLine: { lineStyle: { color: "#edf0f5", type: "dashed" } },
      axisLine: { show: false },
    },
    series: series.map((s) => ({
      name: s.name,
      type: s.type || "line",
      data: s.data,
      stack: s.stack,
      barMaxWidth: 18,
      showSymbol: true,
      symbol: "circle",
      symbolSize: 4,
      connectNulls: false,
      smooth: false,
      lineStyle: { width: 2 },
      itemStyle: {
        color: s.color,
        borderRadius: s.type === "bar" ? [2, 2, 0, 0] : undefined,
      },
      emphasis: { focus: "series" },
    })),
  };
}
export function barChart(
  labels: string[],
  values: number[],
  color = chartColors.blue,
): EChartsOption {
  return {
    animationDuration: 250,
    grid: { top: 8, bottom: 12, left: 8, right: 46, containLabel: true },
    tooltip: {
      trigger: "axis",
      appendTo: "body",
      axisPointer: { type: "shadow" },
    },
    xAxis: { type: "value", show: false, min: 0 },
    yAxis: {
      type: "category",
      inverse: true,
      data: labels,
      axisTick: { show: false },
      axisLine: { show: false },
      axisLabel: {
        color: "#536078",
        width: 130,
        overflow: "truncate",
        fontSize: 12,
      },
    },
    series: [
      {
        type: "bar",
        data: values,
        barMaxWidth: 14,
        itemStyle: { color, borderRadius: [0, 3, 3, 0] },
        label: {
          show: true,
          position: "right",
          color: "#536078",
          fontSize: 12,
        },
      },
    ],
  };
}
export function Legend({
  items,
}: {
  items: { name: string; color: string }[];
}) {
  return (
    <div className="chart-legend">
      {items.map((item) => (
        <span key={item.name}>
          <i style={{ background: item.color }} />
          {item.name}
        </span>
      ))}
    </div>
  );
}
