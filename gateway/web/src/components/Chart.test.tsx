import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { Chart, timeChart } from "./Chart";

afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
});
it("draws a scrollable chart after its initially hidden viewport receives a width", async () => {
  let width = 0,
    resize: ResizeObserverCallback = () => {};
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(
    () => new DOMRect(0, 0, width, 280),
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    measureText: (value: string) => ({ width: value.length * 6 }),
  } as unknown as CanvasRenderingContext2D);
  vi.stubGlobal(
    "ResizeObserver",
    class {
      constructor(callback: ResizeObserverCallback) {
        resize = callback;
      }
      observe() {}
      unobserve() {}
      disconnect() {}
    },
  );
  render(
    <Chart
      label="test trend"
      ticks={24}
      option={{
        animation: false,
        xAxis: { type: "category", data: ["2026/09/24 10:00"] },
        yAxis: { type: "value" },
        series: [{ type: "bar", data: [12] }],
      }}
    />,
  );
  act(() => {
    width = 320;
    resize([], {} as ResizeObserver);
  });
  expect(await screen.findByText("2026/09/24 10:00")).toBeTruthy();
});

it("does not overflow a fractional grid width when clientWidth rounds up", async () => {
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(821);
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
    new DOMRect(0, 0, 820.5, 280),
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    measureText: (value: string) => ({ width: value.length * 6 }),
  } as unknown as CanvasRenderingContext2D);
  render(
    <Chart
      label="fractional grid"
      option={{
        animation: false,
        xAxis: { type: "category", data: ["sample"] },
        yAxis: { type: "value" },
        series: [{ type: "bar", data: [1] }],
      }}
    />,
  );
  await screen.findByText("sample");
  const svg = screen
    .getByRole("img", { name: "fractional grid" })
    .querySelector("svg");
  expect(Number(svg?.getAttribute("width"))).toBeLessThanOrEqual(820.5);
});
it("keeps low percentage changes legible without exceeding 100 percent", () => {
  const option = timeChart(
    [],
    [{ name: "命中率", data: [2, 8, 12], color: "#000" }],
    "percent",
  );
  expect(option.yAxis).toMatchObject({ min: 0, max: 15 });
  expect(
    timeChart([], [{ name: "命中率", data: [100], color: "#000" }], "percent")
      .yAxis,
  ).toMatchObject({ max: 100 });
});
it("renders an isolated percentage sample even when adjacent buckets have no reviewed requests", async () => {
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
    new DOMRect(0, 0, 400, 280),
  );
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
    measureText: (value: string) => ({ width: value.length * 6 }),
  } as unknown as CanvasRenderingContext2D);
  const option = timeChart(
    [],
    [{ name: "命中率", data: [null, 20, null], color: "#cf626b" }],
    "percent",
  );
  option.xAxis = { type: "category", data: ["a", "b", "c"] };
  option.animation = false;
  const { container } = render(<Chart label="sparse review" option={option} />);
  await screen.findByText("b");
  expect(
    container.querySelectorAll('path[fill="#cf626b"]').length,
  ).toBeGreaterThan(0);
});

it.each(["percent", "ms"] as const)(
  "connects existing %s samples across empty buckets without inventing values",
  async (unit) => {
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue(
      new DOMRect(0, 0, 500, 280),
    );
    vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue({
      measureText: (value: string) => ({ width: value.length * 6 }),
    } as unknown as CanvasRenderingContext2D);
    const option = timeChart(
      [],
      [{ name: "sample", data: [20, 30, null, 40, 50], color: "#cf626b" }],
      unit,
    );
    option.xAxis = { type: "category", data: ["a", "b", "c", "d", "e"] };
    option.animation = false;
    const { container } = render(
      <Chart label="continuous samples" option={option} />,
    );
    await screen.findByText("e");
    const line = container.querySelector('path[stroke="#cf626b"][fill="none"]');
    expect(line?.getAttribute("d")?.match(/M/g)).toHaveLength(1);
    expect((option.series as { data: unknown[] }[])[0].data).toEqual([
      20,
      30,
      null,
      40,
      50,
    ]);
  },
);

it("formats an empty ECharts bucket as no sample rather than NaN or zero", () => {
  const option = timeChart(
    [
      {
        start: "2026-09-30T00:00:00Z",
        end: "2026-09-30T01:00:00Z",
        checked: 0,
        hits: 0,
        blocked: 0,
      },
    ] as never,
    [{ name: "审查 P95", data: [null], color: "#516de5" }],
    "ms",
  );
  const format = (option.tooltip as { formatter: Function }).formatter;
  const text = format([{ dataIndex: 0, seriesName: "审查 P95", value: "-" }]);
  expect(text).toContain("无样本");
  expect(text).not.toContain("NaN");
  expect(text).not.toContain("0 ms");
});
