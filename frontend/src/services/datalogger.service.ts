import type {
  CreateDataLoggerRequest,
  DataManagementOverview,
  DataLogger,
  DataLoggerHistoryParams,
  DataLoggerHistoryResponse,
  DataLoggerListParams,
  DataLoggerListResponse,
  DataLoggerQueryParams,
  DataLoggerQueryResponse,
  RetentionCleanupRequest,
  RetentionCleanupResult,
  RetentionPlan,
  RetentionStatus,
  UpdateDataLoggerRequest,
} from "../types/datalogger";
import api from "./api";

function dataLoggerPath(id: string): string {
  return `/data-loggers/${encodeURIComponent(id)}`;
}

export async function listDataLoggers(
  params?: DataLoggerListParams,
  signal?: AbortSignal,
): Promise<DataLoggerListResponse> {
  const response = await api.get<DataLoggerListResponse>("/data-loggers", { params, signal });
  return response.data;
}

export async function getDataManagementOverview(signal?: AbortSignal): Promise<DataManagementOverview> {
  const response = await api.get<DataManagementOverview>("/data-management/overview", { signal });
  return response.data;
}

export async function getDataLoggerRetention(id: string, signal?: AbortSignal): Promise<RetentionStatus> {
  const response = await api.get<RetentionStatus>(`${dataLoggerPath(id)}/retention`, { signal });
  return response.data;
}

export async function previewDataLoggerRetention(id: string, signal?: AbortSignal): Promise<RetentionPlan> {
  const response = await api.get<RetentionPlan>(`${dataLoggerPath(id)}/retention/preview`, { signal });
  return response.data;
}

export async function cleanupDataLoggerRetention(id: string, data: RetentionCleanupRequest): Promise<RetentionCleanupResult> {
  const response = await api.post<RetentionCleanupResult>(`${dataLoggerPath(id)}/retention/cleanup`, data);
  return response.data;
}

export async function createDataLogger(data: CreateDataLoggerRequest): Promise<DataLogger> {
  const response = await api.post<DataLogger>("/data-loggers", data);
  return response.data;
}

export async function getDataLogger(id: string, signal?: AbortSignal): Promise<DataLogger> {
  const response = await api.get<DataLogger>(dataLoggerPath(id), { signal });
  return response.data;
}

export async function getDataLoggerHistory(
  id: string,
  params?: DataLoggerHistoryParams,
  signal?: AbortSignal,
): Promise<DataLoggerHistoryResponse> {
  const response = await api.get<DataLoggerHistoryResponse>(`${dataLoggerPath(id)}/history`, { params, signal });
  return response.data;
}

export async function queryDataLogger(
  id: string,
  params: DataLoggerQueryParams,
  signal?: AbortSignal,
): Promise<DataLoggerQueryResponse> {
  const response = await api.get<DataLoggerQueryResponse>(`${dataLoggerPath(id)}/query`, { params, signal });
  return response.data;
}

export async function updateDataLogger(id: string, data: UpdateDataLoggerRequest): Promise<DataLogger> {
  const response = await api.put<DataLogger>(dataLoggerPath(id), data);
  return response.data;
}

export async function deleteDataLogger(id: string): Promise<void> {
  await api.delete(dataLoggerPath(id));
}
