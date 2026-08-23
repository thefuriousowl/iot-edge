// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import { enableDataPublisher, listDataPublishers } from "../../../services/publisher.service";
import type { DataPublisher, PublisherRuntimeStatus } from "../../../types/publisher";
import DataPublisherListPage from "./DataPublisherListPage";

vi.mock("../../../services/publisher.service", () => ({
  deleteDataPublisher: vi.fn(), disableDataPublisher: vi.fn(), enableDataPublisher: vi.fn(),
  listDataPublishers: vi.fn(), restartDataPublisher: vi.fn(),
}));

vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet online</span> }));

const mockedEnable = vi.mocked(enableDataPublisher);
const mockedList = vi.mocked(listDataPublishers);
const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "mqtt", state: "stopped", config_version: 1,
  last_transition_at: "2026-08-23T00:00:00Z", request_count: 10, publish_count: 8,
  failure_count: 1, queue_depth: 0, drop_count: 1, reconnect_count: 2, connected: false,
  connection_count: 3, delivery_count: 8, delivery_failure_count: 1, transport_queue_depth: 0,
  transport_drop_count: 1, diagnostic_count: 2, diagnostic_drop_count: 0,
};
const publisher: DataPublisher = {
  id: "publisher-1", type: "mqtt", name: "Plant telemetry", enabled: false, config_version: 1,
  source_count: 2, runtime, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};
const httpPublisher: DataPublisher = {
  id: "publisher-2", type: "http_server", name: "Plant snapshot", enabled: true, config_version: 2,
  source_count: 3, runtime: {
    ...runtime, publisher_id: "publisher-2", type: "http_server", state: "running", connected: true,
    external_request_count: 24, rejected_request_count: 3, active_connections: 2, publish_count: 12,
  },
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

describe("DataPublisherListPage", () => {
  beforeEach(() => {
    mockedList.mockReset().mockResolvedValue({ data: [publisher, httpPublisher], pagination: { page: 1, per_page: 20, total: 2, total_pages: 1 } });
    mockedEnable.mockReset().mockResolvedValue({ ...publisher, enabled: true, runtime: { ...runtime, state: "running" } });
  });

  afterEach(() => cleanup());

  it("lists MQTT and HTTP runtime health with transport-specific configure routes", async () => {
    render(<MemoryRouter><DataPublisherListPage /></MemoryRouter>);

    expect(await screen.findByText("Plant telemetry")).toBeInTheDocument();
    expect(screen.getByText("8 delivered")).toBeInTheDocument();
    expect(screen.getByText("2 reconnects")).toBeInTheDocument();
    expect(screen.getByText("24 requests")).toBeInTheDocument();
    expect(screen.getByText("3 rejected · 12 snapshot updates")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Configure Plant telemetry" })).toHaveAttribute("href", "/data-publishers/publisher-1/mqtt");
    expect(screen.getByRole("link", { name: "Configure Plant snapshot" })).toHaveAttribute("href", "/data-publishers/publisher-2/http-server");
    expect(screen.getByRole("button", { name: "Restart Plant telemetry" })).toBeDisabled();

    fireEvent.click(screen.getByRole("button", { name: "Enable Plant telemetry" }));
    await waitFor(() => expect(mockedEnable).toHaveBeenCalledWith("publisher-1"));
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2));
  });

  it("sends optional transport, search, and desired-state filters", async () => {
    render(<MemoryRouter initialEntries={["/data-publishers?type=http_server&enabled=false"]}><DataPublisherListPage /></MemoryRouter>);
    await screen.findByText("Plant telemetry");
    fireEvent.change(screen.getByRole("searchbox"), { target: { value: "plant" } });
    fireEvent.submit(screen.getByRole("search"));

    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith(expect.objectContaining({ type: "http_server", enabled: false, search: "plant" }), expect.any(AbortSignal)));
  });
});
