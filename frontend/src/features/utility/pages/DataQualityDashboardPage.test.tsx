// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAssetMeasurements, listAssets } from "../../../services/asset.service";
import { getEnergyOverview } from "../../../services/energy.service";
import { listPlugins } from "../../../services/plugin.service";
import type { AssetMeasurementProjection } from "../../../types/asset";
import type { EnergyOverviewResponse } from "../../../types/plugin";
import DataQualityDashboardPage from "./DataQualityDashboardPage";

vi.mock("../../../services/asset.service", () => ({ getAssetMeasurements: vi.fn(), listAssets: vi.fn() }));
vi.mock("../../../services/energy.service", () => ({ getEnergyOverview: vi.fn() }));
vi.mock("../../../services/plugin.service", () => ({ listPlugins: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));
const mockedAssets = vi.mocked(listAssets); const mockedMeasurements = vi.mocked(getAssetMeasurements); const mockedPlugins = vi.mocked(listPlugins); const mockedEnergy = vi.mocked(getEnergyOverview);

function measurement(id: string, available: boolean, quality: string, observedAt?: string, error?: string): AssetMeasurementProjection {
  const semantic = { resource: "electricity" as const, quantity: "power" as const, unit: "kW" as const, precision: 2 };
  const source = { kind: "tag" as const, tag_id: id };
  return { binding: { id, owner_asset_id: "asset-1", boundary_asset_id: "asset-1", source_key: `tag:${id}`, source, semantic, meter_role: "direct", rollup_policy: "include" }, latest: { binding_id: id, source, semantic, available, value: available ? 10 : null, quality, observed_at: observedAt, error }, history: [], history_retention: "runtime_memory" };
}

const summary = (coverage: number, skipped: number, issues: EnergyOverviewResponse["today"]["electrical"]["issues"]) => ({ kilowatt_hours: 10, covered_seconds: 3_000, skipped_seconds: 600, coverage_percent: coverage, segments: 10, skipped_segments: skipped, issues });
const overview: EnergyOverviewResponse = { instance_id: "energy-1", logger_id: "logger-1", timezone: "Asia/Bangkok", currency: "THB", tariff_mode: "flat", rate_per_kwh: 4, as_of: "2026-08-28T10:00:00Z", latest: null, today: { from: "2026-08-28T00:00:00Z", to: "2026-08-28T10:00:00Z", electrical: summary(80, 2, [{ from: "2026-08-28T01:00:00Z", to: "2026-08-28T02:00:00Z", code: "source_bad", errors: [{ code: "source_bad", tag_id: "power", at: "2026-08-28T01:00:00Z", message: "Modbus exception 0x02" }] }]), thermal: summary(60, 3, [{ from: "2026-08-28T03:00:00Z", to: "2026-08-28T04:00:00Z", code: "stale_gap", errors: null }]), cost: { value: 0, valid: false, error: "tariff coverage incomplete" }, cop: { value: 0, valid: false, error: "thermal coverage incomplete" } }, month: { from: "2026-08-01T00:00:00Z", to: "2026-08-28T10:00:00Z", electrical: summary(90, 1, null), thermal: summary(90, 1, null), cost: { value: 40, valid: true }, cop: { value: 2, valid: true } } };

describe("DataQualityDashboardPage", () => {
  beforeEach(() => {
    mockedAssets.mockResolvedValue({ data: [{ id: "asset-1", parent_id: null, name: "Building A", kind: "building", description: null, enabled: true, timezone: null, position: 0, metadata: {}, created_at: "", updated_at: "" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedMeasurements.mockResolvedValue({ asset_id: "asset-1", captured_at: "2026-08-28T10:00:00Z", measurements: [measurement("missing", false, "bad", undefined, "No sample"), measurement("modbus", false, "bad", undefined, "Modbus timeout"), measurement("stale", true, "good", "2026-08-28T09:50:00Z"), measurement("good", true, "good", "2026-08-28T09:59:00Z")] });
    mockedPlugins.mockResolvedValue({ data: [{ id: "energy-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1, runtime: { state: "running", started_at: null, last_transition_at: null, error: null }, created_at: "", updated_at: "" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedEnergy.mockResolvedValue(overview);
  });
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("summarizes missing, bad, stale, Modbus, Logger gaps, coverage and affected calculations", async () => {
    render(<MemoryRouter><DataQualityDashboardPage /></MemoryRouter>);
    const kpis = await screen.findByRole("region", { name: "Data quality summary" });
    const metric = (label: string) => within(within(kpis).getByText(label).closest("article")!).getByText(/.*/, { selector: "strong" });
    expect(metric("Missing")).toHaveTextContent("2"); expect(metric("Bad quality")).toHaveTextContent("2"); expect(metric("Stale")).toHaveTextContent("1"); expect(metric("Modbus errors")).toHaveTextContent("2"); expect(metric("Logger gaps")).toHaveTextContent("5"); expect(metric("Affected calculations")).toHaveTextContent("2"); expect(metric("Energy coverage")).toHaveTextContent("70%");
    expect(screen.getByText("Modbus timeout")).toBeInTheDocument(); expect(screen.getByText("Modbus exception 0x02")).toBeInTheDocument(); expect(screen.getByText("tariff coverage incomplete")).toBeInTheDocument();
    expect(screen.getAllByRole("link", { name: "Investigate" })[0]).toHaveAttribute("href");
    expect(screen.getByRole("region", { name: "Quality interpretation" })).toHaveTextContent("Missing is not zero");
  });

  it("renders a healthy empty state without converting absent Energy coverage to zero", async () => {
    mockedMeasurements.mockResolvedValueOnce({ asset_id: "asset-1", captured_at: "2026-08-28T10:00:00Z", measurements: [measurement("good", true, "good", "2026-08-28T09:59:00Z")] });
    mockedPlugins.mockResolvedValueOnce({ data: [], pagination: { page: 1, per_page: 100, total: 0, total_pages: 0 } });
    render(<MemoryRouter><DataQualityDashboardPage /></MemoryRouter>);
    expect(await screen.findByText("No quality issues detected")).toBeInTheDocument();
    const kpis = screen.getByRole("region", { name: "Data quality summary" });
    expect(within(within(kpis).getByText("Energy coverage").closest("article")!).getByText("Unavailable")).toBeInTheDocument();
  });

  it("sanitizes source loading failures", async () => {
    mockedAssets.mockRejectedValueOnce(new Error("database password=secret"));
    render(<MemoryRouter><DataQualityDashboardPage /></MemoryRouter>);
    const alert = await screen.findByRole("alert"); expect(alert).toHaveTextContent("Data-quality sources unavailable"); expect(alert).not.toHaveTextContent("password=secret");
  });
});
