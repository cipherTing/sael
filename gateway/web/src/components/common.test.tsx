import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { Choice, CopyButton } from "./common";

vi.mock("sonner", async () => {
  const actual = await vi.importActual<typeof import("sonner")>("sonner");
  return {
    ...actual,
    toast: { ...actual.toast, error: vi.fn(), success: vi.fn() },
  };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
});

it("keeps a visible field name when the selected time value changes", () => {
  const props = {
    label: "统计粒度",
    visibleLabel: "时间粒度",
    onChange: vi.fn(),
    options: [
      { value: "1h", label: "1 小时" },
      { value: "1d", label: "1 天" },
    ],
  };
  const { rerender } = render(<Choice {...props} value="1h" />);
  expect(screen.getByText("时间粒度")).toBeTruthy();
  expect(
    screen.getByRole("combobox", { name: "统计粒度" }).textContent,
  ).toContain("1 小时");
  rerender(<Choice {...props} value="1d" />);
  expect(screen.getByText("时间粒度")).toBeTruthy();
  expect(
    screen.getByRole("combobox", { name: "统计粒度" }).textContent,
  ).toContain("1 天");
});

it("shows a toast when the browser rejects clipboard access", async () => {
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText: vi.fn().mockRejectedValue(new Error("denied")) },
  });
  render(<CopyButton value="secret" label="复制密钥" />);

  fireEvent.click(screen.getByRole("button", { name: "复制密钥" }));

  await waitFor(() => expect(toast.error).toHaveBeenCalledWith("复制失败"));
});

it("reports success only after clipboard writing completes and keeps the parent action inactive", async () => {
  let finish!: () => void;
  const writeText = vi.fn(
    () =>
      new Promise<void>((resolve) => {
        finish = resolve;
      }),
  );
  Object.defineProperty(navigator, "clipboard", {
    configurable: true,
    value: { writeText },
  });
  const parentClick = vi.fn();
  render(
    <div onClick={parentClick}>
      <CopyButton value="sk-test******tail" label="复制调用密钥" />
    </div>,
  );
  fireEvent.click(screen.getByRole("button", { name: "复制调用密钥" }));
  expect(writeText).toHaveBeenCalledWith("sk-test******tail");
  expect(parentClick).not.toHaveBeenCalled();
  expect(toast.success).not.toHaveBeenCalled();
  finish();
  await waitFor(() => expect(toast.success).toHaveBeenCalledWith("已复制"));
});
