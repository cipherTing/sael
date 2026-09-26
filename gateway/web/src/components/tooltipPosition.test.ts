import { expect, it } from "vitest";
import { placeTooltip } from "./tooltipPosition";

it("keeps tooltips inside the visible window after the timeline scrolls horizontally", () => {
  expect(
    placeTooltip({ left: -560, top: 400 }, [850, 280], [240, 320], [375, 844]),
  ).toEqual([598, -52]);
});
it("places a tooltip beside the pointer when the viewport has room", () => {
  expect(
    placeTooltip({ left: 200, top: 150 }, [100, 70], [180, 120], [1440, 900]),
  ).toEqual([112, 82]);
});
