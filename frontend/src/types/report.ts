import type {
  DataLoggerAggregate,
  DataLoggerPagination,
  DataLoggerQueryBucket,
  DataLoggerQueryMode,
  DataLoggerQueryRow,
} from "./datalogger";
import type { TagDataType, TagType } from "./tag";

export interface ReportColumn {
  tag_id: string;
  position: number;
  name: string;
  aggregate?: DataLoggerAggregate;
  tag_name: string;
  tag_type: TagType;
  data_type: TagDataType;
}

export interface Report {
  id: string;
  name: string;
  description: string | null;
  logger_id: string;
  logger_name: string;
  timezone: string;
  mode: DataLoggerQueryMode;
  bucket?: DataLoggerQueryBucket;
  column_count: number;
  columns?: ReportColumn[];
  created_at: string;
  updated_at: string;
}

export interface ReportColumnRequest {
  tag_id: string;
  name: string;
  aggregate?: DataLoggerAggregate;
}

export interface SaveReportRequest {
  name: string;
  description: string | null;
  logger_id: string;
  timezone: string;
  mode: DataLoggerQueryMode;
  bucket?: DataLoggerQueryBucket;
  columns: ReportColumnRequest[];
}

export interface ReportListParams {
  logger_id?: string;
  mode?: DataLoggerQueryMode;
  search?: string;
  page?: number;
  per_page?: number;
}

export interface ReportListResponse {
  data: Report[];
  pagination: DataLoggerPagination;
}

export interface ReportQueryParams {
  from: string;
  to: string;
  page?: number;
  per_page?: number;
}

export interface ReportQueryResponse {
  report: Report;
  data: DataLoggerQueryRow[];
  pagination: DataLoggerPagination;
}
