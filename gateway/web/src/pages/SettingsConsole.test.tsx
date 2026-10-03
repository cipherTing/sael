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
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import SettingsConsole from "./SettingsConsole";
import type { PolicyResponse } from "../policy";
import { request } from "../api";

vi.mock("../api", () => ({ request: vi.fn() }));

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});
const cacheState = {
  ttl_days: 7,
  max_bytes: 1024 * 1024 * 1024,
  available: true,
  used_bytes: 0,
  effective_max_bytes: 1024 * 1024 * 1024,
  entries: 0,
  lookups: 0,
  hits: 0,
};
beforeEach(() => {
  vi.mocked(request).mockImplementation(async (path) => {
    if (path === "/admin/access")
      return { ingress_url: "http://localhost:8091" };
    if (path === "/admin/upstream")
      return { base_url: "https://relay.example", updated_at: "" };
    if (path === "/admin/review-cache") return cacheState;
    if (path.startsWith("/admin/trusted-keys")) return { items: [], total: 0 };
    throw new Error(`unexpected request: ${path}`);
  });
});

const policy: PolicyResponse = {
  enabled: false,
  scenes: [],
  questions: [],
  preview_chars: null,
  retention_days: 30,
};

function mount(
  entry = "/settings",
  onSave = vi.fn().mockResolvedValue(undefined),
) {
  return render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={[entry]}>
        <SettingsConsole
          policy={policy}
          onSave={onSave}
          jev={<div>分类器工作台</div>}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

it("groups gateway controls and relay addresses in one tab and keeps retention with cache", async () => {
  mount();
  expect(screen.getAllByRole("tab").map((tab) => tab.textContent)).toEqual([
    "网关",
    "Jev 分类器",
    "可信密钥",
    "数据与缓存",
  ]);
  const gateway = screen.getByRole("tabpanel", { name: "网关" });
  expect(
    within(gateway).getByRole("switch", { name: "全局审查" }),
  ).toBeTruthy();
  expect(
    await within(gateway).findByText("http://localhost:8091"),
  ).toBeTruthy();
  expect(
    within(gateway).getByRole("textbox", { name: "出站地址" }),
  ).toBeTruthy();
  expect(
    within(gateway).getByRole("textbox", { name: "拦截提示" }),
  ).toBeTruthy();
  expect(
    within(gateway).queryByRole("spinbutton", { name: "记录保留天数" }),
  ).toBeNull();
  expect(
    vi
      .mocked(request)
      .mock.calls.some(
        ([path]) =>
          path.startsWith("/admin/trusted-keys") ||
          path === "/admin/review-cache",
      ),
  ).toBe(false);
  fireEvent.mouseDown(screen.getByRole("tab", { name: "数据与缓存" }), {
    button: 0,
  });
  const data = screen.getByRole("tabpanel", { name: "数据与缓存" });
  expect(
    within(data).getByRole("spinbutton", { name: "记录保留天数" }),
  ).toBeTruthy();
  expect(
    await within(data).findByRole("meter", { name: "缓存容量使用" }),
  ).toBeTruthy();
});

it.each(["review", "access"])(
  "opens the gateway controls from the former %s URL",
  async (tab) => {
    mount(`/settings?tab=${tab}`);
    expect(
      screen.getByRole("tab", { name: "网关" }).getAttribute("aria-selected"),
    ).toBe("true");
    expect(await screen.findByText("http://localhost:8091")).toBeTruthy();
  },
);

it("keeps global switching immediate and saves retention independently of an edited cache", async () => {
  const onSave = vi.fn().mockResolvedValue(undefined);
  mount("/settings", onSave);
  fireEvent.click(screen.getByRole("switch", { name: "全局审查" }));
  await waitFor(() => expect(onSave).toHaveBeenCalledWith({ enabled: true }));
  fireEvent.mouseDown(screen.getByRole("tab", { name: "数据与缓存" }), {
    button: 0,
  });
  await screen.findByRole("meter", { name: "缓存容量使用" });
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "缓存有效期（天）" }),
    { target: { value: "14" } },
  );
  fireEvent.change(screen.getByRole("spinbutton", { name: "记录保留天数" }), {
    target: { value: "60" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存记录保留" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({ retention_days: 60 }),
  );
  expect(
    vi.mocked(request).mock.calls.some(([, init]) => init?.method === "PUT"),
  ).toBe(false);
  expect(
    (
      screen.getByRole("spinbutton", {
        name: "缓存有效期（天）",
      }) as HTMLInputElement
    ).value,
  ).toBe("14");
});

it("keeps gateway drafts while visiting the other settings tabs", async () => {
  mount();
  await screen.findByText("http://localhost:8091");
  fireEvent.change(screen.getByRole("textbox", { name: "出站地址" }), {
    target: { value: "https://edited.example" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "拦截提示" }), {
    target: { value: "Denied {model}" },
  });
  fireEvent.mouseDown(screen.getByRole("tab", { name: "可信密钥" }), {
    button: 0,
  });
  await screen.findByText("暂无数据");
  fireEvent.mouseDown(screen.getByRole("tab", { name: "网关" }), { button: 0 });
  expect(
    (screen.getByRole("textbox", { name: "出站地址" }) as HTMLInputElement)
      .value,
  ).toBe("https://edited.example");
  expect(
    (screen.getByRole("textbox", { name: "拦截提示" }) as HTMLTextAreaElement)
      .value,
  ).toBe("Denied {model}");
});

