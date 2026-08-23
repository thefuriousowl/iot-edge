// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { listCredentials } from "../../../services/credential.service";
import { createDataPublisher, getDataPublisher, getDataPublisherStatus, listPublisherSources, validatePublisherPayload } from "../../../services/publisher.service";
import type { CredentialProfile } from "../../../types/credential";
import type { PublisherRuntimeStatus, PublisherSourceCatalogEntry } from "../../../types/publisher";
import HTTPServerPublisherWizardPage from "./HTTPServerPublisherWizardPage";

vi.mock("../../../services/credential.service", () => ({ listCredentials: vi.fn() }));
vi.mock("../../../services/publisher.service", () => ({
  createDataPublisher: vi.fn(), disableDataPublisher: vi.fn(), enableDataPublisher: vi.fn(),
  getDataPublisher: vi.fn(), getDataPublisherStatus: vi.fn(), listPublisherSources: vi.fn(),
  restartDataPublisher: vi.fn(), updateDataPublisher: vi.fn(), validatePublisherPayload: vi.fn(),
}));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet online</span> }));

const mockedCreate = vi.mocked(createDataPublisher);
const mockedCredentials = vi.mocked(listCredentials);
const mockedDetail = vi.mocked(getDataPublisher);
const mockedStatus = vi.mocked(getDataPublisherStatus);
const mockedSources = vi.mocked(listPublisherSources);
const mockedValidate = vi.mocked(validatePublisherPayload);

const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "http_server", state: "stopped", config_version: 1,
  last_transition_at: "2026-08-23T00:00:00Z", request_count: 0, publish_count: 0,
  failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false,
  connection_count: 0, delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0,
  transport_drop_count: 0, diagnostic_count: 0, diagnostic_drop_count: 0,
};

const catalog: PublisherSourceCatalogEntry[] = [
  {
    descriptor: {
      reference: { kind: "tag", tag_id: "tag-1" }, name: "Active power", owner_name: "Power meter",
      schema_version: 1, data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
    },
    current: { quality: "good", sequence: 8, observed_at: "2026-08-23T08:00:00Z" },
  },
  {
    descriptor: {
      reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.covered_estimated_cost" },
      name: "Today covered cost", owner_name: "Energy Management", schema_version: 1,
      data_type: "float64", unit: "THB", period_kind: "windowed", enabled: true,
    },
    current: {
      quality: "partial", sequence: 18, observed_at: "2026-08-23T14:07:06Z",
      period_start: "2026-08-22T17:00:00Z", period_end: "2026-08-23T14:07:06Z", coverage_percent: 6.793,
    },
  },
];

