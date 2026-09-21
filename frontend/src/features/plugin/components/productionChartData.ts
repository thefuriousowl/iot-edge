export interface DonutDatum { name: string; value: number }
export const maximumDonutSlices = 6;

export function collapseDonutData(data: DonutDatum[], limit = maximumDonutSlices): DonutDatum[] {
  const valid = data.filter((item) => item.name.trim() && Number.isFinite(item.value) && item.value > 0).sort((a, b) => b.value - a.value);
  if (valid.length <= limit) return valid.map((item) => ({ ...item }));
  const keep = valid.slice(0, Math.max(1, limit - 1));
  return [...keep, { name: "Other", value: valid.slice(keep.length).reduce((sum, item) => sum + item.value, 0) }];
}
