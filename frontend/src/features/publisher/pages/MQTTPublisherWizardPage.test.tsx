// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter } from "react-router-dom";

import {
  createDataPublisher,
  getDataPublisherStatus,
  listPublisherDiagnostics,
  listPublisherSources,
  validatePublisherPayload,
} from "../../../services/publisher.service";
import { listCredentials } from "../../../services/credential.service";
import type { PublisherRuntimeStatus, PublisherSourceCatalogEntry } from "../../../types/publisher";
import MQTTPublisherWizardPage from "./MQTTPublisherWizardPage";

vi.mock("../../../services/publisher.service", () => ({
  createDataPublisher: vi.fn(),
  deletePublisherSecret: vi.fn(),
  disableDataPublisher: vi.fn(),
  enableDataPublisher: vi.fn(),
  getDataPublisher: vi.fn(),
  getDataPublisherStatus: vi.fn(),
  listPublisherDiagnostics: vi.fn(),
  listPublisherSources: vi.fn(),
  putPublisherSecret: vi.fn(),
  restartDataPublisher: vi.fn(),
  testMQTTConnection: vi.fn(),
  updateDataPublisher: vi.fn(),
  validatePublisherPayload: vi.fn(),
}));

vi.mock("../../../services/credential.service", () => ({ listCredentials: vi.fn() }));

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet online</span>,
}));

const mockedCreateDataPublisher = vi.mocked(createDataPublisher);
const mockedGetDataPublisherStatus = vi.mocked(getDataPublisherStatus);
const mockedListPublisherDiagnostics = vi.mocked(listPublisherDiagnostics);
const mockedListCredentials = vi.mocked(listCredentials);
const mockedListPublisherSources = vi.mocked(listPublisherSources);
const mockedValidatePublisherPayload = vi.mocked(validatePublisherPayload);
const runtime: PublisherRuntimeStatus = {
  publisher_id: "33333333-3333-4333-8333-333333333333", type: "mqtt", state: "stopped", config_version: 3,
  last_transition_at: "2026-08-23T08:35:25.573Z", request_count: 0, publish_count: 0, failure_count: 0,
  queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false, connection_count: 0,
  delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0, transport_drop_count: 0,
  diagnostic_count: 0, diagnostic_drop_count: 0,
};
const catalog: PublisherSourceCatalogEntry[] = [
  {
    descriptor: {
      reference: { kind: "tag", tag_id: "11111111-1111-4111-8111-111111111111" },
      name: "Active power", owner_name: "Power meter", schema_version: 1, data_type: "float64",
      unit: "kW", period_kind: "instantaneous", enabled: true,
    },
    current: { quality: "good", sequence: 42, observed_at: "2026-08-23T08:35:25.573Z" },
  },
  {
    descriptor: {
      reference: { kind: "plugin_output", plugin_instance_id: "22222222-2222-4222-8222-222222222222", output_key: "energy_today_kwh" },
      name: "Energy today", owner_name: "Energy Management", schema_version: 1, data_type: "float64",
      unit: "kWh", period_kind: "windowed", enabled: true,
    },
    current: {
      quality: "partial", sequence: 7, observed_at: "2026-08-23T08:35:25.573Z",
      period_start: "2026-08-23T00:00:00Z", period_end: "2026-08-23T08:35:25.573Z", coverage_percent: 82.5,
    },
  },
];

function renderPage() {
  return render(<MemoryRouter><MQTTPublisherWizardPage /></MemoryRouter>);
}

async function addCatalogSource(name: string) {
  const title = await screen.findByText(name);
  const row = title.closest<HTMLElement>("article");
  expect(row).not.toBeNull();
  fireEvent.click(within(row!).getByRole("button", { name: "Add" }));
}

