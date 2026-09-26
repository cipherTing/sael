import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { BrowserRouter } from "react-router";
import { afterEach, expect, it, vi } from "vitest";
import App from "./App";
const policy = {
  enabled: false,
  version: 1,
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
  vi.unstubAllGlobals();
  window.history.pushState({}, "", "/");
});
it("logs in and exposes the five task-oriented pages", async () => {
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
  for (const label of ["总览", "场景", "记录", "接入", "设置"])
    expect(screen.getByRole("link", { name: label })).toBeTruthy();
});
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
    policy_version: 1,
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
  window.history.pushState({}, "", "/settings");
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
it("saves configurable trusted-key inactivity without losing the scenes", async () => {
  window.history.pushState({}, "", "/settings");
  let saved: any;
  vi.stubGlobal(
    "fetch",
    vi.fn(async (path: string, init?: RequestInit) => {
      if (path === "/admin/policy") {
        if (init?.method === "PUT") {
          saved = JSON.parse(String(init.body));
          return Response.json({ ...saved, version: 2 });
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
  expect(saved.scenes).toEqual(policy.scenes);
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
