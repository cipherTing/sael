import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import App from "./App";
const policy = {
  enabled: false,
  scenes: [],
  preview_chars: null,
  retention_days: null,
  questions: [],
};
const analytics = {
  since: "2026-09-24T00:00:00Z",
  until: "2026-09-24T01:00:00Z",
  step_seconds: 300,
  traffic: [],
  previous: [],
  distributions: [],
  scenes: [],
  errors: [],
  models: [],
};
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
  vi.unstubAllGlobals();
  window.history.pushState({}, "", "/");
});
it("logs in and exposes four pages with access inside settings", async () => {
  let authenticated = false;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/session")
        return authenticated
          ? Response.json({ authenticated: true })
          : new Response("unauthorized", { status: 401 });
      if (path === "/admin/login") {
        authenticated = true;
        return Response.json({ authenticated: true });
      }
      if (path === "/admin/policy") return Response.json(policy);
      if (path.startsWith("/admin/analytics")) return Response.json(analytics);
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  fireEvent.change(await screen.findByLabelText("管理员密码"), {
    target: { value: "secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登录" }));
  await screen.findByRole("heading", { name: "总览" }, { timeout: 10000 });
  for (const label of ["总览", "场景", "记录", "设置"])
    expect(screen.getByRole("link", { name: label })).toBeTruthy();
}, 30000);
it("preserves endpoint, error and exact time filters on the next records page", async () => {
  window.history.pushState(
    {},
    "",
    "/events?kind=failure&endpoint=anthropic&start=2026-09-24T00:00:00Z&end=2026-09-24T01:00:00Z",
  );
  const events = Array.from({ length: 50 }, (_, i) => ({
    id: `event-${i}`,
    time: "2026-09-24T00:01:00Z",
    kind: "failure",
    request_id: `request-${i}`,
    protocol: "anthropic",
    endpoint: "/v1/messages",
    model: "test",
    stream: false,
    has_non_text_input: false,
    text_preview: "",
    classifier_ms: 10,
    decision: { action: "allow", hits: [] },
  }));
  const fetch = vi.fn(async (path: string) => {
    if (path === "/admin/policy") return Response.json(policy);
    if (path.startsWith("/admin/events")) return Response.json(events);
    return Response.json({ authenticated: true });
  });
  vi.stubGlobal("fetch", fetch);
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "下一页" }));
  await waitFor(() =>
    expect(
      fetch.mock.calls.some(([path]) => {
        const q = new URL(path, "http://localhost").searchParams;
        return (
          q.get("offset") === "50" &&
          q.get("kind") === "failure" &&
          q.get("endpoint") === "anthropic" &&
          q.get("start") === "2026-09-24T00:00:00Z" &&
          q.get("end") === "2026-09-24T01:00:00Z"
        );
      }),
    ).toBe(true),
  );
});
it("opens Jev settings directly without navigating a policy tab", async () => {
  window.history.pushState({}, "", "/settings?tab=jev");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/jev")
        return Response.json({
          base_url: "https://example.test/v1",
          model: "jev-test",
          api_key_set: true,
          timeout_ms: 5000,
        });
      if (path === "/admin/runtime")
        return Response.json({ classifier: "not_checked" });
      return Response.json({ authenticated: true });
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  expect(await screen.findByDisplayValue("jev-test")).toBeTruthy();
  expect(screen.getByRole("button", { name: "测试连接" })).toBeTruthy();
});

