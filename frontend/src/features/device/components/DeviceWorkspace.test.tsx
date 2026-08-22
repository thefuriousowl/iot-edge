// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  createDatasource,
  createDevice,
  deleteDatasource,
  deleteDevice,
  listDatasources,
  listDevices,
  monitorDatasource,
  previewDatasource,
  previewSavedDatasource,
  updateDatasource,
  updateDevice,
} from "../../../services/device.service";
import type { Datasource, Device } from "../../../types/device";
import DeviceWorkspace from "./DeviceWorkspace";

vi.mock("../../../services/device.service", () => ({
  createDatasource: vi.fn(), createDevice: vi.fn(), deleteDatasource: vi.fn(), deleteDevice: vi.fn(), listDatasources: vi.fn(), listDevices: vi.fn(), monitorDatasource: vi.fn(), previewDatasource: vi.fn(), previewSavedDatasource: vi.fn(), updateDatasource: vi.fn(), updateDevice: vi.fn(),
}));

const mockedCreateDatasource = vi.mocked(createDatasource);
const mockedCreateDevice = vi.mocked(createDevice);
const mockedListDatasources = vi.mocked(listDatasources);
const mockedListDevices = vi.mocked(listDevices);
const mockedMonitorDatasource = vi.mocked(monitorDatasource);
const mockedPreviewDatasource = vi.mocked(previewDatasource);
const mockedPreviewSavedDatasource = vi.mocked(previewSavedDatasource);
const mockedDeleteDatasource = vi.mocked(deleteDatasource);
const mockedDeleteDevice = vi.mocked(deleteDevice);
const mockedUpdateDatasource = vi.mocked(updateDatasource);
const mockedUpdateDevice = vi.mocked(updateDevice);

