import type {
  MQTTPayloadFixture,
  MQTTPayloadPreview,
  MQTTPublisherConfigExport,
  MQTTPublisherDraft,
  DataPublisher,
  PublisherSourceCatalogEntry,
  PublisherSourceDraft,
} from "../../../types/publisher";
import { createPublisherDraftID } from "./shared";

const aliasPattern = /^[A-Za-z_][A-Za-z0-9_.-]{0,63}$/;
const helperSpecs: Record<string, { minimum: number; maximum: number; source: boolean }> = {
  value: { minimum: 1, maximum: 1, source: true },
  available: { minimum: 1, maximum: 1, source: true },
  quality: { minimum: 1, maximum: 1, source: true },
  error: { minimum: 1, maximum: 1, source: true },
  unit: { minimum: 1, maximum: 1, source: true },
  sequence: { minimum: 1, maximum: 1, source: true },
  schema_version: { minimum: 1, maximum: 1, source: true },
  data_type: { minimum: 1, maximum: 1, source: true },
  observed_at: { minimum: 1, maximum: 1, source: true },
  emitted_at: { minimum: 1, maximum: 1, source: true },
  period_start: { minimum: 1, maximum: 1, source: true },
  period_end: { minimum: 1, maximum: 1, source: true },
  coverage: { minimum: 1, maximum: 1, source: true },
  round: { minimum: 2, maximum: 2, source: true },
  scale: { minimum: 3, maximum: 3, source: true },
  default: { minimum: 2, maximum: 2, source: true },
  format_time: { minimum: 4, maximum: 4, source: true },
  published_at: { minimum: 0, maximum: 0, source: false },
  published_unix_ms: { minimum: 0, maximum: 0, source: false },
  publisher_id: { minimum: 0, maximum: 0, source: false },
};

export const mqttPayloadHelpers = Object.keys(helperSpecs);

export type MQTTPayloadSourceHelper = "value" | "available" | "quality" | "error" | "unit" | "sequence" | "schema_version" | "data_type" | "observed_at" | "emitted_at" | "period_start" | "period_end" | "coverage" | "round" | "scale" | "default" | "format_time";

export const mqttPayloadSourceHelpers: Array<{ id: MQTTPayloadSourceHelper; label: string }> = [
  { id: "value", label: "Value" },
  { id: "available", label: "Available" },
  { id: "quality", label: "Quality" },
  { id: "error", label: "Error" },
  { id: "unit", label: "Unit" },
  { id: "sequence", label: "Sequence" },
  { id: "schema_version", label: "Schema version" },
  { id: "data_type", label: "Data type" },
  { id: "observed_at", label: "Observed at" },
  { id: "emitted_at", label: "Emitted at" },
  { id: "period_start", label: "Period start" },
  { id: "period_end", label: "Period end" },
  { id: "coverage", label: "Coverage" },
  { id: "round", label: "Round (2 decimals)" },
  { id: "scale", label: "Scale (gain 1, offset 0)" },
  { id: "default", label: "Default (0)" },
  { id: "format_time", label: "Format time (Bangkok)" },
];

export function mqttPayloadSourceSyntax(helper: MQTTPayloadSourceHelper, alias: string): string {
  const source = JSON.stringify(alias);
  switch (helper) {
    case "round": return `{{round ${source} 2}}`;
    case "scale": return `{{scale ${source} 1 0}}`;
    case "default": return `{{default ${source} 0}}`;
    case "format_time": return `{{format_time ${source} "observed_at" "Asia/Bangkok" "2006-01-02 15:04:05"}}`;
    default: return `{{${helper} ${source}}}`;
  }
}

export function initialMQTTPublisherDraft(): MQTTPublisherDraft {
  const sources: PublisherSourceDraft[] = [];

  return {
    name: "MQTT telemetry",
    enabled: false,
    credential_id: "",
    credential_slots: [],
    trigger: {
      mode: "interval",
      interval_ms: 60_000,
      source_alias: "",
      coalesce_ms: 100,
    },
    sources,
    mqtt: {
      broker_host: "broker.example.com",
      broker_port: 8883,
      use_tls: true,
      plaintext_acknowledged: false,
      client_id: "",
      tls: {
        server_name: "",
      },
      publish: {
        topic: "site/edge/telemetry",
        qos: 1,
        retain: false,
        payload_template: `{
  "timestamp": {{published_unix_ms}},
  "data": {}
}`,
      },
      diagnostics: [
        { id: createPublisherDraftID(), label: "ack", topic_filter: "site/edge/ack", qos: 1 },
        { id: createPublisherDraftID(), label: "error", topic_filter: "site/edge/error", qos: 1 },
      ],
      keep_alive_ms: 30_000,
      connect_timeout_ms: 10_000,
      publish_timeout_ms: 10_000,
      reconnect_min_ms: 1_000,
      reconnect_max_ms: 60_000,
      queue_capacity: 256,
      diagnostic_history_depth: 100,
    },
  };
}

