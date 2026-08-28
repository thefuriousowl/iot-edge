// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getAssetMeasurements, listAssets } from "../../../services/asset.service";
import type { AssetMeasurementProjection } from "../../../types/asset";
import CompressedAirDashboardPage from "./CompressedAirDashboardPage";

vi.mock("../../../services/asset.service", () => ({ getAssetMeasurements: vi.fn(), listAssets: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));
const mockedAssets = vi.mocked(listAssets); const mockedMeasurements = vi.mocked(getAssetMeasurements);

function output(key: string, value: number | null, quantity: "power" | "flow_rate" | "pressure" | "energy" | "volume" | "ratio" | "cost", unit: "kW" | "Nm3/h" | "bar" | "kWh" | "Nm3" | "1"): AssetMeasurementProjection {
  return { binding: { id: key, owner_asset_id: "asset-1", boundary_asset_id: "asset-1", source_key: `plugin_output:plugin-1:${key}`, source: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: key }, semantic: { resource: "compressed_air", quantity, unit, precision: 3 }, meter_role: "direct", rollup_policy: "include" }, latest: { binding_id: key, source: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: key }, semantic: { resource: "compressed_air", quantity, unit, precision: 3 }, available: value !== null, value, quality: value === null ? "bad" : "good", observed_at: "2026-08-28T10:00:00Z", period_start: "2026-08-28T00:00:00Z", period_end: "2026-08-28T10:00:00Z" }, history: [], history_retention: "latest_only" };
}

describe("CompressedAirDashboardPage", () => {
  beforeEach(() => {
    mockedAssets.mockResolvedValue({ data: [{ id: "asset-1", parent_id: null, name: "Compressor room", kind: "area", description: null, enabled: true, timezone: null, position: 0, metadata: {}, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedMeasurements.mockResolvedValue({ asset_id: "asset-1", captured_at: "2026-08-28T10:00:01Z", measurements: [
      output("compressed_air.live_power", 80, "power", "kW"), output("compressed_air.live_flow", 400, "flow_rate", "Nm3/h"), output("compressed_air.live_pressure", 7, "pressure", "bar"),
      output("compressed_air.energy", 800, "energy", "kWh"), output("compressed_air.volume", 4_000, "volume", "Nm3"), output("compressed_air.sec", 0.2, "ratio", "1"), output("compressed_air.cost", 3_200, "cost", "1"), output("compressed_air.cost_per_volume", 0.8, "ratio", "1"),
      output("compressed_air.runtime_ratio", 0.9, "ratio", "1"), output("compressed_air.load_ratio", 0.75, "ratio", "1"), output("compressed_air.pressure_average", 6.8, "pressure", "bar"), output("compressed_air.pressure_stddev", 0.2, "pressure", "bar"), output("compressed_air.pressure_drop", 0.5, "pressure", "bar"),
      output("compressed_air.estimated_leak_flow", 40, "flow_rate", "Nm3/h"), output("compressed_air.estimated_leak_energy", 80, "energy", "kWh"), output("compressed_air.estimated_leak_cost", 320, "cost", "1"),
    ] });
  });
  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("renders Asset-bound efficiency, state, pressure and explicit baseline comparisons", async () => {
    render(<MemoryRouter initialEntries={["/utilities/compressed-air?asset=asset-1"]}><CompressedAirDashboardPage /></MemoryRouter>);
    const summary = await screen.findByRole("region", { name: "Compressed-air performance summary" });
    expect(within(summary).getByText("80 kW")).toBeInTheDocument(); expect(within(summary).getByText("400 Nm3/h")).toBeInTheDocument(); expect(within(summary).getByText("0.2 1")).toBeInTheDocument();
    expect(screen.getByText("75%")).toBeInTheDocument(); expect(screen.getByText("25%")).toBeInTheDocument(); expect(screen.getByText("10%")).toBeInTheDocument();
    expect(screen.getByText(/not an automatic leakage diagnosis/)).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Compressed-air comparison availability" })).toHaveTextContent("Time comparison unavailable");
    expect(screen.getByRole("region", { name: "Compressed-air source period" })).toHaveTextContent("Latest Plugin output");
  });

  it("fails metrics closed when Plugin output quality is unavailable", async () => {
    mockedMeasurements.mockResolvedValueOnce({ asset_id: "asset-1", captured_at: "2026-08-28T10:00:01Z", measurements: [output("compressed_air.sec", null, "ratio", "1")] });
    render(<MemoryRouter initialEntries={["/utilities/compressed-air?asset=asset-1"]}><CompressedAirDashboardPage /></MemoryRouter>);
    const summary = await screen.findByRole("region", { name: "Compressed-air performance summary" });
    expect(within(summary).getAllByText("Unavailable").length).toBeGreaterThan(0);
    expect(screen.getByText("Share of energy").closest("div")).toHaveTextContent("Unavailable");
  });

  it("switches hierarchy without reusing the previous Asset snapshot", async () => {
    mockedAssets.mockResolvedValueOnce({ data: [{ id: "asset-1", parent_id: null, name: "Room A", kind: "area", description: null, enabled: true, timezone: null, position: 0, metadata: {}, created_at: "", updated_at: "" }, { id: "asset-2", parent_id: null, name: "Room B", kind: "area", description: null, enabled: true, timezone: null, position: 1, metadata: {}, created_at: "", updated_at: "" }], pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 } });
    mockedMeasurements.mockResolvedValueOnce({ asset_id: "asset-1", captured_at: "", measurements: [output("compressed_air.live_power", 80, "power", "kW")] }).mockResolvedValueOnce({ asset_id: "asset-2", captured_at: "", measurements: [] });
    render(<MemoryRouter initialEntries={["/utilities/compressed-air?asset=asset-1"]}><CompressedAirDashboardPage /></MemoryRouter>);
    await screen.findByText("80 kW"); fireEvent.change(screen.getByLabelText("Compressed-air Asset"), { target: { value: "asset-2" } });
    expect(await screen.findByText("No compressed-air outputs bound")).toBeInTheDocument();
    await waitFor(() => expect(mockedMeasurements).toHaveBeenLastCalledWith("asset-2", expect.any(AbortSignal)));
  });
});