const device: Device = { id: "device-1", vgateway_id: "gateway-1", name: "Power meter", type: "modbus_device", description: null, enabled: true, config: { unit_id: 7, request_timeout_ms: null }, datasource_count: 1, tag_count: 0, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-21T00:00:00Z" };
const datasource: Datasource = { id: "source-1", device_id: device.id, name: "Voltage registers", type: "modbus_read", description: null, enabled: true, status: "idle", config: { function_code: 3, start_address: 10, quantity: 2, poll_interval_ms: 60000 }, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-21T00:00:00Z" };

describe("DeviceWorkspace", () => {
  beforeEach(() => {
    for (const mock of [mockedCreateDatasource, mockedCreateDevice, mockedListDatasources, mockedListDevices, mockedMonitorDatasource, mockedPreviewDatasource, mockedPreviewSavedDatasource, mockedDeleteDatasource, mockedDeleteDevice, mockedUpdateDatasource, mockedUpdateDevice]) mock.mockReset();
    mockedListDevices.mockResolvedValue([]);
  });
  afterEach(cleanup);

  it("renders the empty state and creates a protocol-typed device", async () => {
    mockedCreateDevice.mockResolvedValue(device);
    render(<DeviceWorkspace vgatewayId="gateway-1" />);
    expect(await screen.findByText("No devices configured")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Add device" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Power meter" } });
    fireEvent.change(screen.getByLabelText("Unit ID"), { target: { value: "7" } });
    fireEvent.click(screen.getByRole("button", { name: "Save device" }));
    await waitFor(() => expect(mockedCreateDevice).toHaveBeenCalledWith("gateway-1", expect.objectContaining({ name: "Power meter", type: "modbus_device", enabled: true, config: { unit_id: 7 } })));
    expect(await screen.findByText("Power meter")).toBeInTheDocument();
  });

  it("edits a device and persists its enabled state", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedUpdateDevice.mockResolvedValue({ ...device, name: "Paused meter", enabled: false });
    render(<DeviceWorkspace vgatewayId="gateway-1" />);

    fireEvent.click(await screen.findByRole("button", { name: "Edit Power meter" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Paused meter" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Enabled" }));
    fireEvent.click(screen.getByRole("button", { name: "Update device" }));

    await waitFor(() => expect(mockedUpdateDevice).toHaveBeenCalledWith(device.id, {
      name: "Paused meter",
      description: null,
      enabled: false,
      config: { unit_id: 7 },
    }));
    expect(await screen.findByText("Paused meter")).toBeInTheDocument();
    expect(screen.getByText("Paused")).toBeInTheDocument();
  });

  it("expands a device and exposes read-once and monitor controls", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([datasource]);
    mockedPreviewSavedDatasource.mockResolvedValue({ datasource_id: datasource.id, sequence: 1, observed_at: "2026-08-21T00:00:00Z", latency_ms: 2, quality: "good", raw_hex: "1234", data: { registers: [{ address: 10, value: 4660, hex: "1234" }] } });
    mockedMonitorDatasource.mockImplementation(async (_id, onSample, signal) => { onSample({ datasource_id: datasource.id, sequence: 2, observed_at: "2026-08-21T00:00:01Z", latency_ms: 3, quality: "good", raw_hex: "5678", data: { registers: [] } }); await new Promise<void>((resolve) => signal.addEventListener("abort", () => resolve(), { once: true })); });
    render(<DeviceWorkspace vgatewayId="gateway-1" />);
    fireEvent.click(await screen.findByRole("button", { name: /^Power meter/ }));
    expect(await screen.findByText("Voltage registers")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Read once" }));
    expect(await screen.findAllByText("1234")).toHaveLength(2);
    fireEvent.click(screen.getByRole("button", { name: "Monitor" }));
    expect(await screen.findByRole("button", { name: "Stop" })).toBeInTheDocument();
    expect(screen.getByText("5678")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Stop" }));
    await waitFor(() => expect(screen.getByRole("button", { name: "Monitor" })).toBeInTheDocument());
  });

  it("edits a datasource and persists its enabled state", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([datasource]);
    mockedUpdateDatasource.mockResolvedValue({ ...datasource, name: "Voltage paused", enabled: false, status: "paused" });
    render(<DeviceWorkspace vgatewayId="gateway-1" />);

    fireEvent.click(await screen.findByRole("button", { name: /^Power meter/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Edit Voltage registers" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Voltage paused" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Enabled" }));
    fireEvent.click(screen.getByRole("button", { name: "Update" }));

    await waitFor(() => expect(mockedUpdateDatasource).toHaveBeenCalledWith(datasource.id, {
      name: "Voltage paused",
      description: null,
      enabled: false,
      config: { function_code: 3, start_address: 10, quantity: 2, poll_interval_ms: 60000 },
    }));
    expect(await screen.findByText("Voltage paused")).toBeInTheDocument();
    expect(screen.getByText("Paused")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Monitor" })).toBeDisabled();
  });

  it("applies Modbus quantity limits before previewing", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([]);
    mockedPreviewDatasource.mockResolvedValue({
      datasource_id: "",
      sequence: 0,
      observed_at: "2026-08-21T00:00:00Z",
      latency_ms: 2,
      quality: "good",
      raw_hex: "1234",
      data: { registers: [] },
    });
    render(<DeviceWorkspace vgatewayId="gateway-1" />);

    fireEvent.click(await screen.findByRole("button", { name: /^Power meter/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Add datasource" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Register block" } });
    const quantity = screen.getByLabelText("Quantity");
    expect(quantity).toHaveAttribute("max", "125");

    fireEvent.change(quantity, { target: { value: "126" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(mockedPreviewDatasource).not.toHaveBeenCalled();

    fireEvent.change(screen.getByLabelText("Function"), { target: { value: "1" } });
    expect(quantity).toHaveAttribute("max", "2000");
    fireEvent.click(screen.getByRole("button", { name: "Preview" }));

    await waitFor(() => expect(mockedPreviewDatasource).toHaveBeenCalledWith(device.id, expect.objectContaining({
      config: expect.objectContaining({ function_code: 1, quantity: 126 }),
    })));
  });

  it("clears stale preview samples and errors between retries", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([]);
    const sample = {
      datasource_id: "",
      sequence: 0,
      observed_at: "2026-08-21T00:00:00Z",
      latency_ms: 2,
      quality: "good" as const,
      raw_hex: "1234",
      data: { registers: [] },
    };
    mockedPreviewDatasource
      .mockResolvedValueOnce(sample)
      .mockRejectedValueOnce({
        isAxiosError: true,
        response: { data: { error: { code: "DS005", message: "Modbus exception 0x02: Illegal Data Address. The requested address range 99-100 is not available on the server." } } },
      })
      .mockResolvedValueOnce({ ...sample, raw_hex: "5678" });
    render(<DeviceWorkspace vgatewayId="gateway-1" />);

    fireEvent.click(await screen.findByRole("button", { name: /^Power meter/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Add datasource" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Retry source" } });

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByText("1234")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Modbus exception 0x02: Illegal Data Address");
    expect(screen.getByRole("alert")).toHaveTextContent("address range 99-100");
    expect(screen.queryByText("1234")).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Preview" }));
    expect(await screen.findByText("5678")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("clears a saved datasource sample when a retry fails", async () => {
    mockedListDevices.mockResolvedValue([device]);
    mockedListDatasources.mockResolvedValue([datasource]);
    mockedPreviewSavedDatasource
      .mockResolvedValueOnce({ datasource_id: datasource.id, sequence: 1, observed_at: "2026-08-21T00:00:00Z", latency_ms: 2, quality: "good", raw_hex: "1234", data: { registers: [] } })
      .mockRejectedValueOnce(new Error("read failed"));
    render(<DeviceWorkspace vgatewayId="gateway-1" />);

    fireEvent.click(await screen.findByRole("button", { name: /^Power meter/ }));
    fireEvent.click(await screen.findByRole("button", { name: "Read once" }));
    expect(await screen.findByText("1234")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Read once" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("read failed");
    expect(screen.queryByText("1234")).not.toBeInTheDocument();
  });
});
