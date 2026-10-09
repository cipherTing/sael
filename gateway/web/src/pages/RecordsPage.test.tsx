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

it("explains cached condition-only recording without inventing scores", async () => {
  const item = {
    ...event,
    review_source: "cache",
    scores: [],
    execution_mode: "blocking",
    decision: {
      ...event.decision,
      action: "allow",
      review_mode: "blocking",
      reason: "condition_record_only",
      hits: [{ question: "gore", threshold: 1.5, record_only: true }],
    },
    trace: [
      {
        id: "scene-old",
        name: "历史场景",
        status: "effective",
        conditions: [
          {
            question: "gore",
            threshold: 1.5,
            matched: true,
            record_only: true,
          },
        ],
      },
    ],
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  const row = await screen.findByRole("row", { name: "查看请求 request-1" });
  expect(within(row).getByText("条件仅记录")).toBeTruthy();
  fireEvent.click(row);
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText("缓存判定，无原始分数")).toBeTruthy();
  expect(within(dialog).queryByRole("img", { name: /原始刻度/ })).toBeNull();
});

it("filters records by request source", async () => {
  vi.mocked(request).mockResolvedValue([
    { ...event, id: "http-event", request_source: "review_api" },
  ]);
  mount("/events?request_source=review_api");
  await screen.findByText("HTTP 审查");
  expect(request).toHaveBeenCalledWith(
    expect.stringContaining("request_source=review_api"),
  );
  expect(screen.getByRole("combobox", { name: "请求来源" })).toBeTruthy();
});

it("labels HTTP and legacy gateway records in the combined list", async () => {
  vi.mocked(request).mockResolvedValue([
    { ...event, id: "http-event", request_source: "review_api" },
    event,
  ]);
  mount();
  const rows = await screen.findAllByRole("row", {
    name: "查看请求 request-1",
  });
  expect(within(rows[0]).getByText("HTTP 审查")).toBeTruthy();
  expect(within(rows[1]).getByText("网关请求")).toBeTruthy();
});

it.each([
  ["review_api", "HTTP 审查"],
  [undefined, "网关请求"],
])("shows the %s request source in record details", async (source, label) => {
  const item = { ...event, request_source: source };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  expect(await within(dialog).findByText(label!)).toBeTruthy();
});

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
  expect(screen.getByText("高")).toBeTruthy();
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

it("keeps core caller information before input and scores with secondary details below and testing in the fixed header", async () => {
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
  ).toBeLessThan(headings.indexOf("更多信息"));
  const core = within(dialog).getByRole("region", { name: "处理摘要" });
  expect(within(core).getByText("sk-a********9876")).toBeTruthy();
  expect(within(core).getByText("203.0.113.7")).toBeTruthy();
  expect(within(core).getByText("session-123")).toBeTruthy();
  expect(
    within(dialog)
      .getByRole("link", { name: "用此记录试算" })
      .closest("[data-slot=sheet-header]"),
  ).toBeTruthy();
  expect(within(dialog).getAllByText("session-123")).toHaveLength(1);
  const secondary = within(dialog).getByRole("region", { name: "更多信息" });
  expect(secondary.closest("details")).toBeNull();
  expect(secondary.textContent).not.toContain("session-123");
  expect(within(dialog).queryByText("请求来源")).toBeNull();
  expect(within(dialog).queryByText("请求参数")).toBeNull();
});

it("removes both request IDs from details, optional columns and the visible search hint", async () => {
  const item = { ...event, client_request_id: "client-request-secret" };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("current user text");
  expect(within(dialog).queryByText("request-1")).toBeNull();
  expect(within(dialog).queryByText("client-request-secret")).toBeNull();
  expect(within(dialog).queryByText("请求 ID")).toBeNull();
  expect(
    within(dialog).queryByRole("button", { name: "复制请求 ID" }),
  ).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  expect(
    (screen.getByRole("textbox", { name: "搜索记录" }) as HTMLInputElement)
      .placeholder,
  ).not.toContain("请求 ID");
  fireEvent.keyDown(screen.getByRole("button", { name: "显示列" }), {
    key: "Enter",
  });
  await screen.findByRole("menuitemcheckbox", { name: "客户端" });
  expect(
    screen.queryByRole("menuitemcheckbox", { name: "请求 ID" }),
  ).toBeNull();
});