it("places idle expiry in the key list and preserves its draft when saving fails", async () => {
  const errorToast = vi.spyOn(toast, "error").mockReturnValue("toast");
  const onSave = vi.fn().mockRejectedValue(new Error("保存失败"));
  mount("/settings?tab=keys", onSave);
  await screen.findByText("暂无数据");
  expect(screen.getAllByRole("heading", { name: /可信密钥/ })).toHaveLength(1);
  const days = screen.getByRole("spinbutton", {
    name: "闲置清除天数",
  }) as HTMLInputElement;
  fireEvent.change(days, { target: { value: "7" } });
  fireEvent.click(screen.getByRole("button", { name: "保存可信密钥设置" }));
  await waitFor(() => expect(errorToast).toHaveBeenCalled());
  expect(onSave).toHaveBeenCalledWith({ trusted_key_idle_days: 7 });
  expect(days.value).toBe("7");
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "保存可信密钥设置" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );
  errorToast.mockRestore();
});

it("copies a masked key from the trusted-key list", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  vi.mocked(request).mockResolvedValue({
    items: [
      {
        id: "key-one",
        masked_key: "sk-prefix******suffix",
        upstream: "https://example.test",
        first_seen_at: "2026-09-30T00:00:00Z",
        last_seen_at: "2026-09-30T01:00:00Z",
      },
    ],
    total: 1,
  });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/settings?tab=keys"]}>
        <SettingsConsole policy={policy} onSave={vi.fn()} jev={null} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  fireEvent.click(await screen.findByRole("button", { name: "复制调用密钥" }));
  await waitFor(() =>
    expect(writeText).toHaveBeenCalledWith("sk-prefix******suffix"),
  );
});

it("previews the rendered block message and saves only that setting", async () => {
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SettingsConsole policy={policy} onSave={onSave} jev={null} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const editor = screen.getByRole("textbox", { name: "拦截提示" });
  expect((editor as HTMLTextAreaElement).value).toBe("Request denied.");
  expect(screen.getByText("{request_id}")).toBeTruthy();
  fireEvent.change(editor, {
    target: { value: "Blocked {request_id} for {model}" },
  });
  expect(screen.getByText(/Blocked req-preview for gpt-4\.1/)).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "保存拦截提示" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({
      block_message: "Blocked {request_id} for {model}",
    }),
  );
});

it("inserts a supported variable and rejects unknown variables", () => {
  const onSave = vi.fn();
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SettingsConsole policy={policy} onSave={onSave} jev={null} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const editor = screen.getByRole("textbox", {
    name: "拦截提示",
  }) as HTMLTextAreaElement;
  fireEvent.change(editor, { target: { value: "Blocked " } });
  fireEvent.click(screen.getByRole("button", { name: "插入请求 ID" }));
  expect(editor.value).toBe("Blocked {request_id}");
  fireEvent.change(editor, { target: { value: "Blocked {scene}" } });
  expect(
    screen
      .getByRole("button", { name: "保存拦截提示" })
      .hasAttribute("disabled"),
  ).toBe(true);
  expect(onSave).not.toHaveBeenCalled();
});

it("shows saved message whitespace exactly as clients receive it", () => {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter>
        <SettingsConsole
          policy={{ ...policy, block_message: " Access denied.\n" }}
          onSave={vi.fn()}
          jev={null}
        />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  expect(
    (screen.getByRole("textbox", { name: "拦截提示" }) as HTMLTextAreaElement)
      .value,
  ).toBe(" Access denied.\n");
});

it("shows actual cache capacity as used out of total with a labelled usage meter", async () => {
  vi.mocked(request).mockResolvedValue({
    ttl_days: 7,
    max_bytes: 1024 * 1024 * 1024,
    available: true,
    used_bytes: 256 * 1024 * 1024,
    effective_max_bytes: 1024 * 1024 * 1024,
    entries: 1200,
    lookups: 800,
    hits: 200,
  });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/settings?tab=cache"]}>
        <SettingsConsole policy={policy} onSave={vi.fn()} jev={null} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  const meter = await screen.findByRole("meter", { name: "缓存容量使用" });
  expect(meter.getAttribute("aria-valuenow")).toBe("25");
  expect(screen.getByText("256.0 / 1,024 MiB")).toBeTruthy();
  const metrics = screen.getByRole("region", { name: "缓存复用统计" });
  expect(within(metrics).getByText("200")).toBeTruthy();
  expect(within(metrics).getByText("25.0%")).toBeTruthy();
});

it("retains cache draft after a save failure and supports a successful retry", async () => {
  const current = {
    ttl_days: 7,
    max_bytes: 1024 * 1024 * 1024,
    available: true,
    used_bytes: 0,
    effective_max_bytes: 1024 * 1024 * 1024,
    entries: 0,
    lookups: 0,
    hits: 0,
  };
  vi.mocked(request).mockImplementation(async (_path, init) => {
    if (init?.method === "PUT") throw new Error("save failed");
    return current;
  });
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <MemoryRouter initialEntries={["/settings?tab=cache"]}>
        <SettingsConsole policy={policy} onSave={vi.fn()} jev={null} />
      </MemoryRouter>
    </QueryClientProvider>,
  );
  await screen.findByText("0.0 / 1,024 MiB");
  const ttl = screen.getByRole("spinbutton", {
    name: "缓存有效期（天）",
  }) as HTMLInputElement;
  fireEvent.change(ttl, { target: { value: "14" } });
  fireEvent.click(screen.getByRole("button", { name: "保存缓存配置" }));
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "保存缓存配置" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );
  expect(ttl.value).toBe("14");
  vi.mocked(request).mockImplementation(async (_path, init) =>
    init?.method === "PUT" ? {} : { ...current, ttl_days: 14 },
  );
  fireEvent.click(screen.getByRole("button", { name: "保存缓存配置" }));
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "保存缓存配置" })
        .hasAttribute("disabled"),
    ).toBe(true),
  );
  expect(ttl.value).toBe("14");
});
