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
import ReviewAPIPage from "./pages/ReviewAPIPage";
import { request } from "./api";
import type { PolicyResponse } from "./policy";

vi.mock("./api", () => ({ request: vi.fn() }));

const overview = {
  enabled: true,
  requests: 40,
  rpm: 3,
  allowed: 25,
  hits: 10,
  blocked: 6,
  errors: 5,
  cache_hits: 4,
  p50_ms: 25,
  p95_ms: 500,
  outcomes: [],
  scenes: [],
  keys: [],
  trend: [],
};
const policy: PolicyResponse = {
  enabled: false,
  review_api_enabled: false,
  scenes: [],
  preview_chars: null,
  retention_days: null,
  questions: [],
};

function setup(enabled = false, tab = "docs") {
  const keys: Record<string, unknown>[] = [
    {
      id: "existing",
      name: "接入程序",
      masked: "sk-sael-review-abcd******",
      created_at: "2026-10-08T00:00:00Z",
      request_count: 5,
    },
  ];
  vi.mocked(request).mockImplementation(async (path, init) => {
    if (path === "/admin/access")
      return {
        ingress_url: "https://gateway.example.com",
        endpoints: [
          {
            id: "openai_chat",
            name: "Chat Completions",
            path: "/v1/chat/completions",
          },
          {
            id: "openai_responses",
            name: "Responses",
            path: "/v1/responses",
          },
          { id: "anthropic", name: "Messages", path: "/v1/messages" },
          {
            id: "openai_images",
            name: "Images",
            path: "/v1/images/generations",
          },
        ],
      } as never;
    if (path.startsWith("/admin/review-api/overview")) return overview as never;
    if (path === "/admin/review-api/keys" && init?.method === "POST")
      return {
        id: "new",
        name: "新密钥",
        masked: "sk-sael-review-new******",
        secret: "created-secret-once",
      } as never;
    if (path === "/admin/review-api/keys") return keys as never;
    if (
      path.startsWith("/admin/review-api/keys/") &&
      init?.method === "DELETE"
    ) {
      keys[0] = { ...keys[0], revoked_at: "2026-10-08T00:00:00Z" };
      return { revoked: true } as never;
    }
    return {
      since: "2026-10-08T00:00:00Z",
      until: "2026-10-08T01:00:00Z",
      traffic: [],
      previous: [],
      distributions: [],
      scenes: [],
      errors: [],
      models: [],
    } as never;
  });
  const onSave = vi.fn(async () => {});
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter
        initialEntries={[`/settings?tab=review-api&review_api_tab=${tab}`]}
      >
        <ReviewAPIPage
          policy={{ ...policy, review_api_enabled: enabled }}
          onSave={onSave}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  return onSave;
}

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
  window.history.pushState({}, "", "/");
});

it("shows integration instructions first and saves only the independent HTTP switch", async () => {
  const onSave = setup();
  expect(await screen.findByText("审核接口接入说明")).toBeTruthy();
  expect(screen.queryByText("请求数")).toBeNull();
  const toggle = screen.getByRole("switch", { name: "HTTP 审核接口" });
  expect((toggle as HTMLButtonElement).getAttribute("data-state")).toBe(
    "unchecked",
  );
  expect(
    vi
      .mocked(request)
      .mock.calls.some(([path]) =>
        path.startsWith("/admin/review-api/overview"),
      ),
  ).toBe(false);
  fireEvent.click(toggle);
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({ review_api_enabled: true }),
  );
  expect(screen.getByRole("tab", { name: "接入说明" })).toBeTruthy();
  expect(screen.queryByRole("tab", { name: "统计" })).toBeNull();
  expect(screen.getByRole("tab", { name: "密钥管理" })).toBeTruthy();
});

it("documents the real endpoint, request fields and review response examples", async () => {
  setup();
  expect(await screen.findByText("审核接口接入说明")).toBeTruthy();
  expect(
    await screen.findByText("https://gateway.example.com/v1/moderations"),
  ).toBeTruthy();
  expect(screen.getByText("Authorization: Bearer <审核密钥>")).toBeTruthy();
  for (const field of ["input", "endpoint"])
    expect(screen.getByText(field, { exact: true })).toBeTruthy();
  const parameterTable = screen.getByRole("table");
  expect(within(parameterTable).queryByText("model", { exact: true })).toBeNull();
  expect(screen.getByText(/只支持以上两个字段/)).toBeTruthy();
  for (const endpoint of [
    "openai_chat",
    "openai_responses",
    "anthropic",
    "openai_images",
  ])
    expect(screen.getByText(endpoint, { exact: true })).toBeTruthy();
  expect(screen.getByText("安全内容示例")).toBeTruthy();
  expect(screen.getByText("命中并拦截示例")).toBeTruthy();
  const docs = screen.getByRole("tabpanel", { name: "接入说明" });
  expect(
    Array.from(docs.querySelectorAll("pre")).some((pre) =>
      pre.textContent?.includes('"object": "sael.moderation"'),
    ),
  ).toBe(true);
});

it("does not request independent statistics while they are hidden and disabled", async () => {
  setup();
  expect(await screen.findByText("审核接口接入说明")).toBeTruthy();
  expect(screen.queryByText("请求数")).toBeNull();
  expect(screen.getByRole("switch", { name: "HTTP 审核接口" })).toBeTruthy();
});

it("creates a one-time review key and revokes an existing key", async () => {
  setup(false, "keys");
  expect(await screen.findByText("接入程序")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("密钥名称"), {
    target: { value: "新密钥" },
  });
  fireEvent.click(screen.getByRole("button", { name: "创建密钥" }));
  expect(await screen.findByText("created-secret-once")).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "已保存密钥" }));
  expect(screen.queryByText("created-secret-once")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "撤销 接入程序" }));
  await waitFor(() =>
    expect(vi.mocked(request)).toHaveBeenCalledWith(
      "/admin/review-api/keys/existing",
      { method: "DELETE" },
    ),
  );
});

it("keeps statistics out of settings while preserving the off switch and keys", async () => {
  const onSave = setup(true, "docs");
  expect(await screen.findByText("审核接口接入说明")).toBeTruthy();
  expect(screen.queryByText("请求数")).toBeNull();
  expect(
    (
      screen.getByRole("switch", { name: "HTTP 审核接口" }) as HTMLButtonElement
    ).getAttribute("data-state"),
  ).toBe("checked");
  fireEvent.click(screen.getByRole("switch", { name: "HTTP 审核接口" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({ review_api_enabled: false }),
  );
  expect(
    vi.mocked(request).mock.calls.some(([, init]) => init?.method === "DELETE"),
  ).toBe(false);
  expect(screen.getByRole("tab", { name: "密钥管理" })).toBeTruthy();
});
