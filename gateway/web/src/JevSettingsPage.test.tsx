import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
  within,
} from "@testing-library/react";
import { afterEach, expect, it, vi } from "vitest";
import JevSettingsPage from "./JevSettingsPage";
const config = {
  base_url: "https://api.example/v1",
  model: "jev-test",
  api_key_set: true,
  timeout_ms: 5000,
  updated_at: "",
};
afterEach(cleanup);
it("defaults to 5000 characters and prevents disabling the guard with zero", () => {
  render(<JevSettingsPage config={config} onSave={vi.fn()} onTest={vi.fn()} />);
  const limit = screen.getByRole("spinbutton", {
    name: "送审上限（字符）",
  }) as HTMLInputElement;
  expect(limit.value).toBe("5000");
  fireEvent.change(limit, { target: { value: "0" } });
  expect(
    screen
      .getByRole("button", { name: "保存 Jev 配置" })
      .hasAttribute("disabled"),
  ).toBe(true);
});

it("saves the edited character limit", async () => {
  const onSave = vi
    .fn()
    .mockResolvedValue({ ...config, max_input_chars: 5000 });
  render(
    <JevSettingsPage
      config={{ ...config, max_input_chars: 5000 }}
      onSave={onSave}
      onTest={vi.fn()}
    />,
  );
  expect(
    (screen.getByLabelText("送审上限（字符）") as HTMLInputElement).value,
  ).toBe("5000");
  fireEvent.change(screen.getByLabelText("送审上限（字符）"), {
    target: { value: "6000" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 Jev 配置" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith(
      expect.objectContaining({ max_input_chars: 6000 }),
    ),
  );
});
it("requires a key for the first save and clears its input after saving", async () => {
  const onSave = vi.fn().mockResolvedValue(config);
  render(
    <JevSettingsPage
      config={{ ...config, base_url: "", model: "", api_key_set: false }}
      onSave={onSave}
      onTest={vi.fn()}
    />,
  );
  fireEvent.change(screen.getByLabelText("接口地址"), {
    target: { value: "https://api.example/v1" },
  });
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-test" },
  });
  expect(
    screen
      .getByRole("button", { name: "保存 Jev 配置" })
      .hasAttribute("disabled"),
  ).toBe(true);
  fireEvent.change(screen.getByLabelText("API Key", { selector: "input" }), {
    target: { value: "secret-key" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 Jev 配置" }));
  await waitFor(() =>
    expect(onSave).toHaveBeenCalledWith({
      base_url: "https://api.example/v1",
      model: "jev-test",
      api_key: "secret-key",
      timeout_ms: 5000,
      max_input_chars: 5000,
    }),
  );
  await waitFor(() =>
    expect(screen.queryByDisplayValue("secret-key")).toBeNull(),
  );
});
it("tests an edited connection without saving it to production", async () => {
  const onSave = vi.fn(),
    onTest = vi.fn().mockResolvedValue({
      scores: [],
      decision: null,
      policy_ready: false,
      classifier_ms: 20,
    });
  render(<JevSettingsPage config={config} onSave={onSave} onTest={onTest} />);
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-new" },
  });
  fireEvent.click(screen.getByRole("button", { name: "测试连接" }));
  await waitFor(() =>
    expect(onTest).toHaveBeenCalledWith(
      expect.any(String),
      expect.objectContaining({ model: "jev-new", api_key: "" }),
    ),
  );
  expect(onSave).not.toHaveBeenCalled();
  expect(await screen.findByText("连接正常")).toBeTruthy();
});
it("shows classifier scores from the current connection and clears the test", async () => {
  const onTest = vi.fn().mockResolvedValue({
    scores: [{ question: "gore", type: "score", value: 1.9 }],
    decision: null,
    policy_ready: false,
    classifier_ms: 20,
  });
  render(<JevSettingsPage config={config} onSave={vi.fn()} onTest={onTest} />);
  fireEvent.change(screen.getByLabelText("测试文本"), {
    target: { value: "sample" },
  });
  fireEvent.click(screen.getByRole("button", { name: "测试 Jev 分类器" }));
  expect(await screen.findByText("1.9")).toBeTruthy();
  expect(onTest).toHaveBeenCalledWith("sample", undefined);
  fireEvent.click(screen.getByRole("button", { name: "清空测试" }));
  expect(screen.queryByText("1.9")).toBeNull();
});

it("keeps the saved connection separate from a successful draft test", async () => {
  const onSave = vi.fn();
  const onTest = vi.fn().mockResolvedValue({
    scores: [],
    decision: null,
    policy_ready: false,
    classifier_ms: 20,
  });
  render(
    <JevSettingsPage
      config={config}
      runtime={{ classifier: "ok" }}
      onSave={onSave}
      onTest={onTest}
    />,
  );
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-draft" },
  });
  fireEvent.click(screen.getByRole("button", { name: "测试连接" }));
  expect(await screen.findByText("连接正常")).toBeTruthy();
  const active = screen.getByRole("region", { name: "正在使用" });
  expect(within(active).getByText(config.model)).toBeTruthy();
  expect(within(active).queryByText("jev-draft")).toBeNull();
  const testResult = screen.getByRole("region", { name: "测试结果" });
  expect(within(testResult).getByText("当前填写配置")).toBeTruthy();
  expect(within(testResult).getByText("jev-draft")).toBeTruthy();
  expect(screen.getByText("未保存")).toBeTruthy();
  expect(onSave).not.toHaveBeenCalled();
});

