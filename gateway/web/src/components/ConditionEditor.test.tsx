import { useState } from "react";
import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import { afterEach, expect, it } from "vitest";
import { ConditionEditor } from "./ConditionEditor";
import type { Condition } from "../policy";

afterEach(cleanup);

it("does not silently change the business threshold when selecting a smaller score scale", () => {
  function Harness() {
    const [condition, setCondition] = useState<Condition>({ question: "gore", threshold: 1.5 });
    return <ConditionEditor condition={condition} index={0} invalid={false} questions={[{ key: "gore", type: "score", max: 3 }, { key: "self_harm", type: "noul", max: 1 }]} onChange={(patch) => setCondition((current) => ({ ...current, ...patch }))} onRemove={() => {}} />;
  }
  render(<Harness />);
  fireEvent.keyDown(screen.getByRole("combobox", { name: "审核项 1" }), { key: "ArrowDown" });
  fireEvent.click(screen.getByRole("option", { name: "自伤风险" }));
  expect((screen.getByRole("spinbutton", { name: "自伤风险阈值" }) as HTMLInputElement).value).toBe("1.5");
  expect(screen.getByRole("slider", { name: "自伤风险阈值滑杆" }).getAttribute("aria-valuenow")).toBe("1");
});
