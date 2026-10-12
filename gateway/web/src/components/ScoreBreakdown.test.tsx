import {
  cleanup,
  fireEvent,
  render,
  screen,
  within,
} from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { ScoreBreakdown } from "./ScoreBreakdown";

afterEach(cleanup);

it("uses the original 0–3 scale and a threshold marker for graded sexual and gore scores", () => {
  render(
    <ScoreBreakdown
      scores={[
        { question: "gore", type: "score", value: 1.9 },
        { question: "sexual", type: "score", value: 2.7 },
      ]}
      hits={[
        { question: "gore", value: 1.9, threshold: 1.5 },
        { question: "sexual", value: 2.7, threshold: 2.5 },
      ]}
    />,
  );
  const gore = screen.getByRole("article", { name: "血腥程度评分" });
  expect(within(gore).getByText("程度分级")).toBeTruthy();
  expect(within(gore).getByText("/ 3")).toBeTruthy();
  expect(gore.querySelector(".risk-score-scale")).toBeNull();
  const marker = within(gore).getByLabelText("血腥程度阈值 1.5");
  expect(marker.getAttribute("style")).toContain("left: 50%");
  expect(within(gore).getByText("> 1.5")).toBeTruthy();
});

it("keeps 0–1 risk scores visually distinct and places untriggered scores below in a collapsed section", () => {
  render(
    <ScoreBreakdown
      scores={[
        { question: "self_harm", type: "noul", value: 0.9 },
        { question: "gore", type: "score", value: 1 },
      ]}
      comparisons={[
        { question: "self_harm", value: 0.9, threshold: 0.8, matched: true },
        { question: "gore", value: 1, threshold: 1.5, matched: false },
      ]}
    />,
  );
  const risk = screen.getByRole("article", { name: "自伤风险评分" });
  expect(within(risk).getByText("风险评分")).toBeTruthy();
  expect(within(risk).getByText("/ 1")).toBeTruthy();
  expect(within(risk).queryByText("程度分级")).toBeNull();
  expect(
    within(risk).getByLabelText("自伤风险阈值 0.8").getAttribute("style"),
  ).toContain("left: 80%");
  const hidden = screen.getByText("未达到阈值").closest("details")!;
  expect(hidden.open).toBe(false);
  expect(hidden.textContent).toContain("血腥程度");
  expect(hidden.textContent).not.toContain("自伤风险");
});

it("shows disposition only for matched conditions and styles the action as a pill", () => {
  render(
    <ScoreBreakdown
      scores={[
        { question: "sexual", type: "score", value: 2.6 },
        { question: "self_harm", type: "noul", value: 0.2 },
      ]}
      comparisons={[
        {
          question: "sexual",
          value: 2.6,
          threshold: 2,
          matched: true,
          record_only: false,
        },
        {
          question: "self_harm",
          value: 0.2,
          threshold: 0.8,
          matched: false,
          record_only: true,
        },
      ]}
    />,
  );

  const reached = screen.getByRole("article", { name: "性内容程度评分" });
  const blockTag = within(reached).getByText("拦截");
  expect(blockTag.className).toContain("risk-condition-disposition");
  expect(blockTag.className).toContain("blocked");

  const hidden = screen.getByText("未达到阈值").closest("details")!;
  fireEvent.click(within(hidden).getByText("未达到阈值"));
  const below = within(hidden).getByRole("article", { name: "自伤风险评分" });
  expect(within(below).queryByText("仅记录")).toBeNull();
});

it("omits scores whose thresholds are not configured", () => {
  render(
    <ScoreBreakdown
      scores={[{ question: "privacy_pii", type: "noul", value: 0.7 }]}
    />,
  );

  expect(screen.queryByText("未配置阈值")).toBeNull();
  expect(screen.queryByText("隐私信息")).toBeNull();
  expect(screen.queryByText("达到阈值")).toBeNull();
});

it("shows cached disposition only on matched condition facts", () => {
  render(
    <ScoreBreakdown
      scores={[]}
      comparisons={[
        {
          question: "privacy_pii",
          threshold: 0.5,
          matched: true,
          record_only: true,
        },
        {
          question: "self_harm",
          threshold: 0.8,
          matched: false,
          record_only: false,
        },
      ]}
    />,
  );

  const privateInfo = screen.getByText("隐私信息").closest("article")!;
  const recordTag = within(privateInfo).getByText("仅记录");
  expect(recordTag.className).toContain("risk-condition-disposition");
  expect(recordTag.className).toContain("record-only");
  const selfHarm = screen.getByText("自伤风险").closest("article")!;
  expect(within(selfHarm).queryByText("拦截")).toBeNull();
});

it("renders only explicitly supplied cached hits when no classification sample exists", () => {
  render(
    <ScoreBreakdown
      scores={[]}
      hits={[{ question: "gore", value: 2.3, threshold: 1.5 }]}
      comparisons={[
        { question: "self_harm", value: 0.9, threshold: 0.8, matched: true },
      ]}
    />,
  );
  expect(screen.getByRole("article", { name: "血腥程度评分" })).toBeTruthy();
  expect(screen.getByText("2.3")).toBeTruthy();
  expect(screen.queryByText("自伤风险")).toBeNull();
});

it("does not create score UI for missing or unknown samples", () => {
  const { container } = render(
    <ScoreBreakdown
      scores={[{ question: "unknown", type: "score", value: 0.8 }]}
    />,
  );
  expect(container.textContent).toBe("");
});
