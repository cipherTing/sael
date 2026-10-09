import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, expect, it, vi } from "vitest";
import { request } from "../api";
import GatewayVersion from "./GatewayVersion";

vi.mock("../api", () => ({ request: vi.fn() }));
afterEach(() => {
  cleanup();
  vi.resetAllMocks();
});

function mount() {
  render(
    <QueryClientProvider
      client={
        new QueryClient({ defaultOptions: { queries: { retry: false } } })
      }
    >
      <GatewayVersion />
    </QueryClientProvider>,
  );
}

it("shows the running gateway version and the newer gateway release", async () => {
  vi.mocked(request).mockResolvedValue({
    current_version: "0.1.0",
    revision: "abc123",
    latest_version: "0.2.0",
    update_available: true,
    release_url:
      "https://github.com/cipherTing/sael/releases/tag/gateway/v0.2.0",
  });
  mount();
  expect(await screen.findByText("网关 v0.1.0")).toBeTruthy();
  expect(
    screen.getByRole("link", { name: "新版 v0.2.0" }).getAttribute("href"),
  ).toContain("gateway/v0.2.0");
  fireEvent.click(screen.getByText("更新方式"));
  expect(screen.getByText("更新方式").closest("details")?.open).toBe(true);
  expect(screen.getByText("docker compose pull gateway")).toBeTruthy();
});

it("refreshes manually and does not present failed checks as up to date", async () => {
  vi.mocked(request)
    .mockResolvedValueOnce({
      current_version: "0.1.0",
      check_error: "检查更新失败，请稍后重试",
    })
    .mockResolvedValueOnce({
      current_version: "0.1.0",
      latest_version: "0.1.0",
      update_available: false,
    });
  mount();
  expect(await screen.findByText("检查更新失败，请稍后重试")).toBeTruthy();
  expect(screen.queryByText("已是最新版")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "检查更新" }));
  expect(await screen.findByText("已是最新版")).toBeTruthy();
  expect(vi.mocked(request).mock.calls.at(-1)).toEqual([
    "/admin/version",
    { method: "POST" },
  ]);
});

it("labels an unversioned build without a release verdict", async () => {
  vi.mocked(request).mockResolvedValue({ current_version: "devel" });
  mount();
  expect(await screen.findByText("网关开发构建")).toBeTruthy();
  expect(screen.queryByText("已是最新版")).toBeNull();
});

it("ends the loading state when the version endpoint fails", async () => {
  vi.mocked(request).mockRejectedValue(new Error("service unavailable"));
  mount();
  expect(await screen.findByText("版本信息加载失败")).toBeTruthy();
  expect(screen.queryByText("网关版本加载中")).toBeNull();
});
