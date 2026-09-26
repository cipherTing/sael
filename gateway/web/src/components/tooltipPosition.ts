// ECharts expects chart coordinates even when its tooltip is appended to document.body.
export function placeTooltip(
  origin: { left: number; top: number },
  point: number[],
  size: number[],
  viewport: number[],
): number[] {
  return [origin.left, origin.top].map((offset, axis) => {
    const pointer = offset + point[axis],
      max = viewport[axis] - size[axis] - 12;
    const preferred =
      pointer + 12 > max ? pointer - size[axis] - 12 : pointer + 12;
    return Math.max(12, Math.min(preferred, max)) - offset;
  });
}
