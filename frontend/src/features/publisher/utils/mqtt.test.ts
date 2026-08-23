import { describe, expect, it } from "vitest";

import type { DataPublisher, PublisherRuntimeStatus, PublisherSourceCatalogEntry, PublisherSourceDraft } from "../../../types/publisher";
import {
  exportMQTTPublisherConfig,
  importMQTTPublisherDraft,
  initialMQTTPublisherDraft,
  isValidPublishTopic,
  isValidTopicFilter,
  mqttPayloadSourceSyntax,
  previewPayloadTemplate,
  validateBrokerURL,
  validateMQTTPublisherDraft,
} from "./mqtt";

const sources: PublisherSourceDraft[] = [
  {
    id: "tag-source",
    alias: "power_kw",
    name: "Power",
    owner_name: "Power meter",
    kind: "tag",
    data_type: "float64",
    unit: "kW",
    period_kind: "instantaneous",
  },
  {
    id: "output-source",
    alias: "energy_today",
    name: "Energy today",
    owner_name: "Energy Management",
    kind: "plugin_output",
    data_type: "float64",
    unit: "kWh",
    period_kind: "windowed",
  },
];

describe("MQTT Publisher frontend contract", () => {
  it("validates strict broker URLs without embedded credentials", () => {
    expect(validateBrokerURL("mqtts://broker.example.com:8883")).toBeNull();
    expect(validateBrokerURL("mqtt://127.0.0.1:1883")).toBeNull();
    expect(validateBrokerURL("https://broker.example.com:8883")).toMatch(/scheme/i);
    expect(validateBrokerURL("mqtts://user:password@broker.example.com:8883")).toMatch(/credentials/i);
    expect(validateBrokerURL("mqtts://broker.example.com")).toMatch(/port/i);
    expect(validateBrokerURL("mqtts://broker.example.com:8883/path")).toMatch(/path/i);
  });

  it("distinguishes publish topics from generic diagnostic filters", () => {
    expect(isValidPublishTopic("tenant/site/telemetry")).toBe(true);
    expect(isValidPublishTopic("tenant/+/telemetry")).toBe(false);
    expect(isValidTopicFilter("tenant/+/ack")).toBe(true);
    expect(isValidTopicFilter("tenant/#")).toBe(true);
    expect(isValidTopicFilter("tenant/#/ack")).toBe(false);
    expect(isValidTopicFilter("tenant/a+ck")).toBe(false);
  });

  it("builds insertable source helper syntax without generating the payload", () => {
    expect(mqttPayloadSourceSyntax("value", "power_kw")).toBe('{{value "power_kw"}}');
    expect(mqttPayloadSourceSyntax("period_start", "energy_today")).toBe('{{period_start "energy_today"}}');
    expect(mqttPayloadSourceSyntax("round", "power_kw")).toBe('{{round "power_kw" 2}}');
    expect(mqttPayloadSourceSyntax("scale", "power_kw")).toBe('{{scale "power_kw" 1 0}}');
    expect(mqttPayloadSourceSyntax("format_time", "power_kw")).toBe('{{format_time "power_kw" "observed_at" "Asia/Bangkok" "2006-01-02 15:04:05"}}');
  });

  it.each(["good", "unavailable", "windowed"] as const)("renders valid %s payload fixtures", (fixture) => {
    const template = `{
      "timestamp": {{published_unix_ms}},
      "power": {{round "power_kw" 2}},
      "available": {{available "power_kw"}},
      "energy": {{default "energy_today" 0}},
      "periodStart": {{period_start "energy_today"}},
      "coverage": {{coverage "energy_today"}}
    }`;
    const preview = previewPayloadTemplate(template, sources, fixture);

    expect(() => JSON.parse(preview.rendered)).not.toThrow();
    expect(preview.referencedAliases).toEqual(["energy_today", "power_kw"]);
    expect(preview.helperCalls).toBe(6);
    if (fixture === "unavailable") expect(preview.rendered).toContain('"available": false');
    if (fixture === "windowed") expect(preview.rendered).toContain('"coverage": 98.6');
  });

  it("rejects unsafe, unresolved, and malformed payloads", () => {
    expect(() => previewPayloadTemplate('{{if true}}{"ok":true}{{end}}', sources, "good")).toThrow(/control flow/i);
    expect(() => previewPayloadTemplate('{"value":{{value "missing"}}}', sources, "good")).toThrow(/unknown source alias/i);
    expect(() => previewPayloadTemplate('{"value":{{not_allowed "power_kw"}}}', sources, "good")).toThrow(/unknown payload helper/i);
    expect(() => previewPayloadTemplate('{"value":{{value "power_kw"}}', sources, "good")).toThrow(/valid JSON/i);
  });

  it("exports the backend MQTT v3 shape with internal profile references", () => {
    const draft = initialMQTTPublisherDraft();
    draft.sources = sources;
    draft.trigger = { mode: "on_change", source_alias: "power_kw", interval_ms: 60_000, coalesce_ms: 250 };
    draft.credential_id = "credential-1";
    draft.credential_slots = ["mqtt.username", "mqtt.password", "mqtt.custom_ca", "mqtt.client_identity"];
    const exported = exportMQTTPublisherConfig(draft);

    expect(exported.trigger).toEqual({ mode: "on_change", source_alias: "power_kw", coalesce_ms: 250 });
    expect(exported.mqtt.auth).toEqual({ username: { name: "mqtt.username" }, password: { name: "mqtt.password" } });
    expect(exported.mqtt.tls).toEqual({ custom_ca: { name: "mqtt.custom_ca" }, client_identity: { name: "mqtt.client_identity" } });
    expect(JSON.stringify(exported)).not.toContain("certificate");
    expect(JSON.stringify(exported)).not.toContain("private_key");
  });

  it("imports an existing Publisher detail using authoritative catalog descriptors", () => {
    const runtime: PublisherRuntimeStatus = {
      publisher_id: "publisher-1", type: "mqtt", state: "stopped", config_version: 4,
      last_transition_at: "2026-08-23T00:00:00Z", request_count: 0, publish_count: 0, failure_count: 0,
      queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false, connection_count: 0,
      delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0, transport_drop_count: 0,
      diagnostic_count: 0, diagnostic_drop_count: 0,
    };
    const catalog: PublisherSourceCatalogEntry[] = [{
      descriptor: {
        reference: { kind: "tag", tag_id: "tag-1" }, name: "Power", owner_name: "Meter",
        schema_version: 1, data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
      },
      current: { quality: "good" },
    }];
    const sourceDraft = initialMQTTPublisherDraft();
    sourceDraft.credential_id = "credential-1";
    sourceDraft.credential_slots = ["mqtt.username"];
    const config = exportMQTTPublisherConfig(sourceDraft);
    const entity: DataPublisher = {
      id: "publisher-1", type: "mqtt", name: "Imported telemetry", enabled: false, config_version: 4,
      source_count: 1, credential_id: "credential-1", runtime, config, sources: [{ alias: "power_kw", reference: { kind: "tag", tag_id: "tag-1" } }],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    };

    const draft = importMQTTPublisherDraft(entity, catalog);

    expect(draft.name).toBe("Imported telemetry");
    expect(draft.sources[0]).toMatchObject({ alias: "power_kw", name: "Power", owner_name: "Meter", unit: "kW" });
    expect(draft.credential_id).toBe("credential-1");
    expect(draft.credential_slots).toContain("mqtt.username");
    expect(draft.mqtt).toMatchObject({ broker_host: "broker.example.com", broker_port: 8883, use_tls: true });
    expect(JSON.stringify(draft)).not.toContain("password_value");
  });

  it("rejects an edit when an authoritative source descriptor disappeared", () => {
    const draft = initialMQTTPublisherDraft();
    const runtime = {
      publisher_id: "publisher-1", type: "mqtt" as const, state: "stopped" as const, config_version: 1,
      last_transition_at: "2026-08-23T00:00:00Z", request_count: 0, publish_count: 0, failure_count: 0,
      queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false, connection_count: 0,
      delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0, transport_drop_count: 0,
      diagnostic_count: 0, diagnostic_drop_count: 0,
    };
    const entity: DataPublisher = {
      id: "publisher-1", type: "mqtt", name: "Broken", enabled: false, config_version: 1, source_count: 1,
      runtime, config: exportMQTTPublisherConfig(draft),
      sources: [{ alias: "missing", reference: { kind: "tag", tag_id: "missing-tag" } }],
      created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
    };

    expect(() => importMQTTPublisherDraft(entity, [])).toThrow(/no longer available/i);
  });

  it("requires explicit acknowledgement for plain MQTT", () => {
    const draft = initialMQTTPublisherDraft();
    draft.mqtt.use_tls = false;
    draft.mqtt.broker_port = 1883;
    draft.mqtt.plaintext_acknowledged = false;

    expect(validateMQTTPublisherDraft(draft)).toContain("Acknowledge that plain MQTT sends traffic without TLS");
    draft.mqtt.plaintext_acknowledged = true;
    expect(validateMQTTPublisherDraft(draft)).not.toContain("Acknowledge that plain MQTT sends traffic without TLS");
  });
});
