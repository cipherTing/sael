import { describe, expect, it } from "vitest";
import {
  summarize,
  quantile,
  groupTraffic,
  drilldownQuery,
  latencySeries,
} from "./analytics";
import type { Analytics } from "./analytics";

const data: Analytics = {
  since: "2026-09-24T00:00:00Z",
  until: "2026-09-24T00:03:00Z",
  step_seconds: 60,
  traffic: [
    {
      time: "2026-09-24T00:00:00Z",
      endpoint: "openai_chat",
      model: "test",
      outcome: "clean",
      count: 70,
    },
    {
      time: "2026-09-24T00:00:00Z",
      endpoint: "openai_chat",
      model: "test",
      outcome: "blocked",
      count: 20,
    },
    {
      time: "2026-09-24T00:01:00Z",
      endpoint: "anthropic",
      model: "test",
      outcome: "hit_allowed",
      count: 10,
    },
    {
      time: "2026-09-24T00:01:00Z",
      endpoint: "anthropic",
      model: "test",
      outcome: "unreviewed",
      count: 5,
    },
    {
      time: "2026-09-24T00:01:00Z",
      endpoint: "anthropic",
      model: "test",
      outcome: "disabled",
      count: 95,
    },
  ],
  previous: [],
  distributions: [],
  scenes: [],
  errors: [],
  models: ["test"],
};

describe("dashboard metric definitions", () => {
  it("keeps preflight skips and frozen sessions out of Jev denominators", () => {
    const extra = [
      { ...data.traffic[0], outcome: "input_too_long", count: 4 },
      { ...data.traffic[0], outcome: "session_blocked", count: 3 },
    ];
    expect(summarize([...data.traffic, ...extra])).toMatchObject({
      total: 207,
      checked: 100,
      hits: 30,
      blocked: 20,
      failures: 5,
      skipped: 4,
      frozen: 3,
      failureRate: (5 / 105) * 100,
    });
  });
  it("counts reviewed and failed requests with different denominators", () => {
    expect(summarize(data.traffic)).toMatchObject({
      total: 200,
      checked: 100,
      hits: 30,
      blocked: 20,
      failures: 5,
      hitRate: 30,
      blockRate: 20,
      failureRate: (5 / 105) * 100,
    });
  });
  it("does not invent percentages or latency when no samples exist", () => {
    expect(summarize([])).toMatchObject({
      hitRate: null,
      blockRate: null,
      failureRate: null,
    });
    expect(quantile([], 0.95)).toBeNull();
  });
  it("combines histogram counts instead of averaging percentiles", () => {
    expect(
      quantile(
        [
          { upper: 20, count: 99 },
          { upper: 5000, count: 1 },
        ],
        0.95,
      ),
    ).toBe(20);
  });
  it("retains zero-request time buckets and every full date label", () => {
    const buckets = groupTraffic(data);
    expect(buckets).toHaveLength(3);
    expect(buckets[2].total).toBe(0);
    expect(buckets[0].label).toMatch(/2026\/09\/24/);
    expect(buckets[2].hitRate).toBeNull();
  });
  it("anchors hourly buckets to the clock and clips the first and last bucket to the query", () => {
    const partial: Analytics = {
      ...data,
      since: "2026-09-24T10:47:00Z",
      until: "2026-09-24T12:17:00Z",
      step_seconds: 3600,
      traffic: [
        { ...data.traffic[0], time: "2026-09-24T10:00:00Z", count: 13 },
        { ...data.traffic[0], time: "2026-09-24T12:00:00Z", count: 17 },
      ],
    };
    const buckets = groupTraffic(partial);
    expect(buckets[0]).toMatchObject({ start: "2026-09-24T10:47:00.000Z" });
    expect(buckets.map((b) => [b.time, b.end, b.total])).toEqual([
      ["2026-09-24T10:00:00.000Z", "2026-09-24T11:00:00.000Z", 13],
      ["2026-09-24T11:00:00.000Z", "2026-09-24T12:00:00.000Z", 0],
      ["2026-09-24T12:00:00.000Z", "2026-09-24T12:17:00.000Z", 17],
    ]);
    expect(
      groupTraffic({ ...partial, since: "2026-09-24T10:48:00Z" }).map(
        (b) => b.time,
      ),
    ).toEqual(buckets.map((b) => b.time));
  });
  it("uses local midnight for daily buckets and respects partial days", () => {
    const start = new Date(2026, 8, 24, 13, 0).toISOString();
    const end = new Date(2026, 8, 26, 10, 0).toISOString();
    const buckets = groupTraffic({
      ...data,
      since: start,
      until: end,
      step_seconds: 86400,
      traffic: [],
    });
    expect(buckets.map((b) => new Date(b.time).getHours())).toEqual([0, 0, 0]);
    expect(buckets.map((b) => new Date(b.time).getDate())).toEqual([
      24, 25, 26,
    ]);
    expect(buckets[0]).toMatchObject({ start });
    expect(buckets[2].end).toBe(end);
  });
  it("joins server timestamps and browser timestamps as instants, not strings", () => {
    const measured: Analytics = {
      ...data,
      distributions: [
        {
          time: "2026-09-24T00:00:00Z",
          endpoint: "openai_chat",
          metric: "review_ms",
          upper: 200,
          count: 4,
        },
      ],
    };
    expect(latencySeries(measured, groupTraffic(measured))).toEqual([
      200,
      null,
      null,
    ]);
  });
  it("pins drilldown to the actual time window and retains endpoint and model", () => {
    const q = drilldownQuery(
      new URLSearchParams("minutes=1440&endpoint=openai_chat&model=test"),
      data,
      { action: "block", scene: "rule-1" },
    );
    expect(Object.fromEntries(q)).toEqual({
      endpoint: "openai_chat",
      model: "test",
      start: "2026-09-24T00:00:00Z",
      end: "2026-09-24T00:03:00Z",
      action: "block",
      scene: "rule-1",
    });
  });
});
