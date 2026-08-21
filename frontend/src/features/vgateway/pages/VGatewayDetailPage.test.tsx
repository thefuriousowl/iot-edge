// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  connectVGateway,
  disconnectVGateway,
  getVGateway,
  getVGatewayStatus,
} from "../../../services/vgateway.service";
import type { VGatewayDetail, VGatewayStatusResponse } from "../../../types/vgateway";
import VGatewayDetailPage from "./VGatewayDetailPage";

vi.mock("../../../services/vgateway.service", () => ({
  connectVGateway: vi.fn(),
  disconnectVGateway: vi.fn(),
  getVGateway: vi.fn(),
  getVGatewayStatus: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

vi.mock("../../device/components/DeviceWorkspace", () => ({
  default: () => <div>No devices configured</div>,
}));

const mockedConnectVGateway = vi.mocked(connectVGateway);
const mockedDisconnectVGateway = vi.mocked(disconnectVGateway);
const mockedGetVGateway = vi.mocked(getVGateway);
const mockedGetVGatewayStatus = vi.mocked(getVGatewayStatus);

const gatewayID = "7b194e9f-4f74-4a19-8cb1-c4d0d8d5400f";
const gateway: VGatewayDetail = {
  id: gatewayID,
  name: "Main PLC Gateway",
  type: "modbus_tcp",
  description: "Factory floor controller network.",
  enabled: true,
  status: "connected",
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
    connected_at: "2026-08-21T10:15:00Z",
    request_count: 100,
    error_count: 2,
    avg_latency_ms: 9.5,
  },
  created_at: "2026-08-20T08:00:00Z",
  updated_at: "2026-08-21T10:30:00Z",
};

const runtime: VGatewayStatusResponse = {
  id: gatewayID,
  status: "connected",
  connected_at: "2026-08-21T10:15:00Z",
  last_activity: "2026-08-21T10:30:00Z",
  statistics: {
    request_count: 12480,
    error_count: 18,
    bytes_received: 4096,
    avg_latency_ms: 12.5,
  },
  health: {
    status: "unknown",
    last_check: null,
    latency_ms: null,
  },
};

function renderPage() {
  return render(
    <MemoryRouter initialEntries={[`/vgateways/${gatewayID}`]}>
      <Routes>
        <Route path="/vgateways/:id" element={<VGatewayDetailPage />} />
        <Route path="/vgateways/:id/edit" element={<p>Gateway edit destination</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("VGatewayDetailPage", () => {
  beforeEach(() => {
    mockedConnectVGateway.mockReset();
    mockedDisconnectVGateway.mockReset();
    mockedGetVGateway.mockReset();
    mockedGetVGatewayStatus.mockReset();
  });

  afterEach(() => {
    cleanup();
  });

  it("loads detail and runtime status together and renders the complete empty-device view", async () => {
    let resolveDetail: ((value: VGatewayDetail) => void) | undefined;
    mockedGetVGateway.mockReturnValue(
      new Promise((resolve) => {
        resolveDetail = resolve;
      }),
    );
    mockedGetVGatewayStatus.mockResolvedValue(runtime);

    renderPage();

    expect(screen.getByRole("status")).toHaveTextContent("Loading vGateway details");
    resolveDetail?.(gateway);

    expect(await screen.findByRole("heading", { name: "Main PLC Gateway" })).toBeInTheDocument();
    expect(screen.getByText("Factory floor controller network.")).toBeInTheDocument();
    expect(screen.getByText("12,480")).toBeInTheDocument();
    expect(screen.getByText("12.50 ms")).toBeInTheDocument();
    expect(screen.getByText("192.0.2.10:502")).toBeInTheDocument();
    expect(screen.getByText("3 attempts / 1000 ms delay")).toBeInTheDocument();
    expect(screen.getByText("Health probes are not available yet.", { exact: false })).toBeInTheDocument();
    expect(screen.getByText("No devices configured")).toBeInTheDocument();
    expect(screen.getByText(gatewayID)).toBeInTheDocument();
    expect(mockedGetVGateway).toHaveBeenCalledWith(gatewayID, expect.any(AbortSignal));
    expect(mockedGetVGatewayStatus).toHaveBeenCalledWith(gatewayID, expect.any(AbortSignal));
  });

  it("keeps gateway details visible when runtime status fails and retries status only", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    mockedGetVGatewayStatus
      .mockRejectedValueOnce(new Error("status unavailable"))
      .mockResolvedValueOnce(runtime);

    renderPage();

    expect(await screen.findByRole("heading", { name: "Main PLC Gateway" })).toBeInTheDocument();
    expect(screen.getByRole("alert")).toHaveTextContent("Unable to load runtime status");
    expect(screen.getByText("100")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Retry" }));

    await waitFor(() => {
      expect(mockedGetVGatewayStatus).toHaveBeenCalledTimes(2);
      expect(screen.queryByRole("alert")).not.toBeInTheDocument();
    });
    expect(screen.getByText("12,480")).toBeInTheDocument();
    expect(mockedGetVGateway).toHaveBeenCalledOnce();
  });

  it("retries the complete load after a detail failure", async () => {
    mockedGetVGateway
      .mockRejectedValueOnce(new Error("detail unavailable"))
      .mockResolvedValueOnce(gateway);
    mockedGetVGatewayStatus.mockResolvedValue(runtime);

    renderPage();

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load this vGateway");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(await screen.findByRole("heading", { name: "Main PLC Gateway" })).toBeInTheDocument();
    expect(mockedGetVGateway).toHaveBeenCalledTimes(2);
    expect(mockedGetVGatewayStatus).toHaveBeenCalledTimes(2);
  });

  it("disconnects a connected gateway and refreshes its authoritative status", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    mockedGetVGatewayStatus
      .mockResolvedValueOnce(runtime)
      .mockResolvedValueOnce({ ...runtime, status: "disconnected", connected_at: null });
    mockedDisconnectVGateway.mockResolvedValue({
      message: "Disconnected successfully",
      status: "disconnected",
    });

    renderPage();
    await screen.findByRole("heading", { name: "Main PLC Gateway" });
    fireEvent.click(screen.getByRole("button", { name: "Disconnect" }));

    await waitFor(() => {
      expect(mockedDisconnectVGateway).toHaveBeenCalledWith(gatewayID);
      expect(mockedGetVGatewayStatus).toHaveBeenCalledTimes(2);
    });
    expect(screen.getByRole("button", { name: "Connect" })).toBeInTheDocument();
  });

  it("connects a disconnected gateway and reports action failures without hiding details", async () => {
    const disconnectedRuntime = { ...runtime, status: "disconnected" as const, connected_at: null };
    mockedGetVGateway.mockResolvedValue({ ...gateway, status: "disconnected" });
    mockedGetVGatewayStatus.mockResolvedValue(disconnectedRuntime);
    mockedConnectVGateway.mockRejectedValue(new Error("connect unavailable"));

    renderPage();
    await screen.findByRole("heading", { name: "Main PLC Gateway" });
    fireEvent.click(screen.getByRole("button", { name: "Connect" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unable to change the gateway connection state",
    );
    expect(mockedConnectVGateway).toHaveBeenCalledWith(gatewayID);
    expect(screen.getByText("192.0.2.10:502")).toBeInTheDocument();
  });

  it("disables connection actions for a disabled gateway", async () => {
    mockedGetVGateway.mockResolvedValue({ ...gateway, enabled: false, status: "stopped" });
    mockedGetVGatewayStatus.mockResolvedValue({ ...runtime, status: "stopped", connected_at: null });

    renderPage();

    const connectButton = await screen.findByRole("button", { name: "Connect" });
    expect(connectButton).toBeDisabled();
    expect(connectButton).toHaveAttribute("title", "Enable the gateway before connecting");
    expect(screen.getByText("This gateway is disabled.")).toBeInTheDocument();
  });

  it("renders healthy status with the device workspace and navigates to edit", async () => {
    mockedGetVGateway.mockResolvedValue({
      ...gateway,
      devices: [
        { id: "device-1", name: "Boiler PLC", unit_id: 7 },
        { id: "device-2", name: "Meter", unit_id: 12 },
      ],
    });
    mockedGetVGatewayStatus.mockResolvedValue({
      ...runtime,
      health: { status: "healthy", last_check: "2026-08-21T10:29:00Z", latency_ms: 8.25 },
    });

    renderPage();

    expect(await screen.findByText("No devices configured")).toBeInTheDocument();
    expect(screen.getAllByText("Healthy")).toHaveLength(2);
    expect(screen.getByText("8.25 ms")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Edit Gateway" }));
    expect(await screen.findByText("Gateway edit destination")).toBeInTheDocument();
  });

  it("refreshes runtime metrics without reloading gateway metadata", async () => {
    mockedGetVGateway.mockResolvedValue(gateway);
    mockedGetVGatewayStatus
      .mockResolvedValueOnce(runtime)
      .mockResolvedValueOnce({
        ...runtime,
        statistics: { ...runtime.statistics, request_count: 13000 },
      });

    renderPage();
    await screen.findByText("12,480");
    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));

    expect(await screen.findByText("13,000")).toBeInTheDocument();
    expect(mockedGetVGatewayStatus).toHaveBeenCalledTimes(2);
    expect(mockedGetVGateway).toHaveBeenCalledOnce();
  });
});
