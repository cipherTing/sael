import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import {
  TimeRangePicker,
  rangeQuery,
  rangeFromQuery,
  requestRange,
  type TimeRangeValue,
} from "./TimeRangePicker";

afterEach(cleanup);

const current: TimeRangeValue = { key: "24h", label: "近 24 小时", hours: 24 };

it("applies a quick range immediately when it is clicked", () => {
  const onChange = vi.fn();
  render(<TimeRangePicker value={current} onChange={onChange} />);
  fireEvent.click(screen.getByRole("button", { name: "时间范围：近 24 小时" }));
  fireEvent.click(screen.getByRole("button", { name: "近 7 天" }));
  expect(onChange).toHaveBeenCalledWith(
    expect.objectContaining({ key: "7d", hours: 168, label: "近 7 天" }),
  );
});

it("waits for apply before submitting a custom start and end date", () => {
  const onChange = vi.fn();
  render(<TimeRangePicker value={current} onChange={onChange} />);
  fireEvent.click(screen.getByRole("button", { name: "时间范围：近 24 小时" }));
  fireEvent.change(screen.getByLabelText("开始日期"), {
    target: { value: "2026-09-23" },
  });
  fireEvent.change(screen.getByLabelText("结束日期"), {
    target: { value: "2026-09-24" },
  });
  expect(onChange).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "应用" }));
  expect(onChange).toHaveBeenCalledWith(
    expect.objectContaining({
      key: "custom",
      startDate: "2026-09-23",
      endDate: "2026-09-24",
    }),
  );
});
it("resolves today's end again for each refresh and keeps its preset label", () => {
  const q = rangeQuery(new URLSearchParams(), {
    key: "today",
    label: "今天",
    hours: 1,
  });
  expect(rangeFromQuery(q).label).toBe("今天");
  expect(requestRange(q, new Date("2026-09-24T11:00:00Z")).get("end")).toBe(
    "2026-09-24T11:00:00.000Z",
  );
  expect(requestRange(q, new Date("2026-09-24T12:00:00Z")).get("end")).toBe(
    "2026-09-24T12:00:00.000Z",
  );
});
it("reopens a custom selection without resetting its entered times", () => {
  const from = new Date(2026, 8, 23, 13, 25),
    to = new Date(2026, 8, 23, 16, 41);
  const value = rangeFromQuery(
    new URLSearchParams({ start: from.toISOString(), end: to.toISOString() }),
  );
  render(<TimeRangePicker value={value} onChange={vi.fn()} />);
  fireEvent.click(
    screen.getByRole("button", { name: `时间范围：${value.label}` }),
  );
  expect((screen.getByLabelText("开始时间") as HTMLInputElement).value).toBe(
    "13:25",
  );
  expect((screen.getByLabelText("结束时间") as HTMLInputElement).value).toBe(
    "16:40",
  );
});
