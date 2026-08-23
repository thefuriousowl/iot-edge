import { describe, expect, it } from "vitest";

import type { CredentialProfile } from "../../../types/credential";
import type { DataPublisher, PublisherRuntimeStatus, PublisherSourceCatalogEntry, PublisherSourceDraft } from "../../../types/publisher";
import {
  exportHTTPServerPublisherConfig,
  generateHTTPServerPayloadTemplate,
  httpServerEndpoint,
  importHTTPServerPublisherDraft,
  initialHTTPServerPublisherDraft,
  normalizeHTTPServerErrorMessage,
  validateHTTPServerEndpoint,
  validateHTTPServerPublisherDraft,
} from "./httpServer";

const source: PublisherSourceDraft = {
  id: "source-1",
  alias: "active_power",
  name: "Active power",
  owner_name: "Power meter",
  kind: "tag",
  data_type: "float64",
  unit: "kW",
  period_kind: "instantaneous",
  reference: { kind: "tag", tag_id: "tag-1" },
};

const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "http_server", state: "stopped", config_version: 2,
  last_transition_at: "2026-08-23T00:00:00Z", request_count: 0, publish_count: 0,
  failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false,
  connection_count: 0, delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0,
  transport_drop_count: 0, diagnostic_count: 0, diagnostic_drop_count: 0,
};

