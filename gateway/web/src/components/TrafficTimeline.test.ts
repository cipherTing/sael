import { expect, it } from "vitest";
import { trafficTimeline, reviewTimeline } from "./TrafficTimeline";
import { groupTraffic, type Analytics } from "../analytics";
import type { LineSeriesOption, TooltipComponentOption } from "echarts";

it("stacks endpoint input counts instead of overlaying four indistinguishable lines", () => {
  const data: Analytics = {
    since: "2026-09-25T10:00:00Z",
    until: "2026-09-25T10:05:00Z",
    step_seconds: 300,
    traffic: [
      {
        time: "2026-09-25T10:00:00Z",
        endpoint: "openai_chat",
        model: "a",
        outcome: "clean",
        count: 40,
      },
    ],
    previous: [],
    scenes: [],
    errors: [],
    distributions: [],
    models: [],
  };
  const series = trafficTimeline(groupTraffic(data), "").series as {
    type: string;
    stack?: string;
    data: number[];
  }[];
  expect(series.map((s) => s.type)).toEqual(["bar", "bar", "bar", "bar"]);
  expect(new Set(series.map((s) => s.stack))).toEqual(new Set(["inputs"]));
  expect(series[0].data).toEqual([40]);
});

it("normalizes each endpoint RPM with the actual partial bucket duration", () => {
  const data: Analytics = {
    since: "2026-09-25T10:03:00Z",
    until: "2026-09-25T10:07:00Z",
    step_seconds: 300,
    previous: [],
    scenes: [],
    errors: [],
    distributions: [],
    models: [],
    traffic: [
      {
        time: "2026-09-25T10:00:00Z",
        endpoint: "openai_chat",
        model: "a",
        outcome: "clean",
        count: 40,
      },
      {
        time: "2026-09-25T10:05:00Z",
        endpoint: "openai_chat",
        model: "a",
        outcome: "clean",
        count: 10,
      },
    ],
  };
  const option = trafficTimeline(groupTraffic(data), "", "rpm");
  const chat = (option.series as LineSeriesOption[]).find(
    (s) => s.name === "Chat",
  );
  expect(chat?.data).toEqual([20, 5]);
  expect(Array.isArray(option.yAxis)).toBe(false);
  const tooltip = option.tooltip as TooltipComponentOption;
  expect(
    (tooltip.formatter as Function)([
      { dataIndex: 1, seriesName: "Chat", value: 5 },
    ]),
  ).toContain("5 RPM");
});

it("keeps request counts and full rotated dates on their own complete chart", () => {
  const data: Analytics = {
    since: "2026-09-25T10:00:00Z",
    until: "2026-09-25T10:05:00Z",
    step_seconds: 300,
    traffic: [],
    previous: [],
    scenes: [],
    errors: [],
    distributions: [],
    models: [],
  };
  const option = trafficTimeline(groupTraffic(data), "");
  expect(Array.isArray(option.grid)).toBe(false);
  expect(option.xAxis).toMatchObject({
    axisLabel: { interval: 0, rotate: 60 },
  });
  expect((option.xAxis as { data: string[] }).data[0]).toMatch(/2026\/09\/25/);
  expect((option.series as LineSeriesOption[]).map((s) => s.name)).toEqual([
    "Chat",
    "Responses",
    "Messages",
    "Images",
  ]);
});

it("shows one true mean series by default and a separate slow-request series on demand", () => {
  const data: Analytics = {
    since: "2026-09-30T00:00:00Z",
    until: "2026-09-30T00:03:00Z",
    step_seconds: 60,
    traffic: [
      {
        time: "2026-09-30T00:00:00Z",
        endpoint: "openai_chat",
        model: "test",
        outcome: "clean",
        count: 2,
        classifier_calls: 2,
        classifier_sum_ms: 70,
      },
      {
        time: "2026-09-30T00:02:00Z",
        endpoint: "openai_chat",
        model: "test",
        outcome: "blocked",
        count: 1,
        classifier_calls: 1,
        classifier_sum_ms: 80,
      },
    ],
    distributions: [
      {
        time: "2026-09-30T00:00:00Z",
        endpoint: "openai_chat",
        metric: "review_ms",
        upper: 50,
        count: 2,
      },
      {
        time: "2026-09-30T00:02:00Z",
        endpoint: "openai_chat",
        metric: "review_ms",
        upper: 100,
        count: 1,
      },
    ],
    previous: [],
    scenes: [],
    errors: [],
    models: [],
  };
  const buckets = groupTraffic(data);
  expect(
    (reviewTimeline(data, buckets).series as LineSeriesOption[]).map((s) => [
      s.name,
      s.data,
    ]),
  ).toEqual([["平均耗时", [35, null, 80]]]);
  expect(
    (reviewTimeline(data, buckets, "slow").series as LineSeriesOption[]).map(
      (s) => [s.name, s.data],
    ),
  ).toEqual([["慢请求耗时", [50, null, 100]]]);
});
