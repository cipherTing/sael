import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
  act,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import DashboardPage from "./DashboardPage";
import type { Analytics } from "../analytics";
import { request } from "../api";
vi.mock("../api", () => ({ request: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("refreshes the displayed RPM every thirty seconds", async () => {
  vi.useFakeTimers();
  let responses = 100;
  vi.mocked(request).mockImplementation(async () => ({
    ...data,
    current_rpm: ++responses,
  }));
  await act(async () => {
    mount();
  });
  await act(async () => {
    await vi.advanceTimersByTimeAsync(1);
  });
  const value = () =>
    screen
      .getByText("RPM", { selector: ".metric-label" })
      .parentElement?.querySelector(".metric-value")?.textContent;
  expect(value()).toBe("101");
  await act(async () => {
    await vi.advanceTimersByTimeAsync(29998);
  });
  expect(value()).toBe("101");
  await act(async () => {
    await vi.advanceTimersByTimeAsync(2);
  });
  expect(value()).toBe("102");
});
const data: Analytics = {
  since: "2026-09-24T00:00:00Z",
  until: "2026-09-24T01:00:00Z",
  step_seconds: 300,
  traffic: [
    {
      time: "2026-09-24T00:00:00Z",
      endpoint: "openai_chat",
      model: "gpt-test",
      outcome: "blocked",
      count: 5,
    },
    {
      time: "2026-09-24T00:00:00Z",
      endpoint: "openai_chat",
      model: "gpt-test",
      outcome: "clean",
      count: 95,
    },
  ],
  previous: [],
  distributions: [],
  scenes: [],
  errors: [],
  models: ["gpt-test"],
};
function mount(initialEntry = "/") {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[initialEntry]}>
        <DashboardPage enabled />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("links blocked requests to records with the same resolved time window", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  const links = await screen.findAllByRole("link", { name: "查看拦截记录" });
  expect(links).toHaveLength(2);
  for (const link of links) {
    const url = new URL(link.getAttribute("href")!, "http://localhost");
    expect(url.searchParams.get("start")).toBe(data.since);
    expect(url.searchParams.get("end")).toBe(data.until);
    expect(url.searchParams.get("action")).toBe("block");
  }
  expect(screen.getAllByText("5%").length).toBeGreaterThan(0);
});

it("preserves a chosen minute granularity when changing to a three-day range", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount("/?granularity=1m");
  fireEvent.click(
    await screen.findByRole("button", { name: "时间范围：近 24 小时" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "近 3 天" }));
  await waitFor(() => {
    const url = String(vi.mocked(request).mock.lastCall?.[0]);
    const q = new URL(url, "http://localhost").searchParams;
    expect(q.get("minutes")).toBe("4320");
    expect(q.get("granularity")).toBe("1m");
  });
  expect(screen.getByRole("combobox", { name: "统计粒度" }).textContent).toBe(
    "1 分钟",
  );
});
it("clicking an endpoint loads its own statistics", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  fireEvent.click(
    await screen.findByRole("button", { name: "筛选 Chat Completions" }),
  );
  await screen.findByRole("button", { name: "清除端点筛选" });
  expect(
    vi
      .mocked(request)
      .mock.calls.some(([url]) => String(url).includes("endpoint=openai_chat")),
  ).toBe(true);
});
it("shows a failed query instead of a zero-traffic dashboard", async () => {
  vi.mocked(request).mockRejectedValue(new Error("统计数据暂不可用"));
  mount();
  expect((await screen.findByRole("alert")).textContent).toContain(
    "统计数据暂不可用",
  );
  expect(screen.queryByRole("link", { name: "查看拦截记录" })).toBeNull();
});

it("shows traffic, review quality and latency together and correlates errors to the selected window", async () => {
  vi.mocked(request).mockResolvedValue({
    ...data,
    traffic: [
      ...data.traffic,
      {
        time: data.since,
        endpoint: "openai_chat",
        model: "gpt-test",
        outcome: "unreviewed",
        count: 2,
      },
    ],
    errors: [
      {
        time: data.since,
        endpoint: "openai_chat",
        kind: "classifier_timeout",
        count: 2,
      },
    ],
    distributions: [
      {
        time: data.since,
        endpoint: "openai_chat",
        model: "gpt-test",
        metric: "review_ms",
        upper: 200,
        count: 100,
      },
    ],
  });
  mount();
  expect(
    await screen.findByRole("img", {
      name: "进网请求趋势",
    }),
  ).toBeTruthy();
  expect(screen.getByRole("img", { name: "命中与拦截趋势" })).toBeTruthy();
  expect(screen.getByRole("img", { name: "审查耗时趋势" })).toBeTruthy();
  const link = screen.getByRole("link", { name: /调用超时/ });
  const url = new URL(link.getAttribute("href")!, "http://localhost");
  expect(url.searchParams.get("kind")).toBe("failure");
  expect(url.searchParams.get("error_kind")).toBe("classifier_timeout");
  expect(url.searchParams.get("start")).toBe(data.since);
  expect(
    screen.getByRole("button", { name: "筛选 Chat Completions" }),
  ).toBeTruthy();
});

it("changes the requested granularity and includes the browser time zone", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  fireEvent.keyDown(await screen.findByRole("combobox", { name: "统计粒度" }), {
    key: "Enter",
  });
  fireEvent.click(await screen.findByRole("option", { name: "1 小时" }));
  await screen.findByRole("img", { name: "进网请求趋势" });
  expect(
    vi.mocked(request).mock.calls.some(([url]) => {
      const q = new URL(String(url), "http://localhost").searchParams;
      return (
        q.get("granularity") === "1h" &&
        q.get("timezone") === Intl.DateTimeFormat().resolvedOptions().timeZone
      );
    }),
  ).toBe(true);
});

it("shows actions as shares of problem requests, without clean traffic diluting the bar", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  const links = await screen.findAllByRole("link", { name: "查看拦截记录" });
  const segment = links.find(
    (link) => link.parentElement?.className === "outcome-strip",
  );
  expect(segment?.style.width).toBe("100%");
});

it("shows the recent minute RPM independently of the selected historical window", async () => {
  vi.mocked(request).mockResolvedValue({ ...data, current_rpm: 42 });
  mount();
  const label = await screen.findByText("RPM", { selector: ".metric-label" });
  expect(label.parentElement?.textContent).toContain("42");
  expect(label.parentElement?.textContent).toContain("最近一分钟");
});