export function importMQTTPublisherDraft(entity: DataPublisher, catalog: PublisherSourceCatalogEntry[]): MQTTPublisherDraft {
  const initial = initialMQTTPublisherDraft();
  const config = entity.config as MQTTPublisherConfigExport | undefined;
  if (!config || !config.mqtt || !entity.sources) throw new Error("MQTT Publisher detail is incomplete");
  const descriptorByReference = new Map(catalog.map((entry) => [sourceReferenceKey(entry.descriptor.reference), entry.descriptor]));
  const sources: PublisherSourceDraft[] = entity.sources.map((selection) => {
    const descriptor = descriptorByReference.get(sourceReferenceKey(selection.reference));
    if (!descriptor) throw new Error(`Publisher source is no longer available: ${selection.alias}`);
    return {
      id: createPublisherDraftID(), alias: selection.alias, reference: selection.reference,
      name: descriptor.name, owner_name: descriptor.owner_name ?? "Core", kind: descriptor.reference.kind,
      data_type: descriptor.data_type, unit: descriptor.unit ?? "", period_kind: descriptor.period_kind,
    };
  });
  const trigger = config.trigger.mode === "interval"
    ? { ...initial.trigger, mode: "interval" as const, interval_ms: config.trigger.interval_ms }
    : { ...initial.trigger, mode: "on_change" as const, source_alias: config.trigger.source_alias, coalesce_ms: config.trigger.coalesce_ms };
  return {
    ...initial,
    name: entity.name,
    enabled: entity.enabled,
    credential_id: entity.credential_id ?? "",
    credential_slots: [
      ...(config.mqtt.auth.username ? ["mqtt.username" as const] : []),
      ...(config.mqtt.auth.password ? ["mqtt.password" as const] : []),
      ...(config.mqtt.tls.custom_ca ? ["mqtt.custom_ca" as const] : []),
      ...(config.mqtt.tls.client_identity ? ["mqtt.client_identity" as const] : []),
    ],
    trigger,
    sources,
    mqtt: {
      ...initial.mqtt,
      broker_host: new URL(config.mqtt.broker_url).hostname,
      broker_port: Number(new URL(config.mqtt.broker_url).port),
      use_tls: config.mqtt.broker_url.startsWith("mqtts://"),
      plaintext_acknowledged: config.mqtt.plaintext_acknowledged ?? false,
      client_id: config.mqtt.client_id ?? "",
      tls: {
        server_name: config.mqtt.tls.server_name ?? "",
      },
      publish: { ...config.mqtt.publish },
      diagnostics: config.mqtt.diagnostics.map((diagnostic) => ({ id: createPublisherDraftID(), ...diagnostic })),
      keep_alive_ms: config.mqtt.keep_alive_ms,
      connect_timeout_ms: config.mqtt.connect_timeout_ms,
      publish_timeout_ms: config.mqtt.publish_timeout_ms,
      reconnect_min_ms: config.mqtt.reconnect_min_ms,
      reconnect_max_ms: config.mqtt.reconnect_max_ms,
      queue_capacity: config.mqtt.queue_capacity,
      diagnostic_history_depth: config.mqtt.diagnostic_history_depth,
    },
  };
}

function sourceReferenceKey(reference: PublisherSourceDraft["reference"]): string {
  if (!reference) return "";
  return reference.kind === "tag" ? `tag:${reference.tag_id}` : `plugin_output:${reference.plugin_instance_id}:${reference.output_key}`;
}

export function validateBrokerURL(value: string): string | null {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return "Enter a complete mqtt:// or mqtts:// URL with an explicit port";
  }
  if (parsed.protocol !== "mqtt:" && parsed.protocol !== "mqtts:") {
    return "Broker scheme must be mqtt:// or mqtts://";
  }
  if (!parsed.hostname || !parsed.port) return "Broker host and port are required";
  if (parsed.username || parsed.password) return "Keep credentials in a Credential Profile, not the broker URL";
  if ((parsed.pathname && parsed.pathname !== "/") || parsed.search || parsed.hash) {
    return "Broker URL cannot contain a path, query, or fragment";
  }
  return null;
}

