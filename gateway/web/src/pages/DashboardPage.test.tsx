import {
  cleanup,
  render,
  screen,
  fireEvent,
  waitFor,
  act,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import DashboardPage from "./DashboardPage";
import type { Analytics } from "../analytics";
import { request } from "../api";
vi.mock("../api", () => ({ request: vi.fn() }));

it("uses endpoint and mutually exclusive outcome rings with a separate real-sample latency distribution", async () => {
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? { keys: [], ips: [] }
      : {
          ...data,
          traffic: [
            ...data.traffic,
            {
              time: data.since,
              endpoint: "openai_chat",
              model: "gpt-test",
              outcome: "hit_allowed",
              count: 3,
            },
          ],
          distributions: [
            {
              time: data.since,
              endpoint: "openai_chat",
              metric: "review_ms",
              upper: 300,
              count: 2,
            },
            {
              time: data.since,
              endpoint: "openai_chat",
              metric: "review_ms",
              upper: 1000,
              count: 3,
            },
            {
              time: data.since,
              endpoint: "openai_chat",
              metric: "cache_hit",
              upper: 0,
              count: 7,
            },
          ],
        },
  );
  mount();
  expect(await screen.findByRole("img", { name: "端点构成" })).toBeTruthy();
  expect(screen.getByRole("img", { name: "命中处置构成" })).toBeTruthy();
  expect(screen.getByRole("img", { name: "审查耗时分布" })).toBeTruthy();
  expect(screen.queryByRole("table", { name: "耗时区间明细" })).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "查看耗时区间明细" }));
  const distribution = screen.getByRole("table", { name: "耗时区间明细" });
  expect(
    within(distribution).getByRole("row", { name: /≤ 300 ms.*2.*40%/ }),
  ).toBeTruthy();
  expect(
    within(distribution).getByRole("row", { name: /300 ms–1 s.*3.*60%/ }),
  ).toBeTruthy();
  const allowed = screen.getByRole("link", { name: "查看记录放行记录" });
  const q = new URL(allowed.getAttribute("href")!, "http://localhost")
    .searchParams;
  expect(q.get("kind")).toBe("hit");
  expect(q.get("action")).toBe("allow");
  expect(q.get("start")).toBe(data.since);
  expect(
    screen.getByRole("region", { name: "核心指标" }).children,
  ).toHaveLength(4);
  expect(screen.getByText("时间范围")).toBeTruthy();
  expect(screen.getByText("时间粒度")).toBeTruthy();
});

it("shows no latency samples instead of a zero-valued distribution for cache-only requests", async () => {
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? { keys: [], ips: [] }
      : {
          ...data,
          distributions: [
            {
              time: data.since,
              endpoint: "openai_chat",
              metric: "cache_hit",
              upper: 0,
              count: 5,
            },
          ],
        },
  );
  mount();
  const distribution = await screen.findByRole("region", {
    name: "构成与分布",
  });
  expect(
    within(distribution).getByRole("heading", { name: "耗时分布" }),
  ).toBeTruthy();
  expect(
    within(distribution).queryByRole("img", { name: "审查耗时分布" }),
  ).toBeNull();
  expect(within(distribution).getByText("暂无数据")).toBeTruthy();
  expect(screen.queryByRole("button", { name: "查看耗时区间明细" })).toBeNull();
});

it("keeps scene volume drilldowns scoped after integrating the ranking into the table", async () => {
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? { keys: [], ips: [] }
      : {
          ...data,
          scenes: [
            {
              time: data.since,
              endpoint: "openai_chat",
              scene_id: "risk-1",
              name: "高风险",
              action: "block",
              winner_id: "risk-1",
              winner_name: "高风险",
              count: 5,
            },
          ],
        },
  );
  mount("/?endpoint=openai_chat&model=gpt-test");
  const link = await screen.findByRole("link", {
    name: "查看 高风险 生效记录",
  });
  expect(link.textContent).toContain("5");
  const query = new URL(link.getAttribute("href")!, "http://localhost")
    .searchParams;
  expect(Object.fromEntries(query)).toEqual({
    endpoint: "openai_chat",
    model: "gpt-test",
    start: data.since,
    end: data.until,
    scene: "risk-1",
    kind: "hit",
  });
});
afterEach(() => {
  cleanup();
  vi.useRealTimers();
  vi.clearAllMocks();
});