it("shows meaningful inbound parameters by default with Chinese known enums and exact decimals", async () => {
  const item = {
    ...event,
    has_non_text_input: true,
    user_agent: "codex-test/1",
    parameters: {
      reasoning_effort: "high",
      service_tier: "priority",
      max_output_tokens: 2048,
      temperature: 0.123456789,
      top_p: 0,
      tool_count: 2,
      tool_choice: "required",
      response_format: "json_schema",
      thinking_type: "enabled",
      thinking_budget: 4096,
    },
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const more = await screen.findByRole("region", { name: "更多信息" });
  expect(within(more).getByText("高")).toBeTruthy();
  expect(within(more).getByText("优先")).toBeTruthy();
  expect(within(more).getByText("2,048")).toBeTruthy();
  expect(within(more).getByText("0.123456789")).toBeTruthy();
  expect(within(more).getByText("0")).toBeTruthy();
  expect(within(more).getByText("必须调用")).toBeTruthy();
  expect(within(more).getByText("结构化 JSON")).toBeTruthy();
  expect(within(more).getByText("已开启")).toBeTruthy();
  expect(within(more).getByText("包含非文本输入")).toBeTruthy();
  expect(within(more).getByText("codex-test/1")).toBeTruthy();
});

it("preserves future enum values and omits missing fields rather than inferring them", async () => {
  const item = {
    ...event,
    stream: undefined,
    has_non_text_input: undefined,
    user_agent: undefined,
    client_ip: undefined,
    masked_key: undefined,
    session_id: undefined,
    parameters: {
      reasoning_effort: "future-effort",
      service_tier: "future-tier",
      previous_response_id: "resp-old",
    },
  } as unknown as Event;
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  const more = await within(dialog).findByRole("region", { name: "更多信息" });
  expect(within(more).getByText("future-effort")).toBeTruthy();
  expect(within(more).getByText("future-tier")).toBeTruthy();
  expect(within(more).queryByText("resp-old")).toBeNull();
  for (const label of [
    "响应方式",
    "客户端",
    "非文本输入",
    "调用密钥",
    "来源 IP",
    "会话 ID",
  ])
    expect(within(dialog).queryByText(label)).toBeNull();
  expect(within(more).queryByText("模型")).toBeNull();
  expect(within(more).queryByText("请求时间")).toBeNull();
  expect(within(more).queryByText("端点")).toBeNull();
});

it("shows only explicitly supplied image settings with readable real values", async () => {
  const item = {
    ...event,
    protocol: "openai_images",
    endpoint: "/v1/images/edits",
    image_operation: "edit" as const,
    parameters: {
      image_size: "1536x1024",
      image_quality: "high",
      image_count: 2,
      image_output_format: "png",
    },
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const more = await screen.findByRole("region", { name: "更多信息" });
  expect(within(more).getByText("1536x1024")).toBeTruthy();
  expect(within(more).getByText("高")).toBeTruthy();
  expect(within(more).getByText("2 张")).toBeTruthy();
  expect(within(more).getByText("PNG")).toBeTruthy();
  expect(within(more).queryByText("采样温度")).toBeNull();
});

it("does not supply image defaults absent from an earlier record", async () => {
  const item = {
    ...event,
    protocol: "openai_images",
    image_operation: "generation" as const,
    parameters: {},
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const more = await screen.findByRole("region", { name: "更多信息" });
  for (const label of ["图片尺寸", "图片质量", "图片数量", "图片格式"])
    expect(within(more).queryByText(label)).toBeNull();
});

it("omits an empty additional-information section when nothing was recorded", async () => {
  const item = {
    ...event,
    stream: undefined,
    has_non_text_input: undefined,
    user_agent: undefined,
    parameters: undefined,
  } as unknown as Event;
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const dialog = await screen.findByRole("dialog");
  await within(dialog).findByText("current user text");
  expect(within(dialog).queryByRole("region", { name: "更多信息" })).toBeNull();
});

it("shows long client and request parameter values fully by default without disclosure controls", async () => {
  const client = `long-client/1 ${"framework-detail ".repeat(20)}`;
  const outputFormat = `resp-${"r".repeat(180)}`;
  const contentType = `multipart/form-data; boundary=${"a".repeat(130)}`;
  const item = {
    ...event,
    user_agent: client,
    content_type: contentType,
    parameters: {
      max_output_tokens: 2048,
      response_format: outputFormat,
    },
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const more = await screen.findByRole("region", { name: "更多信息" });
  expect(within(more).getByText(client.trim()).closest("details")).toBeNull();
  expect(within(more).getByText(outputFormat).closest("details")).toBeNull();
  expect(within(more).getByText(contentType).closest("details")).toBeNull();
  expect(within(more).getByText("2,048").closest("details")).toBeNull();
  expect(within(more).queryByLabelText("展开客户端")).toBeNull();
});

it("shows recorded inbound body size and content type without inventing absent request metadata", async () => {
  const item = {
    ...event,
    content_type: "application/json; charset=utf-8",
    request_bytes: 1536,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const more = await screen.findByRole("region", { name: "更多信息" });
  expect(
    within(more).getByText("application/json; charset=utf-8"),
  ).toBeTruthy();
  expect(within(more).getByText("1.5 KiB")).toBeTruthy();
  expect(within(more).queryByText("请求 ID")).toBeNull();
});

it("shows original input length next to the preview only when it differs from the displayed counts", async () => {
  const item = {
    ...event,
    text_preview: "短预览",
    text_chars: 200,
    input_chars: 500,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const input = await screen.findByRole("region", { name: "用户输入" });
  expect(within(input).getByText(/原始输入 500 字/)).toBeTruthy();
  expect(within(input).getByText("200 字")).toBeTruthy();
});

it("does not duplicate the original input count when it already matches the full-text count", async () => {
  const item = {
    ...event,
    text_preview: "短预览",
    text_chars: 200,
    input_chars: 200,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? item : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  const input = await screen.findByRole("region", { name: "用户输入" });
  expect(within(input).getByText("200 字")).toBeTruthy();
  expect(within(input).queryByText(/原始输入/)).toBeNull();
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
  const fullText = "current user text，第一条完整内容";
  const first = { ...event, text_chars: Array.from(fullText).length };
  const another = {
    ...event,
    id: "event-2",
    request_id: "request-2",
    text_preview: "另一条预览",
    text_chars: 5,
  };
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  vi.mocked(request).mockImplementation(async (path) => {
    if (path === "/admin/events/event-1/text") return { text: fullText };
    if (path === "/admin/events/event-1") return first;
    if (path === "/admin/events/event-2") return another;
    return [first, another];
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
  await screen.findByText(fullText);
  fireEvent.click(screen.getByRole("button", { name: "复制用户输入" }));
  await waitFor(() => expect(writeText).toHaveBeenLastCalledWith(fullText));
  fireEvent.click(screen.getByRole("button", { name: "Close" }));
  fireEvent.click(screen.getByRole("row", { name: "查看请求 request-2" }));
  await screen.findByText("另一条预览");
  expect(screen.queryByText(fullText)).toBeNull();
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
  const preview = "前 500 字预览" + "字".repeat(491);
  const fullValue = preview + "完整的已去敏用户输入 [REDACTED]";
  let resolveText!: (value: { text: string }) => void;
  const full = new Promise<{ text: string }>((resolve) => {
    resolveText = resolve;
  });
  const item = {
    ...event,
    kind: "warning" as const,
    error_kind: "classifier_input_too_long",
    text: undefined,
    text_preview: preview,
    text_available: true,
    text_chars: Array.from(fullValue).length,
    input_chars: 750000,
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
  expect(screen.getByText(/原始输入 750,000 字/)).toBeTruthy();
  expect(screen.getByText("750,000 字符")).toBeTruthy();
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
  resolveText({ text: fullValue });
  await screen.findByText(fullValue);
  expect(screen.getByRole("heading", { name: "用户输入全文" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "返回预览" }));
  expect(screen.getByText(preview)).toBeTruthy();
  expect(screen.queryByText(fullValue)).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "查看全文" }));
  await screen.findByText(fullValue);
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

it.each([
  { name: "short text", preview: "简短输入", chars: 4, inputChars: 4 },
  {
    name: "exactly 500 Unicode characters with emoji",
    preview: "🙂".repeat(250) + "字".repeat(250),
    chars: 500,
    inputChars: 500,
  },
  {
    name: "redacted input shortened to its complete preview",
    preview: "[REDACTED]",
    chars: 10,
    inputChars: 5000,
  },
  {
    name: "missing full-text length metadata",
    preview: "历史完整输入",
    chars: undefined,
    inputChars: 6000,
  },
])(
  "does not offer or fetch full text for $name",
  async ({ preview, chars, inputChars }) => {
    const item = {
      ...event,
      text_preview: preview,
      text_chars: chars,
      input_chars: inputChars,
    };
    vi.mocked(request).mockImplementation(async (path) =>
      path === "/admin/events/event-1" ? item : [item],
    );
    mount();
    fireEvent.click(
      await screen.findByRole("row", { name: "查看请求 request-1" }),
    );
    await screen.findByRole("heading", { name: "用户输入预览" });
    expect(screen.queryByRole("button", { name: "查看全文" })).toBeNull();
    expect(
      vi.mocked(request).mock.calls.some(([path]) => path.endsWith("/text")),
    ).toBe(false);
  },
);

it("loads a genuinely truncated emoji preview using Unicode character counts", async () => {
  const preview = "🙂".repeat(500);
  const fullValue = preview + "补";
  const item = {
    ...event,
    text_preview: preview,
    text_chars: 501,
    input_chars: 501,
  };
  vi.mocked(request).mockImplementation(async (path) =>
    path.endsWith("/text")
      ? { text: fullValue }
      : path === "/admin/events/event-1"
        ? item
        : [item],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  fireEvent.click(await screen.findByRole("button", { name: "查看全文" }));
  await screen.findByText(fullValue);
  expect(screen.getByRole("button", { name: "返回预览" })).toBeTruthy();
});

it("never renders response or conversation association IDs from request parameters", async () => {
  const item = {
    ...event,
    session_id: "actual-session",
    parameters: {
      previous_response_id: "resp-hidden-private",
      conversation_id: "conv-hidden-private",
      reasoning_effort: "high",
    },
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
  expect(within(dialog).queryByText("resp-hidden-private")).toBeNull();
  expect(within(dialog).queryByText("conv-hidden-private")).toBeNull();
  expect(within(dialog).queryByText("前序响应 ID")).toBeNull();
  expect(within(dialog).queryByText("对话 ID")).toBeNull();
  expect(within(dialog).getByText("actual-session")).toBeTruthy();
  expect(within(dialog).getByText("高")).toBeTruthy();
});

it("keeps the preview and offers a toast retry when loading the full text fails", async () => {
  const notify = vi.spyOn(toast, "error").mockReturnValue("toast-id");
  const fullValue = "保留的预览，已加载的完整文本";
  const item = {
    ...event,
    text_preview: "保留的预览",
    text_chars: Array.from(fullValue).length,
  };
  let failed = true;
  vi.mocked(request).mockImplementation(async (path) => {
    if (path.endsWith("/text")) {
      if (failed) throw new Error("全文暂不可用");
      return { text: fullValue };
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
  await screen.findByText(fullValue);
  expect(screen.getByRole("button", { name: "返回预览" })).toBeTruthy();
  notify.mockRestore();
});
