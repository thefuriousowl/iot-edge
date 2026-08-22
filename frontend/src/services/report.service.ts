import type {
  Report,
  ReportListParams,
  ReportListResponse,
  ReportQueryParams,
  ReportQueryResponse,
  SaveReportRequest,
} from "../types/report";
import api from "./api";

function reportPath(id: string): string {
  return `/reports/${encodeURIComponent(id)}`;
}

export async function listReports(params?: ReportListParams, signal?: AbortSignal): Promise<ReportListResponse> {
  const response = await api.get<ReportListResponse>("/reports", { params, signal });
  return response.data;
}

export async function createReport(data: SaveReportRequest): Promise<Report> {
  const response = await api.post<Report>("/reports", data);
  return response.data;
}

export async function getReport(id: string, signal?: AbortSignal): Promise<Report> {
  const response = await api.get<Report>(reportPath(id), { signal });
  return response.data;
}

export async function updateReport(id: string, data: SaveReportRequest): Promise<Report> {
  const response = await api.put<Report>(reportPath(id), data);
  return response.data;
}

export async function deleteReport(id: string): Promise<void> {
  await api.delete(reportPath(id));
}

export async function queryReport(id: string, params: ReportQueryParams, signal?: AbortSignal): Promise<ReportQueryResponse> {
  const response = await api.get<ReportQueryResponse>(`${reportPath(id)}/query`, { params, signal });
  return response.data;
}

export async function exportReportCSV(id: string, params: ReportQueryParams): Promise<Blob> {
  const response = await api.get<Blob>(`${reportPath(id)}/export.csv`, { params, responseType: "blob" });
  return response.data;
}
