// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  connectVGateway,
  deleteVGateway,
  disconnectVGateway,
  listVGateways,
} from "../../../services/vgateway.service";
import type {
  VGatewayListItem,
  VGatewayListResponse,
} from "../../../types/vgateway";
import VGatewayListPage from "./VGatewayListPage";

vi.mock("../../../services/vgateway.service", () => ({
  connectVGateway: vi.fn(),
  deleteVGateway: vi.fn(),
  disconnectVGateway: vi.fn(),
  listVGateways: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

const mockedConnectVGateway = vi.mocked(connectVGateway);
const mockedDeleteVGateway = vi.mocked(deleteVGateway);
const mockedDisconnectVGateway = vi.mocked(disconnectVGateway);
const mockedListVGateways = vi.mocked(listVGateways);

const gateways: VGatewayListItem[] = [
  {
    id: "7b194e9f-4f74-4a19-8cb1-c4d0d8d5400f",
    name: "Main PLC Gateway",
    type: "modbus_tcp",
    description: "Factory floor",
    enabled: true,
    status: "connected",
    device_count: 3,
    last_activity: "2026-08-21T10:30:00Z",
    created_at: "2026-08-20T08:00:00Z",
    updated_at: "2026-08-21T10:30:00Z",
  },
  {
    id: "d184a809-a351-4a3c-96ab-59c364f8eb1e",
    name: "Boiler Room",
    type: "modbus_tcp",
    description: null,
    enabled: true,
    status: "error",
    device_count: 0,
    last_activity: null,
    created_at: "2026-08-20T09:00:00Z",
    updated_at: "2026-08-21T10:00:00Z",
  },
];

function response(data = gateways, overrides?: Partial<VGatewayListResponse>): VGatewayListResponse {
  return {
    data,
    pagination: {
      page: 1,
      per_page: 20,
      total: data.length,
      total_pages: data.length > 0 ? 1 : 0,
    },
    ...overrides,
  };
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/vgateways"]}>
      <VGatewayListPage />
    </MemoryRouter>,
  );
}

describe("VGatewayListPage", () => {
  beforeEach(() => {
    mockedListVGateways.mockReset();
    mockedDeleteVGateway.mockReset();
    mockedConnectVGateway.mockReset();
    mockedDisconnectVGateway.mockReset();
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows loading, summary, gateway rows, and client-side search", async () => {
    let resolveList: ((value: VGatewayListResponse) => void) | undefined;
    mockedListVGateways.mockReturnValue(
      new Promise((resolve) => {
        resolveList = resolve;
      }),
    );

    renderPage();

    expect(screen.getByRole("status")).toHaveTextContent("Loading vGateways");

    resolveList?.(response());

    expect(await screen.findByText("Main PLC Gateway")).toBeInTheDocument();
    expect(screen.getByText("Boiler Room")).toBeInTheDocument();
    expect(screen.getByText("Factory floor")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Main PLC Gateway" })).toHaveAttribute(
      "href",
      `/vgateways/${gateways[0].id}`,
    );
    expect(screen.getByText("3")).toBeInTheDocument();
    expect(screen.getAllByText("Connected")).toHaveLength(2);
    expect(screen.getByText("Error")).toBeInTheDocument();

    fireEvent.change(screen.getByRole("searchbox", { name: "Search gateways" }), {
      target: { value: "boiler" },
    });

    expect(screen.queryByText("Main PLC Gateway")).not.toBeInTheDocument();
    expect(screen.getByText("Boiler Room")).toBeInTheDocument();
  });

  it("uses the canonical application navigation", async () => {
    mockedListVGateways.mockResolvedValue(response());

    renderPage();

    await screen.findByText("Main PLC Gateway");
    const navigation = screen.getByRole("navigation", { name: "Primary navigation" });

    for (const name of [
      "Dashboard",
      "vGateways",
      "Devices",
      "Tags",
      "Data Loggers",
      "Reports",
      "Plugins",
      "Data Publishers",
      "Credentials",
      "Settings",
    ]) {
      expect(within(navigation).getByRole("link", { name })).toBeInTheDocument();
    }

    expect(within(navigation).getByRole("link", { name: "vGateways" })).toHaveClass("active");
  });

  it("requests enabled filters and paginated pages", async () => {
    mockedListVGateways
      .mockResolvedValueOnce(
        response(gateways, {
          pagination: {
            page: 1,
            per_page: 20,
            total: 22,
            total_pages: 2,
          },
        }),
      )
      .mockResolvedValueOnce(
        response([gateways[0]], {
          pagination: {
            page: 2,
            per_page: 20,
            total: 22,
            total_pages: 2,
          },
        }),
      )
      .mockResolvedValueOnce(response([gateways[0]]));

    renderPage();
    await screen.findByText("Main PLC Gateway");

    fireEvent.click(screen.getByRole("button", { name: "Next page" }));
    await waitFor(() => {
      expect(mockedListVGateways).toHaveBeenNthCalledWith(
        2,
        { page: 2, per_page: 20, enabled: undefined },
        expect.any(AbortSignal),
      );
    });

    fireEvent.change(screen.getByRole("combobox", { name: "Enabled state" }), {
      target: { value: "enabled" },
    });
    await waitFor(() => {
      expect(mockedListVGateways).toHaveBeenNthCalledWith(
        3,
        { page: 1, per_page: 20, enabled: true },
        expect.any(AbortSignal),
      );
    });
  });

  it("shows the API error and retries the list request", async () => {
    mockedListVGateways
      .mockRejectedValueOnce(new Error("network unavailable"))
      .mockResolvedValueOnce(response());

    renderPage();

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unable to load vGateways",
    );
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));

    expect(await screen.findByText("Main PLC Gateway")).toBeInTheDocument();
    expect(mockedListVGateways).toHaveBeenCalledTimes(2);
  });

  it("shows the empty and no-search-result states", async () => {
    mockedListVGateways.mockResolvedValueOnce(response([]));
    const firstRender = renderPage();

    expect(await screen.findByText("No vGateways yet")).toBeInTheDocument();
    firstRender.unmount();

    mockedListVGateways.mockResolvedValueOnce(response());
    renderPage();
    await screen.findByText("Main PLC Gateway");
    fireEvent.change(screen.getByRole("searchbox", { name: "Search gateways" }), {
      target: { value: "does-not-exist" },
    });

    expect(screen.getByText("No matching gateways")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear search" }));
    expect(screen.getByText("Main PLC Gateway")).toBeInTheDocument();
  });

  it("confirms deletion and reloads the current page", async () => {
    mockedListVGateways
      .mockResolvedValueOnce(response())
      .mockResolvedValueOnce(response([gateways[1]]));
    mockedDeleteVGateway.mockResolvedValue();
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderPage();
    await screen.findByText("Main PLC Gateway");
    fireEvent.click(
      screen.getByRole("button", { name: "Delete Main PLC Gateway" }),
    );

    await waitFor(() => {
      expect(mockedDeleteVGateway).toHaveBeenCalledWith(gateways[0].id);
      expect(mockedListVGateways).toHaveBeenCalledTimes(2);
    });
  });

  it("does not delete when confirmation is cancelled", async () => {
    mockedListVGateways.mockResolvedValue(response());
    vi.spyOn(window, "confirm").mockReturnValue(false);

    renderPage();
    await screen.findByText("Main PLC Gateway");
    fireEvent.click(
      screen.getByRole("button", { name: "Delete Main PLC Gateway" }),
    );

    expect(mockedDeleteVGateway).not.toHaveBeenCalled();
  });

  it("keeps the list and reports a failed deletion", async () => {
    mockedListVGateways.mockResolvedValue(response());
    mockedDeleteVGateway.mockRejectedValue(new Error("delete failed"));
    vi.spyOn(window, "confirm").mockReturnValue(true);

    renderPage();
    await screen.findByText("Main PLC Gateway");
    fireEvent.click(
      screen.getByRole("button", { name: "Delete Main PLC Gateway" }),
    );

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unable to load vGateways",
    );
    expect(screen.getByText("Main PLC Gateway")).toBeInTheDocument();
  });

  it("connects a disconnected gateway and refreshes the list", async () => {
    mockedListVGateways.mockResolvedValue(response());
    mockedConnectVGateway.mockResolvedValue({
      message: "Connected successfully",
      status: "connected",
    });

    renderPage();
    await screen.findByText("Boiler Room");
    fireEvent.click(screen.getByRole("button", { name: "Connect Boiler Room" }));

    await waitFor(() => {
      expect(mockedConnectVGateway).toHaveBeenCalledWith(gateways[1].id);
      expect(mockedListVGateways).toHaveBeenCalledTimes(2);
    });
  });

  it("disconnects a connected gateway", async () => {
    mockedListVGateways.mockResolvedValue(response());
    mockedDisconnectVGateway.mockResolvedValue({
      message: "Disconnected successfully",
      status: "disconnected",
    });

    renderPage();
    await screen.findByText("Main PLC Gateway");
    fireEvent.click(screen.getByRole("button", { name: "Disconnect Main PLC Gateway" }));

    await waitFor(() => {
      expect(mockedDisconnectVGateway).toHaveBeenCalledWith(gateways[0].id);
    });
  });
});
