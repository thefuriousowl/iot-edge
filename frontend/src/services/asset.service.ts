import axios from "axios";

import type { SSEStreamContext } from "../types/sse";
import type {
  Asset, AssetBindingsResponse, AssetCollectionResponse, AssetConnectivity, AssetErrorCode,
  AssetErrorResponse, AssetListParams, AssetListResponse, AssetMeasurementReading,
  AssetMeasurementSnapshot, AssetTreeNode, CreateAssetRequest, MeasurementBinding,
  MoveAssetRequest, ReplaceAssetBindingsRequest, TagAssetConnectivity, UpdateAssetRequest,
} from "../types/asset";
import api, { getAccessToken } from "./api";

function assetPath(id: string): string { return `/assets/${encodeURIComponent(id)}`; }

export class AssetAPIError extends Error {
  readonly code: AssetErrorCode;
  readonly status?: number;

  constructor(code: AssetErrorCode, message: string, status?: number) {
    super(message);
    this.name = "AssetAPIError";
    this.code = code;
    this.status = status;
  }
}

export function mapAssetAPIError(error: unknown): unknown {
  if (!axios.isAxiosError<AssetErrorResponse>(error)) return error;
  const payload = error.response?.data;
  if (!payload?.error?.code || typeof payload.error.message !== "string") return error;
  return new AssetAPIError(payload.error.code, payload.error.message, error.response?.status);
}

async function mapped<T>(request: Promise<{ data: T }>): Promise<T> {
  try { return (await request).data; } catch (error) { throw mapAssetAPIError(error); }
}

export const listAssets = (params?: AssetListParams, signal?: AbortSignal) => mapped<AssetListResponse>(api.get("/assets", { params, signal }));
export const getAssetRoots = (signal?: AbortSignal) => mapped<AssetCollectionResponse>(api.get("/assets/roots", { signal }));
export const getAsset = (id: string, signal?: AbortSignal) => mapped<Asset>(api.get(assetPath(id), { signal }));
export const getAssetChildren = (id: string, signal?: AbortSignal) => mapped<AssetCollectionResponse>(api.get(`${assetPath(id)}/children`, { signal }));
export const getAssetTree = (id: string, signal?: AbortSignal) => mapped<AssetTreeNode>(api.get(`${assetPath(id)}/tree`, { signal }));
export const getAssetAncestors = (id: string, signal?: AbortSignal) => mapped<AssetCollectionResponse>(api.get(`${assetPath(id)}/ancestors`, { signal }));
export const createAsset = (data: CreateAssetRequest, signal?: AbortSignal) => mapped<Asset>(api.post("/assets", data, { signal }));
export const updateAsset = (id: string, data: UpdateAssetRequest, signal?: AbortSignal) => mapped<Asset>(api.put(assetPath(id), data, { signal }));
export const moveAsset = (id: string, data: MoveAssetRequest, signal?: AbortSignal) => mapped<Asset>(api.post(`${assetPath(id)}/move`, data, { signal }));
export async function deleteAsset(id: string, signal?: AbortSignal): Promise<void> {
  try { await api.delete(assetPath(id), { signal }); } catch (error) { throw mapAssetAPIError(error); }
}
export const getAssetBindings = (id: string, signal?: AbortSignal) => mapped<AssetBindingsResponse>(api.get(`${assetPath(id)}/bindings`, { signal }));
export const replaceAssetBindings = (id: string, data: ReplaceAssetBindingsRequest, signal?: AbortSignal) => mapped<AssetBindingsResponse>(api.put(`${assetPath(id)}/bindings`, data, { signal }));
export const getAssetMeasurements = (id: string, signal?: AbortSignal) => mapped<AssetMeasurementSnapshot>(api.get(`${assetPath(id)}/measurements`, { signal }));
export const getAssetConnectivity = (id: string, signal?: AbortSignal) => mapped<AssetConnectivity>(api.get(`${assetPath(id)}/connectivity`, { signal }));
export const getTagAssets = (id: string, signal?: AbortSignal) => mapped<TagAssetConnectivity>(api.get(`/tags/${encodeURIComponent(id)}/assets`, { signal }));

export async function monitorAssetMeasurements(id: string, context: SSEStreamContext<AssetMeasurementReading>): Promise<void> {
  const headers: Record<string, string> = {};
  const token = getAccessToken();
  if (token) headers.Authorization = `Bearer ${token}`;
  if (context.lastEventId !== null) headers["Last-Event-ID"] = String(context.lastEventId);
  const baseURL = import.meta.env.VITE_API_URL || "/api";
  const response = await fetch(`${baseURL}${assetPath(id)}/measurements/stream`, { headers, credentials: "include", signal: context.signal });
  if (!response.ok || !response.body) throw new AssetAPIError("ASSET_LIVE_UNAVAILABLE", `Asset measurement stream failed with status ${response.status}`, response.status);
  context.onOpen();
  const reader = response.body.getReader();
  const decoder = new TextDecoder();
  let buffer = "";
  let event = "";
  let data: string[] = [];
  const dispatch = () => {
    if (event === "asset_measurement" && data.length) {
      try { context.onMessage(JSON.parse(data.join("\n")) as AssetMeasurementReading); } catch { /* ignore malformed events */ }
    }
    event = ""; data = [];
  };
  const line = (value: string) => {
    if (!value) return dispatch();
    if (value.startsWith(":")) return;
    const separator = value.indexOf(":");
    const field = separator < 0 ? value : value.slice(0, separator);
    const fieldValue = (separator < 0 ? "" : value.slice(separator + 1)).replace(/^ /, "");
    if (field === "event") event = fieldValue;
    if (field === "data") data.push(fieldValue);
    if (field === "retry" && /^\d+$/.test(fieldValue)) context.onRetry(Number(fieldValue));
  };
  while (true) {
    const chunk = await reader.read();
    buffer += decoder.decode(chunk.value, { stream: !chunk.done });
    let newline = buffer.indexOf("\n");
    while (newline >= 0) { line(buffer.slice(0, newline).replace(/\r$/, "")); buffer = buffer.slice(newline + 1); newline = buffer.indexOf("\n"); }
    if (chunk.done) { if (buffer) line(buffer.replace(/\r$/, "")); dispatch(); return; }
  }
}

export type { MeasurementBinding };
