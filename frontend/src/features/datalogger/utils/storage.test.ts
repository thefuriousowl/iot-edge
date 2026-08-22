import { describe, expect, it } from "vitest";

import { bytesPerMiB, estimateDataLoggerStorage, formatEstimatedDuration, formatStorageBytes } from "./storage";

describe("Data Logger storage estimates", () => {
  it("rounds interval capacity down to complete synchronized batches", () => {
    expect(estimateDataLoggerStorage({ maxSizeBytes: 10_000, tagCount: 3, averageRowBytes: 100, batchCount: 20, mode: "interval", config: { interval_seconds: 10 } })).toEqual({
      capacityRows: 99,
      capacityBatches: 33,
      estimatedRetentionSeconds: 330,
      estimatedSecondsUntilRollover: 130,
    });
  });

  it("estimates multi-run calendar schedules and unlimited storage", () => {
    expect(estimateDataLoggerStorage({ maxSizeBytes: 8_000, tagCount: 2, averageRowBytes: 100, mode: "schedule", config: { unit: "day", every: 2, times: ["08:00", "17:00"] } })?.estimatedRetentionSeconds).toBe(40 * 24 * 60 * 60);
    expect(estimateDataLoggerStorage({ maxSizeBytes: null, tagCount: 2, mode: "interval", config: { interval_seconds: 60 } })).toBeNull();
  });

  it("formats storage and approximate durations", () => {
    expect(formatStorageBytes(384)).toBe("384 B");
    expect(formatStorageBytes(100 * bytesPerMiB)).toBe("100 MiB");
    expect(formatEstimatedDuration(2 * 24 * 60 * 60 + 3 * 60 * 60)).toBe("2 days 3 hours");
    expect(formatEstimatedDuration(0)).toBe("Less than one capture");
  });
});
