// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listDeviceInventory } from "../../../services/device.service";
import { listVGateways } from "../../../services/vgateway.service";
import type { DeviceInventoryItem, DeviceInventoryResponse } from "../../../types/device";
import type { VGatewayListResponse } from "../../../types/vgateway";
import DeviceListPage from "./DeviceListPage";

vi.mock("../../../services/device.service", () => ({
  listDeviceInventory: vi.fn(),
}));

vi.mock("../../../services/vgateway.service", () => ({
  listVGateways: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

const mockedListDeviceInventory = vi.mocked(listDeviceInventory);
const mockedListVGateways = vi.mocked(listVGateways);

const devices: DeviceInventoryItem[] = [
  {
    id: "11111111-1111-4111-8111-111111111111",
    vgateway_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    vgateway_name: "Factory Gateway",
    vgateway_type: "modbus_tcp",
    vgateway_enabled: true,
    name: "Main Meter",
    type: "modbus_device",
    description: "Incoming power meter",
    enabled: true,
    config: { unit_id: 7, poll_interval_ms: 1000, request_timeout_ms: null },
    datasource_count: 2,
    tag_count: 3,
    created_at: "2026-08-21T00:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
  {
    id: "22222222-2222-4222-8222-222222222222",
    vgateway_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    vgateway_name: "Boiler Gateway",
    vgateway_type: "modbus_tcp",
    vgateway_enabled: false,
    name: "Boiler PLC",
    type: "modbus_device",
    description: null,
    enabled: true,
    config: { unit_id: 1, poll_interval_ms: 500, request_timeout_ms: 1000 },
    datasource_count: 4,
    tag_count: 2,
    created_at: "2026-08-21T00:00:00Z",
    updated_at: "2026-08-22T03:00:00Z",
  },
];

const gatewayResponse: VGatewayListResponse = {
  data: [
    { id: devices[0].vgateway_id, name: "Factory Gateway", type: "modbus_tcp", description: null, enabled: true, status: "connected", device_count: 1, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
    { id: devices[1].vgateway_id, name: "Boiler Gateway", type: "modbus_tcp", description: null, enabled: false, status: "stopped", device_count: 1, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
  ],
  pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 },
};

function response(data: DeviceInventoryItem[] = devices, overrides?: Partial<DeviceInventoryResponse>): DeviceInventoryResponse {
  return {
    data,
    pagination: { page: 1, per_page: 20, total: data.length, total_pages: data.length > 0 ? 1 : 0 },
    ...overrides,
  };
}

function LocationProbe() {
  const location = useLocation();
  return <div data-testid="location-search">{location.search}</div>;
}

function renderPage(path = "/devices") {
  return render(<MemoryRouter initialEntries={[path]}><DeviceListPage /><LocationProbe /></MemoryRouter>);
}

function currentSearchParams(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId("location-search").textContent ?? "");
}

describe("DeviceListPage", () => {
  beforeEach(() => {
    mockedListDeviceInventory.mockReset();
    mockedListVGateways.mockReset();
    mockedListVGateways.mockResolvedValue(gatewayResponse);
  });

  afterEach(() => cleanup());

  it("shows loading, global metrics, hierarchy state, and topology links", async () => {
    let resolveInventory: ((value: DeviceInventoryResponse) => void) | undefined;
    mockedListDeviceInventory.mockReturnValue(new Promise((resolve) => { resolveInventory = resolve; }));
    renderPage();

    expect(screen.getByRole("status")).toHaveTextContent("Loading devices");
    resolveInventory?.(response());

    expect(await screen.findByText("Main Meter")).toBeInTheDocument();
    expect(screen.getByText("Boiler PLC")).toBeInTheDocument();
    expect(screen.getByText("Gateway paused")).toBeInTheDocument();
    expect(screen.getAllByText("Modbus TCP").length).toBeGreaterThan(0);
    expect(screen.getByText("2 datasources")).toBeInTheDocument();
    expect(screen.getByText("3 reading tags")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Devices" })).toHaveClass("active");
    expect(screen.getByRole("link", { name: "Open Main Meter topology" })).toHaveAttribute("href", `/vgateways/${devices[0].vgateway_id}#devices`);
    const summary = screen.getByRole("region", { name: "Device summary" });
    expect(within(summary).getByText("6")).toBeInTheDocument();
    expect(within(summary).getByText("1")).toBeInTheDocument();
    expect(mockedListDeviceInventory).toHaveBeenCalledWith({ vgateway_id: undefined, type: undefined, enabled: undefined, search: undefined, page: 1, per_page: 20 }, expect.any(AbortSignal));
    expect(mockedListVGateways).toHaveBeenCalledWith({ page: 1, per_page: 100 }, expect.any(AbortSignal));
  });

  it("applies gateway, protocol, state, and search filters through the URL", async () => {
    mockedListDeviceInventory.mockResolvedValue(response());
    renderPage();
    await screen.findByText("Main Meter");
    await waitFor(() => expect(screen.getByRole("option", { name: "Factory Gateway" })).toBeInTheDocument());

    fireEvent.change(screen.getByRole("combobox", { name: "vGateway" }), { target: { value: devices[0].vgateway_id } });
    fireEvent.change(screen.getByRole("combobox", { name: "Device type" }), { target: { value: "modbus_device" } });
    fireEvent.change(screen.getByRole("combobox", { name: "Enabled state" }), { target: { value: "enabled" } });
    fireEvent.change(screen.getByRole("searchbox", { name: "Search devices" }), { target: { value: " meter " } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));

    await waitFor(() => expect(mockedListDeviceInventory).toHaveBeenLastCalledWith({ vgateway_id: devices[0].vgateway_id, type: "modbus_device", enabled: true, search: "meter", page: 1, per_page: 20 }, expect.any(AbortSignal)));
    const params = currentSearchParams();
    expect(params.get("vgateway_id")).toBe(devices[0].vgateway_id);
    expect(params.get("type")).toBe("modbus_device");
    expect(params.get("enabled")).toBe("true");
    expect(params.get("search")).toBe("meter");
    expect(screen.getByText("4 active filters")).toBeInTheDocument();
  });

  it("hydrates pagination from the URL and resets page when a filter changes", async () => {
    mockedListDeviceInventory.mockResolvedValue(response(devices, { pagination: { page: 2, per_page: 20, total: 42, total_pages: 3 } }));
    renderPage(`/devices?enabled=false&page=2`);

    await screen.findByText("Main Meter");
    expect(mockedListDeviceInventory).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, page: 2 }), expect.any(AbortSignal));
    fireEvent.click(screen.getByRole("button", { name: "Previous page" }));
    await waitFor(() => expect(mockedListDeviceInventory).toHaveBeenLastCalledWith(expect.objectContaining({ page: 1 }), expect.any(AbortSignal)));
    fireEvent.change(screen.getByRole("combobox", { name: "Enabled state" }), { target: { value: "all" } });
    await waitFor(() => {
      expect(currentSearchParams().has("page")).toBe(false);
      expect(currentSearchParams().has("enabled")).toBe(false);
    });
  });

  it("shows failures, retries, and distinguishes filtered empty results", async () => {
    mockedListDeviceInventory.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(response([])).mockResolvedValueOnce(response());
    renderPage("/devices?search=missing");

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load devices");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No matching devices")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    expect(await screen.findByText("Main Meter")).toBeInTheDocument();
    expect(currentSearchParams().toString()).toBe("");
  });

  it("aborts inventory and gateway requests when leaving the page", async () => {
    mockedListDeviceInventory.mockReturnValue(new Promise(() => {}));
    mockedListVGateways.mockReturnValue(new Promise(() => {}));
    const rendered = renderPage();
    await waitFor(() => expect(mockedListDeviceInventory).toHaveBeenCalledOnce());
    const inventorySignal = mockedListDeviceInventory.mock.calls[0][1];
    const gatewaySignal = mockedListVGateways.mock.calls[0][1];

    rendered.unmount();
    expect(inventorySignal?.aborted).toBe(true);
    expect(gatewaySignal?.aborted).toBe(true);
  });
});
