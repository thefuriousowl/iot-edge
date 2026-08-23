import type { DataLoggerQueryBucket } from "../../../types/datalogger";
import type { EnergyPeriodSummary } from "../../../types/plugin";

export type EnergyRangePreset = "today" | "24h" | "7d" | "30d" | "month";

export const energyBuckets: DataLoggerQueryBucket[] = ["1m", "5m", "15m", "1h", "6h", "1d", "1w"];

const bucketMilliseconds: Record<DataLoggerQueryBucket, number> = {
  "1m": 60_000,
  "5m": 5 * 60_000,
  "15m": 15 * 60_000,
  "1h": 60 * 60_000,
  "6h": 6 * 60 * 60_000,
  "1d": 24 * 60 * 60_000,
  "1w": 7 * 24 * 60 * 60_000,
};

interface LocalParts {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
}

export interface EnergyRange {
  from: string;
  to: string;
}

export interface EnergyHistorySummary {
  electricalKWh: number;
  thermalKWh: number;
  cost: number | null;
  cop: number | null;
  electricalCoverage: number;
  thermalCoverage: number;
  affectedBuckets: number;
}

const localInputPattern = /^(\d{4})-(\d{2})-(\d{2})T(\d{2}):(\d{2})$/;

function localParts(date: Date, timezone: string): LocalParts | null {
  try {
    const parts = new Intl.DateTimeFormat("en-CA", {
      timeZone: timezone,
      year: "numeric",
      month: "2-digit",
      day: "2-digit",
      hour: "2-digit",
      minute: "2-digit",
      hourCycle: "h23",
    }).formatToParts(date);
    const value = (type: Intl.DateTimeFormatPartTypes) => Number(parts.find((part) => part.type === type)?.value);
    const result = { year: value("year"), month: value("month"), day: value("day"), hour: value("hour"), minute: value("minute") };
    return Object.values(result).every(Number.isFinite) ? result : null;
  } catch {
    return null;
  }
}

function parseLocalInput(value: string): LocalParts | null {
  const match = localInputPattern.exec(value);
  if (!match) return null;
  const parts = { year: Number(match[1]), month: Number(match[2]), day: Number(match[3]), hour: Number(match[4]), minute: Number(match[5]) };
  const probe = new Date(Date.UTC(parts.year, parts.month - 1, parts.day, parts.hour, parts.minute));
  if (probe.getUTCFullYear() !== parts.year || probe.getUTCMonth() + 1 !== parts.month || probe.getUTCDate() !== parts.day || parts.hour > 23 || parts.minute > 59) return null;
  return parts;
}

function sameParts(first: LocalParts | null, second: LocalParts): boolean {
  return Boolean(first && first.year === second.year && first.month === second.month && first.day === second.day && first.hour === second.hour && first.minute === second.minute);
}

export function energyLocalInput(value: string | Date, timezone: string): string {
  const date = value instanceof Date ? value : new Date(value);
  const parts = localParts(date, timezone);
  if (!parts) return "";
  const pad = (number: number) => String(number).padStart(2, "0");
  return `${parts.year}-${pad(parts.month)}-${pad(parts.day)}T${pad(parts.hour)}:${pad(parts.minute)}`;
}

export function energyLocalDate(value: string, timezone: string): Date | null {
  const target = parseLocalInput(value);
  if (!target) return null;
  const targetUTC = Date.UTC(target.year, target.month - 1, target.day, target.hour, target.minute);
  let candidate = targetUTC;
  for (let attempt = 0; attempt < 4; attempt += 1) {
    const observed = localParts(new Date(candidate), timezone);
    if (!observed) return null;
    const observedUTC = Date.UTC(observed.year, observed.month - 1, observed.day, observed.hour, observed.minute);
    candidate += targetUTC - observedUTC;
  }
  let earliest: number | null = null;
  for (let offset = -180; offset <= 180; offset += 1) {
    const timestamp = candidate + offset * 60_000;
    if (sameParts(localParts(new Date(timestamp), timezone), target)) earliest = earliest === null ? timestamp : Math.min(earliest, timestamp);
  }
  return earliest === null ? null : new Date(earliest);
}

export function defaultEnergyRange(now = new Date()): EnergyRange {
  const to = new Date(now);
  to.setSeconds(0, 0);
  return { from: new Date(to.getTime() - 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };
}

export function presetEnergyRange(preset: EnergyRangePreset, timezone: string, now = new Date()): EnergyRange {
  const to = new Date(now);
  to.setSeconds(0, 0);
  if (preset === "24h") return { from: new Date(to.getTime() - 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };
  if (preset === "7d") return { from: new Date(to.getTime() - 7 * 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };
  if (preset === "30d") return { from: new Date(to.getTime() - 30 * 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };

  const local = energyLocalInput(to, timezone);
  const date = local.slice(0, 10);
  const startLocal = preset === "month" ? `${date.slice(0, 8)}01T00:00` : `${date}T00:00`;
  const from = energyLocalDate(startLocal, timezone);
  return { from: (from ?? new Date(to.getTime() - 24 * 60 * 60 * 1000)).toISOString(), to: to.toISOString() };
}

export function validEnergyRange(from: string, to: string): boolean {
  const fromDate = new Date(from);
  const toDate = new Date(to);
  return Number.isFinite(fromDate.getTime()) && Number.isFinite(toDate.getTime()) && fromDate < toDate && toDate.getTime() - fromDate.getTime() <= 366 * 24 * 60 * 60 * 1000;
}

export function availableEnergyBuckets(from: string, to: string): DataLoggerQueryBucket[] {
  const span = new Date(to).getTime() - new Date(from).getTime();
  if (!Number.isFinite(span) || span <= 0) return ["1h"];
  return energyBuckets.filter((bucket) => Math.ceil(span / bucketMilliseconds[bucket]) + 1 <= 500);
}

export function automaticEnergyBucket(from: string, to: string): DataLoggerQueryBucket {
  return availableEnergyBuckets(from, to)[0] ?? "1w";
}

export function summarizeEnergyHistory(rows: EnergyPeriodSummary[]): EnergyHistorySummary {
  const duration = (row: EnergyPeriodSummary) => Math.max(0, new Date(row.to).getTime() - new Date(row.from).getTime()) / 1000;
  const totalSeconds = rows.reduce((sum, row) => sum + duration(row), 0);
  const electricalKWh = rows.reduce((sum, row) => sum + row.electrical.kilowatt_hours, 0);
  const thermalKWh = rows.reduce((sum, row) => sum + row.thermal.kilowatt_hours, 0);
  const costsValid = rows.length > 0 && rows.every((row) => row.cost.valid);
  return {
    electricalKWh,
    thermalKWh,
    cost: costsValid ? rows.reduce((sum, row) => sum + row.cost.value, 0) : null,
    cop: electricalKWh > 0 ? thermalKWh / electricalKWh : null,
    electricalCoverage: totalSeconds > 0 ? rows.reduce((sum, row) => sum + row.electrical.covered_seconds, 0) / totalSeconds * 100 : 0,
    thermalCoverage: totalSeconds > 0 ? rows.reduce((sum, row) => sum + row.thermal.covered_seconds, 0) / totalSeconds * 100 : 0,
    affectedBuckets: rows.filter((row) => (row.electrical.issues?.length ?? 0) > 0 || (row.thermal.issues?.length ?? 0) > 0 || !row.cost.valid || !row.cop.valid).length,
  };
}
