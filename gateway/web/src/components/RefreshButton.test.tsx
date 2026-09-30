import {
  fireEvent,
  render,
  screen,
  waitFor,
  cleanup,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { RefreshButton } from "./RefreshButton";
afterEach(cleanup);
it("allows one refresh until completion and restores the action", async () => {
  let resolve!: () => void;
  let calls = 0;
  render(
    <RefreshButton
      label="刷新记录"
      onRefresh={() => {
        calls++;
        return new Promise<void>((done) => {
          resolve = done;
        });
      }}
    />,
  );
  const button = screen.getByRole("button", {
    name: "刷新记录",
  }) as HTMLButtonElement;
  expect(button.textContent).toContain("刷新记录");
  fireEvent.click(button);
  fireEvent.click(button);
  expect(button.disabled).toBe(true);
  expect(calls).toBe(1);
  expect(button.querySelector("svg")?.classList.contains("animate-spin")).toBe(
    true,
  );
  resolve();
  await waitFor(() => expect(button.disabled).toBe(false));
});
