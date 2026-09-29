import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import { CopyButton } from "./common";

vi.mock("sonner", async () => {
  const actual = await vi.importActual<typeof import("sonner")>("sonner");
  return { ...actual, toast: { ...actual.toast, error: vi.fn() } };
});

afterEach(() => {
  cleanup();
  vi.clearAllMocks();
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
