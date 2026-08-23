// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { deletePlugin, disablePlugin, enablePlugin, listPlugins, listPluginTypes, restartPlugin } from "../../../services/plugin.service";
import type { PluginInstanceSummary, PluginListResponse } from "../../../types/plugin";
import PluginListPage from "./PluginListPage";

vi.mock("../../../services/plugin.service", () => ({ deletePlugin: vi.fn(), disablePlugin: vi.fn(), enablePlugin: vi.fn(), listPlugins: vi.fn(), listPluginTypes: vi.fn(), restartPlugin: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedDelete = vi.mocked(deletePlugin); const mockedDisable = vi.mocked(disablePlugin); const mockedEnable = vi.mocked(enablePlugin); const mockedList = vi.mocked(listPlugins); const mockedTypes = vi.mocked(listPluginTypes); const mockedRestart = vi.mocked(restartPlugin);
const running = { state: "running" as const, started_at: "2026-08-23T00:00:00Z", last_transition_at: "2026-08-23T00:00:00Z", error: null };
const plugins: PluginInstanceSummary[] = [
  { id: "plugin-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1, runtime: running, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" },
  { id: "plugin-2", type: "energy_management", name: "Boiler Energy", enabled: false, config_version: 1, runtime: { state: "stopped", started_at: null, last_transition_at: null, error: null }, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" },
];
const manifest = { type: "energy_management" as const, name: "Energy Management", description: "Energy", version: "0.1.0", config_version: 1, multiple_instances: true, capabilities: ["logger.committed_batches" as const] };
function response(data = plugins, total = data.length): PluginListResponse { return { data, pagination: { page: 1, per_page: 20, total, total_pages: total > 0 ? 1 : 0 } }; }
function renderPage() { return render(<MemoryRouter initialEntries={["/plugins"]}><PluginListPage /></MemoryRouter>); }

describe("PluginListPage", () => {
  beforeEach(() => { mockedDelete.mockReset(); mockedDisable.mockReset(); mockedEnable.mockReset(); mockedList.mockReset(); mockedRestart.mockReset(); mockedTypes.mockReset(); mockedTypes.mockResolvedValue({ data: [manifest] }); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("shows loading, desired/runtime states, summary, and manifests", async () => {
    let resolveList: ((value: PluginListResponse) => void) | undefined;
    mockedList.mockReturnValue(new Promise((resolve) => { resolveList = resolve; }));
    renderPage();
    expect(screen.getByRole("status")).toHaveTextContent("Loading Plugins");
    resolveList?.(response());
    expect(await screen.findByText("Plant Energy")).toBeInTheDocument();
    expect(screen.getByText("Boiler Energy")).toBeInTheDocument();
    expect(screen.getByRole("option", { name: "Energy Management" })).toBeInTheDocument();
    expect(screen.getByText("Running")).toBeInTheDocument();
    expect(screen.getByText("Enabled", { selector: ".plugin-desired" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "New Energy instance" })).toHaveAttribute("href", "/plugins/new/energy");
    expect(screen.getByRole("link", { name: "Configure Plant Energy" })).toHaveAttribute("href", "/plugins/plugin-1/configure");
  });

  it("requests search, type, desired state, and pagination filters", async () => {
    mockedList.mockResolvedValue(response(plugins, 22));
    renderPage(); await screen.findByText("Plant Energy");
    fireEvent.change(screen.getByLabelText("Desired state"), { target: { value: "false" } });
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith(expect.objectContaining({ enabled: false, page: 1 }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByLabelText("Plugin type"), { target: { value: "energy_management" } });
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith(expect.objectContaining({ type: "energy_management" }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByRole("searchbox", { name: "Search Plugins" }), { target: { value: "plant" } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith(expect.objectContaining({ search: "plant" }), expect.any(AbortSignal)));
  });

  it("enables, disables, restarts, and deletes with refresh", async () => {
    mockedList.mockResolvedValue(response()); mockedEnable.mockResolvedValue({ ...plugins[1], enabled: true, config: {}, runtime: running }); mockedDisable.mockResolvedValue({ ...plugins[0], enabled: false, config: {}, runtime: plugins[1].runtime }); mockedRestart.mockResolvedValue({ id: plugins[0].id, type: plugins[0].type, enabled: true, runtime: running }); mockedDelete.mockResolvedValue(); vi.spyOn(window, "confirm").mockReturnValue(true);
    renderPage(); await screen.findByText("Plant Energy");
    fireEvent.click(screen.getByRole("button", { name: "Enable Boiler Energy" }));
    await waitFor(() => expect(mockedEnable).toHaveBeenCalledWith("plugin-2"));
    fireEvent.click(screen.getByRole("button", { name: "Disable Plant Energy" }));
    await waitFor(() => expect(mockedDisable).toHaveBeenCalledWith("plugin-1"));
    fireEvent.click(screen.getByRole("button", { name: "Restart Plant Energy" }));
    await waitFor(() => expect(mockedRestart).toHaveBeenCalledWith("plugin-1"));
    fireEvent.click(screen.getByRole("button", { name: "Delete Boiler Energy" }));
    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith("plugin-2"));
  });

  it("shows API errors, retries, and renders the empty state", async () => {
    mockedList.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(response([]));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Plugins");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No Plugin instances found")).toBeInTheDocument();
  });
});
