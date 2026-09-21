// @vitest-environment jsdom

import { describe, expect, it } from "vitest";

import type { EnergyHistoryResponse } from "../../../types/plugin";
import { buildEnergyDashboardCSV, collectDashboardPNGComponents, pngDataURLBlob } from "./dashboardExport";

const metric = { kilowatt_hours: 12, covered_seconds: 3_000, skipped_seconds: 600, coverage_percent: 83.3, segments: 5, skipped_segments: 1, issues: [{ code: "missing_sample" as const, errors: [{ code: "missing_sample" as const, message: "missing source", tag_id: "power", at: "2026-08-01T00:00:00Z" }], from: "2026-08-01T00:00:00Z", to: "2026-08-01T00:10:00Z" }] };
const response: EnergyHistoryResponse = { instance_id: "plugin-1", logger_id: "logger-1", timezone: "Asia/Bangkok", bucket: "1h", requested_bucket: "1h", downsampled: false, point_limit: 500, currency: "THB", tariff_mode: "flat", rate_per_kwh: 4.5, data: [{ from: "2026-08-01T00:00:00Z", to: "2026-08-01T01:00:00Z", electrical: metric, thermal: { ...metric, kilowatt_hours: 24, issues: [] }, cost: { value: 54, valid: true }, cop: { value: 2, valid: true } }], pagination: { page: 1, per_page: 500, total: 1, total_pages: 1 } };

describe("dashboard export", () => {
  it("exports active filters, provenance, quality and aligned comparison as CSV", () => {
    const csv = buildEnergyDashboardCSV(response, response, { pluginName: "Plant, East", assetID: "asset-1", assetName: "Chiller Plant", displayTimezone: "UTC", comparePrevious: true });
    expect(csv).toContain('plugin_instance_id,plugin_name,logger_id,hierarchy_asset_id');
    expect(csv).toContain('plugin-1,"Plant, East",logger-1,asset-1,Chiller Plant,Asia/Bangkok,UTC,1h,true');
    expect(csv).toContain("83.3,83.3,600,600,missing_sample: missing source");
    expect(csv).toContain("2026-08-01T00:00:00Z,2026-08-01T01:00:00Z,12,24,83.3,83.3");
  });

  it("accepts only a valid PNG data URL from the visual renderer", () => {
    const blob = pngDataURLBlob("data:image/png;base64,iVBORw0KGgo=");
    expect(blob.type).toBe("image/png");
    expect(blob.size).toBeGreaterThan(0);
    expect(() => pngDataURLBlob("data:text/plain;base64,dGV4dA==")).toThrow("invalid data");
  });

  it("collects each dashboard component as a separately named PNG", () => {
    document.body.innerHTML = `<main><section class="energy-history-summary" aria-label="Selected range summary"></section><section class="energy-history-chart" aria-label="Energy chart"></section><section class="utility-ranking"><h2>Top consumption buckets</h2></section><section class="energy-history-table" aria-label="Bucket details"></section></main>`;
    const components = collectDashboardPNGComponents(document.querySelector("main")!);
    expect(components.map(({ filename }) => filename)).toEqual([
      "01-selected-range-summary.png", "02-energy-chart.png", "03-top-consumption-buckets.png", "04-bucket-details.png",
    ]);
  });
});