const apiKeyProfile: CredentialProfile = {
  id: "credential-1", type: "http", name: "Plant HTTP API", secret_revision: 1, usage_count: 0,
  secrets: [{ slot: "http.api_key", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" }],
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

function renderPage() {
  return render(<MemoryRouter><HTTPServerPublisherWizardPage /></MemoryRouter>);
}

describe("HTTPServerPublisherWizardPage", () => {
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
    mockedCredentials.mockReset().mockResolvedValue([apiKeyProfile]);
    mockedSources.mockReset().mockResolvedValue(catalog);
    mockedValidate.mockReset().mockResolvedValue({
      referenced_aliases: ["active_power"], helper_calls: 1,
      good: { power_kw: 42.75 }, unavailable: { power_kw: null }, windowed: { power_kw: 42.75 },
    });
    mockedCreate.mockReset().mockResolvedValue({
      id: "publisher-1", type: "http_server", name: "HTTP snapshot", enabled: false,
      config_version: 1, source_count: 1, runtime, config: {}, sources: [],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    });
    mockedDetail.mockReset();
    mockedStatus.mockReset().mockResolvedValue(runtime);
  });

  afterEach(() => cleanup());

  it("starts secure and blocks API-key access until a matching profile is selected", async () => {
    renderPage();

    expect(screen.getByRole("heading", { name: "HTTP Server Publisher" })).toBeInTheDocument();
    expect(screen.getByLabelText("Bind address")).toHaveValue("127.0.0.1");
    expect(screen.getByLabelText("HTTP Server port")).toHaveValue(8088);
    expect(screen.getByLabelText("Access mode")).toHaveValue("api_key");
    expect(await screen.findByRole("option", { name: "Plant HTTP API" })).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("alert")).toHaveTextContent("must contain http.api_key");

    fireEvent.change(screen.getByLabelText("HTTP Credential Profile"), { target: { value: "credential-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("heading", { name: "Sources and snapshot trigger" })).toBeInTheDocument();
  });

  it("requires an explicit operator acknowledgement for anonymous access", () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Access mode"), { target: { value: "anonymous" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("alert")).toHaveTextContent("endpoint will be anonymous");

    fireEvent.click(screen.getByRole("checkbox", { name: /I understand this endpoint has no authentication/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(screen.getByRole("heading", { name: "Sources and snapshot trigger" })).toBeInTheDocument();
  });

  it("keeps JSON operator-authored, validates it, and creates a disabled HTTP Server", async () => {
    renderPage();
    fireEvent.change(await screen.findByLabelText("HTTP Credential Profile"), { target: { value: "credential-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    const sourceTitle = await screen.findByText("Active power");
    fireEvent.click(within(sourceTitle.closest("article")!).getByRole("button", { name: "Add" }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    const editor = screen.getByLabelText("Response payload template") as HTMLTextAreaElement;
    fireEvent.change(editor, { target: { value: '{"power_kw":}' } });
    const insertionPoint = editor.value.indexOf(":") + 1;
    editor.setSelectionRange(insertionPoint, insertionPoint);
    fireEvent.click(screen.getByRole("button", { name: /active_power/ }));
    expect(editor).toHaveValue('{"power_kw":{{value "active_power"}}}');
    fireEvent.click(screen.getByRole("button", { name: "Validate with Core" }));
    await waitFor(() => expect(mockedValidate).toHaveBeenCalledWith(
      '{"power_kw":{{value "active_power"}}}',
      [{ alias: "active_power", reference: { kind: "tag", tag_id: "tag-1" } }],
    ));

    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Save disabled Publisher" }));

    await waitFor(() => expect(mockedCreate).toHaveBeenCalled());
    expect(mockedCreate.mock.calls[0][0]).toMatchObject({
      type: "http_server", enabled: false, credential_id: "credential-1",
      config: { http: { access: { mode: "api_key", api_key_header: "X-Api-Key" } } },
      sources: [{ alias: "active_power", reference: { kind: "tag", tag_id: "tag-1" } }],
    });
    expect(JSON.stringify(mockedCreate.mock.calls[0][0].config)).not.toContain("credential-1");
    expect(screen.getByRole("status")).toHaveTextContent("Disabled HTTP Server saved");
  });

  it("generates HTTP JSON with Plugin-output period and coverage", async () => {
    renderPage();
    fireEvent.change(await screen.findByLabelText("HTTP Credential Profile"), { target: { value: "credential-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    for (const name of ["Active power", "Today covered cost"]) {
      const title = await screen.findByText(name);
      fireEvent.click(within(title.closest("article")!).getByRole("button", { name: "Add" }));
    }
    expect(screen.getByRole("progressbar", { name: "Coverage 6.8 percent" })).toBeInTheDocument();
    expect(screen.getByLabelText("Period from 2026-08-22T17:00:00Z to 2026-08-23T14:07:06Z")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Generate JSON" }));

    const template = (screen.getByLabelText("Response payload template") as HTMLTextAreaElement).value;
    expect(template).toContain('"active_power": {');
    expect(template).toContain('"today_covered_cost": {');
    expect(template).toContain('{{period_start "today_covered_cost"}}');
    expect(template).toContain('{{coverage "today_covered_cost"}}');
    expect(screen.getByRole("status")).toHaveTextContent("Generated HTTP JSON from 2 selected sources");
  });

  it("reports HTTP response wording when a shared validator returns an MQTT-labelled error", async () => {
    mockedValidate.mockRejectedValueOnce(Object.assign(new Error("request failed"), {
      isAxiosError: true,
      response: { data: { error: { message: "MQTT payload invalid" } } },
    }));
    renderPage();
    fireEvent.change(await screen.findByLabelText("HTTP Credential Profile"), { target: { value: "credential-1" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    const sourceTitle = await screen.findByText("Active power");
    fireEvent.click(within(sourceTitle.closest("article")!).getByRole("button", { name: "Add" }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: "Validate with Core" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("HTTP response template invalid");
    expect(screen.getByRole("alert")).not.toHaveTextContent("MQTT");
  });

  it("keeps Disable and Restart available outside the enabled configuration lock", async () => {
    mockedDetail.mockResolvedValue({
      id: "publisher-1", type: "http_server", name: "Running snapshot", enabled: true,
      config_version: 1, source_count: 1, credential_id: "credential-1",
      runtime: { ...runtime, state: "running", connected: true },
      config: {
        trigger: { mode: "interval", interval_ms: 60_000 },
        http: {
          bind_address: "127.0.0.1", port: 8088, path: "/snapshot",
          access: { mode: "api_key", api_key_header: "X-Api-Key" }, quality_policy: "payload",
          read_timeout_ms: 5_000, write_timeout_ms: 5_000, idle_timeout_ms: 30_000,
          max_header_bytes: 16_384, max_connections: 64,
        },
        response: { payload_template: '{"power_kw":{{value "active_power"}}}' },
      },
      sources: [{ alias: "active_power", reference: { kind: "tag", tag_id: "tag-1" } }],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    });
    render(<MemoryRouter initialEntries={["/data-publishers/publisher-1/http-server"]}><Routes><Route path="/data-publishers/:id/http-server" element={<HTTPServerPublisherWizardPage />} /></Routes></MemoryRouter>);

    await screen.findByText("Configuration locked while enabled.");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(await screen.findByRole("button", { name: "Disable" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Restart" })).toBeEnabled();
    expect(screen.getByRole("button", { name: "Update configuration" })).toBeDisabled();
  });
});
