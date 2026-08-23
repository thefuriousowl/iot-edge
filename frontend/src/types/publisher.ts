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

export interface PublisherSourceDraft {
  id: string;
  alias: string;
  name: string;
  owner_name: string;
  kind: PublisherSourceKind;
  data_type: PublisherSourceDataType;
  unit: string;
  period_kind: PublisherSourcePeriodKind;
}

export interface MQTTPayloadMapping {
  id: string;
  field: string;
  alias: string;
  helper: "value" | "quality" | "unit" | "observed_at" | "period_start" | "period_end" | "coverage";
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
  trigger: {
    mode: "interval" | "on_change";
    interval_ms: number;
    source_alias: string;
    coalesce_ms: number;
  };
  sources: PublisherSourceDraft[];
  mqtt: {
    broker_url: string;
    plaintext_acknowledged: boolean;
    client_id: string;
    auth: {
      username_ref: string;
      password_ref: string;
    };
    tls: {
      server_name: string;
      custom_ca_ref: string;
      client_identity_ref: string;
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
  mappings: MQTTPayloadMapping[];
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

export type MQTTPayloadFixture = "good" | "unavailable" | "windowed";

export interface MQTTPayloadPreview {
  rendered: string;
  referencedAliases: string[];
  helperCalls: number;
}
