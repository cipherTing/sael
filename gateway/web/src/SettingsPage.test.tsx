import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { toast } from "sonner";
import SettingsPage from "./SettingsPage";
import type { PolicyResponse } from "./policy";
const policy: PolicyResponse = {
  enabled: true,
  preview_chars: null,
  retention_days: 30,
  questions: [
    { key: "gore", type: "score", max: 3 },
    { key: "self_harm", type: "noul", max: 1 },
  ],
  scenes: [
    {
      id: "one",
      name: "高风险组合",
      note: "需要人工关注",
      match: "all",
      action: "block",
      conditions: [
        { question: "gore", threshold: 1.5 },
        { question: "self_harm", threshold: 0.8 },
      ],
    },
  ],
};
afterEach(() => {
  cleanup();
  sessionStorage.clear();
  vi.restoreAllMocks();
});

it("shows the bottom saving actions only while the scene draft has changes", () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />);
  expect(screen.queryByText("场景已生效")).toBeNull();
  expect(screen.queryByRole("button", { name: "保存并生效" })).toBeNull();
  fireEvent.change(screen.getByRole("textbox", { name: "场景名称" }), {
    target: { value: "临时修改" },
  });
  expect(screen.getByRole("button", { name: "保存并生效" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
  expect(screen.queryByText("场景已生效")).toBeNull();
  expect(screen.queryByRole("button", { name: "保存并生效" })).toBeNull();
});
it("saves original-scale thresholds independently inside a scene", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "1.8" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() =>
    expect(save).toHaveBeenCalledWith(
      expect.objectContaining({
        scenes: [
          expect.objectContaining({
            conditions: [
              { question: "gore", threshold: 1.8 },
              { question: "self_harm", threshold: 0.8 },
            ],
          }),
        ],
      }),
    ),
  );
});
it("focuses the invalid scene instead of submitting an empty policy", () => {
  const save = vi.fn();
  const errorToast = vi.spyOn(toast, "error").mockImplementation(() => "toast");
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.click(screen.getByRole("button", { name: "新建场景" }));
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  expect(errorToast.mock.calls[0][0]).toBe("请填写场景名称");
  expect(save).not.toHaveBeenCalled();
});
it("retains edits when saving fails", async () => {
  const save = vi.fn().mockRejectedValue(new Error("连接中断"));
  const errorToast = vi.spyOn(toast, "error").mockImplementation(() => "toast");
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "2.2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(errorToast.mock.calls[0][0]).toBe("连接中断"));
  expect(screen.queryByRole("alert")).toBeNull();
  expect(
    (
      screen.getByRole("spinbutton", {
        name: "血腥程度阈值",
      }) as HTMLInputElement
    ).value,
  ).toBe("2.2");
});
it("keeps an unsaved scene edit when another setting changes", () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const { rerender } = render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "2.2" },
  });
  rerender(
    <SettingsPage policy={{ ...policy, enabled: false }} onSave={save} />,
  );
  expect(
    (
      screen.getByRole("spinbutton", {
        name: "血腥程度阈值",
      }) as HTMLInputElement
    ).value,
  ).toBe("2.2");
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  expect(save.mock.calls[0][0].scenes[0].conditions[0].threshold).toBe(2.2);
});
it("does not show a scene save for a change made only in global settings", () => {
  const { rerender } = render(
    <SettingsPage policy={policy} onSave={vi.fn()} />,
  );
  rerender(
    <SettingsPage policy={{ ...policy, enabled: false }} onSave={vi.fn()} />,
  );
  expect(screen.queryByRole("button", { name: "保存并生效" })).toBeNull();
});
it("restores an unpublished draft when returning to the workspace", () => {
  sessionStorage.setItem(
    "draft",
    JSON.stringify({
      ...policy,
      scenes: [{ ...policy.scenes[0], name: "未保存的场景" }],
    }),
  );
  render(<SettingsPage policy={policy} onSave={vi.fn()} storageKey="draft" />);
  expect((screen.getByLabelText("场景名称") as HTMLInputElement).value).toBe(
    "未保存的场景",
  );
});
it("edits a copied condition without modifying the original scene", async () => {
  const success = vi.spyOn(toast, "success").mockImplementation(() => "toast");
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.click(screen.getByRole("button", { name: "复制场景" }));
  expect(success).toHaveBeenCalledWith("场景已复制");
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "2" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0].conditions[0].threshold).toBe(1.5);
  expect(save.mock.calls[0][0].scenes[1].conditions[0].threshold).toBe(2);
});

it("puts draft testing in the second scene tab without a header shortcut", () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />);
  expect(screen.queryByRole("button", { name: "试算" })).toBeNull();
  expect(screen.queryByRole("textbox", { name: "测试文本" })).toBeNull();
  fireEvent.mouseDown(screen.getByRole("tab", { name: "试算" }), { button: 0 });
  expect(screen.getByRole("textbox", { name: "测试文本" })).toBeTruthy();
  expect(screen.queryByRole("button", { name: "草稿试算" })).toBeNull();
});

it("defaults a new scene's session freeze to off", () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />);
  fireEvent.click(screen.getByRole("button", { name: "新建场景" }));
  expect(
    screen
      .getByRole("switch", { name: "命中后冻结会话" })
      .getAttribute("aria-checked"),
  ).toBe("false");
});

it("saves freeze settings on the selected scene", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  const freeze = screen.getByRole("switch", { name: "命中后冻结会话" });
  expect(freeze.getAttribute("aria-checked")).toBe("false");
  fireEvent.click(screen.getByRole("switch", { name: "命中后冻结会话" }));
  fireEvent.change(
    screen.getByRole("spinbutton", { name: "冻结时长（分钟）" }),
    {
      target: { value: "120" },
    },
  );
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0]).toMatchObject({
    session_block_enabled: true,
    session_block_ttl_seconds: 7200,
  });
});
