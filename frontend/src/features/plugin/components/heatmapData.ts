export interface HeatmapPoint { at: string; value: number | null; coverage: number }
export interface HeatmapCell { weekday: number; hour: number; value: number; coverage: number; samples: number }

export const maximumHeatmapPoints = 500;
export const weekdayLabels = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"];

export function buildHeatmapCells(points: HeatmapPoint[], timezone: string): HeatmapCell[] {
  const grouped = new Map<string, HeatmapCell>();
  for (const point of points.slice(-maximumHeatmapPoints)) {
    if (point.value === null || !Number.isFinite(point.value) || !Number.isFinite(point.coverage)) continue;
    const date = new Date(point.at);
    if (Number.isNaN(date.getTime())) continue;

    let weekday: number;
    let hour: number;
    try {
      const parts = new Intl.DateTimeFormat("en-US", { timeZone: timezone, weekday: "short", hour: "2-digit", hourCycle: "h23" }).formatToParts(date);
      weekday = weekdayLabels.indexOf(parts.find((part) => part.type === "weekday")?.value ?? "");
      hour = Number(parts.find((part) => part.type === "hour")?.value);
    } catch {
      continue;
    }
    if (weekday < 0 || !Number.isInteger(hour) || hour < 0 || hour > 23) continue;

    const key = `${weekday}-${hour}`;
    const current = grouped.get(key) ?? { weekday, hour, value: 0, coverage: 0, samples: 0 };
    current.value += point.value;
    current.coverage += point.coverage;
    current.samples += 1;
    grouped.set(key, current);
  }

  return [...grouped.values()]
    .map((cell) => ({ ...cell, value: cell.value / cell.samples, coverage: cell.coverage / cell.samples }))
    .sort((a, b) => a.weekday - b.weekday || a.hour - b.hour);
}
