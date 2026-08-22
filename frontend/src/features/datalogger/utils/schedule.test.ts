import { describe, expect, it } from "vitest";

import type { DataLogger } from "../../../types/datalogger";
import { dateToLocalInput, isValidTimezone, nextDataLoggerRun, scheduleSummary, zonedDateTimeToDate } from "./schedule";

function logger(overrides: Partial<DataLogger> = {}): DataLogger {
  return {
    id: "logger-1",
    name: "History",
    description: null,
    enabled: true,
    timezone: "UTC",
    mode: "interval",
    start_at: "2026-08-22T00:00:00Z",
    end_at: null,
    config: { interval_seconds: 60 },
    tag_count: 1,
    created_at: "2026-08-22T00:00:00Z",
    updated_at: "2026-08-22T00:00:00Z",
    ...overrides,
  };
}

describe("Data Logger schedule utilities", () => {
  it("validates IANA timezones and converts Bangkok wall clock values", () => {
    expect(isValidTimezone("Asia/Bangkok")).toBe(true);
    expect(isValidTimezone("Not/A_Zone")).toBe(false);
    expect(zonedDateTimeToDate("2026-08-22T08:30", "Asia/Bangkok")?.toISOString()).toBe("2026-08-22T01:30:00.000Z");
    expect(dateToLocalInput("2026-08-22T01:30:00Z", "Asia/Bangkok")).toBe("2026-08-22T08:30");
  });

  it("rejects DST gaps and resolves folds to the earliest occurrence", () => {
    expect(zonedDateTimeToDate("2026-03-08T02:30", "America/New_York")).toBeNull();
    expect(zonedDateTimeToDate("2026-11-01T01:30", "America/New_York")?.toISOString()).toBe("2026-11-01T05:30:00.000Z");
  });

  it("keeps interval and minute schedules anchored to start", () => {
    expect(nextDataLoggerRun(logger(), new Date("2026-08-22T00:02:01Z"))?.toISOString()).toBe("2026-08-22T00:03:00.000Z");
    expect(nextDataLoggerRun(logger({ mode: "schedule", config: { unit: "minute", every: 5 } }), new Date("2026-08-22T00:11:00Z"))?.toISOString()).toBe("2026-08-22T00:15:00.000Z");
  });

  it("finds timezone-aware daily and ISO-weekly occurrences", () => {
    const daily = logger({ timezone: "Asia/Bangkok", mode: "schedule", start_at: "2026-08-22T01:00:00Z", config: { unit: "day", every: 1, times: ["09:30"] } });
    expect(nextDataLoggerRun(daily, new Date("2026-08-22T02:00:00Z"))?.toISOString()).toBe("2026-08-22T02:30:00.000Z");

    const weekly = logger({ mode: "schedule", start_at: "2026-08-17T00:00:00Z", config: { unit: "week", every: 1, weekdays: [3, 5], times: ["08:00"] } });
    expect(nextDataLoggerRun(weekly, new Date("2026-08-20T10:00:00Z"))?.toISOString()).toBe("2026-08-21T08:00:00.000Z");
  });

  it("honors the inclusive end boundary and rejects invalid definitions", () => {
    expect(nextDataLoggerRun(logger({ end_at: "2026-08-22T00:02:59Z" }), new Date("2026-08-22T00:02:01Z"))).toBeNull();
    expect(nextDataLoggerRun(logger({ timezone: "Invalid/Zone" }))).toBeNull();
  });

  it("summarizes interval and calendar definitions", () => {
    expect(scheduleSummary("interval", { interval_seconds: 1 })).toBe("Every 1 second");
    expect(scheduleSummary("schedule", { unit: "day", every: 2, times: ["08:00", "17:00"] })).toBe("Every 2 days at 08:00, 17:00");
    expect(scheduleSummary("schedule", { unit: "week", every: 1, weekdays: [1, 5], times: ["09:00"] })).toBe("Every 1 week · Mon, Fri · 09:00");
  });
});
