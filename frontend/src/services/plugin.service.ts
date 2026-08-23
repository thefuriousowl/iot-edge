import type {
  CreatePluginRequest,
  PluginInstance,
  PluginListParams,
  PluginListResponse,
  PluginStatusResponse,
  PluginTypeListResponse,
  UpdatePluginRequest,
} from "../types/plugin";
import api from "./api";

function pluginPath(id: string): string {
  return `/plugins/${encodeURIComponent(id)}`;
}

export async function listPluginTypes(signal?: AbortSignal): Promise<PluginTypeListResponse> {
  const response = await api.get<PluginTypeListResponse>("/plugin-types", { signal });
  return response.data;
}

export async function listPlugins(params?: PluginListParams, signal?: AbortSignal): Promise<PluginListResponse> {
  const response = await api.get<PluginListResponse>("/plugins", { params, signal });
  return response.data;
}

export async function createPlugin(data: CreatePluginRequest): Promise<PluginInstance> {
  const response = await api.post<PluginInstance>("/plugins", data);
  return response.data;
}

export async function getPlugin(id: string, signal?: AbortSignal): Promise<PluginInstance> {
  const response = await api.get<PluginInstance>(pluginPath(id), { signal });
  return response.data;
}

export async function updatePlugin(id: string, data: UpdatePluginRequest): Promise<PluginInstance> {
  const response = await api.put<PluginInstance>(pluginPath(id), data);
  return response.data;
}

export async function deletePlugin(id: string): Promise<void> {
  await api.delete(pluginPath(id));
}

export async function enablePlugin(id: string): Promise<PluginInstance> {
  const response = await api.post<PluginInstance>(`${pluginPath(id)}/enable`);
  return response.data;
}

export async function disablePlugin(id: string): Promise<PluginInstance> {
  const response = await api.post<PluginInstance>(`${pluginPath(id)}/disable`);
  return response.data;
}

export async function restartPlugin(id: string): Promise<PluginStatusResponse> {
  const response = await api.post<PluginStatusResponse>(`${pluginPath(id)}/restart`);
  return response.data;
}

export async function getPluginStatus(id: string, signal?: AbortSignal): Promise<PluginStatusResponse> {
  const response = await api.get<PluginStatusResponse>(`${pluginPath(id)}/status`, { signal });
  return response.data;
}