export function buildBrokerURL(host: string, port: number, useTLS: boolean): string {
  return `${useTLS ? "mqtts" : "mqtt"}://${host.trim()}:${port}`;
}

export function isValidPublishTopic(topic: string): boolean {
  return validTopicText(topic) && !topic.includes("+") && !topic.includes("#");
}

export function isValidTopicFilter(filter: string): boolean {
  if (!validTopicText(filter)) return false;
  const levels = filter.split("/");
  return levels.every((level, index) => {
    if (level.includes("#")) return level === "#" && index === levels.length - 1;
    if (level.includes("+")) return level === "+";
    return true;
  });
}

function validTopicText(value: string): boolean {
  return value.length > 0 && new TextEncoder().encode(value).length <= 1024 && !value.includes("\0");
}

export function validateSourceAliases(sources: PublisherSourceDraft[]): string[] {
  const errors: string[] = [];
  const aliases = new Set<string>();
  for (const source of sources) {
    if (!aliasPattern.test(source.alias)) errors.push(`Invalid source alias: ${source.alias || "(blank)"}`);
    if (aliases.has(source.alias)) errors.push(`Duplicate source alias: ${source.alias}`);
    aliases.add(source.alias);
  }
  return errors;
}

export function validateMQTTPublisherDraft(draft: MQTTPublisherDraft): string[] {
  const errors: string[] = [];
  if (!draft.name.trim()) errors.push("Publisher name is required");
  const brokerError = validateBrokerURL(buildBrokerURL(draft.mqtt.broker_host, draft.mqtt.broker_port, draft.mqtt.use_tls));
  if (brokerError) errors.push(brokerError);
  if (!draft.mqtt.use_tls && !draft.mqtt.plaintext_acknowledged) {
    errors.push("Acknowledge that plain MQTT sends traffic without TLS");
  }
  if (!isValidPublishTopic(draft.mqtt.publish.topic.trim())) errors.push("Publish topic is invalid or contains wildcards");
  if (draft.credential_slots.includes("mqtt.password") && !draft.credential_slots.includes("mqtt.username")) errors.push("The selected Credential Profile has a password but no username");
  errors.push(...validateSourceAliases(draft.sources));
  if (draft.sources.length === 0) errors.push("Add at least one Publisher source alias");
  if (draft.trigger.mode === "interval" && (draft.trigger.interval_ms < 100 || draft.trigger.interval_ms > 86_400_000)) {
    errors.push("Interval must be between 100 ms and 24 hours");
  }
  if (draft.trigger.mode === "on_change") {
    if (!draft.sources.some((source) => source.alias === draft.trigger.source_alias)) errors.push("Select an on-change source alias");
    if (draft.trigger.coalesce_ms < 1 || draft.trigger.coalesce_ms > 60_000) errors.push("Coalesce window must be between 1 ms and 60 seconds");
  }
  const diagnosticLabels = new Set<string>();
  const diagnosticFilters = new Set<string>();
  for (const diagnostic of draft.mqtt.diagnostics) {
    if (!aliasPattern.test(diagnostic.label)) errors.push(`Invalid diagnostic label: ${diagnostic.label || "(blank)"}`);
    if (!isValidTopicFilter(diagnostic.topic_filter)) errors.push(`Invalid diagnostic topic filter: ${diagnostic.topic_filter || "(blank)"}`);
    if (diagnosticLabels.has(diagnostic.label)) errors.push(`Duplicate diagnostic label: ${diagnostic.label}`);
    if (diagnosticFilters.has(diagnostic.topic_filter)) errors.push(`Duplicate diagnostic topic filter: ${diagnostic.topic_filter}`);
    diagnosticLabels.add(diagnostic.label);
    diagnosticFilters.add(diagnostic.topic_filter);
  }
  if (draft.mqtt.diagnostics.length > 16) errors.push("Use no more than 16 diagnostic subscriptions");
  if (draft.mqtt.reconnect_max_ms < draft.mqtt.reconnect_min_ms) errors.push("Maximum reconnect delay must not be lower than the minimum");
  if (draft.mqtt.queue_capacity < 1 || draft.mqtt.queue_capacity > 10_000) errors.push("Queue capacity must be between 1 and 10,000");
  if (draft.mqtt.diagnostic_history_depth < 1 || draft.mqtt.diagnostic_history_depth > 1_000) errors.push("Diagnostic history must be between 1 and 1,000");
  try {
    previewPayloadTemplate(draft.mqtt.publish.payload_template, draft.sources, "good");
    previewPayloadTemplate(draft.mqtt.publish.payload_template, draft.sources, "unavailable");
    previewPayloadTemplate(draft.mqtt.publish.payload_template, draft.sources, "windowed");
  } catch (error) {
    errors.push(error instanceof Error ? error.message : "Payload template is invalid");
  }
  return [...new Set(errors)];
}

