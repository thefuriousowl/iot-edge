export type PublisherSourceKind = "tag" | "plugin_output";
export type PublisherSourceDataType =
  | "bool"
  | "int16"
  | "uint16"
  | "int32"
  | "uint32"
  | "float32"
  | "float64"
  | "string";
export type PublisherSourcePeriodKind = "instantaneous" | "windowed";
export type MQTTQoS = 0 | 1;

export type PublisherSourceReference =
  | { kind: "tag"; tag_id: string }
  | { kind: "plugin_output"; plugin_instance_id: string; output_key: string };

export interface PublisherSourceDescriptor {
  reference: PublisherSourceReference;
  name: string;
  owner_name?: string;
  description?: string;
  schema_version: number;
  data_type: PublisherSourceDataType;
  unit?: string;
  dynamic_unit?: boolean;
  period_kind: PublisherSourcePeriodKind;
  enabled: boolean;
}

export interface PublisherSourceCatalogEntry {
  descriptor: PublisherSourceDescriptor;
  current: {
    quality: "good" | "partial" | "bad" | "unavailable";
    sequence?: number;
    observed_at?: string;
    period_start?: string;
    period_end?: string;
    coverage_percent?: number;
  };
}

export interface PublisherSourceSelection {
  alias: string;
  reference: PublisherSourceReference;
}

export interface PublisherSourceDraft {
  id: string;
  alias: string;
  name: string;
  owner_name: string;
  kind: PublisherSourceKind;
  data_type: PublisherSourceDataType;
  unit: string;
  period_kind: PublisherSourcePeriodKind;
  reference?: PublisherSourceReference;
}

export interface MQTTDiagnosticSubscription {
  id: string;
  label: string;
  topic_filter: string;
  qos: MQTTQoS;
}

export interface MQTTPublisherDraft {
  name: string;
  enabled: boolean;
  credential_id: string;
  credential_slots: CredentialSecretSlot[];
  trigger: {
    mode: "interval" | "on_change";
    interval_ms: number;
    source_alias: string;
    coalesce_ms: number;
  };
  sources: PublisherSourceDraft[];
  mqtt: {
    broker_host: string;
    broker_port: number;
    use_tls: boolean;
    plaintext_acknowledged: boolean;
    client_id: string;
    tls: {
      server_name: string;
    };
    publish: {
      topic: string;
      qos: MQTTQoS;
      retain: boolean;
      payload_template: string;
    };
    diagnostics: MQTTDiagnosticSubscription[];
    keep_alive_ms: number;
    connect_timeout_ms: number;
    publish_timeout_ms: number;
    reconnect_min_ms: number;
    reconnect_max_ms: number;
    queue_capacity: number;
    diagnostic_history_depth: number;
  };
}

export interface MQTTPublisherConfigExport {
  trigger:
    | { mode: "interval"; interval_ms: number }
    | { mode: "on_change"; source_alias: string; coalesce_ms: number };
  mqtt: {
    broker_url: string;
    plaintext_acknowledged?: true;
    client_id?: string;
    auth: {
      username?: { name: string };
      password?: { name: string };
    };
    tls: {
      server_name?: string;
      custom_ca?: { name: string };
      client_identity?: { name: string };
    };
    publish: {
      topic: string;
      qos: MQTTQoS;
      retain: boolean;
      payload_template: string;
    };
    diagnostics: Array<{
      label: string;
      topic_filter: string;
      qos: MQTTQoS;
    }>;
    keep_alive_ms: number;
    connect_timeout_ms: number;
    publish_timeout_ms: number;
    reconnect_min_ms: number;
    reconnect_max_ms: number;
    queue_capacity: number;
    diagnostic_history_depth: number;
  };
}

export type HTTPServerAccessMode = "api_key" | "basic" | "bearer" | "anonymous";
export type HTTPServerQualityPolicy = "payload" | "strict";

export interface HTTPServerPublisherDraft {
  name: string;
  enabled: boolean;
  credential_id: string;
  credential_slots: CredentialSecretSlot[];
  trigger: MQTTPublisherDraft["trigger"];
  sources: PublisherSourceDraft[];
  http: {
    bind_address: string;
    port: number;
    path: string;
    access_mode: HTTPServerAccessMode;
    api_key_header: string;
    anonymous_acknowledged: boolean;
    quality_policy: HTTPServerQualityPolicy;
    read_timeout_ms: number;
    write_timeout_ms: number;
    idle_timeout_ms: number;
    max_header_bytes: number;
    max_connections: number;
  };
  response: {
    payload_template: string;
  };
}

