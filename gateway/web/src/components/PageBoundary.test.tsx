import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { PageBoundary } from "./PageBoundary";
afterEach(() => {
  cleanup();
  vi.restoreAllMocks();
});
it("keeps a recovery action visible when a page chunk cannot be loaded", () => {
  vi.spyOn(console, "error").mockImplementation(() => {});
  const errorToast = vi.spyOn(toast, "error").mockImplementation(() => "toast");
  function BrokenPage(): never {
    throw new TypeError("Failed to fetch dynamically imported module");
  }
  render(
    <PageBoundary>
      <BrokenPage />
    </PageBoundary>,
  );
  expect(screen.queryByRole("alert")).toBeNull();
  expect(errorToast).toHaveBeenCalledWith("页面加载失败", {
    id: "page-load-failed",
  });
  expect(screen.getByRole("button", { name: "重新加载" })).toBeTruthy();
});