it("refreshes the displayed RPM every thirty seconds", async () => {
  vi.useFakeTimers();
  let responses = 100;
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? { keys: [], ips: [] }
      : { ...data, current_rpm: ++responses },
  );
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
      classifier_calls: 5,
      classifier_sum_ms: 1000,
    },
    {
      time: "2026-09-24T00:00:00Z",
      endpoint: "openai_chat",
      model: "gpt-test",
      outcome: "clean",
      count: 95,
      classifier_calls: 95,
      classifier_sum_ms: 19000,
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

it("starts with one average latency line and lets the operator switch to slow requests", async () => {
  vi.mocked(request).mockResolvedValue({
    ...data,
    distributions: [
      {
        time: data.since,
        endpoint: "openai_chat",
        metric: "review_ms",
        upper: 300,
        count: 100,
      },
    ],
  });
  mount();
  const controls = within(
    await screen.findByRole("group", { name: "耗时指标" }),
  );
  expect(
    controls
      .getByRole("button", { name: "平均耗时" })
      .getAttribute("aria-pressed"),
  ).toBe("true");
  expect(screen.queryByRole("button", { name: "慢请求耗时" })).toBeNull();
  fireEvent.click(controls.getByRole("button", { name: /^慢请求$/ }));
  expect(
    await screen.findByRole("button", { name: "慢请求耗时" }),
  ).toBeTruthy();
  expect(
    controls
      .getByRole("button", { name: /^慢请求$/ })
      .getAttribute("aria-pressed"),
  ).toBe("true");
  fireEvent.click(controls.getByRole("button", { name: "平均耗时" }));
  expect(screen.queryByRole("button", { name: "慢请求耗时" })).toBeNull();
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
  const errorToast = vi.spyOn(toast, "error").mockImplementation(() => "toast");
  vi.mocked(request).mockRejectedValue(new Error("统计数据暂不可用"));
  mount();
  await waitFor(() =>
    expect(errorToast.mock.calls[0][0]).toBe("统计数据暂不可用"),
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

it("shows outcome shares of hit requests without clean traffic diluting the ring", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  const links = await screen.findAllByRole("link", { name: "查看拦截记录" });
  const segment = links.find((link) => link.className === "composition-row");
  expect(segment?.textContent).toContain("100%");
  expect(segment?.textContent).toContain("5");
});

it("shows the recent minute RPM independently of the selected historical window", async () => {
  vi.mocked(request).mockResolvedValue({ ...data, current_rpm: 42 });
  mount();
  const label = await screen.findByText("RPM", { selector: ".metric-label" });
  expect(label.parentElement?.textContent).toContain("42");
  expect(label.parentElement?.textContent).toContain("最近一分钟");
});

it("shows cache reuse and its rate for the selected traffic scope", async () => {
  vi.mocked(request).mockResolvedValue({
    ...data,
    distributions: [
      {
        time: data.since,
        endpoint: "openai_chat",
        model: "gpt-test",
        metric: "cache_lookup",
        upper: 0,
        count: 40,
      },
      {
        time: data.since,
        endpoint: "openai_chat",
        model: "gpt-test",
        metric: "cache_hit",
        upper: 0,
        count: 10,
      },
      {
        time: data.until,
        endpoint: "openai_chat",
        model: "gpt-test",
        metric: "cache_lookup",
        upper: 0,
        count: 60,
      },
      {
        time: data.until,
        endpoint: "openai_chat",
        model: "gpt-test",
        metric: "cache_hit",
        upper: 0,
        count: 20,
      },
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
  mount("/?endpoint=openai_chat&model=gpt-test&minutes=60");
  const label = await screen.findByText("缓存命中", {
    selector: ".metric-label",
  });
  expect(label.parentElement?.querySelector(".metric-value")?.textContent).toBe(
    "30",
  );
  expect(
    label.parentElement?.querySelector(".metric-detail")?.textContent,
  ).toBe("命中率 30%");
  const query = new URL(
    String(
      vi
        .mocked(request)
        .mock.calls.find(([url]) =>
          String(url).startsWith("/admin/analytics"),
        )?.[0],
    ),
    "http://localhost",
  ).searchParams;
  expect(query.get("endpoint")).toBe("openai_chat");
  expect(query.get("model")).toBe("gpt-test");
  expect(query.get("minutes")).toBe("60");
});

it("keeps source ranking drilldowns scoped to the resolved range, endpoint, model and selected credential", async () => {
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? {
          keys: [
            {
              credential_id: "credential-1",
              masked_key: "sk-abc******def",
              count: 5,
            },
          ],
          ips: [{ client_ip: "203.0.113.9", count: 3 }],
        }
      : data,
  );
  mount("/?endpoint=openai_chat&model=gpt-test&minutes=60");
  const keyLink = await screen.findByRole("link", {
    name: "查看 sk-abc******def 命中记录",
  });
  const query = new URL(keyLink.getAttribute("href")!, "http://localhost")
    .searchParams;
  expect(Object.fromEntries(query)).toEqual({
    endpoint: "openai_chat",
    model: "gpt-test",
    start: data.since,
    end: data.until,
    kind: "hit",
    credential_id: "credential-1",
  });
  fireEvent.click(screen.getByRole("button", { name: /^IP$/ }));
  const ipLink = await screen.findByRole("link", {
    name: "查看 203.0.113.9 命中记录",
  });
  const ipQuery = new URL(ipLink.getAttribute("href")!, "http://localhost")
    .searchParams;
  expect(ipQuery.get("client_ip")).toBe("203.0.113.9");
  expect(ipQuery.get("credential_id")).toBeNull();
  expect(ipQuery.get("start")).toBe(data.since);
});

it("shows a missing masked credential as a placeholder while preserving its record drilldown", async () => {
  vi.mocked(request).mockImplementation(async (url) =>
    String(url).includes("risk-sources")
      ? {
          keys: [{ credential_id: "legacy-key", masked_key: "", count: 5 }],
          ips: [],
        }
      : data,
  );
  mount();
  const link = await screen.findByRole("link", { name: "查看 — 命中记录" });
  expect(within(link).getByText("—")).toBeTruthy();
  const query = new URL(link.getAttribute("href")!, "http://localhost")
    .searchParams;
  expect(query.get("credential_id")).toBe("legacy-key");
  expect(query.get("kind")).toBe("hit");
  expect(query.get("start")).toBe(data.since);
});

it("does not invent a cache hit rate when there have been no lookups", async () => {
  vi.mocked(request).mockResolvedValue(data);
  mount();
  const label = await screen.findByText("缓存命中", {
    selector: ".metric-label",
  });
  expect(label.parentElement?.querySelector(".metric-value")?.textContent).toBe(
    "0",
  );
  expect(
    label.parentElement?.querySelector(".metric-detail")?.textContent,
  ).toBe("命中率 —");
});
