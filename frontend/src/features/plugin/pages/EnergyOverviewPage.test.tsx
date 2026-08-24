// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getDataLogger } from "../../../services/datalogger.service";
import { getEnergyOverview, monitorEnergy } from "../../../services/energy.service";
import { getPlugin } from "../../../services/plugin.service";
import { expectNoAxeViolations } from "../../../test/axe";
import type { DataLogger, DataLoggerTagReference } from "../../../types/datalogger";
import type { EnergyLiveEvent, EnergyOverviewResponse, EnergyStreamContext, PluginInstance } from "../../../types/plugin";
import { useTagLiveStore } from "../../tag/stores/tagLive.store";
import EnergyOverviewPage from "./EnergyOverviewPage";

vi.mock("../../../services/datalogger.service", () => ({ getDataLogger: vi.fn() }));
vi.mock("../../../services/energy.service", () => ({ getEnergyOverview: vi.fn(), monitorEnergy: vi.fn() }));
vi.mock("../../../services/plugin.service", () => ({ getPlugin: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedGetDataLogger = vi.mocked(getDataLogger);
const mockedGetOverview = vi.mocked(getEnergyOverview);
const mockedMonitorEnergy = vi.mocked(monitorEnergy);
const mockedGetPlugin = vi.mocked(getPlugin);

const tags: DataLoggerTagReference[] = [
  { id: "tag-hz", name: "Hz", type: "reading", data_type: "float32", enabled: true },
  { id: "tag-temp-supply", name: "TempSupply", type: "reading", data_type: "float32", enabled: true },
  { id: "tag-temp-return", name: "TempReturn", type: "reading", data_type: "float32", enabled: true },
  { id: "tag-flow", name: "Flow_m3h", type: "reading", data_type: "float32", enabled: true },
  { id: "tag-power", name: "ActivePowerTotal_kW", type: "reading", data_type: "float32", enabled: true },
  { id: "tag-thermal", name: "ThermalEnergy_kW", type: "calculated", data_type: "float64", enabled: true },
  { id: "tag-tariff", name: "Tariff", type: "constant", data_type: "float64", enabled: true },
];

const logger: DataLogger = {
  id: "logger-1", name: "Demo Energy Logger", description: null, enabled: true, timezone: "Asia/Bangkok", mode: "interval",
  start_at: "2026-08-23T00:00:00Z", end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: tags.length, tags,
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

const plugin: PluginInstance = {
  id: "plugin-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1,
  config: {
    logger_id: logger.id,
    electrical_power_tags: [{ tag_id: "tag-power", unit: "kW" }],
    thermal_power_tags: [{ tag_id: "tag-thermal", unit: "kW" }],
    timezone: "Asia/Bangkok", max_gap_seconds: 120,
    tariff: { mode: "tag", currency: "THB", tag_id: "tag-tariff" },
  },
  runtime: { state: "running", started_at: "2026-08-23T00:00:00Z", last_transition_at: "2026-08-23T00:00:00Z", error: null },
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

const demand = { kilowatts: 12.5, valid: true, errors: null };
const latest = {
  batch_at: "2026-08-23T03:30:00Z",
  electrical: demand,
  thermal: { ...demand, kilowatts: 37.5 },
  tariff: { rate_per_kwh: 4.5, valid: true, errors: null },
  cop: { value: 3, valid: true },
};
const todayElectrical = { kilowatt_hours: 300, covered_seconds: 86_400, skipped_seconds: 0, coverage_percent: 100, segments: 1_440, skipped_segments: 0, issues: null };
const today = { from: "2026-08-22T17:00:00Z", to: "2026-08-23T17:00:00Z", electrical: todayElectrical, thermal: { ...todayElectrical, kilowatt_hours: 900 }, cost: { value: 1_350, valid: true }, cop: { value: 3, valid: true } };
const month = { ...today, from: "2026-07-31T17:00:00Z", electrical: { ...todayElectrical, kilowatt_hours: 6_300, coverage_percent: 98.7 }, thermal: { ...todayElectrical, kilowatt_hours: 18_900, coverage_percent: 98.7 }, cost: { value: 28_350, valid: true } };
const overview: EnergyOverviewResponse = {
  instance_id: plugin.id, logger_id: logger.id, timezone: "Asia/Bangkok", currency: "THB", tariff_mode: "tag", tariff_tag_id: "tag-tariff", rate_per_kwh: 0,
  as_of: latest.batch_at, latest, today, month,
};

let streamContext: EnergyStreamContext | null;

function renderPage() {
  return render(<MemoryRouter initialEntries={["/plugins/plugin-1/energy"]}><Routes><Route path="/plugins/:id/energy" element={<EnergyOverviewPage />} /></Routes></MemoryRouter>);
}

describe("EnergyOverviewPage", () => {
  beforeEach(() => {
    streamContext = null;
    useTagLiveStore.getState().reset();
    mockedGetPlugin.mockReset();
    mockedGetOverview.mockReset();
    mockedGetDataLogger.mockReset();
    mockedMonitorEnergy.mockReset();
    mockedGetPlugin.mockResolvedValue(plugin);
    mockedGetOverview.mockResolvedValue(overview);
    mockedGetDataLogger.mockResolvedValue(logger);
    mockedMonitorEnergy.mockImplementation((_id, context) => {
      streamContext = context;
      context.onOpen();
      return new Promise<void>((resolve) => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    });
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("renders accessible live demand, synchronized periods, dynamic tariff, and Logger Tags", async () => {
    useTagLiveStore.getState().setConnectionState("live");
    useTagLiveStore.getState().ingest({ tag_id: "tag-hz", sequence: 1, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "good", data_type: "float32", value: 49.98 });
    useTagLiveStore.getState().ingest({ tag_id: "tag-temp-supply", sequence: 2, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "good", data_type: "float32", value: 7.2 });
    useTagLiveStore.getState().ingest({ tag_id: "tag-temp-return", sequence: 3, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "good", data_type: "float32", value: 12.5 });
    useTagLiveStore.getState().ingest({ tag_id: "tag-flow", sequence: 4, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "good", data_type: "float32", value: 12.8 });
    useTagLiveStore.getState().ingest({ tag_id: "tag-tariff", sequence: 5, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "good", data_type: "float64", value: 4.5 });
    const { container } = renderPage();

    expect(await screen.findByRole("heading", { name: "Plant Energy" })).toBeInTheDocument();
    const liveRegion = screen.getByRole("region", { name: "Live operational metrics" });
    expect(within(liveRegion).getByText("12.50 kW")).toBeInTheDocument();
    expect(within(liveRegion).getByText("37.50 kW")).toBeInTheDocument();
    expect(within(liveRegion).getByText("4.50 THB/kWh")).toBeInTheDocument();
    const thermalRegion = screen.getByRole("region", { name: "Thermal circuit" });
    expect(within(thermalRegion).getByText("7.20 °C")).toBeInTheDocument();
    expect(within(thermalRegion).getByText("12.50 °C")).toBeInTheDocument();
    expect(within(thermalRegion).getByText("5.30 °C")).toBeInTheDocument();
    expect(within(thermalRegion).getByText("12.80 m³/h")).toBeInTheDocument();
    expect(screen.getByText("300.00 kWh")).toBeInTheDocument();
    expect(screen.getByText("6,300.00 kWh")).toBeInTheDocument();
    expect(screen.getByText("28,350.00 THB")).toBeInTheDocument();
    const openTagLink = screen.getByRole("link", { name: "Open Hz" });
    expect(openTagLink).toHaveAttribute("href", "/tags/tag-hz");
    openTagLink.focus();
    expect(openTagLink).toHaveFocus();
    expect(screen.getByText("49.98")).toBeInTheDocument();
    expect(screen.getByText("THB/kWh", { selector: "code" })).toBeInTheDocument();
    expect(screen.getByText("All available sources healthy")).toBeInTheDocument();
    expect(mockedMonitorEnergy).toHaveBeenCalledWith("plugin-1", expect.objectContaining({ lastEventId: null }));
    await expectNoAxeViolations(container);
  });

  it("replaces persisted latest metrics with newer synchronized SSE batches", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant Energy" });
    const event: EnergyLiveEvent = {
      id: "generation:22", sequence: 22, instance_id: plugin.id,
      metrics: { ...latest, batch_at: "2026-08-23T03:31:00Z", electrical: { ...demand, kilowatts: 14 }, thermal: { ...demand, kilowatts: 42 }, tariff: { rate_per_kwh: 5.25, valid: true, errors: null }, cop: { value: 3.1, valid: true } },
    };
    act(() => streamContext?.onMessage(event));
    const liveRegion = screen.getByRole("region", { name: "Live operational metrics" });
    expect(within(liveRegion).getByText("14.00 kW")).toBeInTheDocument();
    expect(within(liveRegion).getByText("42.00 kW")).toBeInTheDocument();
    expect(within(liveRegion).getByText("5.25 THB/kWh")).toBeInTheDocument();
    expect(within(liveRegion).getByText("3.10")).toBeInTheDocument();
  });

  it("preserves the last Logger batch and labels a disconnected Energy stream", async () => {
    mockedMonitorEnergy.mockRejectedValueOnce(new Error("stream offline"));
    renderPage();
    expect(await screen.findByText("Energy stream disconnected")).toBeInTheDocument();
    expect(screen.getByText(/Showing the last synchronized Logger batch/)).toBeInTheDocument();
    expect(screen.getByText(/No live Tag value is substituted/)).toBeInTheDocument();
    expect(screen.getByRole("region", { name: "Live operational metrics" })).toHaveClass("is-stale");
    expect(screen.getByText("12.50 kW")).toBeInTheDocument();
  });

  it("manually reconnects without discarding displayed Energy metrics", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant Energy" });
    fireEvent.click(screen.getByRole("button", { name: "Reconnect Energy stream" }));
    await waitFor(() => expect(mockedMonitorEnergy).toHaveBeenCalledTimes(2));
    expect(screen.getByText("12.50 kW")).toBeInTheDocument();
  });

  it("keeps Energy visible while surfacing protocol and coverage errors", async () => {
    const sourceError = { code: "source_bad" as const, tag_id: "tag-power", at: latest.batch_at, message: "Modbus exception 0x02: Illegal Data Address" };
    mockedGetOverview.mockResolvedValue({
      ...overview,
      latest: { ...latest, electrical: { kilowatts: 0, valid: false, errors: [sourceError] } },
      today: { ...today, electrical: { ...today.electrical, coverage_percent: 75, skipped_segments: 2, issues: [{ from: today.from, to: today.to, code: "source_bad", errors: [sourceError] }] }, cost: { value: 0, valid: false, error: "tariff Tag coverage is incomplete" } },
    });
    renderPage();
    expect(await screen.findByText("Coverage needs attention")).toBeInTheDocument();
    expect(screen.getAllByText("Modbus exception 0x02: Illegal Data Address").length).toBeGreaterThan(0);
    expect(screen.getAllByText("75.0%")).toHaveLength(2);
    expect(screen.getByText("Unavailable", { selector: ".energy-live-band strong" })).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Live Logger Tags (7)" })).toBeInTheDocument();
  });

  it("marks incomplete tariff history as a quality issue without hiding Energy and COP", async () => {
    mockedGetOverview.mockResolvedValue({
      ...overview,
      today: { ...today, cost: { value: 0, valid: false, error: "tariff Tag coverage is incomplete" } },
    });
    renderPage();
    expect(await screen.findByText("Coverage needs attention")).toBeInTheDocument();
    expect(screen.getAllByText("tariff Tag coverage is incomplete").length).toBeGreaterThan(0);
    expect(screen.getByText("cost_unavailable", { selector: "code" })).toBeInTheDocument();
    expect(screen.getByText("12.50 kW")).toBeInTheDocument();
    expect(screen.getAllByText("3.00").length).toBeGreaterThan(0);
  });

  it("retries a failed overview without discarding the page contract", async () => {
    mockedGetPlugin.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(plugin);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load the Energy overview");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Plant Energy" })).toBeInTheDocument();
    expect(mockedGetPlugin).toHaveBeenCalledTimes(2);
    expect(mockedGetOverview).toHaveBeenCalledTimes(2);
  });

  it("shows bad Tag quality and the original runtime error", async () => {
    useTagLiveStore.getState().setConnectionState("live");
    useTagLiveStore.getState().ingest({ tag_id: "tag-temp-supply", sequence: 3, observed_at: latest.batch_at, stored_at: latest.batch_at, quality: "bad", data_type: "float32", value: null, error: "sensor unavailable" });
    renderPage();
    expect(await screen.findByText("sensor unavailable")).toBeInTheDocument();
    expect(screen.getByText("error", { selector: ".energy-tag-quality" })).toBeInTheDocument();
    expect(screen.getAllByText("°C", { selector: "code" })).toHaveLength(2);
    expect(within(screen.getByRole("region", { name: "Thermal circuit" })).getAllByText("Unavailable").length).toBeGreaterThan(0);
  });
});
