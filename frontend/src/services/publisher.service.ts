import type {
  CreateDataPublisherRequest,
  DataPublisherListParams,
  DataPublisherListResponse,
  DataPublisher,
  MQTTConnectionTestResult,
  MQTTDiagnosticEvent,
  PublisherRuntimeStatus,
  PublisherPayloadValidation,
  PublisherSourceCatalogEntry,
  PublisherSourceKind,
  PublisherSourceSelection,
  UpdateDataPublisherRequest,
} from "../types/publisher";
import api from "./api";

export interface PublisherSourceListParams {
  kind?: PublisherSourceKind;
  enabled?: boolean;
  search?: string;
}

export async function listPublisherSources(
  params?: PublisherSourceListParams,
  signal?: AbortSignal,
): Promise<PublisherSourceCatalogEntry[]> {
  const response = await api.get<{ data: PublisherSourceCatalogEntry[] }>("/publisher-sources", { params, signal });
  return response.data.data;
}

export async function validatePublisherPayload(
  payloadTemplate: string,
  sources: PublisherSourceSelection[],
): Promise<PublisherPayloadValidation> {
  const response = await api.post<PublisherPayloadValidation>("/publisher-payloads/validate", {
    payload_template: payloadTemplate,
    sources,
  });
  return response.data;
}

export async function createDataPublisher(data: CreateDataPublisherRequest): Promise<DataPublisher> {
  const response = await api.post<DataPublisher>("/data-publishers", data);
  return response.data;
}

function publisherPath(id: string): string {
  return `/data-publishers/${encodeURIComponent(id)}`;
}

export async function listDataPublishers(params?: DataPublisherListParams, signal?: AbortSignal): Promise<DataPublisherListResponse> {
  const response = await api.get<DataPublisherListResponse>("/data-publishers", { params, signal });
  return response.data;
}

export async function getDataPublisher(id: string, signal?: AbortSignal): Promise<DataPublisher> {
  const response = await api.get<DataPublisher>(publisherPath(id), { signal });
  return response.data;
}

export async function updateDataPublisher(id: string, data: UpdateDataPublisherRequest): Promise<DataPublisher> {
  const response = await api.put<DataPublisher>(publisherPath(id), data);
  return response.data;
}

export async function deleteDataPublisher(id: string): Promise<void> {
  await api.delete(publisherPath(id));
}

export async function enableDataPublisher(id: string): Promise<DataPublisher> {
  const response = await api.post<DataPublisher>(`${publisherPath(id)}/enable`);
  return response.data;
}

export async function disableDataPublisher(id: string): Promise<DataPublisher> {
  const response = await api.post<DataPublisher>(`${publisherPath(id)}/disable`);
  return response.data;
}

export async function restartDataPublisher(id: string): Promise<{ id: string; type: DataPublisher["type"]; enabled: boolean; runtime: PublisherRuntimeStatus }> {
  const response = await api.post<{ id: string; type: DataPublisher["type"]; enabled: boolean; runtime: PublisherRuntimeStatus }>(`${publisherPath(id)}/restart`);
  return response.data;
}

export async function getDataPublisherStatus(id: string, signal?: AbortSignal): Promise<PublisherRuntimeStatus> {
  const response = await api.get<{ runtime: PublisherRuntimeStatus }>(`${publisherPath(id)}/status`, { signal });
  return response.data.runtime;
}

export async function listPublisherDiagnostics(id: string, signal?: AbortSignal): Promise<MQTTDiagnosticEvent[]> {
  const response = await api.get<{ data: MQTTDiagnosticEvent[] | null }>(`${publisherPath(id)}/diagnostics`, { signal });
  return Array.isArray(response.data.data) ? response.data.data : [];
}

export async function testMQTTConnection(id: string): Promise<MQTTConnectionTestResult> {
  const response = await api.post<MQTTConnectionTestResult>(`${publisherPath(id)}/test-connection`);
  return response.data;
}
