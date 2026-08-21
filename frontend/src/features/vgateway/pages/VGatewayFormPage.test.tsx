// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  createVGateway,
  getVGateway,
  testVGatewayConfig,
  updateVGateway,
} from "../../../services/vgateway.service";
import type { VGateway, VGatewayDetail } from "../../../types/vgateway";
import VGatewayFormPage from "./VGatewayFormPage";

vi.mock("../../../services/vgateway.service", () => ({
  createVGateway: vi.fn(),
  getVGateway: vi.fn(),
  testVGatewayConfig: vi.fn(),
  updateVGateway: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

const mockedCreateVGateway = vi.mocked(createVGateway);
const mockedGetVGateway = vi.mocked(getVGateway);
const mockedTestVGatewayConfig = vi.mocked(testVGatewayConfig);
const mockedUpdateVGateway = vi.mocked(updateVGateway);

const gatewayID = "7b194e9f-4f74-4a19-8cb1-c4d0d8d5400f";
const gateway: VGatewayDetail = {
  id: gatewayID,
  name: "Main PLC Gateway",
  type: "modbus_tcp",
  description: "Factory floor",
  enabled: true,
  status: "disconnected",
  config: {
    host: "192.0.2.10",
    port: 502,
    timeout: 5000,
    retry_count: 3,
    retry_delay: 1000,
    keep_alive: true,
    reconnect_interval: 30,
  },
  devices: [],
  statistics: {
    connected_at: null,
    request_count: 0,
    error_count: 0,
    avg_latency_ms: null,
  },
  created_at: "2026-08-20T08:00:00Z",
  updated_at: "2026-08-21T10:30:00Z",
};

function renderForm(path: string) {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/vgateways/new" element={<VGatewayFormPage />} />
        <Route path="/vgateways/:id/edit" element={<VGatewayFormPage />} />
        <Route path="/vgateways" element={<p>Gateway list destination</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

function createdGateway(): VGateway {
  return {
    ...gateway,
    devices: undefined,
    statistics: undefined,
  } as unknown as VGateway;
}

describe("VGatewayFormPage", () => {
  beforeEach(() => {
    mockedCreateVGateway.mockReset();
    mockedGetVGateway.mockReset();
    mockedTestVGatewayConfig.mockReset();
    mockedUpdateVGateway.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it("creates a Modbus TCP gateway with defaults and normalized optional description", async () => {
    mockedCreateVGateway.mockResolvedValue(createdGateway());
    renderForm("/vgateways/new");

    fireEvent.change(screen.getByLabelText(/Gateway name/), {
      target: { value: "  Main PLC Gateway  " },
    });
    fireEvent.change(screen.getByLabelText(/Host/), {
      target: { value: " 192.0.2.10 " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save Gateway" }));

    await waitFor(() => {
      expect(mockedCreateVGateway).toHaveBeenCalledWith({
        name: "Main PLC Gateway",
        type: "modbus_tcp",
        description: null,
        enabled: true,
        config: {
          host: "192.0.2.10",
          port: 502,
          timeout: 5000,
          retry_count: 3,
          retry_delay: 1000,
          keep_alive: true,
          reconnect_interval: 30,
        },
      });
    });
    expect(await screen.findByText("Gateway list destination")).toBeInTheDocument();
    expect(mockedGetVGateway).not.toHaveBeenCalled();
  });

  it("validates required fields and backend numeric boundaries", async () => {
    renderForm("/vgateways/new");

    fireEvent.change(screen.getByLabelText(/Port/), { target: { value: "70000" } });
    fireEvent.change(screen.getByLabelText(/Timeout/), { target: { value: "99" } });
    fireEvent.change(screen.getByLabelText(/Retry count/), { target: { value: "11" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Gateway" }));

    expect(await screen.findByText("Gateway name is required")).toBeInTheDocument();
    expect(screen.getByText("Host is required")).toBeInTheDocument();
    expect(screen.getByText("Port must be between 1 and 65535")).toBeInTheDocument();
    expect(screen.getByText("Timeout must be between 100 and 60000 ms")).toBeInTheDocument();
    expect(screen.getByText("Retry count must be between 0 and 10")).toBeInTheDocument();
    expect(mockedCreateVGateway).not.toHaveBeenCalled();
  });

  it("loads and updates an existing gateway", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    mockedUpdateVGateway.mockResolvedValue(createdGateway());
    renderForm(`/vgateways/${gatewayID}/edit`);

    expect(screen.getByRole("status")).toHaveTextContent("Loading vGateway");
    expect(await screen.findByDisplayValue("Main PLC Gateway")).toBeInTheDocument();
    expect(screen.getByDisplayValue("192.0.2.10")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/Gateway name/), {
      target: { value: "Updated PLC" },
    });
    fireEvent.change(screen.getByLabelText(/Description/), {
      target: { value: "Updated description" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Save Gateway" }));

    await waitFor(() => {
      expect(mockedUpdateVGateway).toHaveBeenCalledWith(
        gatewayID,
        expect.objectContaining({
          name: "Updated PLC",
          description: "Updated description",
          config: expect.objectContaining({ host: "192.0.2.10" }),
        }),
      );
    });
    expect(await screen.findByText("Gateway list destination")).toBeInTheDocument();
  });

  it("tests current edit-form settings with an optional unit ID", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    mockedTestVGatewayConfig.mockResolvedValue({
      success: true,
      latency_ms: 12.5,
      message: "Connection successful",
    });
    renderForm(`/vgateways/${gatewayID}/edit`);
    await screen.findByDisplayValue("Main PLC Gateway");

    fireEvent.change(screen.getByLabelText("Unit ID (optional)"), {
      target: { value: "7" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Test Connection" }));

    expect(await screen.findByText("Connection successful")).toBeInTheDocument();
    expect(screen.getByText("12.50 ms")).toBeInTheDocument();
    expect(mockedTestVGatewayConfig).toHaveBeenCalledWith({
      type: "modbus_tcp",
      config: gateway.config,
      options: { unit_id: 7 },
    });
  });

  it("rejects an out-of-range unit ID without calling the API", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    renderForm(`/vgateways/${gatewayID}/edit`);
    await screen.findByDisplayValue("Main PLC Gateway");

    fireEvent.change(screen.getByLabelText("Unit ID (optional)"), {
      target: { value: "256" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Test Connection" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Unit ID must be between 0 and 255");
    expect(mockedTestVGatewayConfig).not.toHaveBeenCalled();
  });

  it("tests unsaved connection settings without requiring a gateway name", async () => {
    mockedTestVGatewayConfig.mockResolvedValue({
      success: true,
      latency_ms: 8,
      message: "Connection successful",
    });
    renderForm("/vgateways/new");

    fireEvent.change(screen.getByLabelText(/Host/), {
      target: { value: " plc.local " },
    });
    fireEvent.click(screen.getByRole("button", { name: "Test Connection" }));

    expect(await screen.findByText("Connection successful")).toBeInTheDocument();
    expect(mockedTestVGatewayConfig).toHaveBeenCalledWith({
      type: "modbus_tcp",
      config: {
        host: "plc.local",
        port: 502,
        timeout: 5000,
        retry_count: 3,
        retry_delay: 1000,
        keep_alive: true,
        reconnect_interval: 30,
      },
    });
    expect(screen.queryByText("Gateway name is required")).not.toBeInTheDocument();
    expect(mockedCreateVGateway).not.toHaveBeenCalled();
  });

  it("validates connection settings before an unsaved test", async () => {
    renderForm("/vgateways/new");

    fireEvent.click(screen.getByRole("button", { name: "Test Connection" }));

    expect(await screen.findByText("Host is required")).toBeInTheDocument();
    expect(mockedTestVGatewayConfig).not.toHaveBeenCalled();
  });

  it("shows load and submit errors without navigating", async () => {
    mockedGetVGateway.mockRejectedValueOnce(new Error("load failed"));
    const firstRender = renderForm(`/vgateways/${gatewayID}/edit`);
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load this vGateway");
    firstRender.unmount();

    mockedCreateVGateway.mockRejectedValueOnce(new Error("save failed"));
    renderForm("/vgateways/new");
    fireEvent.change(screen.getByLabelText(/Gateway name/), { target: { value: "PLC" } });
    fireEvent.change(screen.getByLabelText(/Host/), { target: { value: "plc.local" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Gateway" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to create the vGateway");
    expect(screen.getByRole("heading", { name: "Add vGateway" })).toBeInTheDocument();
  });

  it("cancels back to the gateway list", async () => {
    renderForm("/vgateways/new");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(await screen.findByText("Gateway list destination")).toBeInTheDocument();
  });
});
