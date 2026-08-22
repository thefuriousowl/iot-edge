import api, { getAccessToken } from "./api";
import type {
  CreateDatasourceRequest,
  CreateDeviceRequest,
  Datasource,
  DatasourceSample,
  Device,
  DeviceInventoryParams,
  DeviceInventoryResponse,
  UpdateDatasourceRequest,
  UpdateDeviceRequest,
} from "../types/device";

interface ListResponse<T> { data: T[] }

export async function listDevices(vgatewayId: string, signal?: AbortSignal): Promise<Device[]> {
  const response = await api.get<ListResponse<Device>>(`/vgateways/${vgatewayId}/devices`, { signal });
  return response.data.data;
}

export async function listDeviceInventory(
  params?: DeviceInventoryParams,
  signal?: AbortSignal,
): Promise<DeviceInventoryResponse> {
  const response = await api.get<DeviceInventoryResponse>("/devices", { params, signal });
  return response.data;
}

export async function createDevice(vgatewayId: string, request: CreateDeviceRequest): Promise<Device> {
  const response = await api.post<Device>(`/vgateways/${vgatewayId}/devices`, request);
  return response.data;
}

export async function updateDevice(id: string, request: UpdateDeviceRequest): Promise<Device> {
  const response = await api.put<Device>(`/devices/${id}`, request);
  return response.data;
}

export async function deleteDevice(id: string): Promise<void> { await api.delete(`/devices/${id}`); }

export async function listDatasources(deviceId: string, signal?: AbortSignal): Promise<Datasource[]> {
  const response = await api.get<ListResponse<Datasource>>(`/devices/${deviceId}/datasources`, { signal });
  return response.data.data;
}

export async function createDatasource(deviceId: string, request: CreateDatasourceRequest): Promise<Datasource> {
  const response = await api.post<Datasource>(`/devices/${deviceId}/datasources`, request);
  return response.data;
}

export async function updateDatasource(id: string, request: UpdateDatasourceRequest): Promise<Datasource> {
  const response = await api.put<Datasource>(`/datasources/${id}`, request);
  return response.data;
}

export async function deleteDatasource(id: string): Promise<void> { await api.delete(`/datasources/${id}`); }

export async function previewDatasource(deviceId: string, request: CreateDatasourceRequest): Promise<DatasourceSample> {
  const response = await api.post<DatasourceSample>(`/devices/${deviceId}/datasources/preview`, {
    type: request.type,
    config: request.config,
  });
  return response.data;
}

export async function previewSavedDatasource(id: string): Promise<DatasourceSample> {
  const response = await api.post<DatasourceSample>(`/datasources/${id}/preview`);
  return response.data;
}

export async function monitorDatasource(
  id: string,
  onSample: (sample: DatasourceSample) => void,
  signal: AbortSignal,
): Promise<void> {
  const baseURL = import.meta.env.VITE_API_URL || "/api";
  const token = getAccessToken();
  const response = await fetch(`${baseURL}/datasources/${id}/stream`, {
    headers: token ? { Authorization: `Bearer ${token}` } : undefined,
    credentials: "include",
    signal,
  });
  if (!response.ok || !response.body) {
    throw new Error(`Datasource stream failed with status ${response.status}`);
  }

  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  while (true) {
    const { done, value } = await reader.read();
    if (done) return;
    buffer += decoder.decode(value, { stream: true });
    const events = buffer.split("\n\n");
    buffer = events.pop() ?? "";
    for (const event of events) {
      const dataLine = event.split("\n").find((line) => line.startsWith("data: "));
      if (dataLine) onSample(JSON.parse(dataLine.slice(6)) as DatasourceSample);
    }
  }
}
