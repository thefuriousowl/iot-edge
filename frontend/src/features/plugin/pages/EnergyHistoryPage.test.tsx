// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { expectNoAxeViolations } from "../../../test/axe";

import { getEnergyHistory } from "../../../services/energy.service";
import { getAssetBindings, listAssets } from "../../../services/asset.service";
import { getPlugin } from "../../../services/plugin.service";
import type { EnergyHistoryResponse, EnergyPeriodSummary, PluginInstance } from "../../../types/plugin";
import EnergyHistoryPage from "./EnergyHistoryPage";

vi.mock("../../../services/energy.service", () => ({ getEnergyHistory: vi.fn() }));
vi.mock("../../../services/asset.service", () => ({ getAssetBindings: vi.fn(), listAssets: vi.fn() }));
vi.mock("../../../services/plugin.service", () => ({ getPlugin: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));
vi.mock("../components/ProductionLineChart", () => ({ default: ({ ariaLabel }: { ariaLabel: string }) => <div role="img" aria-label={ariaLabel} /> }));
vi.mock("../components/ProductionBarChart", () => ({ default: ({ ariaLabel }: { ariaLabel: string }) => <div role="img" aria-label={ariaLabel} /> }));
vi.mock("../components/ProductionDonutChart", () => ({ default: ({ ariaLabel }: { ariaLabel: string }) => <div role="img" aria-label={ariaLabel} /> }));
vi.mock("../components/HeatmapRankingView", () => ({ default: () => <div data-testid="heatmap-ranking">Heatmap ranking</div> }));
vi.mock("recharts", () => {
  const Container = ({ children }: { children?: React.ReactNode }) => <div>{children}</div>;
  const Empty = () => null;
  return {
    ResponsiveContainer: Container,
    AreaChart: Container,
    BarChart: Container,
    LineChart: Container,
    PieChart: Container,
    Area: Empty,
    Bar: Empty,
    CartesianGrid: Empty,
    Legend: Empty,
    Line: Empty,
    Pie: Empty,
    Tooltip: Empty,
    XAxis: Empty,
    YAxis: Empty,
  };
});

const mockedAssetBindings = vi.mocked(getAssetBindings);
const mockedAssets = vi.mocked(listAssets);
const mockedHistory = vi.mocked(getEnergyHistory);
const mockedPlugin = vi.mocked(getPlugin);

const plugin: PluginInstance = {
  id: "plugin-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1,
  config: { logger_id: "logger-1", electrical_power_tags: [{ tag_id: "power", unit: "kW" }], thermal_power_tags: [{ tag_id: "thermal", unit: "kW" }], timezone: "Asia/Bangkok", max_gap_seconds: 120, tariff: { mode: "tag", currency: "THB", tag_id: "tariff" } },
  runtime: { state: "running", started_at: "2026-08-23T00:00:00Z", last_transition_at: "2026-08-23T00:00:00Z", error: null },
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

function period(from: string, electrical: number, thermal: number): EnergyPeriodSummary {
  const to = new Date(new Date(from).getTime() + 60 * 60 * 1000).toISOString();
  return {
    from, to,
    electrical: { kilowatt_hours: electrical, covered_seconds: 3_600, skipped_seconds: 0, coverage_percent: 100, segments: 60, skipped_segments: 0, issues: null },
    thermal: { kilowatt_hours: thermal, covered_seconds: 3_600, skipped_seconds: 0, coverage_percent: 100, segments: 60, skipped_segments: 0, issues: null },
    cost: { value: electrical * 4.5, valid: true }, cop: { value: thermal / electrical, valid: true },
  };
}

const history: EnergyHistoryResponse = {
  instance_id: plugin.id, logger_id: "logger-1", timezone: "Asia/Bangkok", bucket: "1h", requested_bucket: "1h", downsampled: false, point_limit: 500, currency: "THB", tariff_mode: "tag", tariff_tag_id: "tariff", rate_per_kwh: 0,
  data: [period("2026-08-23T02:00:00Z", 20, 60), period("2026-08-23T01:00:00Z", 10, 30)],
  pagination: { page: 1, per_page: 500, total: 2, total_pages: 1 },
};

function Probe() { return <span data-testid="search">{useLocation().search}</span>; }
function renderPage(path = "/plugins/plugin-1/energy/history?from=2026-08-23T01%3A00%3A00.000Z&to=2026-08-23T03%3A00%3A00.000Z&bucket=1h") {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/plugins/:id/energy/history" element={<><EnergyHistoryPage /><Probe /></>} /></Routes></MemoryRouter>);
}

describe("EnergyHistoryPage", () => {
  beforeEach(() => {
    mockedHistory.mockReset(); mockedPlugin.mockReset();
    mockedAssets.mockReset(); mockedAssetBindings.mockReset();
    mockedPlugin.mockResolvedValue(plugin); mockedHistory.mockResolvedValue(history);
    mockedAssets.mockResolvedValue({ data: [{ id: "asset-1", parent_id: null, name: "Chiller Plant", kind: "system", description: null, enabled: true, timezone: null, position: 0, metadata: {}, created_at: "", updated_at: "" }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedAssetBindings.mockResolvedValue({ data: [{ id: "binding-1", owner_asset_id: "asset-1", boundary_asset_id: "asset-1", source_key: "plugin_output:plugin-1:thermal_output_kw", source: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "thermal_output_kw" }, semantic: { resource: "thermal", quantity: "power", unit: "kW", precision: 2 }, meter_role: "direct", rollup_policy: "include" }] });
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:energy") });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() });
    vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {});
  });

  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders Plugin-scoped summaries, charts, quality table, and navigation", async () => {
    renderPage();
    expect(await screen.findByRole("heading", { name: "Plant Energy" })).toBeInTheDocument();
    expect(mockedHistory).toHaveBeenCalledWith("plugin-1", { from: "2026-08-23T01:00:00.000Z", to: "2026-08-23T03:00:00.000Z", bucket: "1h", page: 1, per_page: 500 }, expect.any(AbortSignal));
    const summary = screen.getByRole("region", { name: "Selected range summary" });
    expect(within(summary).getByText("30.00 kWh")).toBeInTheDocument();
    expect(within(summary).getByText("90.00 kWh")).toBeInTheDocument();
    expect(within(summary).getByText("135.00 THB")).toBeInTheDocument();
    expect(within(summary).getByText("3.00")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /comparing electrical and thermal/i })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /coefficient of performance/i })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /estimated cost/i })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /average electrical demand/i })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /donut chart of cost contribution/i })).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /data coverage/i })).toBeInTheDocument();
    expect(screen.getByTestId("heatmap-ranking")).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Electricity source period" })).toHaveTextContent("Logger logger-1");
    const thermal = screen.getByRole("region", { name: "Thermal performance summary" });
    expect(within(thermal).getByText("90.00 kWh")).toBeInTheDocument();
    expect(within(thermal).getByText("45.00 kW")).toBeInTheDocument();
    expect(within(thermal).getByText("15.00 kW")).toBeInTheDocument();
    expect(screen.getByRole("img", { name: /average thermal output and electrical input/i })).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Thermal circuit history availability" })).toHaveTextContent("does not persist these circuit inputs");
    expect(screen.getByRole("link", { name: "Open live thermal circuit" })).toHaveAttribute("href", "/plugins/plugin-1/energy");
    expect(screen.getByRole("link", { name: "Overview" })).toHaveAttribute("href", "/plugins/plugin-1/energy");
    expect(screen.getByRole("link", { name: "History & charts" })).toHaveAttribute("aria-current", "page");
    expect(screen.getByText("2 buckets")).toBeInTheDocument();
  });

  it("has no automated accessibility violations in the populated dashboard", async () => {
    const { container } = renderPage();
    await screen.findByRole("heading", { name: "Plant Energy" });
    await waitFor(() => expect(screen.getByText("2 buckets")).toBeInTheDocument());
    await expectNoAxeViolations(container);
  });

  it("applies an exact Plugin-timezone range and automatically selects a safe bucket", async () => {
    renderPage(); await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-23T08:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-23T10:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Run analysis" }));
    await waitFor(() => expect(screen.getByTestId("search")).toHaveTextContent("from=2026-08-23T01%3A00%3A00.000Z"));
    await waitFor(() => expect(mockedHistory).toHaveBeenLastCalledWith("plugin-1", expect.objectContaining({ from: "2026-08-23T01:00:00.000Z", to: "2026-08-23T03:00:00.000Z", bucket: "1m" }), expect.any(AbortSignal)));
  });

  it("removes resolutions that would exceed the bounded Plugin chart payload", async () => {
    renderPage("/plugins/plugin-1/energy/history?from=2026-08-01T00%3A00%3A00.000Z&to=2026-08-31T00%3A00%3A00.000Z");
    await screen.findByRole("heading", { name: "Plant Energy" });
    const resolution = screen.getByRole("combobox", { name: "Chart resolution" });
    expect(within(resolution).queryByRole("option", { name: "1 hour" })).not.toBeInTheDocument();
    expect(within(resolution).getByRole("option", { name: "6 hours" })).toBeInTheDocument();
    expect(mockedHistory).toHaveBeenCalledWith("plugin-1", expect.objectContaining({ bucket: "6h", per_page: 500 }), expect.any(AbortSignal));
  });

  it("exports the visible dashboard data with active filter provenance", async () => {
    renderPage(); await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.click(screen.getByRole("button", { name: "Export CSV" }));
    await waitFor(() => expect(URL.createObjectURL).toHaveBeenCalledWith(expect.objectContaining({ type: "text/csv;charset=utf-8" })));
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalled();
  });

  it("compares the previous equal period and persists comparison in the URL", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.click(screen.getByRole("checkbox", { name: "Compare previous equal period" }));
    await waitFor(() => expect(mockedHistory).toHaveBeenCalledTimes(3));
    expect(mockedHistory).toHaveBeenLastCalledWith("plugin-1", { from: "2026-08-22T23:00:00.000Z", to: "2026-08-23T01:00:00.000Z", bucket: "1h", page: 1, per_page: 500 }, expect.any(AbortSignal));
    expect(screen.getByTestId("search")).toHaveTextContent("compare=previous");
    expect(screen.getByRole("region", { name: "Electricity source period" })).toHaveTextContent("30.00 kWh previous period");
  });

  it("persists display timezone and mapped Asset hierarchy filters in the URL", async () => {
    renderPage(); await screen.findByRole("heading", { name: "Plant Energy" });
    await waitFor(() => expect(screen.getByRole("combobox", { name: "Dashboard hierarchy" })).not.toBeDisabled());
    fireEvent.change(screen.getByRole("combobox", { name: "Display timezone" }), { target: { value: "UTC" } });
    await waitFor(() => expect(screen.getByTestId("search")).toHaveTextContent("timezone=UTC"));
    fireEvent.change(screen.getByRole("combobox", { name: "Dashboard hierarchy" }), { target: { value: "asset-1" } });
    await waitFor(() => expect(screen.getByTestId("search")).toHaveTextContent("asset=asset-1"));
    expect(screen.getByRole("region", { name: "Selected range summary" })).toBeInTheDocument();
  });

  it("fails hierarchy closed when the selected Asset does not own this Plugin output", async () => {
    renderPage("/plugins/plugin-1/energy/history?from=2026-08-23T01%3A00%3A00.000Z&to=2026-08-23T03%3A00%3A00.000Z&bucket=1h&asset=other");
    expect(await screen.findByText("No Energy data for this hierarchy")).toBeInTheDocument();
    expect(screen.queryByRole("region", { name: "Selected range summary" })).not.toBeInTheDocument();
  });

  it("fails cost closed while preserving Energy and explicit quality", async () => {
    mockedHistory.mockResolvedValue({ ...history, data: [{ ...history.data[0], cost: { value: 0, valid: false, error: "tariff Tag coverage is incomplete" } }, history.data[1]] });
    renderPage();
    const summary = await screen.findByRole("region", { name: "Selected range summary" });
    expect(within(summary).getByText("Unavailable")).toBeInTheDocument();
    expect(within(summary).getByText("30.00 kWh")).toBeInTheDocument();
    expect(within(summary).getByText("1", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByText("Unavailable", { selector: ".energy-history-table .is-unavailable" })).toBeInTheDocument();
  });

  it("does not fabricate thermal output when the selected period has no coverage", async () => {
    mockedHistory.mockResolvedValue({ ...history, data: [{ ...history.data[0], thermal: { ...history.data[0].thermal, kilowatt_hours: 0, covered_seconds: 0, skipped_seconds: 3_600, coverage_percent: 0, skipped_segments: 60 } }] });
    renderPage();
    const thermal = await screen.findByRole("region", { name: "Thermal performance summary" });
    const average = within(thermal).getByText("Average thermal output").closest("article");
    expect(average).not.toBeNull();
    expect(within(average!).getByText("Unavailable")).toBeInTheDocument();
    expect(within(thermal).getByText("0.0% coverage")).toBeInTheDocument();
  });

  it("retries sanitized Plugin API failures", async () => {
    mockedHistory.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(history);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Energy history");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Plant Energy" })).toBeInTheDocument();
    expect(mockedHistory).toHaveBeenCalledTimes(2);
  });

  it("keeps old charts visibly marked while a replacement range loads", async () => {
    let resolveReplacement: ((value: EnergyHistoryResponse) => void) | undefined;
    mockedHistory.mockResolvedValueOnce(history).mockImplementationOnce(() => new Promise((resolve) => { resolveReplacement = resolve; }));
    renderPage(); await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-23T08:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-23T10:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Run analysis" }));
    expect(await screen.findByText("Updating Energy history…")).toBeInTheDocument();
    expect(screen.getByText(/Current charts remain visible/)).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Export CSV" })).toBeDisabled();
    expect(document.querySelector(".energy-history-content")).toHaveAttribute("aria-busy", "true");
    resolveReplacement?.({ ...history, data: [period("2026-08-23T02:00:00Z", 40, 120)], pagination: { ...history.pagination, total: 1 } });
    await waitFor(() => expect(within(screen.getByRole("region", { name: "Selected range summary" })).getByText("40.00 kWh")).toBeInTheDocument());
    expect(screen.queryByText("Updating Energy history…")).not.toBeInTheDocument();
  });

  it("surfaces a replacement query failure while retaining the previous charts", async () => {
    mockedHistory.mockResolvedValueOnce(history).mockRejectedValueOnce(new Error("offline"));
    renderPage(); await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.click(screen.getByRole("button", { name: "Today" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Energy history");
    expect(screen.getByRole("region", { name: "Selected range summary" })).toHaveTextContent("30.00 kWh");
    expect(screen.getByRole("img", { name: /comparing electrical and thermal/i })).toBeInTheDocument();
  });
});