it.each([true, false])(
  "tests both Jev connection and text independently of model-scoped production scenes (enabled=%s)",
  async (enabled) => {
    window.history.pushState({}, "", "/settings?tab=jev");
    const posted: Record<string, unknown>[] = [];
    const fetch = vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy")
        return Response.json({
          ...policy,
          enabled,
          scenes: [
            {
              id: "production-only",
              name: "限定生产模型",
              match: "any",
              action: "block",
              enabled: true,
              endpoints: ["anthropic"],
              models: ["deepseek-flash"],
              conditions: [{ question: "self_harm", threshold: 0.8 }],
            },
          ],
        });
      if (path === "/admin/jev")
        return Response.json({
          base_url: "https://example.test/v1",
          model: "jev-test",
          api_key_set: true,
          timeout_ms: 5000,
          max_input_tokens: 28800,
        });
      if (path === "/admin/runtime")
        return Response.json({ classifier: "not_checked" });
      if (path === "/admin/jev/test") {
        const body = JSON.parse(String(init?.body));
        posted.push(body);
        const catalog = [
          "cyber_abuse",
          "illicit",
          "violence",
          "child_safety",
          "hate_harassment",
          "privacy_pii",
          "fraud_deception",
          "self_harm",
          "bypass_attempt",
          "sexual",
          "gore",
        ];
        const scores =
          body.policy?.scenes?.length === 0
            ? catalog.map((question) => ({
                question,
                type:
                  question === "gore" || question === "sexual"
                    ? "score"
                    : "noul",
                value: question === "gore" ? 1.9 : 0,
              }))
            : [];
        return Response.json({
          scores,
          decision: { action: "allow", hits: [] },
          policy_ready: false,
          classifier_ms: scores.length ? 24 : 0,
        });
      }
      return Response.json({});
    });
    vi.stubGlobal("fetch", fetch);
    render(
      <BrowserRouter>
        <App />
      </BrowserRouter>,
    );
    fireEvent.click(await screen.findByRole("button", { name: "测试连接" }));
    await waitFor(() => expect(posted).toHaveLength(1));
    expect(posted[0]).toMatchObject({
      text: "这是一条连接测试。",
      policy: { enabled: false, scenes: [] },
    });
    expect(posted[0]).not.toHaveProperty("connection");
    expect(await screen.findByText("连接正常")).toBeTruthy();
    fireEvent.change(screen.getByRole("textbox", { name: "测试文本" }), {
      target: { value: "a real classification test" },
    });
    fireEvent.click(screen.getByRole("button", { name: "测试 Jev 分类器" }));
    await waitFor(() => expect(posted).toHaveLength(2));
    expect(posted[1]).toMatchObject({
      text: "a real classification test",
      policy: { enabled: false, scenes: [] },
    });
    expect(posted[1]).not.toHaveProperty("connection");
    expect(await screen.findByText("1.9")).toBeTruthy();
    expect(screen.getByText("血腥程度")).toBeTruthy();
    expect(screen.getByText("自伤风险")).toBeTruthy();
    expect(
      fetch.mock.calls.some(
        ([path, init]) => path === "/admin/policy" && Boolean(init?.method),
      ),
    ).toBe(false);
  },
);

