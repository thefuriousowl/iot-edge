import type { DataLogger, DataLoggerConfig, DataLoggerScheduleConfig } from "../../../types/datalogger";

interface LocalParts {
  year: number;
  month: number;
  day: number;
  hour: number;
  minute: number;
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

export function isValidTimezone(timezone: string): boolean {
  try {
    new Intl.DateTimeFormat("en", { timeZone: timezone }).format();
    return timezone.trim().length > 0;
  } catch {
    return false;
  }
}

export function zonedDateTimeToDate(value: string, timezone: string): Date | null {
  const target = parseLocalInput(value);
  if (!target || !isValidTimezone(timezone)) return null;
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

export function dateToLocalInput(value: string | Date, timezone: string): string {
  const date = value instanceof Date ? value : new Date(value);
  const parts = localParts(date, timezone);
  if (!parts) return "";
  const pad = (number: number) => String(number).padStart(2, "0");
  return `${parts.year}-${pad(parts.month)}-${pad(parts.day)}T${pad(parts.hour)}:${pad(parts.minute)}`;
}

function configIsSchedule(config: DataLoggerConfig): config is DataLoggerScheduleConfig {
  return "unit" in config;
}

function anchoredNext(start: Date, everyMilliseconds: number, threshold: Date): Date {
  if (threshold <= start) return start;
  return new Date(start.getTime() + Math.ceil((threshold.getTime() - start.getTime()) / everyMilliseconds) * everyMilliseconds);
}

function localDate(parts: LocalParts): Date {
  return new Date(Date.UTC(parts.year, parts.month - 1, parts.day));
}

function localInputForDate(date: Date, time: string): string {
  return `${date.toISOString().slice(0, 10)}T${time}`;
}

function isoWeekday(date: Date): number {
  return date.getUTCDay() === 0 ? 7 : date.getUTCDay();
}

function nextDaily(start: Date, threshold: Date, timezone: string, config: DataLoggerScheduleConfig): Date | null {
  const startParts = localParts(start, timezone);
  const thresholdParts = localParts(threshold, timezone);
  if (!startParts || !thresholdParts || !config.times?.length) return null;
  const startDate = localDate(startParts);
  const thresholdDate = localDate(thresholdParts);
  const days = Math.max(0, Math.floor((thresholdDate.getTime() - startDate.getTime()) / 86_400_000));
  let cycle = Math.ceil(days / config.every) * config.every;
  for (let attempt = 0; attempt < 8; attempt += 1, cycle += config.every) {
    const date = new Date(startDate.getTime() + cycle * 86_400_000);
    for (const time of [...config.times].sort()) {
      const candidate = zonedDateTimeToDate(localInputForDate(date, time), timezone);
      if (candidate && candidate >= start && candidate >= threshold) return candidate;
    }
  }
  return null;
}

function nextWeekly(start: Date, threshold: Date, timezone: string, config: DataLoggerScheduleConfig): Date | null {
  const startParts = localParts(start, timezone);
  const thresholdParts = localParts(threshold, timezone);
  if (!startParts || !thresholdParts || !config.times?.length || !config.weekdays?.length) return null;
  const startDate = localDate(startParts);
  const startWeek = new Date(startDate.getTime() - (isoWeekday(startDate) - 1) * 86_400_000);
  const thresholdDate = localDate(thresholdParts);
  const weeks = Math.max(0, Math.floor((thresholdDate.getTime() - startWeek.getTime()) / (7 * 86_400_000)));
  let activeWeek = Math.ceil(weeks / config.every) * config.every;
  for (let attempt = 0; attempt < 8; attempt += 1, activeWeek += config.every) {
    const weekStart = new Date(startWeek.getTime() + activeWeek * 7 * 86_400_000);
    for (const weekday of [...config.weekdays].sort((first, second) => first - second)) {
      const date = new Date(weekStart.getTime() + (weekday - 1) * 86_400_000);
      for (const time of [...config.times].sort()) {
        const candidate = zonedDateTimeToDate(localInputForDate(date, time), timezone);
        if (candidate && candidate >= start && candidate >= threshold) return candidate;
      }
    }
  }
  return null;
}

export function nextDataLoggerRun(logger: Pick<DataLogger, "mode" | "start_at" | "end_at" | "timezone" | "config">, now = new Date()): Date | null {
  const start = new Date(logger.start_at);
  if (Number.isNaN(start.getTime()) || !isValidTimezone(logger.timezone)) return null;
  const threshold = now > start ? now : start;
  let next: Date | null = null;
  if (logger.mode === "interval" && "interval_seconds" in logger.config) {
    next = anchoredNext(start, logger.config.interval_seconds * 1000, threshold);
  } else if (logger.mode === "schedule" && configIsSchedule(logger.config)) {
    if (logger.config.unit === "minute") next = anchoredNext(start, logger.config.every * 60_000, threshold);
    if (logger.config.unit === "hour") next = anchoredNext(start, logger.config.every * 3_600_000, threshold);
    if (logger.config.unit === "day") next = nextDaily(start, threshold, logger.timezone, logger.config);
    if (logger.config.unit === "week") next = nextWeekly(start, threshold, logger.timezone, logger.config);
  }
  if (!next) return null;
  const end = logger.end_at ? new Date(logger.end_at) : null;
  return end && next > end ? null : next;
}

export function scheduleSummary(mode: DataLogger["mode"], config: DataLoggerConfig): string {
  if (mode === "interval" && "interval_seconds" in config) return `Every ${config.interval_seconds} second${config.interval_seconds === 1 ? "" : "s"}`;
  if (!configIsSchedule(config)) return "Invalid schedule";
  const cadence = `Every ${config.every} ${config.unit}${config.every === 1 ? "" : "s"}`;
  if (config.unit === "day") return `${cadence} at ${config.times?.join(", ") ?? "—"}`;
  if (config.unit === "week") return `${cadence} · ${config.weekdays?.map((day) => ["", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"][day]).join(", ")} · ${config.times?.join(", ")}`;
  return `${cadence} from start`;
}

export function formatInTimezone(date: Date, timezone: string): string {
  try {
    return new Intl.DateTimeFormat(undefined, { timeZone: timezone, dateStyle: "medium", timeStyle: "short" }).format(date);
  } catch {
    return "Invalid date";
  }
}
