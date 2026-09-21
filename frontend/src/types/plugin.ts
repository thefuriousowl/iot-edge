import type { DataLoggerPagination, DataLoggerQueryBucket } from "./datalogger";

export type PluginType = "energy_management" | (string & {});
export type PluginCapability = "logger.committed_batches" | (string & {});
export type PluginRuntimeState = "stopped" | "starting" | "running" | "stopping" | "error";
export type PluginConfig = Record<string, unknown>;

export interface PluginManifest {
  type: PluginType;
  name: string;
  description?: string;
  version: string;
  config_version: number;
  multiple_instances: boolean;
  capabilities: PluginCapability[];
}

export interface PluginRuntimeError {
  code: string;
  message: string;
}

export interface PluginRuntimeStatus {
  state: PluginRuntimeState;
  started_at: string | null;
  last_transition_at: string | null;
  error: PluginRuntimeError | null;
}

export interface PluginInstanceSummary {
  id: string;
  type: PluginType;
  name: string;
  enabled: boolean;
  config_version: number;
  runtime: PluginRuntimeStatus;
  created_at: string;
  updated_at: string;
}

export interface PluginInstance extends PluginInstanceSummary {
  config: PluginConfig;
}

export interface PluginStatusResponse {
  id: string;
  type: PluginType;
  enabled: boolean;
  runtime: PluginRuntimeStatus;
}

export interface PluginTypeListResponse {
  data: PluginManifest[];
}

export interface PluginListParams {
  type?: PluginType;
  enabled?: boolean;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface PluginListResponse {
  data: PluginInstanceSummary[];
  pagination: DataLoggerPagination;
}

export interface CreatePluginRequest {
  type: PluginType;
  name: string;
  enabled?: boolean;
  config: PluginConfig;
}

export interface UpdatePluginRequest {
  name?: string;
  enabled?: boolean;
  config?: PluginConfig;
}

export type EnergyPowerUnit = "W" | "kW" | "MW";

export interface EnergyPowerTag {
  tag_id: string;
  unit: EnergyPowerUnit;
}

export interface EnergyFlatTariff {
  mode?: "flat";
  currency: string;
  rate_per_kwh: number;
}

export interface EnergyTagTariff {
  mode: "tag";
  currency: string;
  tag_id: string;
}

export type EnergyTariff = EnergyFlatTariff | EnergyTagTariff;

export interface EnergyConfig extends PluginConfig {
  logger_id: string;
  electrical_power_tags: EnergyPowerTag[];
  thermal_power_tags?: EnergyPowerTag[];
  timezone: string;
  max_gap_seconds: number;
  tariff: EnergyTariff;
}

export type EnergyMetricErrorCode = "missing_sample" | "source_bad" | "invalid_value" | "stale_gap" | "no_coverage";

export interface EnergyMetricError {
  code: EnergyMetricErrorCode;
  tag_id?: string;
  at: string;
  message: string;
}

export interface EnergyDemandMetric {
  kilowatts: number;
  valid: boolean;
  errors: EnergyMetricError[] | null;
}

export interface EnergyRatioMetric {
  value: number;
  valid: boolean;
  error?: string;
}

export interface EnergyTariffMetric {
  rate_per_kwh: number;
  valid: boolean;
  errors: EnergyMetricError[] | null;
}

export interface EnergyBatchMetrics {
  batch_at: string;
  electrical: EnergyDemandMetric;
  thermal: EnergyDemandMetric;
  tariff: EnergyTariffMetric;
  cop: EnergyRatioMetric;
}

export interface EnergySegmentIssue {
  from: string;
  to: string;
  code: EnergyMetricErrorCode;
  errors: EnergyMetricError[] | null;
}

export interface EnergySummary {
  kilowatt_hours: number;
  covered_seconds: number;
  skipped_seconds: number;
  coverage_percent: number;
  segments: number;
  skipped_segments: number;
  issues: EnergySegmentIssue[] | null;
}

export interface EnergyPeriodSummary {
  from: string;
  to: string;
  electrical: EnergySummary;
  thermal: EnergySummary;
  cost: EnergyRatioMetric;
  cop: EnergyRatioMetric;
}

export interface EnergyOverviewResponse {
  instance_id: string;
  logger_id: string;
  timezone: string;
  currency: string;
  tariff_mode: "flat" | "tag";
  tariff_tag_id?: string;
  rate_per_kwh: number;
  as_of: string;
  latest: EnergyBatchMetrics | null;
  today: EnergyPeriodSummary;
  month: EnergyPeriodSummary;
  run?: EnergyMeasurementRun;
}

export interface EnergyMeasurementRun {
  id: string;
  plugin_instance_id: string;
  name: string;
  reason: string;
  status: "active" | "archived";
  started_at: string;
  ended_at?: string;
  config_version: number;
  created_at: string;
  archived_at?: string;
}

export interface EnergyHistoryParams {
  from: string;
  to: string;
  bucket?: DataLoggerQueryBucket;
  page?: number;
  per_page?: number;
}

export interface EnergyHistoryResponse {
  instance_id: string;
  logger_id: string;
  timezone: string;
  bucket: DataLoggerQueryBucket;
  requested_bucket: DataLoggerQueryBucket;
  downsampled: boolean;
  point_limit: number;
  currency: string;
  tariff_mode: "flat" | "tag";
  tariff_tag_id?: string;
  rate_per_kwh: number;
  data: EnergyPeriodSummary[];
  pagination: DataLoggerPagination;
}

export interface EnergyLiveEvent {
  id: string;
  sequence: number;
  instance_id: string;
  metrics: EnergyBatchMetrics;
}

export interface EnergyStreamContext {
  signal: AbortSignal;
  lastEventId: string | null;
  onOpen: () => void;
  onMessage: (event: EnergyLiveEvent) => void;
  onReset: (reason: string) => void;
  onRetry: (milliseconds: number) => void;
}