describe("HTTP Server Publisher frontend contract", () => {
  it("uses secure defaults and requires the matching credential slot", () => {
    const draft = initialHTTPServerPublisherDraft();

    expect(draft.http).toMatchObject({ bind_address: "127.0.0.1", port: 8088, path: "/snapshot", access_mode: "api_key" });
    expect(validateHTTPServerEndpoint(draft)).toContain("Selected HTTP Credential Profile must contain http.api_key");

    draft.credential_id = "credential-1";
    draft.credential_slots = ["http.api_key"];
    expect(validateHTTPServerEndpoint(draft)).toEqual([]);
  });

  it("requires explicit acknowledgement for anonymous access", () => {
    const draft = initialHTTPServerPublisherDraft();
    draft.http.access_mode = "anonymous";

    expect(validateHTTPServerEndpoint(draft)).toContain("Acknowledge that the HTTP endpoint will be anonymous");
    draft.http.anonymous_acknowledged = true;
    expect(validateHTTPServerEndpoint(draft)).toEqual([]);
  });

  it("rejects unsafe endpoint and limit values", () => {
    const draft = initialHTTPServerPublisherDraft();
    draft.http.bind_address = "localhost";
    draft.http.port = 70_000;
    draft.http.path = "/snapshot/../secret?raw=1";
    draft.http.api_key_header = "Authorization";
    draft.http.max_connections = 10_001;

    const errors = validateHTTPServerPublisherDraft(draft);
    expect(errors).toEqual(expect.arrayContaining([
      "Bind address must be an IP address",
      "Port must be between 1 and 65,535",
      "Path must be an exact absolute path without query, fragment, or traversal",
      "API key header is invalid or reserved",
      "Maximum connections must be between 1 and 10,000",
    ]));
  });

  it("exports the exact backend shape without secret material", () => {
    const draft = initialHTTPServerPublisherDraft();
    draft.sources = [source];
    draft.credential_id = "credential-1";
    draft.credential_slots = ["http.api_key"];
    draft.response.payload_template = '{"power_kw":{{value "active_power"}}}';

    const config = exportHTTPServerPublisherConfig(draft);

    expect(config).toEqual({
      trigger: { mode: "interval", interval_ms: 60_000 },
      http: {
        bind_address: "127.0.0.1", port: 8088, path: "/snapshot",
        access: { mode: "api_key", api_key_header: "X-Api-Key" }, quality_policy: "payload",
        read_timeout_ms: 5_000, write_timeout_ms: 5_000, idle_timeout_ms: 30_000,
        max_header_bytes: 16_384, max_connections: 64,
      },
      response: { payload_template: '{"power_kw":{{value "active_power"}}}' },
    });
    expect(JSON.stringify(config)).not.toContain("credential-1");
    expect(JSON.stringify(config)).not.toContain("secret");
  });

  it("generates provenance-aware JSON for every selected source", () => {
    const windowed: PublisherSourceDraft = {
      ...source,
      id: "source-2",
      alias: "today_cost",
      name: "Today covered cost",
      owner_name: "Energy Management",
      kind: "plugin_output",
      unit: "THB",
      period_kind: "windowed",
      reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.covered_estimated_cost" },
    };

    const template = generateHTTPServerPayloadTemplate([source, windowed]);

    expect(template).toContain('"active_power": {');
    expect(template).toContain('"value": {{value "active_power"}}');
    expect(template).not.toContain('{{coverage "active_power"}}');
    expect(template).toContain('"today_cost": {');
    expect(template).toContain('"period_start": {{period_start "today_cost"}}');
    expect(template).toContain('"period_end": {{period_end "today_cost"}}');
    expect(template).toContain('"coverage_percent": {{coverage "today_cost"}}');
    expect(() => JSON.parse(template
      .replace(/{{publisher_id}}/g, '"publisher-1"')
      .replace(/{{published_at}}/g, '"2026-08-23T00:00:00Z"')
      .replace(/{{[^}]+}}/g, "null"))).not.toThrow();
  });

  it("removes MQTT terminology from HTTP Server errors", () => {
    expect(normalizeHTTPServerErrorMessage("MQTT payload invalid")).toBe("HTTP response template invalid");
    expect(normalizeHTTPServerErrorMessage("Core rejected the MQTT payload template")).toBe("Core rejected the HTTP response template");
    expect(normalizeHTTPServerErrorMessage("HTTP response template is invalid")).toBe("HTTP response template is invalid");
  });

  it("imports saved configuration from authoritative source and credential metadata", () => {
    const draft = initialHTTPServerPublisherDraft();
    draft.sources = [source];
    draft.credential_id = "credential-1";
    draft.credential_slots = ["http.api_key"];
    const entity: DataPublisher = {
      id: "publisher-1", type: "http_server", name: "Plant snapshot", enabled: false,
      config_version: 2, source_count: 1, credential_id: "credential-1", runtime,
      config: exportHTTPServerPublisherConfig(draft),
      sources: [{ alias: "active_power", reference: { kind: "tag", tag_id: "tag-1" } }],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    };
    const catalog: PublisherSourceCatalogEntry[] = [{ descriptor: {
      reference: { kind: "tag", tag_id: "tag-1" }, name: "Active power", owner_name: "Power meter",
      schema_version: 1, data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
    }, current: { quality: "good" } }];
    const credentials: CredentialProfile[] = [{
      id: "credential-1", type: "http", name: "Plant API", secret_revision: 1, usage_count: 1,
      secrets: [{ slot: "http.api_key", kind: "opaque", revision: 1, created_at: "2026-08-23T00:00:00Z", rotated_at: "2026-08-23T00:00:00Z" }],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    }];

    const imported = importHTTPServerPublisherDraft(entity, catalog, credentials);

    expect(imported).toMatchObject({ name: "Plant snapshot", credential_id: "credential-1", credential_slots: ["http.api_key"] });
    expect(imported.sources[0]).toMatchObject({ alias: "active_power", name: "Active power", unit: "kW" });
  });

  it("renders wildcard and IPv6 bind addresses as usable endpoint URLs", () => {
    const draft = initialHTTPServerPublisherDraft();
    draft.http.bind_address = "0.0.0.0";
    expect(httpServerEndpoint(draft, "edge.local")).toBe("http://edge.local:8088/snapshot");
    draft.http.bind_address = "::1";
    expect(httpServerEndpoint(draft)).toBe("http://[::1]:8088/snapshot");
  });
});