it.each([
  { draft: false, failed: false },
  { draft: false, failed: true },
  { draft: true, failed: false },
  { draft: true, failed: true },
])(
  "refreshes production Jev status only after saved tests (draft=$draft, failed=$failed)",
  async ({ draft, failed }) => {
    window.history.pushState({}, "", "/settings?tab=jev");
    let runtimeCalls = 0;
    let classifier = "not_checked";
    let posted: Record<string, unknown> | undefined;
    vi.spyOn(toast, "error").mockReturnValue("test-error");
    vi.stubGlobal(
      "fetch",
      vi.fn(async (path: string, init?: RequestInit) => {
        if (path === "/admin/session")
          return Response.json({ authenticated: true });
        if (path === "/admin/policy") return Response.json(policy);
        if (path === "/admin/jev")
          return Response.json({
            base_url: "https://example.test/v1",
            model: "jev-test",
            api_key_set: true,
            timeout_ms: 5000,
            max_input_tokens: 28800,
          });
        if (path === "/admin/runtime") {
          runtimeCalls++;
          return Response.json({ classifier });
        }
        if (path === "/admin/jev/test") {
          const body = JSON.parse(String(init?.body));
          posted = body;
          if (!body.connection) classifier = failed ? "error" : "ok";
          return failed
            ? new Response("Jev 测试失败", { status: 502 })
            : Response.json({
                scores: [],
                decision: null,
                policy_ready: false,
                classifier_ms: 25,
              });
        }
        return Response.json({});
      }),
    );
    render(
      <BrowserRouter>
        <App />
      </BrowserRouter>,
    );
    const test = await screen.findByRole("button", { name: "测试连接" });
    await waitFor(() => expect(runtimeCalls).toBe(1));
    expect(screen.getByText("生产连接 · 未验证")).toBeTruthy();
    if (draft)
      fireEvent.change(screen.getByLabelText("模型 ID"), {
        target: { value: "jev-draft" },
      });
    fireEvent.click(test);
    await waitFor(() => expect(posted).toBeTruthy());
    if (draft) {
      expect(posted).toMatchObject({
        connection: { model: "jev-draft", api_key: "" },
      });
      expect(
        await screen.findByText(failed ? "测试未完成" : "连接正常"),
      ).toBeTruthy();
      expect(runtimeCalls).toBe(1);
      expect(screen.getByText("生产连接 · 未验证")).toBeTruthy();
    } else {
      expect(posted).not.toHaveProperty("connection");
      await waitFor(() => expect(runtimeCalls).toBe(2));
      expect(
        await screen.findByText(failed ? "生产连接 · 异常" : "生产连接 · 正常"),
      ).toBeTruthy();
      expect(
        await screen.findByText(failed ? "测试未完成" : "连接正常"),
      ).toBeTruthy();
    }
  },
);
it("clears scene-only filters when switching to Jev errors", async () => {
  window.history.pushState(
    {},
    "",
    "/events?kind=hit&scene=one&action=block&endpoint=anthropic&minutes=60",
  );
  const fetch = vi.fn(async (path: string) => {
    if (path === "/admin/policy") return Response.json(policy);
    if (path.startsWith("/admin/events")) return Response.json([]);
    return Response.json({ authenticated: true });
  });
  vi.stubGlobal("fetch", fetch);
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "Jev 错误" }));
  await waitFor(() =>
    expect(
      fetch.mock.calls.some(([path]) => {
        const q = new URL(path, "http://localhost").searchParams;
        return (
          q.get("kind") === "failure" &&
          q.get("endpoint") === "anthropic" &&
          !q.has("scene") &&
          !q.has("action")
        );
      }),
    ).toBe(true),
  );
});
it("returns to login when logging out after the server session expires", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/logout")
        return new Response("expired", { status: 401 });
      if (path === "/admin/policy") return Response.json(policy);
      if (path.startsWith("/admin/analytics")) return Response.json(analytics);
      return Response.json({ authenticated: true });
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "退出登录" }));
  expect(await screen.findByLabelText("管理员密码")).toBeTruthy();
});
it("returns to the login home when an authenticated request expires", async () => {
  let analyticsCalls = 0;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy") return Response.json(policy);
      if (path.startsWith("/admin/analytics")) {
        analyticsCalls++;
        return new Response("expired", { status: 401 });
      }
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  expect(await screen.findByLabelText("管理员密码")).toBeTruthy();
  expect(await screen.findByText("登录已失效，请重新登录")).toBeTruthy();
  expect(analyticsCalls).toBeGreaterThan(0);
});
it("saves configurable trusted-key inactivity without losing the scenes", async () => {
  window.history.pushState({}, "", "/settings?tab=keys");
  let saved: any;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/admin/policy") {
        if (init?.method === "PATCH") {
          saved = JSON.parse(String(init.body));
          return Response.json({ ...policy, ...saved });
        }
        return Response.json({ ...policy, trusted_key_idle_days: 30 });
      }
      if (path === "/admin/jev")
        return Response.json({
          base_url: "https://example.test",
          model: "jev",
          timeout_ms: 5000,
          max_input_tokens: 28800,
        });
      if (path.startsWith("/admin/trusted-keys"))
        return Response.json({ items: [], total: 0 });
      if (path === "/admin/runtime")
        return Response.json({ classifier: "not_checked" });
      return Response.json({ authenticated: true });
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  const days = await screen.findByRole("spinbutton", { name: "闲置清除天数" });
  expect((days as HTMLInputElement).value).toBe("30");
  fireEvent.change(days, { target: { value: "7" } });
  fireEvent.click(screen.getByRole("button", { name: "保存可信密钥设置" }));
  await waitFor(() => expect(saved?.trusted_key_idle_days).toBe(7));
  expect(saved.scenes).toBeUndefined();
  expect(saved.enabled).toBeUndefined();
  expect(saved.version).toBeUndefined();
});
it("disables login for the Retry-After duration", async () => {
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) =>
      path === "/admin/login"
        ? new Response("登录尝试过于频繁，请稍后重试", {
            status: 429,
            headers: { "Retry-After": "60" },
          })
        : new Response("unauthorized", { status: 401 }),
    ),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  fireEvent.change(await screen.findByLabelText("管理员密码"), {
    target: { value: "wrong" },
  });
  fireEvent.click(screen.getByRole("button", { name: "登录" }));
  const button = await screen.findByRole("button", { name: /60 秒后重试/ });
  expect((button as HTMLButtonElement).disabled).toBe(true);
});

it("redirects the former access page into settings with persistent tabs", async () => {
  window.history.pushState({}, "", "/access");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/access")
        return Response.json({ ingress_url: "http://localhost:8091" });
      if (path === "/admin/upstream")
        return Response.json({ base_url: "https://relay.example" });
      return Response.json({ authenticated: true });
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  expect(await screen.findByText("http://localhost:8091")).toBeTruthy();
  expect(window.location.pathname + window.location.search).toBe(
    "/settings?tab=access",
  );
  for (const name of ["审查", "Jev 分类器", "接入", "可信密钥", "审核缓存"])
    expect(screen.getByRole("tab", { name })).toBeTruthy();
  expect(screen.queryByRole("link", { name: "接入" })).toBeNull();
});

it("loads full record text before using a record as a scene sample", async () => {
  window.history.pushState({}, "", "/scenes?sample=event-full");
  const metadata = {
    id: "event-full",
    time: "2026-09-24T00:00:00Z",
    kind: "hit",
    request_id: "request-full",
    protocol: "openai_responses",
    endpoint: "/v1/responses",
    endpoint_group: "openai_responses",
    model: "test",
    stream: false,
    has_non_text_input: false,
    text_preview: "截断预览，不应进入试算",
    text_available: true,
    text_chars: 42,
    decision: { action: "block", hits: [] },
  };
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      calls.push(path);
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/events/event-full") return Response.json(metadata);
      if (path === "/admin/events/event-full/text")
        return Response.json({ text: "完整用户输入，应该进入试算" });
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  const input = await screen.findByRole("textbox", { name: "测试文本" });
  await waitFor(() =>
    expect((input as HTMLTextAreaElement).value).toBe(
      "完整用户输入，应该进入试算",
    ),
  );
  expect(calls).toContain("/admin/events/event-full/text");
  expect((input as HTMLTextAreaElement).value).not.toContain("截断预览");
});