it("updates the active snapshot after saving and can discard later changes", async () => {
  const onSave = vi.fn().mockResolvedValue({ ...config, model: "jev-saved" });
  render(<JevSettingsPage config={config} onSave={onSave} onTest={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-saved" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 Jev 配置" }));
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用" })).getByText(
        "jev-saved",
      ),
    ).toBeTruthy(),
  );
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-another-draft" },
  });
  fireEvent.click(screen.getByRole("button", { name: "放弃修改" }));
  expect((screen.getByLabelText("模型 ID") as HTMLInputElement).value).toBe(
    "jev-saved",
  );
  expect(
    screen
      .getByRole("button", { name: "保存 Jev 配置" })
      .hasAttribute("disabled"),
  ).toBe(true);
});

it("invalidates an in-flight test when its text changes", async () => {
  let resolve!: (value: unknown) => void;
  const pending = new Promise((done) => {
    resolve = done;
  });
  render(
    <JevSettingsPage
      config={config}
      onSave={vi.fn()}
      onTest={() => pending as never}
    />,
  );
  fireEvent.change(screen.getByLabelText("测试文本"), {
    target: { value: "old text" },
  });
  fireEvent.click(screen.getByRole("button", { name: "测试 Jev 分类器" }));
  fireEvent.change(screen.getByLabelText("测试文本"), {
    target: { value: "new text" },
  });
  resolve({
    scores: [{ question: "gore", type: "score", value: 2.8 }],
    decision: null,
    policy_ready: false,
    classifier_ms: 20,
  });
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "测试 Jev 分类器" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );
  expect(screen.queryByText("2.8")).toBeNull();
});

it("clears a completed result on configuration edit and recovers from a failed test", async () => {
  const onTest = vi
    .fn()
    .mockResolvedValueOnce({
      scores: [{ question: "gore", type: "score", value: 1.7 }],
      decision: null,
      policy_ready: false,
      classifier_ms: 20,
    })
    .mockRejectedValueOnce(new Error("cannot connect"))
    .mockResolvedValueOnce({
      scores: [],
      decision: null,
      policy_ready: false,
      classifier_ms: 20,
    });
  render(<JevSettingsPage config={config} onSave={vi.fn()} onTest={onTest} />);
  fireEvent.change(screen.getByLabelText("测试文本"), {
    target: { value: "keep this input" },
  });
  fireEvent.click(screen.getByRole("button", { name: "测试 Jev 分类器" }));
  expect(await screen.findByText("1.7")).toBeTruthy();
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-draft" },
  });
  expect(screen.queryByText("1.7")).toBeNull();
  fireEvent.click(screen.getByRole("button", { name: "测试连接" }));
  expect(
    await screen.findByRole("button", { name: "重新测试连接" }),
  ).toBeTruthy();
  expect((screen.getByLabelText("模型 ID") as HTMLInputElement).value).toBe(
    "jev-draft",
  );
  expect((screen.getByLabelText("测试文本") as HTMLTextAreaElement).value).toBe(
    "keep this input",
  );
  fireEvent.click(screen.getByRole("button", { name: "重新测试连接" }));
  expect(await screen.findByText("连接正常")).toBeTruthy();
});

it("keeps an edited connection and secret available after a failed save", async () => {
  const onSave = vi
    .fn()
    .mockRejectedValueOnce(new Error("save failed"))
    .mockResolvedValueOnce({ ...config, model: "jev-draft" });
  render(<JevSettingsPage config={config} onSave={onSave} onTest={vi.fn()} />);
  fireEvent.change(screen.getByLabelText("模型 ID"), {
    target: { value: "jev-draft" },
  });
  fireEvent.change(screen.getByLabelText("API Key", { selector: "input" }), {
    target: { value: "new-secret" },
  });
  fireEvent.click(screen.getByRole("button", { name: "保存 Jev 配置" }));
  await waitFor(() =>
    expect(
      screen
        .getByRole("button", { name: "保存 Jev 配置" })
        .hasAttribute("disabled"),
    ).toBe(false),
  );
  expect(
    (
      screen.getByLabelText("API Key", {
        selector: "input",
      }) as HTMLInputElement
    ).value,
  ).toBe("new-secret");
  expect(
    within(screen.getByRole("region", { name: "正在使用" })).getByText(
      config.model,
    ),
  ).toBeTruthy();
  fireEvent.click(screen.getByRole("button", { name: "保存 Jev 配置" }));
  await waitFor(() => expect(onSave).toHaveBeenCalledTimes(2));
  await waitFor(() =>
    expect(
      within(screen.getByRole("region", { name: "正在使用" })).getByText(
        "jev-draft",
      ),
    ).toBeTruthy(),
  );
});
