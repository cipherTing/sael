import type { ComponentProps } from "react";
import { Slider as SliderPrimitive } from "radix-ui";

export function Slider({
  className = "",
  thumbLabel,
  ...props
}: ComponentProps<typeof SliderPrimitive.Root> & { thumbLabel: string }) {
  return (
    <SliderPrimitive.Root
      data-slot="slider"
      className={`threshold-slider ${className}`}
      {...props}
    >
      <SliderPrimitive.Track
        data-slot="slider-track"
        className="threshold-slider-track"
      >
        <SliderPrimitive.Range
          data-slot="slider-range"
          className="threshold-slider-range"
        />
      </SliderPrimitive.Track>
      <SliderPrimitive.Thumb
        data-slot="slider-thumb"
        className="threshold-slider-thumb"
        aria-label={thumbLabel}
      />
    </SliderPrimitive.Root>
  );
}