it("uses stored scores without treating a preview as the sample text", async () => {
  window.history.pushState({}, "", "/scenes?sample=event-scores");
  const metadata = {
    id: "event-scores",
    time: "2026-09-24T00:00:00Z",
    kind: "hit",
    request_id: "request-scores",
    protocol: "openai_chat",
    endpoint: "/v1/chat/completions",
    endpoint_group: "openai_chat",
    model: "test",
    stream: false,
    has_non_text_input: false,
    text_preview: "历史截断预览",
    text_available: false,
    scores: [{ question: "gore", type: "score", value: 1.9 }],
    decision: { action: "block", hits: [] },
  };
  const calls: string[] = [];
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      calls.push(path);
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/events/event-scores") return Response.json(metadata);
      if (path === "/admin/policy/test")
        return Response.json({
          scores: metadata.scores,
          classifier_ms: 1,
          policy_ready: true,
          decision: { action: "block" },
          trace: [],
        });
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  const input = await screen.findByRole("textbox", { name: "测试文本" });
  await waitFor(() => expect((input as HTMLTextAreaElement).value).toBe(""));
  expect(calls.some((path) => path.endsWith("/text"))).toBe(false);
  expect(screen.getByText(/复用 1 项分数/)).toBeTruthy();
  expect((input as HTMLTextAreaElement).value).not.toContain("历史截断预览");
});

it("keeps the sample empty and offers retry when full record text fails", async () => {
  window.history.pushState({}, "", "/scenes?sample=event-retry");
  const metadata = {
    id: "event-retry",
    time: "2026-09-24T00:00:00Z",
    kind: "hit",
    request_id: "request-retry",
    protocol: "openai_chat",
    endpoint: "/v1/chat/completions",
    endpoint_group: "openai_chat",
    model: "test",
    stream: false,
    has_non_text_input: false,
    text_preview: "不能拿来重审的预览",
    text_available: true,
    text_chars: 20,
    decision: { action: "block", hits: [] },
  };
  let fullAttempts = 0;
  const notify = vi.spyOn(toast, "error").mockReturnValue("toast-id");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/events/event-retry") return Response.json(metadata);
      if (path === "/admin/events/event-retry/text") {
        fullAttempts += 1;
        if (fullAttempts === 1)
          return new Response("temporary failure", { status: 503 });
        return Response.json({ text: "重试后得到的完整输入" });
      }
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  await waitFor(() => expect(fullAttempts).toBe(1));
  await waitFor(() => expect(notify).toHaveBeenCalled());
  expect(await screen.findByRole("button", { name: "重新加载" })).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "测试文本" })).toBeNull();
  const retry = notify.mock.calls.find(
    ([, options]) =>
      options && typeof options === "object" && "action" in options,
  );
  expect(retry?.[1]).toEqual(
    expect.objectContaining({
      action: expect.objectContaining({ label: "重试" }),
    }),
  );
  fireEvent.click(screen.getByRole("button", { name: "重新加载" }));
  await waitFor(() => expect(fullAttempts).toBe(2));
  const input = await screen.findByRole("textbox", { name: "测试文本" });
  await waitFor(() =>
    expect((input as HTMLTextAreaElement).value).toBe("重试后得到的完整输入"),
  );
});

it("does not open a blank test when a record has only a preview", async () => {
  window.history.pushState({}, "", "/scenes?sample=event-preview");
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string) => {
      if (path === "/admin/session")
        return Response.json({ authenticated: true });
      if (path === "/admin/policy") return Response.json(policy);
      if (path === "/admin/events/event-preview")
        return Response.json({
          id: "event-preview",
          protocol: "openai_chat",
          endpoint: "/v1/chat/completions",
          model: "test",
          text_preview: "只有预览，不能作为原输入",
          text_available: false,
          decision: { action: "block", hits: [] },
        });
      return Response.json({});
    }),
  );
  render(
    <BrowserRouter>
      <App />
    </BrowserRouter>,
  );
  expect(
    await screen.findByText("该记录没有可用于试算的全文或分数"),
  ).toBeTruthy();
  expect(screen.queryByRole("textbox", { name: "测试文本" })).toBeNull();
  expect(
    screen.getByRole("link", { name: "返回记录" }).getAttribute("href"),
  ).toBe("/events?event=event-preview");
});
