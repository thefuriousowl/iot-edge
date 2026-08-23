import { describe, expect, it } from "vitest";

import type { PublisherSourceDraft } from "../../../types/publisher";
import {
  exportMQTTPublisherConfig,
  generatePayloadTemplate,
  initialMQTTPublisherDraft,
  isValidPublishTopic,
  isValidTopicFilter,
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

  it("generates custom field mappings with raw safe helper actions", () => {
    const template = generatePayloadTemplate([
      { id: "one", field: "activePower", alias: "power_kw", helper: "value" },
      { id: "two", field: "energyPeriodStart", alias: "energy_today", helper: "period_start" },
    ]);

    expect(template).toContain('"activePower": {{value "power_kw"}}');
    expect(template).toContain('"energyPeriodStart": {{period_start "energy_today"}}');
    expect(previewPayloadTemplate(template, sources, "good").rendered).toContain('"activePower": 42.75');
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

  it("exports the backend MQTT v3 shape with secret references only", () => {
    const draft = initialMQTTPublisherDraft();
    draft.sources = sources;
    draft.trigger = { mode: "on_change", source_alias: "power_kw", interval_ms: 60_000, coalesce_ms: 250 };
    draft.mqtt.tls.custom_ca_ref = "tls.ca";
    draft.mqtt.tls.client_identity_ref = "tls.client";
    const exported = exportMQTTPublisherConfig(draft);

    expect(exported.trigger).toEqual({ mode: "on_change", source_alias: "power_kw", coalesce_ms: 250 });
    expect(exported.mqtt.auth).toEqual({ username: { name: "mqtt.username" }, password: { name: "mqtt.password" } });
    expect(exported.mqtt.tls).toEqual({ custom_ca: { name: "tls.ca" }, client_identity: { name: "tls.client" } });
    expect(JSON.stringify(exported)).not.toContain("certificate");
    expect(JSON.stringify(exported)).not.toContain("private_key");
  });

  it("requires explicit acknowledgement for plain MQTT", () => {
    const draft = initialMQTTPublisherDraft();
    draft.mqtt.broker_url = "mqtt://broker.example.com:1883";
    draft.mqtt.plaintext_acknowledged = false;

    expect(validateMQTTPublisherDraft(draft)).toContain("Acknowledge that plain MQTT sends traffic without TLS");
    draft.mqtt.plaintext_acknowledged = true;
    expect(validateMQTTPublisherDraft(draft)).not.toContain("Acknowledge that plain MQTT sends traffic without TLS");
  });
});
