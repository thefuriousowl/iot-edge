import { describe, expect, it } from "vitest";
import { previousEqualRange, updateUtilityFilters, validDashboardTimezone } from "./dashboardFilters";

describe("utility dashboard filter contract", () => {
  it("updates URL filters without discarding unrelated dashboard state", () => {
    const next = updateUtilityFilters(new URLSearchParams("from=a&to=b&bucket=1h&page=4&compare=previous"), { asset: "asset-1", timezone: "Asia/Bangkok", compare: null });
    expect(next.toString()).toBe("from=a&to=b&bucket=1h&asset=asset-1&timezone=Asia%2FBangkok");
  });
  it("validates IANA timezones and falls back closed", () => {
    expect(validDashboardTimezone("Asia/Bangkok", "UTC")).toBe("Asia/Bangkok");
    expect(validDashboardTimezone("Not/AZone", "UTC")).toBe("UTC");
  });
  it("builds the immediately preceding equal-duration range", () => {
    expect(previousEqualRange("2026-08-23T01:00:00.000Z", "2026-08-23T03:00:00.000Z")).toEqual({ from: "2026-08-22T23:00:00.000Z", to: "2026-08-23T01:00:00.000Z" });
  });
});
