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
import SettingsConsole from "./SettingsConsole";
import type { PolicyResponse } from "../policy";
import { request } from "../api";

vi.mock("../api", () => ({ request: vi.fn() }));

afterEach(cleanup);

const policy: PolicyResponse = {
  enabled: false,
  scenes: [],
  questions: [],
  preview_chars: null,
  retention_days: 30,
};

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
