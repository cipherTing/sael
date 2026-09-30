import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import RecordsPage from "./RecordsPage";
import { request } from "../api";
import type { Event } from "../types";
import { toast } from "sonner";
vi.mock("../api", () => ({ request: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const event: Event = {
  id: "event-1",
  time: "2026-09-24T00:00:00Z",
  kind: "hit",
  request_id: "request-1",
  protocol: "openai_responses",
  endpoint: "/v1/responses",
  model: "test",
  stream: true,
  has_non_text_input: false,
  text_preview: "current user text",
  text_available: true,
  text_chars: 17,
  classifier_ms: 12,
  scores: [{ question: "gore", type: "score", value: 1.9 }],
  decision: {
    action: "block",
    hits: [{ question: "gore", value: 1.9, threshold: 1.5 }],
    scene_id: "scene-old",
    scene_name: "历史场景",
  },
  trace: [
    {
      id: "scene-old",
      name: "历史场景",
      status: "effective",
      conditions: [
        { question: "gore", value: 1.9, threshold: 1.5, matched: true },
      ],
    },
  ],
};
function mount(initialEntry = "/") {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[initialEntry]}>
        <RecordsPage scenes={[]} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("copies the masked key and IP directly from a record row without opening its details", async () => {
  const item = {
    ...event,
    masked_key: "sk-prefix******suffix",
    client_ip: "203.0.113.7",
  };
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  vi.mocked(request).mockResolvedValue([item]);
  mount();
  const row = await screen.findByRole("row", { name: "查看请求 request-1" });
  fireEvent.click(within(row).getByRole("button", { name: "复制调用密钥" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith("sk-prefix******suffix"),
  );
  fireEvent.click(within(row).getByRole("button", { name: "复制来源 IP" }));
  await waitFor(() => expect(writeText).toHaveBeenCalledWith("203.0.113.7"));
  expect(screen.queryByRole("dialog")).toBeNull();
  expect(
    vi
      .mocked(request)
      .mock.calls.some(([path]) => path === "/admin/events/event-1"),
  ).toBe(false);
});

it("keeps the internal caller key ID out of request details while showing its masked value", async () => {
  const item = {
    ...event,
    credential_id: "credential-one",
    masked_key: "sk-a********9876",
  };
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("sk-a********9876");
  expect(within(dialog).queryByText("credential-one")).toBeNull();
  expect(within(dialog).queryByText("调用密钥 ID")).toBeNull();
  expect(
    within(dialog).queryByRole("button", { name: "复制调用密钥 ID" }),
  ).toBeNull();
  fireEvent.click(within(dialog).getByRole("button", { name: "复制调用密钥" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith("sk-a********9876"),
  );
});
it("defaults to hit records and preserves the separate Jev error view", async () => {
  vi.mocked(request).mockResolvedValue([]);
  mount();
  await screen.findByText("暂无数据");
  expect(screen.getByRole("button", { name: "场景命中" }).className).toContain(
    "active",
  );
  expect(screen.getByRole("combobox", { name: "记录场景" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "筛选来源 IP" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "筛选会话 ID" })).toBeTruthy();
  expect(screen.getByRole("textbox", { name: "筛选调用密钥 ID" })).toBeTruthy();
  expect(
    vi
      .mocked(request)
      .mock.calls.some(([path]) => String(path).includes("kind=hit")),
  ).toBe(true);
  fireEvent.click(screen.getByRole("button", { name: "Jev 错误" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(request)
        .mock.calls.some(([path]) => String(path).includes("kind=failure")),
    ).toBe(true),
  );
});

it("keeps the original records filters in the link to scene testing", async () => {
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? event : [event],
  );
  mount("/events?minutes=10080&client_ip=203.0.113.7&model=test");
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const link = await screen.findByRole("link", { name: "用此记录试算" });
  const target = new URL(link.getAttribute("href")!, "http://localhost");
  const back = new URL(
    target.searchParams.get("return") || "",
    "http://localhost",
  );
  expect(back.pathname).toBe("/events");
  expect(back.searchParams.get("minutes")).toBe("10080");
  expect(back.searchParams.get("client_ip")).toBe("203.0.113.7");
  expect(back.searchParams.get("event")).toBe("event-1");
});
it("opens the historical threshold and carries the sample back to its scene", async () => {
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? event : [event],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  expect(await screen.findByText("current user text")).toBeTruthy();
  expect(screen.queryByText("当时的匹配过程")).toBeNull();
  expect(screen.getByText("> 1.5")).toBeTruthy();
  const sampleURL = new URL(
    screen.getByRole("link", { name: "用此记录试算" }).getAttribute("href")!,
    "http://localhost",
  );
  expect(sampleURL.searchParams.get("sample")).toBe("event-1");
  expect(sampleURL.searchParams.get("scene")).toBe("scene-old");
});

it("shows request context and keeps below-threshold scores collapsed", async () => {
  const enriched = {
    ...event,
    client_ip: "203.0.113.7",
    credential_id: "credential-one",
    masked_key: "sk-a********9876",
    session_id: "session-123",
    user_agent: "codex-test/1",
    parameters: { reasoning_effort: "high", max_output_tokens: 2048 },
    scores: [
      ...event.scores!,
      { question: "self_harm", type: "noul", value: 0.2 },
    ],
    trace: [
      {
        ...event.trace![0],
        conditions: [
          ...event.trace![0].conditions,
          { question: "self_harm", value: 0.2, threshold: 0.8, matched: false },
        ],
      },
    ],
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? enriched : [enriched],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  expect(
    await screen.findByRole("button", { name: "复制来源 IP" }),
  ).toBeTruthy();
  expect(screen.getByRole("button", { name: "复制调用密钥" })).toBeTruthy();
  expect(screen.getAllByRole("button", { name: "复制会话 ID" })).toHaveLength(
    1,
  );
  expect(screen.getByText("high")).toBeTruthy();
  expect(screen.getByText("2,048")).toBeTruthy();
  const hidden = screen
    .getByText("未达到阈值", { exact: false })
    .closest("details");
  expect(hidden).not.toBeNull();
  expect(hidden!.open).toBe(false);
  expect(hidden!.textContent).toContain("自伤风险");
  expect(hidden!.textContent).not.toContain("血腥程度");
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  fireEvent.change(screen.getByRole("textbox", { name: "筛选会话 ID" }), {
    target: { value: "session-123" },
  });
  fireEvent.click(screen.getByRole("button", { name: "应用筛选" }));
  await waitFor(() =>
    expect(
      vi
        .mocked(request)
        .mock.calls.some(([p]) => String(p).includes("session_id=session-123")),
    ).toBe(true),
  );
});

it("puts the prompt before risk and source information with the testing action in its fixed header", async () => {
  const item = {
    ...event,
    masked_key: "sk-a********9876",
    client_ip: "203.0.113.7",
    session_id: "session-123",
    parameters: { conversation_id: "session-123", reasoning_effort: "high" },
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("current user text");
  const headings = within(dialog)
    .getAllByRole("heading")
    .map((heading) => heading.textContent);
  expect(headings.indexOf("用户输入预览")).toBeLessThan(
    headings.findIndex((text) => text?.startsWith("达到阈值")),
  );
  expect(
    headings.findIndex((text) => text?.startsWith("达到阈值")),
  ).toBeLessThan(headings.indexOf("请求来源"));
  expect(
    within(dialog)
      .getByRole("link", { name: "用此记录试算" })
      .closest("[data-slot=sheet-header]"),
  ).toBeTruthy();
  expect(within(dialog).getAllByText("session-123")).toHaveLength(1);
  const parameters = within(dialog).getByText("请求参数").closest("details")!;
  expect(parameters.open).toBe(false);
  expect(parameters.textContent).not.toContain("session-123");
});

it("shows a single short historical conversation reference without exposing its hash or algorithm", async () => {
  const ref =
    "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789";
  const item = {
    ...event,
    session_source: "history" as const,
    session_ref: ref,
    session_id: undefined,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("current user text");
  expect(within(dialog).getAllByText("历史关联 · abcdef012345")).toHaveLength(
    1,
  );
  expect(within(dialog).queryByText(ref, { exact: false })).toBeNull();
  expect(within(dialog).queryByText(/指纹|哈希|hash/i)).toBeNull();
});

it("keeps cache records without samples free of invented risk scores and Jev duration", async () => {
  const item = {
    ...event,
    review_source: "cache" as const,
    scores: undefined,
    classifier_ms: undefined,
    trace: undefined,
    decision: { ...event.decision, hits: [] },
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("current user text");
  expect(within(dialog).getByText("缓存命中")).toBeTruthy();
  expect(within(dialog).getByText("历史场景")).toBeTruthy();
  expect(within(dialog).queryByText(/Jev 耗时/)).toBeNull();
  expect(within(dialog).queryByText("血腥程度")).toBeNull();
  expect(within(dialog).queryByText(/达到阈值/)).toBeNull();
});

it("copies the currently displayed text and never shows an earlier record's full text in another detail", async () => {
  const another = {
    ...event,
    id: "event-2",
    request_id: "request-2",
    text_preview: "另一条预览",
  };
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  vi.mocked(request).mockImplementation(async (path) => {
    if (path === "/admin/events/event-1/text")
      return { text: "第一条完整内容" };
    if (path === "/admin/events/event-1") return event;
    if (path === "/admin/events/event-2") return another;
    return [event, another];
  });
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "复制用户输入" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenLastCalledWith("current user text"),
  );
  fireEvent.click(screen.getByRole("button", { name: "查看全文" }));
  await screen.findByText("第一条完整内容");
  fireEvent.click(screen.getByRole("button", { name: "复制用户输入" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenLastCalledWith("第一条完整内容"),
  );
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  fireEvent.click(screen.getByRole("row", { name: "查看请求 request-2" }));
  await screen.findByText("另一条预览");
  expect(screen.queryByText("第一条完整内容")).toBeNull();
  expect(
    vi
      .mocked(request)
      .mock.calls.some(([path]) => path === "/admin/events/event-2/text"),
  ).toBe(false);
});

it("shows the masked caller key in both list and cache detail without fake scores", async () => {
  const item = {
    ...event,
    credential_id: "credential-one",
    masked_key: "sk-a********9876",
    review_source: "cache" as const,
    scores: undefined,
    classifier_ms: undefined,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  expect(await screen.findByText("sk-a********9876")).toBeTruthy();
  fireEvent.click(screen.getByRole("row", { name: "查看请求 request-1" }));
  await screen.findByText("current user text");
  expect(screen.getAllByText("sk-a********9876").length).toBe(2);
  expect(screen.getAllByText("缓存命中").length).toBeGreaterThan(0);
});

it("loads the redacted full text only on request and can return to the explicit preview", async () => {
  let resolveText!: (value: { text: string }) => void;
  const full = new Promise<{ text: string }>((resolve) => {
    resolveText = resolve;
  });
  const item = {
    ...event,
    kind: "warning" as const,
    error_kind: "classifier_input_too_long",
    text: undefined,
    text_preview: "前 500 字预览",
    text_available: true,
    text_chars: 750000,
    input_chars: 750000,
    input_tokens_estimated: 618802,
    jev_input_limit: 60000,
    scores: undefined,
  };
  vi.mocked(request).mockImplementation(async (path) => {
    if (path === "/admin/events/event-1/text") return full;
    return path === "/admin/events/event-1" ? item : [item];
  });
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  await screen.findByRole("heading", { name: "用户输入预览" });
  expect(screen.getByText("750,000 字")).toBeTruthy();
  expect(screen.getByText("618,802 Token")).toBeTruthy();
  expect(vi.mocked(request).mock.calls.some(([p]) => p.endsWith("/text"))).toBe(
    false,
  );
  expect(
    screen.getByRole("link", { name: "用此记录试算" }).getAttribute("href"),
  ).toContain("sample=event-1");
  fireEvent.click(screen.getByRole("button", { name: "查看全文" }));
  await waitFor(() =>
    expect(
      (screen.getByRole("button", { name: "加载全文" }) as HTMLButtonElement)
        .disabled,
    ).toBe(true),
  );
  await waitFor(() =>
    expect(
      vi.mocked(request).mock.calls.filter(([p]) => p.endsWith("/text")),
    ).toHaveLength(1),
  );
  resolveText({ text: "完整的已去敏用户输入 [REDACTED]" });
  await screen.findByText("完整的已去敏用户输入 [REDACTED]");
  expect(screen.getByRole("heading", { name: "用户输入全文" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "返回预览" }));
  expect(screen.getByText("前 500 字预览")).toBeTruthy();
  expect(screen.queryByText("完整的已去敏用户输入 [REDACTED]")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "查看全文" }));
  await screen.findByText("完整的已去敏用户输入 [REDACTED]");
  expect(
    vi.mocked(request).mock.calls.filter(([p]) => p.endsWith("/text")),
  ).toHaveLength(1);
});

it("identifies legacy preview-only records without offering a false full-text action", async () => {
  const item = {
    ...event,
    text: undefined,
    scores: undefined,
    text_preview: "历史预览",
    text_available: false,
    text_chars: 0,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  await screen.findByRole("heading", { name: "用户输入预览" });
  expect(screen.getByText("仅保留预览")).toBeTruthy();
  expect(screen.queryByText("0 字")).toBeNull();
  expect(screen.queryByRole("button", { name: "查看全文" })).toBeNull();
  expect(screen.queryByRole("link", { name: "用此记录试算" })).toBeNull();
});

it("keeps the preview and offers a toast retry when loading the full text fails", async () => {
  const notify = vi.spyOn(toast, "error").mockReturnValue("toast-id");
  const item = { ...event, text_preview: "保留的预览" };
  let failed = true;
  vi.mocked(request).mockImplementation(async (path) => {
    if (path.endsWith("/text")) {
      if (failed) throw new Error("全文暂不可用");
      return { text: "已加载的完整文本" };
    }
    return path === "/admin/events/event-1" ? item : [item];
  });
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "查看全文" }));
  await screen.findByRole("button", { name: "重试全文" });
  expect(screen.getByText("保留的预览")).toBeTruthy();
  expect(notify).toHaveBeenCalledWith(
    "全文暂不可用",
    expect.objectContaining({
      action: expect.objectContaining({ label: "重试" }),
    }),
  );
  failed = false;
  fireEvent.click(screen.getByRole("button", { name: "重试全文" }));
  await screen.findByText("已加载的完整文本");
  expect(screen.getByRole("button", { name: "返回预览" })).toBeTruthy();
  notify.mockRestore();
});
