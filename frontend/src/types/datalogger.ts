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
  config: DataLoggerConfig;
  tag_count: number;
  tags?: DataLoggerTagReference[];
  created_at: string;
  updated_at: string;
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

export interface SaveDataLoggerRequest {
  name: string;
  description: string | null;
  enabled: boolean;
  timezone: string;
  mode: DataLoggerMode;
  start_at: string;
  end_at: string | null;
  config: DataLoggerConfig;
  tag_ids: string[];
}

export type CreateDataLoggerRequest = SaveDataLoggerRequest;
export type UpdateDataLoggerRequest = SaveDataLoggerRequest;
