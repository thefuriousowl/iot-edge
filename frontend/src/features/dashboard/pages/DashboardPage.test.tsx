// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { act, cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { listDataLoggers } from "../../../services/datalogger.service";
import { listDeviceInventory } from "../../../services/device.service";
import { getHealth } from "../../../services/health.service";
import { listTags } from "../../../services/tag.service";
import { listVGateways } from "../../../services/vgateway.service";
import type { DataLogger } from "../../../types/datalogger";
import type { Tag, TagRuntimeValue } from "../../../types/tag";
import { useTagLiveStore } from "../../tag/stores/tagLive.store";
import DashboardPage from "./DashboardPage";

vi.mock("../../../services/datalogger.service", () => ({ listDataLoggers: vi.fn() }));
vi.mock("../../../services/device.service", () => ({ listDeviceInventory: vi.fn() }));
vi.mock("../../../services/health.service", () => ({ getHealth: vi.fn() }));
vi.mock("../../../services/tag.service", () => ({ listTags: vi.fn() }));
vi.mock("../../../services/vgateway.service", () => ({ listVGateways: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet Online</span> }));
vi.mock("../components/UtilitiesOverview", () => ({ default: () => <section>Energy &amp; Utilities</section> }));

const mockedListDataLoggers = vi.mocked(listDataLoggers);
const mockedListDeviceInventory = vi.mocked(listDeviceInventory);
const mockedGetHealth = vi.mocked(getHealth);
const mockedListTags = vi.mocked(listTags);
const mockedListVGateways = vi.mocked(listVGateways);

