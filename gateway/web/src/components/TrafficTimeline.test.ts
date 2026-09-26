import { expect, it } from "vitest";
import { trafficTimeline } from "./TrafficTimeline";
import { groupTraffic, type Analytics } from "../analytics";
import type { LineSeriesOption, TooltipComponentOption } from "echarts";

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
    "Image Edits",
    "Image Variations",
  ]);
});
