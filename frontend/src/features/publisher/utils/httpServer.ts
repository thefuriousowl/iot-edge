import type { CredentialProfile } from "../../../types/credential";
import type {
  DataPublisher,
  HTTPServerPublisherConfigExport,
  HTTPServerPublisherDraft,
  CredentialSecretSlot,
  PublisherSourceCatalogEntry,
  PublisherSourceDraft,
} from "../../../types/publisher";
import { previewPayloadTemplate, validateSourceAliases } from "./mqtt";
import { createPublisherDraftID } from "./shared";

const headerPattern = /^[!#$%&'*+.^_`|~0-9A-Za-z-]{1,64}$/;

export function initialHTTPServerPublisherDraft(): HTTPServerPublisherDraft {
  return {
    name: "HTTP snapshot",
    enabled: false,
    credential_id: "",
    credential_slots: [],
    trigger: { mode: "interval", interval_ms: 60_000, source_alias: "", coalesce_ms: 100 },
    sources: [],
    http: {
      bind_address: "127.0.0.1",
      port: 8088,
      path: "/snapshot",
      access_mode: "api_key",
      api_key_header: "X-Api-Key",
      anonymous_acknowledged: false,
      quality_policy: "payload",
      read_timeout_ms: 5_000,
      write_timeout_ms: 5_000,
      idle_timeout_ms: 30_000,
      max_header_bytes: 16_384,
      max_connections: 64,
    },
    response: {
      payload_template: `{
  "publisher_id": {{publisher_id}},
  "published_at": {{published_at}},
  "values": {}
}`,
    },
  };
}

export function generateHTTPServerPayloadTemplate(sources: PublisherSourceDraft[]): string {
  const blocks = sources.map((source) => {
    const alias = JSON.stringify(source.alias);
    const fields = [
      `      "kind": ${JSON.stringify(source.kind)},`,
      `      "value": {{value ${alias}}},`,
      `      "available": {{available ${alias}}},`,
      `      "quality": {{quality ${alias}}},`,
      `      "error": {{error ${alias}}},`,
      `      "unit": {{unit ${alias}}},`,
      `      "data_type": {{data_type ${alias}}},`,
      `      "observed_at": {{observed_at ${alias}}}`,
    ];
    if (source.period_kind === "windowed") {
      fields[fields.length - 1] += ",";
      fields.push(
        `      "period_start": {{period_start ${alias}}},`,
        `      "period_end": {{period_end ${alias}}},`,
        `      "coverage_percent": {{coverage ${alias}}}`,
      );
    }
    return `    ${alias}: {\n${fields.join("\n")}\n    }`;
  });
  return `{
  "publisher_id": {{publisher_id}},
  "published_at": {{published_at}},
  "sources": {${blocks.length > 0 ? `\n${blocks.join(",\n")}\n  ` : ""}}
}`;
}

export function normalizeHTTPServerErrorMessage(message: string): string {
  if (!/mqtt/i.test(message)) return message;
  return message
    .replace(/MQTT Publisher payload template/gi, "HTTP Server response template")
    .replace(/MQTT payload template/gi, "HTTP response template")
    .replace(/MQTT payload/gi, "HTTP response template")
    .replace(/MQTT Publisher configuration/gi, "HTTP Server configuration")
    .replace(/MQTT Publisher/gi, "HTTP Server Publisher")
    .replace(/\bMQTT\b/gi, "HTTP Server");
}

function referenceKey(reference: PublisherSourceDraft["reference"]): string {
  if (!reference) return "";
  return reference.kind === "tag"
    ? `tag:${reference.tag_id}`
    : `plugin_output:${reference.plugin_instance_id}:${reference.output_key}`;
}

export function importHTTPServerPublisherDraft(
  entity: DataPublisher,
  catalog: PublisherSourceCatalogEntry[],
  credentials: CredentialProfile[],
): HTTPServerPublisherDraft {
  const initial = initialHTTPServerPublisherDraft();
  const config = entity.config as HTTPServerPublisherConfigExport | undefined;
  if (!config?.http || !config.response || !entity.sources) throw new Error("HTTP Server Publisher detail is incomplete");
  const descriptorByReference = new Map(catalog.map((entry) => [referenceKey(entry.descriptor.reference), entry.descriptor]));
  const sources = entity.sources.map((selection) => {
    const descriptor = descriptorByReference.get(referenceKey(selection.reference));
    if (!descriptor) throw new Error(`Publisher source is no longer available: ${selection.alias}`);
    return {
      id: createPublisherDraftID(),
      alias: selection.alias,
      reference: selection.reference,
      name: descriptor.name,
      owner_name: descriptor.owner_name ?? "Core",
      kind: descriptor.reference.kind,
      data_type: descriptor.data_type,
      unit: descriptor.unit ?? "",
      period_kind: descriptor.period_kind,
    } satisfies PublisherSourceDraft;
  });
  const profile = credentials.find((candidate) => candidate.id === entity.credential_id);
  return {
    ...initial,
    name: entity.name,
    enabled: entity.enabled,
    credential_id: entity.credential_id ?? "",
    credential_slots: profile?.secrets.map((secret) => secret.slot) ?? [],
    trigger: config.trigger.mode === "interval"
      ? { ...initial.trigger, mode: "interval", interval_ms: config.trigger.interval_ms }
      : { ...initial.trigger, mode: "on_change", source_alias: config.trigger.source_alias, coalesce_ms: config.trigger.coalesce_ms },
    sources,
    http: {
      bind_address: config.http.bind_address,
      port: config.http.port,
      path: config.http.path,
      access_mode: config.http.access.mode,
      api_key_header: config.http.access.api_key_header ?? "X-Api-Key",
      anonymous_acknowledged: config.http.access.anonymous_acknowledged ?? false,
      quality_policy: config.http.quality_policy,
      read_timeout_ms: config.http.read_timeout_ms,
      write_timeout_ms: config.http.write_timeout_ms,
      idle_timeout_ms: config.http.idle_timeout_ms,
      max_header_bytes: config.http.max_header_bytes,
      max_connections: config.http.max_connections,
    },
    response: { payload_template: config.response.payload_template },
  };
}

function validBindAddress(value: string): boolean {
  const trimmed = value.trim();
  if (trimmed.includes(":")) return /^[0-9A-Fa-f:]+$/.test(trimmed);
  const parts = trimmed.split(".");
  return parts.length === 4 && parts.every((part) => /^\d{1,3}$/.test(part) && Number(part) <= 255);
}

function requiredSlots(draft: HTTPServerPublisherDraft): CredentialSecretSlot[] {
  if (draft.http.access_mode === "api_key") return ["http.api_key"];
  if (draft.http.access_mode === "basic") return ["http.username", "http.password"];
  if (draft.http.access_mode === "bearer") return ["http.bearer_token"];
  return [];
}

export function validateHTTPServerEndpoint(draft: HTTPServerPublisherDraft): string[] {
  const errors: string[] = [];
  if (!draft.name.trim()) errors.push("Publisher name is required");
  if (!validBindAddress(draft.http.bind_address)) errors.push("Bind address must be an IP address");
  if (!Number.isInteger(draft.http.port) || draft.http.port < 1 || draft.http.port > 65_535) errors.push("Port must be between 1 and 65,535");
  if (!draft.http.path.startsWith("/") || draft.http.path.includes("?") || draft.http.path.includes("#") || draft.http.path.includes("..") || draft.http.path.length > 128) errors.push("Path must be an exact absolute path without query, fragment, or traversal");
  if (draft.http.access_mode === "anonymous" && !draft.http.anonymous_acknowledged) errors.push("Acknowledge that the HTTP endpoint will be anonymous");
  if (draft.http.access_mode === "api_key" && (!headerPattern.test(draft.http.api_key_header.trim()) || draft.http.api_key_header.toLowerCase() === "authorization")) errors.push("API key header is invalid or reserved");
  const slots = new Set(draft.credential_slots);
  for (const slot of requiredSlots(draft)) {
    if (!draft.credential_id || !slots.has(slot)) errors.push(`Selected HTTP Credential Profile must contain ${slot}`);
  }
  return errors;
}

export function validateHTTPServerPublisherDraft(draft: HTTPServerPublisherDraft): string[] {
  const errors = validateHTTPServerEndpoint(draft);
  errors.push(...validateSourceAliases(draft.sources));
  if (draft.sources.length === 0) errors.push("Add at least one Publisher source alias");
  if (draft.trigger.mode === "interval" && (draft.trigger.interval_ms < 100 || draft.trigger.interval_ms > 86_400_000)) errors.push("Interval must be between 100 ms and 24 hours");
  if (draft.trigger.mode === "on_change") {
    if (!draft.sources.some((source) => source.alias === draft.trigger.source_alias)) errors.push("Select an on-change source alias");
    if (draft.trigger.coalesce_ms < 1 || draft.trigger.coalesce_ms > 60_000) errors.push("Coalesce window must be between 1 ms and 60 seconds");
  }
  if (draft.http.read_timeout_ms < 100 || draft.http.read_timeout_ms > 120_000) errors.push("Read timeout must be between 100 ms and 120 seconds");
  if (draft.http.write_timeout_ms < 100 || draft.http.write_timeout_ms > 120_000) errors.push("Write timeout must be between 100 ms and 120 seconds");
  if (draft.http.idle_timeout_ms < 100 || draft.http.idle_timeout_ms > 300_000) errors.push("Idle timeout must be between 100 ms and 5 minutes");
  if (draft.http.max_header_bytes < 1_024 || draft.http.max_header_bytes > 1_048_576) errors.push("Maximum header size must be between 1 KiB and 1 MiB");
  if (draft.http.max_connections < 1 || draft.http.max_connections > 10_000) errors.push("Maximum connections must be between 1 and 10,000");
  for (const fixture of ["good", "unavailable", "windowed"] as const) {
    try {
      previewPayloadTemplate(draft.response.payload_template, draft.sources, fixture);
    } catch (error) {
      errors.push(error instanceof Error ? error.message : "Payload template is invalid");
      break;
    }
  }
  return [...new Set(errors)];
}

export function exportHTTPServerPublisherConfig(draft: HTTPServerPublisherDraft): HTTPServerPublisherConfigExport {
  return {
    trigger: draft.trigger.mode === "interval"
      ? { mode: "interval", interval_ms: draft.trigger.interval_ms }
      : { mode: "on_change", source_alias: draft.trigger.source_alias, coalesce_ms: draft.trigger.coalesce_ms },
    http: {
      bind_address: draft.http.bind_address.trim(),
      port: draft.http.port,
      path: draft.http.path.trim(),
      access: draft.http.access_mode === "anonymous"
        ? { mode: "anonymous", anonymous_acknowledged: true }
        : draft.http.access_mode === "api_key"
          ? { mode: "api_key", api_key_header: draft.http.api_key_header.trim() }
          : { mode: draft.http.access_mode },
      quality_policy: draft.http.quality_policy,
      read_timeout_ms: draft.http.read_timeout_ms,
      write_timeout_ms: draft.http.write_timeout_ms,
      idle_timeout_ms: draft.http.idle_timeout_ms,
      max_header_bytes: draft.http.max_header_bytes,
      max_connections: draft.http.max_connections,
    },
    response: { payload_template: draft.response.payload_template },
  };
}

export function httpServerEndpoint(draft: HTTPServerPublisherDraft, browserHostname = "localhost"): string {
  const bind = draft.http.bind_address.trim();
  const hostname = bind === "0.0.0.0" || bind === "::" ? browserHostname : bind.includes(":") ? `[${bind}]` : bind;
  return `http://${hostname}:${draft.http.port}${draft.http.path}`;
}
