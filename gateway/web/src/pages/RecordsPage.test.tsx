import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MemoryRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import RecordsPage from "./RecordsPage";
import { request } from "../api";
import type { Event } from "../types";
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
  text_preview: "",
  text: "current user text",
  policy_version: 2,
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
function mount() {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <RecordsPage scenes={[]} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}
it("defaults to hit records and preserves the separate Jev error view", async () => {
  vi.mocked(request).mockResolvedValue([]);
  mount();
  await screen.findByText("暂无数据");
  expect(screen.getByRole("button", { name: "场景命中" }).className).toContain(
    "active",
  );
  expect(screen.getByRole("combobox", { name: "记录场景" })).toBeTruthy();
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
it("opens the historical threshold and carries the sample back to its scene", async () => {
  vi.mocked(request).mockImplementation(async (path) =>
    path === "/admin/events/event-1" ? event : [event],
  );
  mount();
  fireEvent.click(
    await screen.findByRole("row", { name: "查看请求 request-1" }),
  );
  expect(await screen.findByText("current user text")).toBeTruthy();
  expect(screen.getByText("> 1.5")).toBeTruthy();
  expect(
    screen.getByRole("link", { name: "用此记录试算" }).getAttribute("href"),
  ).toBe("/scenes?sample=event-1&scene=scene-old");
});

it("shows request context and keeps below-threshold scores collapsed", async () => {
  const enriched = {
    ...event,
    client_ip: "203.0.113.7",
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
    await screen.findByRole("button", { name: "查看会话 session-123 的记录" }),
  ).toBeTruthy();
  expect(screen.getByText("high")).toBeTruthy();
  expect(screen.getByText("2,048")).toBeTruthy();
  const hidden = screen
    .getByText("未达到阈值", { exact: false })
    .closest("details");
  expect(hidden).not.toBeNull();
  expect(hidden!.open).toBe(false);
  expect(hidden!.textContent).toContain("自伤风险");
  expect(hidden!.textContent).not.toContain("血腥程度");
  fireEvent.click(
    screen.getByRole("button", { name: "查看会话 session-123 的记录" }),
  );
  await waitFor(() =>
    expect(
      vi
        .mocked(request)
        .mock.calls.some(([p]) => String(p).includes("session_id=session-123")),
    ).toBe(true),
  );
});
