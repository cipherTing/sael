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
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockImplementation(
    () => width,
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
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(400);
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
