import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import SettingsPage from "./SettingsPage";
import type { PolicyResponse } from "./policy";

const policy: PolicyResponse = {
  enabled: true,
  preview_chars: 500,
  retention_days: 30,
  questions: [
    { key: "gore", type: "score", max: 3 },
    { key: "self_harm", type: "noul", max: 1 },
  ],
  scenes: [
    {
      id: "one",
      name: "高风险组合",
      match: "any",
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

it("keeps the graded slider and precise decimal threshold synchronized", () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />);
  const slider = screen.getByRole("slider", { name: "血腥程度阈值滑杆" });
  expect(slider.getAttribute("aria-valuenow")).toBe("1.5");
  expect(slider.getAttribute("aria-valuemax")).toBe("3");
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "1.75" },
  });
  expect(slider.getAttribute("aria-valuenow")).toBe("1.75");
  fireEvent.keyDown(slider, { key: "ArrowRight" });
  expect(
    (
      screen.getByRole("spinbutton", {
        name: "血腥程度阈值",
      }) as HTMLInputElement
    ).value,
  ).toBe("1.76");
});

it("keeps 0–1 risk values on their own scale without rounding decimal input", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  const slider = screen.getByRole("slider", { name: "自伤风险阈值滑杆" });
  expect(slider.getAttribute("aria-valuemax")).toBe("1");
  fireEvent.change(screen.getByRole("spinbutton", { name: "自伤风险阈值" }), {
    target: { value: "0.333" },
  });
  expect(slider.getAttribute("aria-valuenow")).toBe("0.333");
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0].scenes[0].conditions[1].threshold).toBe(0.333);
});

it("marks recorded sample scores against the same threshold scale", () => {
  render(
    <SettingsPage
      policy={policy}
      onSave={vi.fn()}
      sample={{
        text: "示例",
        endpoint: "openai_chat",
        scores: [{ question: "gore", type: "score", value: 2 }],
      }}
    />,
  );
  fireEvent.mouseDown(screen.getByRole("tab", { name: "场景配置" }), {
    button: 0,
  });
  expect(screen.getByLabelText("血腥程度样本分数 2，已满足条件")).toBeTruthy();
});

it("keeps save and discard actions available while viewing the trial tab", async () => {
  const save = vi.fn().mockResolvedValue(undefined);
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.change(screen.getByRole("textbox", { name: "场景名称" }), {
    target: { value: "新的名称" },
  });
  fireEvent.mouseDown(screen.getByRole("tab", { name: "试算" }), { button: 0 });
  expect(screen.getByRole("button", { name: "放弃修改" })).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  await waitFor(() => expect(save).toHaveBeenCalledOnce());
  expect(save.mock.calls[0][0]).toEqual({
    scenes: [{ ...policy.scenes[0], name: "新的名称", review_mode: "blocking" }],
  });
});

it("can discard a scene draft from the analysis tab", () => {
  render(<QueryClientProvider client={new QueryClient({ defaultOptions: { queries: { enabled: false } } })}><SettingsPage policy={policy} onSave={vi.fn()} /></QueryClientProvider>);
  fireEvent.change(screen.getByRole("textbox", { name: "场景名称" }), {
    target: { value: "未保存" },
  });
  fireEvent.mouseDown(screen.getByRole("tab", { name: "场景分析" }), {
    button: 0,
  });
  fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
  fireEvent.mouseDown(screen.getByRole("tab", { name: "场景配置" }), {
    button: 0,
  });
  expect(
    (screen.getByRole("textbox", { name: "场景名称" }) as HTMLInputElement)
      .value,
  ).toBe("高风险组合");
  expect(screen.queryByRole("button", { name: "保存并生效" })).toBeNull();
});

it("does not pass an empty numeric threshold into the slider", () => {
  const save = vi.fn();
  render(<SettingsPage policy={policy} onSave={save} />);
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), {
    target: { value: "" },
  });
  expect(
    screen
      .getByRole("slider", { name: "血腥程度阈值滑杆" })
      .getAttribute("aria-valuenow"),
  ).toBe("0");
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  expect(save).not.toHaveBeenCalled();
});

it("does not create a slider for an unknown question", () => {
  const unknown = {
    ...policy,
    scenes: [
      {
        ...policy.scenes[0],
        conditions: [{ question: "unknown", threshold: 2 }],
      },
    ],
  };
  const save = vi.fn();
  render(<SettingsPage policy={unknown} onSave={save} />);
  expect(screen.getByText("审核项不可用")).toBeTruthy();
  expect(screen.queryByRole("slider")).toBeNull();
  fireEvent.change(screen.getByRole("textbox", { name: "场景名称" }), {
    target: { value: "待修复" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存并生效" }));
  expect(save).not.toHaveBeenCalled();
});

it("keeps the trial input when returning from the condition editor", () => {
  render(<SettingsPage policy={policy} onSave={vi.fn()} />);
  fireEvent.mouseDown(screen.getByRole("tab", { name: "试算" }), { button: 0 });
  fireEvent.change(screen.getByRole("textbox", { name: "测试文本" }), { target: { value: "需要反复验证的输入" } });
  fireEvent.mouseDown(screen.getByRole("tab", { name: "场景配置" }), { button: 0 });
  fireEvent.change(screen.getByRole("spinbutton", { name: "血腥程度阈值" }), { target: { value: "2.1" } });
  fireEvent.mouseDown(screen.getByRole("tab", { name: "试算" }), { button: 0 });
  expect((screen.getByRole("textbox", { name: "测试文本" }) as HTMLTextAreaElement).value).toBe("需要反复验证的输入");
});
