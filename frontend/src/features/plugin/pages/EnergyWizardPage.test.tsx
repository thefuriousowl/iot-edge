// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import { createPlugin, getPlugin, updatePlugin } from "../../../services/plugin.service";
import type { DataLogger, DataLoggerTagReference } from "../../../types/datalogger";
import type { PluginInstance } from "../../../types/plugin";
import EnergyWizardPage from "./EnergyWizardPage";

vi.mock("../../../services/datalogger.service", () => ({ getDataLogger: vi.fn(), listDataLoggers: vi.fn() }));
vi.mock("../../../services/plugin.service", () => ({ createPlugin: vi.fn(), getPlugin: vi.fn(), updatePlugin: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedGetLogger = vi.mocked(getDataLogger);
const mockedListLoggers = vi.mocked(listDataLoggers);
const mockedCreate = vi.mocked(createPlugin);
const mockedGetPlugin = vi.mocked(getPlugin);
const mockedUpdate = vi.mocked(updatePlugin);

const tagNames = ["Hz", "TempSupply", "TempReturn", "Flow_m3h", "ThermalEnergy_W", "ThermalEnergy_kW", "VoltageA_N", "VoltageB_N", "VoltageC_N", "CurrentA", "CurrentB", "CurrentC", "ActivePowerTotal_kW"];
const tags: DataLoggerTagReference[] = tagNames.map((name, index) => ({ id: `tag-${index + 1}`, name, type: "reading", data_type: "float32", enabled: true }));
const logger: DataLogger = {
  id: "logger-1", name: "Demo Energy Logger", description: null, enabled: true, timezone: "Asia/Bangkok", mode: "interval", start_at: "2026-08-23T00:00:00Z", end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: tags.length, tags, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};
const plugin: PluginInstance = {
  id: "plugin-1", type: "energy_management", name: "Plant Energy", enabled: false, config_version: 1,
  config: { logger_id: logger.id, electrical_power_tags: [{ tag_id: "tag-13", unit: "kW" }], thermal_power_tags: [{ tag_id: "tag-6", unit: "kW" }], timezone: "Asia/Bangkok", max_gap_seconds: 120, tariff: { currency: "THB", rate_per_kwh: 4 } },
  runtime: { state: "stopped", started_at: null, last_transition_at: null, error: null }, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

function renderWizard(path = "/plugins/new/energy") {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/plugins" element={<h1>Plugin list</h1>} /><Route path="/plugins/new/energy" element={<EnergyWizardPage />} /><Route path="/plugins/:id/configure" element={<EnergyWizardPage />} /></Routes></MemoryRouter>);
}

async function chooseSource(name = "Demo Energy") {
  fireEvent.change(await screen.findByLabelText("Instance name"), { target: { value: name } });
  fireEvent.change(screen.getByLabelText("Data Logger"), { target: { value: logger.id } });
  await waitFor(() => expect(screen.getByText((_, element) => element?.tagName === "SPAN" && element.textContent === "13 synchronized Tags")).toBeInTheDocument());
  fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
  expect(await screen.findByRole("heading", { name: "Map power Tags" })).toBeInTheDocument();
}

describe("EnergyWizardPage", () => {
  beforeEach(() => {
    mockedGetLogger.mockReset(); mockedListLoggers.mockReset(); mockedCreate.mockReset(); mockedGetPlugin.mockReset(); mockedUpdate.mockReset();
    mockedListLoggers.mockResolvedValue({ data: [logger], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedGetLogger.mockResolvedValue(logger);
    mockedCreate.mockResolvedValue(plugin);
    mockedUpdate.mockResolvedValue(plugin);
  });
  afterEach(cleanup);

  it("creates an Energy instance from kW Tags without requiring a kWh Tag", async () => {
    renderWizard();
    await chooseSource();
    fireEvent.change(screen.getByLabelText("Electrical power Tag 1"), { target: { value: "tag-13" } });
    fireEvent.click(screen.getByRole("button", { name: "Add thermal Tag" }));
    fireEvent.change(screen.getByLabelText("Thermal power Tag 1"), { target: { value: "tag-6" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByRole("heading", { name: "Integration and tariff" })).toBeInTheDocument();
    expect(screen.getByText("∫ Electrical kW dt")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Flat rate per kWh"), { target: { value: "4.5" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByText("No kWh Tag required")).toBeInTheDocument();
    expect(screen.getByText("ActivePowerTotal_kW")).toBeInTheDocument();
    expect(screen.getByText("ThermalEnergy_kW")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Create Energy instance" }));
    await waitFor(() => expect(mockedCreate).toHaveBeenCalledWith({
      type: "energy_management", name: "Demo Energy", enabled: false,
      config: { logger_id: "logger-1", electrical_power_tags: [{ tag_id: "tag-13", unit: "kW" }], thermal_power_tags: [{ tag_id: "tag-6", unit: "kW" }], timezone: "Asia/Bangkok", max_gap_seconds: 120, tariff: { mode: "flat", currency: "THB", rate_per_kwh: 4.5 } },
    }));
    expect(await screen.findByRole("heading", { name: "Plugin list" })).toBeInTheDocument();
  });

  it("hydrates and updates an existing Energy configuration", async () => {
    mockedGetPlugin.mockResolvedValue(plugin);
    renderWizard("/plugins/plugin-1/configure");
    expect(await screen.findByDisplayValue("Plant Energy")).toBeInTheDocument();
    await waitFor(() => expect(screen.getByText((_, element) => element?.tagName === "SPAN" && element.textContent === "13 synchronized Tags")).toBeInTheDocument());
    fireEvent.change(screen.getByLabelText("Instance name"), { target: { value: "Updated Energy" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByDisplayValue("ActivePowerTotal_kW · float32")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save configuration" }));
    await waitFor(() => expect(mockedUpdate).toHaveBeenCalledWith("plugin-1", expect.objectContaining({ name: "Updated Energy", enabled: false, config: expect.objectContaining({ logger_id: "logger-1" }) })));
  });

  it("creates a synchronized Tag-based tariff without a fixed rate", async () => {
    renderWizard();
    await chooseSource("Dynamic tariff");
    fireEvent.change(screen.getByLabelText("Electrical power Tag 1"), { target: { value: "tag-13" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.change(await screen.findByLabelText("Tariff source"), { target: { value: "tag" } });
    fireEvent.change(await screen.findByLabelText(/Tariff Tag/), { target: { value: "tag-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByText("Hz")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Create Energy instance" }));
    await waitFor(() => expect(mockedCreate).toHaveBeenCalledWith(expect.objectContaining({
      config: expect.objectContaining({ tariff: { mode: "tag", currency: "THB", tag_id: "tag-1" } }),
    })));
  });

  it("blocks incomplete mappings and explains the calculated thermal option", async () => {
    renderWizard();
    fireEvent.click(await screen.findByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("alert")).toHaveTextContent("Instance name is required");
    await chooseSource();
    expect(screen.getByText(/Flow_m3h × 1.163/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("alert")).toHaveTextContent("Select at least one electrical power Tag");
  });
});
