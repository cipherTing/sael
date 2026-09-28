import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, it, expect, vi } from "vitest";
import { SessionFreezeSettings } from "./SessionFreezeSettings";
import type { Policy } from "../policy";
afterEach(cleanup);
it("saves separate blocking and nonblocking freeze switches", async () => {
  const policy: Policy = {
    enabled: false,
    version: 7,
    scenes: [],
    preview_chars: 0,
    retention_days: 30,
    session_block_on_blocking_review: false,
    session_block_on_nonblocking_review: false,
  };
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<SessionFreezeSettings policy={policy} onSave={onSave} />);
  fireEvent.click(
    screen.getByRole("switch", { name: "阻塞审查命中后冻结会话" }),
  );
  fireEvent.click(
    screen.getByRole("switch", { name: "非阻塞审查命中后冻结会话" }),
  );
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "冻结时长（分钟）" }),
    { target: { value: "120" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "保存会话冻结" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({
      ...policy,
      session_block_on_blocking_review: true,
      session_block_on_nonblocking_review: true,
      session_block_ttl_seconds: 7200,
    }),
  );
});
