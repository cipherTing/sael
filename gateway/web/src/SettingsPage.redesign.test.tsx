import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import SettingsPage from "./SettingsPage";
import type { PolicyResponse } from "./policy";

afterEach(cleanup);
const policy: PolicyResponse = {
  enabled: false,
  version: 1,
  preview_chars: null,
  retention_days: null,
  questions: [
    { key: "gore", type: "score", max: 3 },
    { key: "self_harm", type: "noul", max: 1 },
  ],
  scenes: [
    {
      id: "one",
      name: "血腥审查",
      conditions: [{ question: "gore", threshold: 1.5 }],
      match: "all",
      action: "block",
    },
  ],
};

it("duplicates a scene as a draft and only publishes when saved", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.click(screen.getByRole("button", { name: "复制场景" }));
  expect(save).not.toHaveBeenCalled();
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  const saved = save.mock.calls[0][0];
  expect(saved.scenes).toHaveLength(2);
  expect(saved.scenes[1].conditions).toEqual([
    { question: "gore", threshold: 1.5 },
  ]);
  expect(saved.scenes[1].id).not.toBe("one");
});

it("saves endpoint selection and a paused scene without changing its threshold", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.click(screen.getByRole("button", { name: "适用于 Responses" }));
  fireEvent.click(screen.getByRole("switch", { name: "启用此场景" }));
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0]).toMatchObject({
    enabled: false,
    endpoints: ["openai_responses"],
    conditions: [{ question: "gore", threshold: 1.5 }],
  });
});

it("copies a selected template into the new editor, keeps its identity and isolates edits", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  const source = {
    ...policy,
    scenes: [
      {
        ...policy.scenes[0],
        note: "用于 Responses 的高风险",
        models: ["gpt-6-luna"],
        endpoints: ["openai_responses"],
      },
    ],
  };
  render(<SettingsPage policy={source} onSave={save} />);
  fireEvent.click(screen.getByRole("button", { name: "新建场景" }));
  fireEvent.click(screen.getByRole("button", { name: "从场景模板创建" }));
  expect(screen.getAllByText("用于 Responses 的高风险").length).toBeGreaterThan(
    0,
  );
  fireEvent.click(screen.getByRole("button", { name: /使用模板 血腥审查/ }));
  expect(
    (screen.getByRole("textbox", { name: "场景注释" }) as HTMLInputElement)
      .value,
  ).toBe("用于 Responses 的高风险");
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "2.1" },
  });
  fireEvent.change(screen.getByRole("textbox", { name: "场景名称" }), {
    target: { value: "按模板新建" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  const saved = save.mock.calls[0][0];
  expect(saved.scenes).toHaveLength(2);
  expect(saved.scenes[0].conditions[0].threshold).toBe(1.5);
  expect(saved.scenes[1]).toMatchObject({
    models: ["gpt-6-luna"],
    endpoints: ["openai_responses"],
    conditions: [{ question: "gore", threshold: 2.1 }],
    note: "用于 Responses 的高风险",
  });
  expect(saved.scenes[1].id).not.toBe("one");
});

it("adds multiple model IDs and restores all-model scope by removing the list", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  const input = screen.getByLabelText("添加生效模型");
  fireEvent.change(input, {
    target: { value: "gpt-6-luna, claude-test, gpt-6-luna" },
  });
  fireEvent.keyDown(input, { key: "Enter" });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0].models).toEqual([
    "gpt-6-luna",
    "claude-test",
  ]);
  fireEvent.click(screen.getByRole("button", { name: "移除模型 gpt-6-luna" }));
  fireEvent.click(screen.getByRole("button", { name: "移除模型 claude-test" }));
  expect(screen.getByText("全部模型")).toBeTruthy();
});

it("a template replaces optional scopes and enabled state instead of leaving old editor values", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(
    <SettingsPage
      policy={{
        ...policy,
        scenes: [
          {
            ...policy.scenes[0],
            enabled: false,
            models: ["old-model"],
            endpoints: ["anthropic"],
            note: "old note",
          },
          { ...policy.scenes[0], id: "template", name: "无范围模板" },
        ],
      }}
      onSave={save}
    />,
  );
  fireEvent.click(screen.getByRole("button", { name: "从场景模板创建" }));
  fireEvent.click(screen.getByRole("button", { name: "使用模板 无范围模板" }));
  expect(
    screen
      .getByRole("switch", { name: "启用此场景" })
      .getAttribute("aria-checked"),
  ).toBe("true");
  expect(
    screen.queryByRole("button", { name: "移除模型 old-model" }),
  ).toBeNull();
  expect((screen.getByLabelText("场景注释") as HTMLInputElement).value).toBe(
    "",
  );
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0].id).toBe("one");
  expect(save.mock.calls[0][0].scenes).toHaveLength(2);
});
