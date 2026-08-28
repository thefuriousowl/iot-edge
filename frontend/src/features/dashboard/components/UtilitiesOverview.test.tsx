// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listAssets } from "../../../services/asset.service";
import { getEnergyOverview } from "../../../services/energy.service";
import { listPlugins } from "../../../services/plugin.service";
import UtilitiesOverview from "./UtilitiesOverview";

vi.mock("../../../services/asset.service", () => ({ listAssets: vi.fn() }));
vi.mock("../../../services/energy.service", () => ({ getEnergyOverview: vi.fn() }));
vi.mock("../../../services/plugin.service", () => ({ listPlugins: vi.fn() }));

const mockedListAssets = vi.mocked(listAssets);
const mockedGetEnergyOverview = vi.mocked(getEnergyOverview);
const mockedListPlugins = vi.mocked(listPlugins);

function summary(kwh: number, thermal: number, cost: number, coverage: number, skipped: number) {
  const energy = (value: number) => ({ kilowatt_hours: value, covered_seconds: 3600, skipped_seconds: 0, coverage_percent: coverage, segments: 4, skipped_segments: skipped, issues: null });
  return { from: "2026-08-01T00:00:00Z", to: "2026-08-28T00:00:00Z", electrical: energy(kwh), thermal: energy(thermal), cost: { value: cost, valid: true }, cop: { value: 2, valid: true } };
}

function overview(id: string, today: ReturnType<typeof summary>, month: ReturnType<typeof summary>, demand: number) {
  return {
    instance_id: id, logger_id: "logger-1", timezone: "Asia/Bangkok", currency: "THB", tariff_mode: "flat" as const, rate_per_kwh: 4,
    as_of: "2026-08-28T10:00:00Z",
    latest: { batch_at: "2026-08-28T10:00:00Z", electrical: { kilowatts: demand, valid: true, errors: null }, thermal: { kilowatts: 0, valid: true, errors: null }, tariff: { rate_per_kwh: 4, valid: true, errors: null }, cop: { value: 0, valid: false } },
    today, month,
  };
}

describe("UtilitiesOverview", () => {
  const renderOverview = () => render(<MemoryRouter><UtilitiesOverview /></MemoryRouter>);
  beforeEach(() => {
    mockedListAssets.mockResolvedValue({ data: [{ id: "asset-1", parent_id: null, name: "Building A", kind: "building", description: null, enabled: true, timezone: null, position: 0, metadata: {}, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedListPlugins.mockResolvedValue({ data: [
      { id: "energy-1", type: "energy_management", name: "Main incomer", enabled: true, config_version: 1, runtime: { state: "running", started_at: null, last_transition_at: null, error: null }, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z" },
      { id: "energy-2", type: "energy_management", name: "Chiller plant", enabled: true, config_version: 1, runtime: { state: "running", started_at: null, last_transition_at: null, error: null }, created_at: "2026-08-01T00:00:00Z", updated_at: "2026-08-01T00:00:00Z" },
    ], pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 } });
    mockedGetEnergyOverview.mockImplementation(async (id) => id === "energy-1"
      ? overview(id, summary(100, 20, 400, 100, 0), summary(1000, 200, 4000, 90, 2), 40)
      : overview(id, summary(50, 10, 200, 80, 2), summary(500, 100, 2000, 70, 4), 20));
  });

  afterEach(() => { cleanup(); vi.clearAllMocks(); });

  it("aggregates plant utility metrics and ranks contributors", async () => {
    renderOverview();
    const summaryRegion = await screen.findByRole("region", { name: "Utility summary" });
    expect(within(summaryRegion).getByText("150 kWh")).toBeInTheDocument();
    expect(within(summaryRegion).getByText("60 kW")).toBeInTheDocument();
    expect(within(summaryRegion).getByText("600 THB")).toBeInTheDocument();
    expect(within(summaryRegion).getByText("90%")).toBeInTheDocument();
    expect(screen.getByText("30 kWh")).toBeInTheDocument();
    const contributors = screen.getByRole("heading", { name: "Top contributors" }).closest("article");
    expect(within(contributors!).getAllByRole("listitem")[0]).toHaveTextContent("Main incomer100 kWh");
    expect(screen.getAllByText("Reporting")).toHaveLength(2);
  });

  it("switches period without refetching and protects unsupported hierarchy allocation", async () => {
    renderOverview();
    const summaryRegion = await screen.findByRole("region", { name: "Utility summary" });
    expect(within(summaryRegion).getByText("150 kWh")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Period"), { target: { value: "month" } });
    expect(within(summaryRegion).getByText("1,500 kWh")).toBeInTheDocument();
    expect(within(summaryRegion).getByText("6,000 THB")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Hierarchy"), { target: { value: "asset-1" } });
    expect(screen.getByText("No utility data for this hierarchy")).toBeInTheDocument();
    expect(screen.getByText(/plant totals are intentionally excluded/)).toBeInTheDocument();
    expect(mockedGetEnergyOverview).toHaveBeenCalledTimes(2);
  });

  it("shows a recoverable error state", async () => {
    mockedListPlugins.mockRejectedValueOnce(new Error("offline"));
    renderOverview();
    expect(await screen.findByRole("alert")).toHaveTextContent("Utility analytics unavailable");
    await waitFor(() => expect(screen.queryByRole("status")).not.toBeInTheDocument());
  });
});
