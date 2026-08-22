import type {
  CreateDataLoggerRequest,
  DataLogger,
  DataLoggerHistoryParams,
  DataLoggerHistoryResponse,
  DataLoggerListParams,
  DataLoggerListResponse,
  DataLoggerQueryParams,
  DataLoggerQueryResponse,
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
