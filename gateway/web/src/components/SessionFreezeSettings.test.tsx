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
it("saves freeze duration without dropping existing scenes or changing review", async () => {
  const policy: Policy = {
    enabled: false,
    version: 7,
    scenes: [],
    preview_chars: 0,
    retention_days: 30,
  };
  const onSave = vi.fn().mockResolvedValue(undefined);
  render(<SessionFreezeSettings policy={policy} onSave={onSave} />);
  fireEvent.click(screen.getByRole("switch", { name: "启用会话冻结" }));
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "冻结时长（分钟）" }),
    { target: { value: "120" } },
  );
  fireEvent.click(screen.getByRole("button", { name: "保存会话策略" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({
      ...policy,
      session_block_enabled: true,
      session_block_ttl_seconds: 7200,
    }),
  );
});