export function previewPayloadTemplate(
  template: string,
  sources: PublisherSourceDraft[],
  fixture: MQTTPayloadFixture,
): MQTTPayloadPreview {
  if (!template.trim()) throw new Error("Payload template is required");
  if (new TextEncoder().encode(template).length > 16 * 1024) throw new Error("Payload template exceeds 16 KiB");
  if (/{{\s*(if|range|with|template|define|block)\b/.test(template)) throw new Error("Payload control flow is not allowed");

  const sourceMap = new Map(sources.map((source) => [source.alias, source]));
  const aliases = new Set<string>();
  let helperCalls = 0;
  const rendered = template.replace(/{{([\s\S]*?)}}/g, (_match, body: string) => {
    helperCalls += 1;
    if (helperCalls > 256) throw new Error("Payload template exceeds 256 helper calls");
    const tokens = tokenizeHelper(body.trim());
    const helperToken = tokens.shift();
    const helper = typeof helperToken === "string" ? helperToken : "";
    const spec = helperSpecs[helper];
    if (!spec) throw new Error(`Unknown payload helper: ${helper || "(blank)"}`);
    if (tokens.length < spec.minimum || tokens.length > spec.maximum) throw new Error(`Invalid arguments for ${helper}`);
    const alias = spec.source ? String(tokens[0] ?? "") : "";
    const source = alias ? sourceMap.get(alias) : undefined;
    if (spec.source && !source) throw new Error(`Unknown source alias: ${alias}`);
    if (alias) aliases.add(alias);
    return renderHelper(helper, tokens, source, fixture);
  });
  if (rendered.includes("{{") || rendered.includes("}}")) throw new Error("Payload template contains an incomplete helper");
  let parsed: unknown;
  try {
    parsed = JSON.parse(rendered);
  } catch {
    throw new Error(`Rendered ${fixture} fixture is not valid JSON`);
  }
  const normalized = JSON.stringify(parsed, null, 2);
  if (new TextEncoder().encode(normalized).length > 64 * 1024) throw new Error("Rendered payload exceeds 64 KiB");
  return { rendered: normalized, referencedAliases: [...aliases].sort(), helperCalls };
}

function tokenizeHelper(body: string): Array<string | number | boolean | null> {
  const tokens: Array<string | number | boolean | null> = [];
  const tokenPattern = /"((?:\\.|[^"\\])*)"|(-?\d+(?:\.\d+)?)|(true|false|null)|([^\s]+)/g;
  let consumed = 0;
  for (const match of body.matchAll(tokenPattern)) {
    const gap = body.slice(consumed, match.index).trim();
    if (gap) throw new Error("Payload helper arguments are malformed");
    if (match[1] !== undefined) tokens.push(JSON.parse(`"${match[1]}"`) as string);
    else if (match[2] !== undefined) tokens.push(Number(match[2]));
    else if (match[3] === "true") tokens.push(true);
    else if (match[3] === "false") tokens.push(false);
    else if (match[3] === "null") tokens.push(null);
    else tokens.push(match[4]);
    consumed = (match.index ?? 0) + match[0].length;
  }
  if (body.slice(consumed).trim()) throw new Error("Payload helper arguments are malformed");
  return tokens;
}

