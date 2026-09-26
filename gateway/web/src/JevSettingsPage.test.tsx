import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
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
it("defaults to the Jev token budget and prevents disabling the guard with zero", () => {
  render(<JevSettingsPage config={config} onSave={vi.fn()} onTest={vi.fn()} />);
  const limit = screen.getByRole("spinbutton", {
    name: "送审上限（估算 Token）",
  }) as HTMLInputElement;
  expect(limit.value).toBe("28800");
  fireEvent.change(limit, { target: { value: "0" } });
  expect(
    screen
      .getByRole("button", { name: "保存 Jev 配置" })
      .hasAttribute("disabled"),
  ).toBe(true);
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
      max_input_tokens: 28800,
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
  expect(onTest).toHaveBeenCalledWith("sample", {
    base_url: config.base_url,
    model: config.model,
    api_key: "",
    timeout_ms: 5000,
    max_input_tokens: 28800,
  });
  fireEvent.click(screen.getByRole("button", { name: "清空测试" }));
  expect(screen.queryByText("1.9")).toBeNull();
});