const tags: Tag[] = [
  {
    id: "11111111-1111-4111-8111-111111111111",
    datasource_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
    name: "Line voltage",
    type: "reading",
    data_type: "float32",
    description: "Main incomer",
    enabled: true,
    config: { decoder: { type: "binary_numeric", data_type: "float32", config: { byte_offset: 0, byte_order: "big_endian", bit_offset: 0 } } },
    created_at: "2026-08-22T01:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
  {
    id: "22222222-2222-4222-8222-222222222222",
    datasource_id: "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb",
    name: "Cooling demand",
    type: "reading",
    data_type: "float64",
    description: null,
    enabled: true,
    config: { decoder: { type: "binary_numeric", data_type: "float64", config: { byte_offset: 0, byte_order: "big_endian", bit_offset: 0 } } },
    created_at: "2026-08-22T01:00:00Z",
    updated_at: "2026-08-22T02:00:00Z",
  },
];

const loggers: DataLogger[] = [
  {
    id: "logger-1",
    name: "Plant history",
    description: null,
    enabled: true,
    timezone: "Asia/Bangkok",
    mode: "interval",
    start_at: "2026-08-22T00:00:00Z",
    end_at: null,
    max_size_bytes: null,
    config: { interval_seconds: 60 },
    tag_count: 2,
    created_at: "2026-08-22T00:00:00Z",
    updated_at: "2026-08-22T00:00:00Z",
  },
  {
    id: "logger-2",
    name: "Paused history",
    description: null,
    enabled: false,
    timezone: "UTC",
    mode: "schedule",
    start_at: "2026-08-22T00:00:00Z",
    end_at: null,
    max_size_bytes: null,
    config: { unit: "hour", every: 1 },
    tag_count: 1,
    created_at: "2026-08-22T00:00:00Z",
    updated_at: "2026-08-22T00:00:00Z",
  },
];

function runtimeValue(tagID: string, sequence: number, overrides: Partial<TagRuntimeValue> = {}): TagRuntimeValue {
  return {
    tag_id: tagID,
    sequence,
    observed_at: "2026-08-22T11:30:00Z",
    stored_at: "2026-08-22T11:30:00Z",
    quality: "good",
    data_type: "float32",
    value: 230.5,
    ...overrides,
  };
}

function mockSuccessfulDashboard(tagData: Tag[] = tags) {
  mockedGetHealth.mockResolvedValue({ status: "ok", version: "0.1.0" });
  mockedListVGateways.mockResolvedValue({
    data: [
      { id: "gateway-1", name: "Plant gateway", type: "modbus_tcp", description: null, enabled: true, status: "connected", device_count: 3, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
      { id: "gateway-2", name: "Standby gateway", type: "modbus_tcp", description: null, enabled: true, status: "disconnected", device_count: 1, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
    ],
    pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 },
  });
  mockedListDeviceInventory.mockResolvedValue({ data: [], pagination: { page: 1, per_page: 1, total: 4, total_pages: 4 } });
  mockedListTags.mockResolvedValue({ data: tagData, pagination: { page: 1, per_page: 8, total: tagData.length, total_pages: tagData.length > 0 ? 1 : 0 } });
  mockedListDataLoggers.mockResolvedValue({ data: loggers, pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 } });
}

function renderPage() {
  return render(<MemoryRouter><DashboardPage /></MemoryRouter>);
}

describe("DashboardPage", () => {
  beforeEach(() => {
    useTagLiveStore.getState().reset();
    mockedGetHealth.mockReset();
    mockedListVGateways.mockReset();
    mockedListDeviceInventory.mockReset();
    mockedListTags.mockReset();
    mockedListDataLoggers.mockReset();
    mockSuccessfulDashboard();
  });

  afterEach(() => {
    cleanup();
    useTagLiveStore.getState().reset();
  });

  it("loads operational inventory and updates live Tag rows without refetching definitions", async () => {
    useTagLiveStore.getState().setConnectionState("live");
    useTagLiveStore.getState().ingest(runtimeValue(tags[0].id, 10));
    useTagLiveStore.getState().ingest(runtimeValue(tags[1].id, 11, { quality: "bad", data_type: "float64", value: null, error: "Modbus exception 0x02: Illegal Data Address" }));

    renderPage();

    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    const inventory = screen.getByRole("region", { name: "Inventory summary" });
    expect(within(inventory).getByText("4")).toBeInTheDocument();
    const gatewayMetric = within(inventory).getByText("vGateways").closest("article");
    expect(gatewayMetric).not.toBeNull();
    expect(within(gatewayMetric!).getByText("2", { selector: "strong" })).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Line voltage/ })).toHaveAttribute("href", `/tags/${tags[0].id}`);
    expect(screen.getByText("230.5")).toBeInTheDocument();
    expect(screen.getByText("Modbus exception 0x02: Illegal Data Address")).toBeInTheDocument();
    expect(screen.getAllByText("live").length).toBeGreaterThan(0);
    expect(screen.getAllByText("error").length).toBeGreaterThan(0);

    act(() => useTagLiveStore.getState().ingest(runtimeValue(tags[0].id, 12, { value: 231.75 })));
    expect(screen.getByText("231.75")).toBeInTheDocument();
    expect(mockedListTags).toHaveBeenCalledOnce();
    expect(mockedListTags).toHaveBeenCalledWith({ enabled: true, page: 1, per_page: 8 }, expect.any(AbortSignal));
  });

  it("marks cached values stale when disconnected and exposes manual reconnect", async () => {
    useTagLiveStore.getState().ingest(runtimeValue(tags[0].id, 20));
    useTagLiveStore.setState({ connectionState: "disconnected", connectionError: "stream unavailable" });
    const reconnectKey = useTagLiveStore.getState().reconnectKey;

    renderPage();

    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
    expect(screen.getByText("stale")).toBeInTheDocument();
    const reconnect = screen.getByRole("button", { name: "Reconnect" });
    expect(reconnect).toHaveAttribute("title", "stream unavailable");
    fireEvent.click(reconnect);
    expect(useTagLiveStore.getState().reconnectKey).toBe(reconnectKey + 1);
  });

  it("keeps available inventory visible when one section fails and retries all sources", async () => {
    mockedGetHealth.mockRejectedValueOnce(new Error("offline"));

    renderPage();

    const alert = await screen.findByRole("alert");
    expect(alert).toHaveTextContent("Backend unavailable");
    expect(screen.queryByText("Plant gateway")).not.toBeInTheDocument();
    expect(screen.getByText("4")).toBeInTheDocument();
    fireEvent.click(within(alert).getByRole("button", { name: "Try again" }));

    await waitFor(() => expect(mockedGetHealth).toHaveBeenCalledTimes(2));
    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    expect(screen.queryByRole("alert")).not.toBeInTheDocument();
  });

  it("renders a useful empty state when no enabled Tags exist", async () => {
    mockSuccessfulDashboard([]);

    renderPage();

    expect(await screen.findByText("No enabled Tags yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Add Tag" })).toHaveAttribute("href", "/tags/new");
    expect(screen.getByText("No runtime event yet")).toBeInTheDocument();
  });
});
