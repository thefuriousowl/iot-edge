import { describe, expect, it } from "vitest";

import type { EnergyPeriodSummary } from "../../../types/plugin";
import {
  automaticEnergyBucket,
  availableEnergyBuckets,
  defaultEnergyRange,
  energyLocalDate,
  energyLocalInput,
  presetEnergyRange,
  summarizeEnergyHistory,
  validEnergyRange,
} from "./energyHistory";

function row(overrides: Partial<EnergyPeriodSummary> = {}): EnergyPeriodSummary {
  return {
    from: "2026-08-23T00:00:00Z",
    to: "2026-08-23T01:00:00Z",
    electrical: { kilowatt_hours: 10, covered_seconds: 3_600, skipped_seconds: 0, coverage_percent: 100, segments: 60, skipped_segments: 0, issues: null },
    thermal: { kilowatt_hours: 30, covered_seconds: 3_600, skipped_seconds: 0, coverage_percent: 100, segments: 60, skipped_segments: 0, issues: null },
    cost: { value: 45, valid: true },
    cop: { value: 3, valid: true },
    ...overrides,
  };
}

describe("Energy history utilities", () => {
  it("creates stable default and timezone-calendar preset ranges", () => {
    const now = new Date("2026-08-23T04:30:45Z");
    expect(defaultEnergyRange(now)).toEqual({ from: "2026-08-22T04:30:00.000Z", to: "2026-08-23T04:30:00.000Z" });
    expect(presetEnergyRange("today", "Asia/Bangkok", now)).toEqual({ from: "2026-08-22T17:00:00.000Z", to: "2026-08-23T04:30:00.000Z" });
    expect(presetEnergyRange("month", "Asia/Bangkok", now).from).toBe("2026-07-31T17:00:00.000Z");
  });

  it("round-trips Plugin-local timezone inputs and rejects nonexistent wall clocks", () => {
    expect(energyLocalDate("2026-08-23T08:30", "Asia/Bangkok")?.toISOString()).toBe("2026-08-23T01:30:00.000Z");
    expect(energyLocalInput("2026-08-23T01:30:00Z", "Asia/Bangkok")).toBe("2026-08-23T08:30");
    expect(energyLocalDate("2026-03-08T02:30", "America/New_York")).toBeNull();
  });

  it("limits chart resolutions to at most 500 Plugin buckets", () => {
    const from = "2026-08-01T00:00:00Z";
    const to = "2026-08-31T00:00:00Z";
    expect(availableEnergyBuckets(from, to)).toEqual(["6h", "1d", "1w"]);
    expect(automaticEnergyBucket(from, to)).toBe("6h");
    expect(validEnergyRange(from, to)).toBe(true);
    expect(validEnergyRange(from, "2027-09-01T00:00:00Z")).toBe(false);
  });

  it("sums Energy instead of averaging bucket COP and fails cost closed", () => {
    const second = row({
      from: "2026-08-23T01:00:00Z",
      to: "2026-08-23T02:00:00Z",
      electrical: { kilowatt_hours: 20, covered_seconds: 1_800, skipped_seconds: 1_800, coverage_percent: 50, segments: 60, skipped_segments: 30, issues: [{ from: "2026-08-23T01:30:00Z", to: "2026-08-23T02:00:00Z", code: "no_coverage", errors: null }] },
      thermal: { kilowatt_hours: 60, covered_seconds: 0, skipped_seconds: 3_600, coverage_percent: 0, segments: 60, skipped_segments: 60, issues: null },
      cost: { value: 0, valid: false, error: "tariff Tag coverage is incomplete" },
      cop: { value: 99, valid: false, error: "COP unavailable" },
    });
    expect(summarizeEnergyHistory([row(), second])).toEqual({
      electricalKWh: 30,
      thermalKWh: 90,
      cost: null,
      cop: 3,
      electricalCoverage: 75,
      thermalCoverage: 50,
      affectedBuckets: 1,
    });
  });
});
