import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { PageBoundary } from "./PageBoundary";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("keeps a recovery action visible when a page chunk cannot be loaded", () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  function BrokenPage(): never {
    throw new TypeError("Failed to fetch dynamically imported module");
  }
  render(
    <PageBoundary>
      <BrokenPage />
    </PageBoundary>,
  );
  expect(screen.getByRole("alert").textContent).toContain("页面加载失败");
  expect(screen.getByRole("button", { name: "重新加载" })).toBeTruthy();
});
