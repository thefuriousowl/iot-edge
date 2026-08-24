// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  enableDataPublisher,
  getDataPublisherStatus,
  listPublisherDiagnostics,
  testMQTTConnection,
} from "../../../services/publisher.service";
import type { DataPublisher, PublisherRuntimeStatus } from "../../../types/publisher";
import MQTTPublisherOperations from "./MQTTPublisherOperations";

vi.mock("../../../services/publisher.service", () => ({
  deletePublisherSecret: vi.fn(), disableDataPublisher: vi.fn(), enableDataPublisher: vi.fn(),
  getDataPublisherStatus: vi.fn(), listPublisherDiagnostics: vi.fn(),
  restartDataPublisher: vi.fn(), testMQTTConnection: vi.fn(),
}));

const mockedEnable = vi.mocked(enableDataPublisher);
const mockedGetStatus = vi.mocked(getDataPublisherStatus);
const mockedListDiagnostics = vi.mocked(listPublisherDiagnostics);
const mockedTestConnection = vi.mocked(testMQTTConnection);

const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "mqtt", state: "stopped", config_version: 2,
  last_transition_at: "2026-08-23T08:35:25.573Z", request_count: 4, publish_count: 3,
  failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 1, connected: false,
  connection_count: 1, delivery_count: 3, delivery_failure_count: 0, transport_queue_depth: 0,
  transport_drop_count: 0, diagnostic_count: 1, diagnostic_drop_count: 0,
  sources: [{
    alias: "today_cost", available: true, quality: "partial", sequence: 18, coverage_percent: 6.8,
    period_start: "2026-08-22T17:00:00Z", period_end: "2026-08-23T08:35:25.573Z",
    reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.covered_estimated_cost" },
  }],
};

const publisher: DataPublisher = {
  id: "publisher-1", type: "mqtt", name: "Telemetry", enabled: false, config_version: 2,
  source_count: 1, runtime, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

describe("MQTTPublisherOperations", () => {
  beforeEach(() => {
    mockedGetStatus.mockReset().mockResolvedValue(runtime);
    mockedListDiagnostics.mockReset().mockResolvedValue([{
      sequence: 1, label: "ack", topic: "site/ack", qos: 1, retained: false, duplicate: false,
      received_at: "2026-08-23T08:35:25.573Z", format: "json", payload: '{"status":"success"}', truncated: false,
    }]);
    mockedTestConnection.mockReset().mockResolvedValue({ connected: true, connected_at: "2026-08-23T08:35:25.573Z", latency_ms: 24 });
    mockedEnable.mockReset().mockResolvedValue({ ...publisher, enabled: true, runtime: { ...runtime, state: "running", connected: true } });
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  function renderOperations(onPublisherChange = vi.fn()) {
    render(<MQTTPublisherOperations publisher={publisher} diagnosticHistoryDepth={100} onPublisherChange={onPublisherChange} />);
    return onPublisherChange;
  }

  it("runs a non-publishing connection test and displays generic diagnostics", async () => {
    renderOperations();
    expect(await screen.findByText('{"status":"success"}')).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Test connection" }));

    await waitFor(() => expect(mockedTestConnection).toHaveBeenCalledWith("publisher-1"));
    expect(screen.getByRole("status")).toHaveTextContent("No telemetry was published");
    expect(screen.getByText("site/ack")).toBeInTheDocument();
    expect(screen.getByText("today_cost")).toBeInTheDocument();
    expect(screen.getByLabelText("Period from 2026-08-22T17:00:00Z to 2026-08-23T08:35:25.573Z")).toBeInTheDocument();
  });

  it("never presents a stale connection as live while runtime is stopped", async () => {
    mockedGetStatus.mockResolvedValue({ ...runtime, connected: true });
    renderOperations();

    expect(await screen.findByText("Disconnected")).toBeInTheDocument();
    expect(screen.queryByText("Connected")).not.toBeInTheDocument();
  });

  it("enables the runtime and reports the changed desired state to the editor", async () => {
    const onPublisherChange = renderOperations();
    fireEvent.click(screen.getByRole("button", { name: "Enable" }));

    await waitFor(() => expect(mockedEnable).toHaveBeenCalledWith("publisher-1"));
    expect(onPublisherChange).toHaveBeenCalledWith(expect.objectContaining({ enabled: true }));
  });

  it("keeps the operations panel available when diagnostics are null", async () => {
    mockedListDiagnostics.mockResolvedValueOnce(null as unknown as never[]);

    renderOperations();

    expect(await screen.findByText("No diagnostic messages received")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Test connection" })).toBeEnabled();
  });
});