describe("MQTTPublisherWizardPage", () => {
  beforeEach(() => {
    const values = new Map<string, string>();
    Object.defineProperty(window, "localStorage", {
      configurable: true,
      value: {
        clear: () => values.clear(),
        getItem: (key: string) => values.get(key) ?? null,
        key: (index: number) => [...values.keys()][index] ?? null,
        get length() { return values.size; },
        removeItem: (key: string) => values.delete(key),
        setItem: (key: string, value: string) => values.set(key, value),
      },
    });
    localStorage.clear();
    Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
    mockedListPublisherSources.mockReset().mockResolvedValue(catalog);
    mockedValidatePublisherPayload.mockReset().mockResolvedValue({
      referenced_aliases: ["active_power"], helper_calls: 1,
      good: { power: 42.75 }, unavailable: { power: null }, windowed: { power: 42.75 },
    });
    mockedCreateDataPublisher.mockReset().mockResolvedValue({
      id: "33333333-3333-4333-8333-333333333333", type: "mqtt", name: "MQTT telemetry", enabled: false,
      config_version: 3, source_count: 1, runtime, config: {}, sources: [],
      created_at: "2026-08-23T08:35:25.573Z", updated_at: "2026-08-23T08:35:25.573Z",
    });
    mockedGetDataPublisherStatus.mockReset().mockResolvedValue(runtime);
    mockedListPublisherDiagnostics.mockReset().mockResolvedValue([]);
    mockedListCredentials.mockReset().mockResolvedValue([{
      id: "credential-1", type: "mqtt", name: "Plant broker", secret_revision: 2, usage_count: 0,
      secrets: [
        { slot: "mqtt.username", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" },
        { slot: "mqtt.password", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" },
      ],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    }]);
  });

  afterEach(() => cleanup());

  it("separates host, port, TLS, and optional Credential Profile", async () => {
    renderPage();

    expect(screen.getByRole("heading", { name: "MQTT Publisher" })).toBeInTheDocument();
    expect(screen.getByLabelText(/Broker host/)).toHaveValue("broker.example.com");
    expect(screen.getByLabelText(/Broker port/)).toHaveValue(8883);
    expect(screen.getByLabelText(/Transport security/)).toHaveValue("tls");
    expect(await screen.findByRole("option", { name: "Plant broker" })).toBeInTheDocument();
    expect(screen.getByLabelText(/Credential Profile/)).toHaveValue("");
    expect(screen.queryByLabelText(/^Username$/)).not.toBeInTheDocument();
    expect(screen.queryByLabelText(/^Password$/)).not.toBeInTheDocument();
    expect(screen.queryByRole("button", { name: "Test connection" })).not.toBeInTheDocument();
  });

  it("requires acknowledgement when the operator selects plain MQTT", () => {
    renderPage();
    fireEvent.change(screen.getByLabelText(/Transport security/), { target: { value: "plain" } });
    expect(screen.getByLabelText(/Broker port/)).toHaveValue(1883);
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("alert")).toHaveTextContent("Acknowledge the plain MQTT exposure");
    fireEvent.click(screen.getByRole("checkbox", { name: /I understand this connection is not encrypted/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("heading", { name: "Payload source aliases" })).toBeInTheDocument();
  });

  it("shows Plugin-output coverage and period in the shared source catalog", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    await screen.findByText("Energy today");
    expect(screen.getByRole("progressbar", { name: "Coverage 82.5 percent" })).toBeInTheDocument();
    expect(screen.getByLabelText("Period from 2026-08-23T00:00:00Z to 2026-08-23T08:35:25.573Z")).toBeInTheDocument();
  });

  it("keeps the payload freely editable and inserts source syntax at the cursor", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await addCatalogSource("Active power");
    fireEvent.change(screen.getByLabelText("Alias for Active power"), { target: { value: "meter_power" } });
    expect(screen.getByLabelText("Display name for meter_power")).toBeDisabled();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("heading", { name: "Custom JSON payload" })).toBeInTheDocument();
    const editor = screen.getByLabelText(/^Advanced payload template/) as HTMLTextAreaElement;
    const customTemplate = '{"active_power_kw":,"note":"keep this custom JSON"}';
    fireEvent.change(editor, { target: { value: customTemplate } });
    const insertionPoint = customTemplate.indexOf(":") + 1;
    editor.setSelectionRange(insertionPoint, insertionPoint);
    fireEvent.click(screen.getByRole("button", { name: "Insert value syntax for meter_power" }));
    expect(editor.value).toBe('{"active_power_kw":{{value "meter_power"}},"note":"keep this custom JSON"}');
    fireEvent.click(screen.getByRole("tab", { name: "Unavailable" }));
    expect(screen.getByText(/"active_power_kw": null/)).toBeInTheDocument();
    expect(screen.getByText(/"note": "keep this custom JSON"/)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("Payload source helper"), { target: { value: "quality" } });
    editor.setSelectionRange(editor.value.length, editor.value.length);
    fireEvent.click(screen.getByRole("button", { name: "Insert quality syntax for meter_power" }));
    expect(editor.value.endsWith('{{quality "meter_power"}}')).toBe(true);
  });

  it("keeps diagnostics generic and saves a secret-free local draft", async () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Publisher name"), { target: { value: "Recovered MQTT draft" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await addCatalogSource("Active power");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(screen.getByRole("heading", { name: "Diagnostics and review" })).toBeInTheDocument();
    expect(screen.getByText(/Messages remain generic JSON/)).toBeInTheDocument();
    expect(screen.queryByRole("heading", { name: "Generic diagnostic monitor" })).not.toBeInTheDocument();
    expect(screen.queryByText("No broker message is fabricated while runtime endpoints are unavailable.")).not.toBeInTheDocument();
    expect(screen.queryByText("processed")).not.toBeInTheDocument();

    const config = screen.getByRole("heading", { name: "Server configuration preview" }).closest<HTMLElement>("div.mqtt-review");
    expect(config).not.toBeNull();
    expect(within(config!).getByText(/"auth": \{\}/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Save local draft" }));

    expect(screen.getByRole("status")).toHaveTextContent("Draft saved in this browser");
    expect(localStorage.getItem("iot-edge.mqtt-publisher-draft.v1")).not.toContain("password");
    expect(localStorage.getItem("iot-edge.mqtt-publisher-draft.v1")).not.toContain("mappings");

    cleanup();
    renderPage();
    expect(screen.getByLabelText("Publisher name")).toHaveValue("Recovered MQTT draft");
  });

  it("assigns a Credential Profile and emits only fixed internal references", async () => {
    renderPage();
    fireEvent.change(await screen.findByLabelText(/Credential Profile/), { target: { value: "credential-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await addCatalogSource("Active power");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save disabled Publisher" }));

    await waitFor(() => expect(mockedCreateDataPublisher).toHaveBeenCalled());
    expect(mockedCreateDataPublisher.mock.calls[0][0]).toMatchObject({
      credential_id: "credential-1",
      config: { mqtt: { auth: { username: { name: "mqtt.username" }, password: { name: "mqtt.password" } } } },
    });
  });

  it("validates with Core and creates only a disabled Publisher", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await addCatalogSource("Active power");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save disabled Publisher" }));

    await waitFor(() => expect(mockedValidatePublisherPayload).toHaveBeenCalled());
    await waitFor(() => expect(mockedCreateDataPublisher).toHaveBeenCalled());
    expect(mockedCreateDataPublisher.mock.calls[0][0]).toMatchObject({ type: "mqtt", enabled: false });
    expect(mockedCreateDataPublisher.mock.calls[0][0].sources).toEqual([{
      alias: "active_power",
      reference: { kind: "tag", tag_id: "11111111-1111-4111-8111-111111111111" },
    }]);
    expect(screen.getByRole("status")).toHaveTextContent("Disabled MQTT Publisher saved");
    expect(await screen.findByRole("button", { name: "Test connection" })).toBeEnabled();
  });
});
