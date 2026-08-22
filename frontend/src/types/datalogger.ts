import type { TagDataType, TagType } from "./tag";

export type DataLoggerMode = "interval" | "schedule";
export type DataLoggerScheduleUnit = "minute" | "hour" | "day" | "week";

export interface DataLoggerIntervalConfig {
  interval_seconds: number;
}

export interface DataLoggerScheduleConfig {
  unit: DataLoggerScheduleUnit;
  every: number;
  times?: string[];
  weekdays?: number[];
}

export type DataLoggerConfig = DataLoggerIntervalConfig | DataLoggerScheduleConfig;

export interface DataLoggerTagReference {
  id: string;
  name: string;
  type: TagType;
  data_type: TagDataType;
  enabled: boolean;
}

export interface DataLogger {
  id: string;
  name: string;
  description: string | null;
  enabled: boolean;
  timezone: string;
  mode: DataLoggerMode;
  start_at: string;
  end_at: string | null;
  max_size_bytes: number | null;
  config: DataLoggerConfig;
  tag_count: number;
  tags?: DataLoggerTagReference[];
  storage?: DataLoggerStorageStats;
  created_at: string;
  updated_at: string;
}

export interface DataLoggerStorageStats {
  row_count: number;
  batch_count: number;
  estimated_size_bytes: number;
  average_row_bytes: number;
  estimated_capacity_rows: number | null;
  estimated_capacity_batches: number | null;
  oldest_batch_at: string | null;
  newest_batch_at: string | null;
}

export interface DataLoggerPagination {
  page: number;
  per_page: number;
  total: number;
  total_pages: number;
}

export interface DataLoggerListParams {
  mode?: DataLoggerMode;
  enabled?: boolean;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface DataLoggerListResponse {
  data: DataLogger[];
  pagination: DataLoggerPagination;
}

export interface DataLoggerRawValue {
  logger_id: string;
  tag_id: string;
  batch_at: string;
  observed_at: string;
  data_type: TagDataType;
  value: boolean | number | null;
  quality: "good" | "bad";
  error?: string;
  persisted_at: string;
}

export interface DataLoggerHistoryParams {
  tag_id?: string;
  from?: string;
  to?: string;
  page?: number;
  per_page?: number;
}

export interface DataLoggerHistoryResponse {
  data: DataLoggerRawValue[];
  last_batch_at: string | null;
  pagination: DataLoggerPagination;
}

export type DataLoggerQueryMode = "raw" | "aggregate";
export type DataLoggerQueryBucket = "1m" | "5m" | "15m" | "1h" | "6h" | "1d" | "1w";
export type DataLoggerAggregate = "min" | "max" | "avg" | "sum" | "count" | "first" | "last";

export interface DataLoggerQueryValue {
  tag_id: string;
  data_type: TagDataType | "mixed";
  value: boolean | number | null;
  quality?: "good" | "bad";
  error?: string;
  observed_at?: string;
  good_count?: number;
  bad_count?: number;
  total_count?: number;
  supported?: boolean;
}

export interface DataLoggerQueryRow {
  at: string;
  values: Record<string, DataLoggerQueryValue>;
}

export interface DataLoggerQueryParams {
  mode: DataLoggerQueryMode;
  tag_ids?: string;
  from: string;
  to: string;
  bucket?: DataLoggerQueryBucket;
  aggregate?: DataLoggerAggregate;
  page?: number;
  per_page?: number;
}

export interface DataLoggerQueryResponse {
  data: DataLoggerQueryRow[];
  mode: DataLoggerQueryMode;
  bucket?: DataLoggerQueryBucket;
  aggregate?: DataLoggerAggregate;
  pagination: DataLoggerPagination;
}

export interface SaveDataLoggerRequest {
  name: string;
  description: string | null;
  enabled: boolean;
  timezone: string;
  mode: DataLoggerMode;
  start_at: string;
  end_at: string | null;
  max_size_bytes: number | null;
  config: DataLoggerConfig;
  tag_ids: string[];
}

export type CreateDataLoggerRequest = SaveDataLoggerRequest;
export type UpdateDataLoggerRequest = SaveDataLoggerRequest;