export interface HTTPServerPublisherConfigExport {
  trigger: MQTTPublisherConfigExport["trigger"];
  http: {
    bind_address: string;
    port: number;
    path: string;
    access: {
      mode: HTTPServerAccessMode;
      api_key_header?: string;
      anonymous_acknowledged?: true;
    };
    quality_policy: HTTPServerQualityPolicy;
    read_timeout_ms: number;
    write_timeout_ms: number;
    idle_timeout_ms: number;
    max_header_bytes: number;
    max_connections: number;
  };
  response: {
    payload_template: string;
  };
}

export interface HTTPServerEndpointMetadata {
  network: "tcp";
  scheme: "http";
  bind_address: string;
  port: number;
  path: string;
  access_mode: HTTPServerAccessMode;
  quality_policy: HTTPServerQualityPolicy;
}

export interface HTTPServerProbeResult {
  reachable: boolean;
  probed_at: string;
  latency_ms: number;
  endpoint: HTTPServerEndpointMetadata;
}

export type MQTTPayloadFixture = "good" | "unavailable" | "windowed";

export interface MQTTPayloadPreview {
  rendered: string;
  referencedAliases: string[];
  helperCalls: number;
}

export interface PublisherPayloadValidation {
  referenced_aliases: string[];
  helper_calls: number;
  good: unknown;
  unavailable: unknown;
  windowed: unknown;
}

export type PublisherRuntimeState = "stopped" | "starting" | "running" | "stopping" | "error";

export interface PublisherRuntimeSource {
  alias: string;
  reference: PublisherSourceReference;
  available: boolean;
  quality: "good" | "partial" | "bad" | "unavailable";
  sequence?: number;
  observed_at?: string;
  period_start?: string;
  period_end?: string;
  coverage_percent?: number;
}

export interface PublisherRuntimeStatus {
  publisher_id: string;
  type: DataPublisher["type"];
  state: PublisherRuntimeState;
  config_version: number;
  started_at?: string;
  last_transition_at: string;
  last_request_at?: string;
  last_publish_at?: string;
  request_count: number;
  publish_count: number;
  failure_count: number;
  queue_depth: number;
  drop_count: number;
  reconnect_count: number;
  connected: boolean;
  connection_count: number;
  delivery_count: number;
  delivery_failure_count: number;
  transport_queue_depth: number;
  transport_drop_count: number;
  diagnostic_count: number;
  diagnostic_drop_count: number;
  external_request_count?: number;
  rejected_request_count?: number;
  active_connections?: number;
  last_external_request_at?: string;
  last_connected_at?: string;
  last_delivered_at?: string;
  last_diagnostic_at?: string;
  transport_error?: string;
  sources?: PublisherRuntimeSource[];
  last_error?: string;
}

export interface MQTTDiagnosticEvent {
  sequence: number;
  label: string;
  topic: string;
  qos: MQTTQoS;
  retained: boolean;
  duplicate: boolean;
  received_at: string;
  format: "json" | "text" | "binary_base64";
  payload: string;
  truncated: boolean;
}

export type CredentialSecretSlot =
  | "mqtt.username"
  | "mqtt.password"
  | "mqtt.custom_ca"
  | "mqtt.client_identity"
  | "http.username"
  | "http.password"
  | "http.api_key"
  | "http.bearer_token"
  | "http.oauth_client_secret"
  | "http.custom_ca"
  | "http.client_identity";

export interface DataPublisher {
  id: string;
  type: "http_server" | "mqtt";
  name: string;
  description?: string;
  enabled: boolean;
  config_version: number;
  source_count: number;
  credential_id?: string;
  endpoint?: HTTPServerEndpointMetadata;
  runtime: PublisherRuntimeStatus;
  config?: MQTTPublisherConfigExport | HTTPServerPublisherConfigExport | Record<string, unknown>;
  sources?: PublisherSourceSelection[];
  created_at: string;
  updated_at: string;
}

export interface DataPublisherListResponse {
  data: DataPublisher[];
  pagination: { page: number; per_page: number; total: number; total_pages: number };
}

export interface DataPublisherListParams {
  type?: DataPublisher["type"];
  enabled?: boolean;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface CreateDataPublisherRequest {
  type: "mqtt" | "http_server";
  name: string;
  description: string | null;
  enabled: false;
  config: MQTTPublisherConfigExport | HTTPServerPublisherConfigExport;
  sources: PublisherSourceSelection[];
  credential_id: string | null;
}

export interface UpdateDataPublisherRequest {
  name?: string;
  description?: string | null;
  config?: MQTTPublisherConfigExport | HTTPServerPublisherConfigExport;
  sources?: PublisherSourceSelection[];
  credential_id?: string | null;
}

export interface MQTTConnectionTestResult {
  connected: boolean;
  connected_at: string;
  latency_ms: number;
}