function renderHelper(
  helper: string,
  tokens: Array<string | number | boolean | null>,
  source: PublisherSourceDraft | undefined,
  fixture: MQTTPayloadFixture,
): string {
  const unavailable = fixture === "unavailable";
  const value = fixtureValue(source);
  const timestamp = "2026-08-23T08:35:25.573Z";
  switch (helper) {
    case "value": return JSON.stringify(unavailable ? null : value);
    case "available": return JSON.stringify(!unavailable);
    case "quality": return JSON.stringify(unavailable ? "unavailable" : fixture === "windowed" ? "partial" : "good");
    case "error": return JSON.stringify(unavailable ? "source unavailable" : "");
    case "unit": return JSON.stringify(source?.unit ?? "");
    case "sequence": return JSON.stringify(unavailable ? 0 : 42);
    case "schema_version": return "1";
    case "data_type": return JSON.stringify(source?.data_type ?? "float64");
    case "observed_at":
    case "emitted_at": return JSON.stringify(unavailable ? null : timestamp);
    case "period_start": return JSON.stringify(unavailable || source?.period_kind !== "windowed" ? null : "2026-08-23T00:00:00.000Z");
    case "period_end": return JSON.stringify(unavailable || source?.period_kind !== "windowed" ? null : timestamp);
    case "coverage": return JSON.stringify(unavailable ? null : source?.period_kind === "windowed" ? 98.6 : null);
    case "round": {
      const decimals = Number(tokens[1]);
      const numeric = typeof value === "number" ? value : 0;
      return JSON.stringify(unavailable ? null : Number(numeric.toFixed(decimals)));
    }
    case "scale": {
      const gain = Number(tokens[1]);
      const offset = Number(tokens[2]);
      return JSON.stringify(unavailable || typeof value !== "number" ? null : value * gain + offset);
    }
    case "default": return JSON.stringify(unavailable ? tokens[1] : value);
    case "format_time": return JSON.stringify(unavailable ? null : timestamp);
    case "published_at": return JSON.stringify(timestamp);
    case "published_unix_ms": return "1787474125573";
    case "publisher_id": return JSON.stringify("00000000-0000-4000-8000-000000000001");
    default: throw new Error(`Unknown payload helper: ${helper}`);
  }
}

function fixtureValue(source: PublisherSourceDraft | undefined): boolean | number | string {
  if (!source) return 0;
  if (source.data_type === "bool") return true;
  if (source.data_type === "string") return "ONLINE";
  if (source.period_kind === "windowed") return 124.68;
  return 42.75;
}

export function exportMQTTPublisherConfig(draft: MQTTPublisherDraft): MQTTPublisherConfigExport {
  const secure = draft.mqtt.use_tls;
  const hasSlot = (slot: MQTTPublisherDraft["credential_slots"][number]) => draft.credential_slots.includes(slot);
  return {
    trigger: draft.trigger.mode === "interval"
      ? { mode: "interval", interval_ms: draft.trigger.interval_ms }
      : { mode: "on_change", source_alias: draft.trigger.source_alias, coalesce_ms: draft.trigger.coalesce_ms },
    mqtt: {
      broker_url: buildBrokerURL(draft.mqtt.broker_host, draft.mqtt.broker_port, secure),
      ...(!secure && draft.mqtt.plaintext_acknowledged ? { plaintext_acknowledged: true as const } : {}),
      ...(draft.mqtt.client_id.trim() ? { client_id: draft.mqtt.client_id.trim() } : {}),
      auth: {
        ...(draft.credential_id && hasSlot("mqtt.username") ? { username: { name: "mqtt.username" } } : {}),
        ...(draft.credential_id && hasSlot("mqtt.password") ? { password: { name: "mqtt.password" } } : {}),
      },
      tls: secure ? {
        ...(draft.mqtt.tls.server_name ? { server_name: draft.mqtt.tls.server_name } : {}),
        ...(draft.credential_id && hasSlot("mqtt.custom_ca") ? { custom_ca: { name: "mqtt.custom_ca" } } : {}),
        ...(draft.credential_id && hasSlot("mqtt.client_identity") ? { client_identity: { name: "mqtt.client_identity" } } : {}),
      } : {},
      publish: {
        topic: draft.mqtt.publish.topic.trim(),
        qos: draft.mqtt.publish.qos,
        retain: draft.mqtt.publish.retain,
        payload_template: draft.mqtt.publish.payload_template,
      },
      diagnostics: draft.mqtt.diagnostics.map(({ label, topic_filter, qos }) => ({ label, topic_filter, qos })),
      keep_alive_ms: draft.mqtt.keep_alive_ms,
      connect_timeout_ms: draft.mqtt.connect_timeout_ms,
      publish_timeout_ms: draft.mqtt.publish_timeout_ms,
      reconnect_min_ms: draft.mqtt.reconnect_min_ms,
      reconnect_max_ms: draft.mqtt.reconnect_max_ms,
      queue_capacity: draft.mqtt.queue_capacity,
      diagnostic_history_depth: draft.mqtt.diagnostic_history_depth,
    },
  };
}
