// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { enableDataPublisher, getDataPublisherStatus, probeHTTPServerListener } from "../../../services/publisher.service";
import type { DataPublisher, PublisherRuntimeStatus } from "../../../types/publisher";
import HTTPServerPublisherOperations from "./HTTPServerPublisherOperations";

vi.mock("../../../services/publisher.service", () => ({
  disableDataPublisher: vi.fn(), enableDataPublisher: vi.fn(), getDataPublisherStatus: vi.fn(), probeHTTPServerListener: vi.fn(), restartDataPublisher: vi.fn(),
}));

const mockedEnable = vi.mocked(enableDataPublisher);
const mockedStatus = vi.mocked(getDataPublisherStatus);
const mockedProbe = vi.mocked(probeHTTPServerListener);
const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "http_server", state: "stopped", config_version: 3,
  last_transition_at: "2026-08-23T08:00:00Z", request_count: 4, publish_count: 4,
  failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false,
  connection_count: 0, delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0,
  transport_drop_count: 0, diagnostic_count: 0, diagnostic_drop_count: 0,
  external_request_count: 12, rejected_request_count: 2, active_connections: 1,
  last_external_request_at: "2026-08-23T08:35:25.573Z",
  sources: [{
    alias: "flow", available: true, quality: "good", sequence: 42, observed_at: "2026-08-23T08:35:24Z",
    reference: { kind: "tag", tag_id: "tag-1" },
  }],
};
const publisher: DataPublisher = {
  id: "publisher-1", type: "http_server", name: "Plant snapshot", enabled: false,
  config_version: 3, source_count: 2, runtime,
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

describe("HTTPServerPublisherOperations", () => {
  beforeEach(() => {
    mockedStatus.mockReset().mockResolvedValue(runtime);
    mockedEnable.mockReset().mockResolvedValue({ ...publisher, enabled: true, runtime: { ...runtime, state: "running", connected: true } });
    mockedProbe.mockReset().mockResolvedValue({ reachable: true, probed_at: "2026-08-23T08:35:25.573Z", latency_ms: 1.25, endpoint: { network: "tcp", scheme: "http", bind_address: "127.0.0.1", port: 8088, path: "/snapshot", access_mode: "anonymous", quality_policy: "payload" } });
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
  });

  afterEach(() => cleanup());

  it("shows the external endpoint and HTTP-specific runtime metrics", async () => {
    render(<HTTPServerPublisherOperations publisher={publisher} endpoint="http://127.0.0.1:8088/snapshot" onPublisherChange={vi.fn()} />);

    expect(screen.getByText("http://127.0.0.1:8088/snapshot")).toBeInTheDocument();
    expect(screen.getByText("12")).toBeInTheDocument();
    expect(screen.getByText("2")).toBeInTheDocument();
    const connections = screen.getByText("Connections").closest("article");
    expect(connections).not.toBeNull();
    expect(within(connections as HTMLElement).getByText("0")).toBeInTheDocument();
    expect(screen.getByText("flow")).toBeInTheDocument();
    expect(screen.getByLabelText("Observed at 2026-08-23T08:35:24Z")).toBeInTheDocument();
    await waitFor(() => expect(mockedStatus).toHaveBeenCalledWith("publisher-1", expect.any(AbortSignal)));
  });

  it("enables the listener and reports the changed desired state", async () => {
    const onPublisherChange = vi.fn();
    render(<HTTPServerPublisherOperations publisher={publisher} endpoint="http://127.0.0.1:8088/snapshot" onPublisherChange={onPublisherChange} />);

    fireEvent.click(screen.getByRole("button", { name: "Enable" }));

    await waitFor(() => expect(mockedEnable).toHaveBeenCalledWith("publisher-1"));
    expect(onPublisherChange).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));
    expect(screen.getByRole("status")).toHaveTextContent("HTTP Server enabled");
  });

  it("probes a running listener without claiming an HTTP request", async () => {
    const running = { ...publisher, enabled: true, runtime: { ...runtime, state: "running" as const } };
    mockedStatus.mockResolvedValue(running.runtime);
    render(<HTTPServerPublisherOperations publisher={running} endpoint="http://127.0.0.1:8088/snapshot" onPublisherChange={vi.fn()} />);

    fireEvent.click(screen.getByRole("button", { name: "Probe listener" }));

    await waitFor(() => expect(mockedProbe).toHaveBeenCalledWith("publisher-1"));
    expect(screen.getByRole("status")).toHaveTextContent("Listener reachable in 1.25 ms");
    expect(screen.getByRole("status")).toHaveTextContent("No HTTP request or acquisition read was sent");
  });
});
