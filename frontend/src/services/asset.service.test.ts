import { AxiosError, type AxiosResponse } from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Asset, AssetMeasurementReading } from "../types/asset";
import api from "./api";
import {
  AssetAPIError, createAsset, deleteAsset, getAsset, getAssetAncestors, getAssetBindings,
  getAssetChildren, getAssetConnectivity, getAssetMeasurements, getAssetRoots, getAssetTree,
  getTagAssets, listAssets, mapAssetAPIError, monitorAssetMeasurements, moveAsset,
  replaceAssetBindings, updateAsset,
} from "./asset.service";

vi.mock("./api", () => ({
  default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() },
  getAccessToken: vi.fn(() => "asset-token"),
}));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);
const asset: Asset = { id: "asset/id", parent_id: null, name: "Main meter", kind: "meter", description: null, enabled: true, timezone: "Asia/Bangkok", position: 0, metadata: {}, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const responseWith = <T,>(data: T) => ({ data }) as AxiosResponse<T>;

describe("Asset service", () => {
  beforeEach(() => { mockedDelete.mockReset(); mockedGet.mockReset(); mockedPost.mockReset(); mockedPut.mockReset(); });
  afterEach(() => vi.unstubAllGlobals());

  it("covers cancellable hierarchy, live, and connectivity reads with encoded IDs", async () => {
    const signal = new AbortController().signal;
    mockedGet.mockResolvedValue(responseWith({ data: [] }));
    await listAssets({ kind: "meter", enabled: false, page: 2, per_page: 10 }, signal);
    await getAssetRoots(signal);
    await getAsset(asset.id, signal);
    await getAssetChildren(asset.id, signal);
    await getAssetTree(asset.id, signal);
    await getAssetAncestors(asset.id, signal);
    await getAssetBindings(asset.id, signal);
    await getAssetMeasurements(asset.id, signal);
    await getAssetConnectivity(asset.id, signal);
    await getTagAssets("tag/id", signal);

    expect(mockedGet.mock.calls.map(([path]) => path)).toEqual([
      "/assets", "/assets/roots", "/assets/asset%2Fid", "/assets/asset%2Fid/children",
      "/assets/asset%2Fid/tree", "/assets/asset%2Fid/ancestors", "/assets/asset%2Fid/bindings",
      "/assets/asset%2Fid/measurements", "/assets/asset%2Fid/connectivity", "/tags/tag%2Fid/assets",
    ]);
    expect(mockedGet.mock.calls.every(([, config]) => config?.signal === signal)).toBe(true);
  });

  it("covers cancellable mutations and preserves explicit nulls", async () => {
    const signal = new AbortController().signal;
    mockedPost.mockResolvedValue(responseWith(asset));
    mockedPut.mockResolvedValue(responseWith(asset));
    mockedDelete.mockResolvedValue(responseWith(undefined));
    await createAsset({ name: asset.name, kind: "meter", parent_id: null }, signal);
    await updateAsset(asset.id, { description: null, timezone: null }, signal);
    await moveAsset(asset.id, { parent_id: null, position: 3 }, signal);
    await replaceAssetBindings(asset.id, { bindings: [] }, signal);
    await deleteAsset(asset.id, signal);
    expect(mockedPost).toHaveBeenCalledWith("/assets", { name: asset.name, kind: "meter", parent_id: null }, { signal });
    expect(mockedPut).toHaveBeenNthCalledWith(1, "/assets/asset%2Fid", { description: null, timezone: null }, { signal });
    expect(mockedPut).toHaveBeenNthCalledWith(2, "/assets/asset%2Fid/move", { parent_id: null, position: 3 }, { signal });
    expect(mockedPut).toHaveBeenNthCalledWith(3, "/assets/asset%2Fid/bindings", { bindings: [] }, { signal });
    expect(mockedDelete).toHaveBeenCalledWith("/assets/asset%2Fid", { signal });
  });

  it("maps only well-formed Asset API errors", () => {
    const response = { status: 409, data: { error: { code: "ASSET_DEPENDENTS", message: "Asset has dependents" } } } as AxiosResponse;
    const mapped = mapAssetAPIError(new AxiosError("conflict", "ERR_BAD_RESPONSE", undefined, undefined, response));
    expect(mapped).toBeInstanceOf(AssetAPIError);
    expect(mapped).toMatchObject({ code: "ASSET_DEPENDENTS", status: 409, message: "Asset has dependents" });
    const ordinary = new Error("offline");
    expect(mapAssetAPIError(ordinary)).toBe(ordinary);
  });

  it("parses fragmented measurement SSE and ignores malformed events", async () => {
    const reading: AssetMeasurementReading = { binding_id: "binding-1", source: { kind: "tag", tag_id: "tag-1" }, semantic: { resource: "electricity", quantity: "power", unit: "kW", precision: 2 }, available: true, value: 12.5, quality: "good" };
    const payload = `retry: 2500\r\n\r\nevent: asset_measurement\r\ndata: nope\r\n\r\nevent: asset_measurement\r\ndata: ${JSON.stringify(reading)}\r\n\r\n`;
    const bytes = new TextEncoder().encode(payload);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(controller) { controller.enqueue(bytes.slice(0, 17)); controller.enqueue(bytes.slice(17)); controller.close(); } }), { status: 200 })));
    const onOpen = vi.fn(), onMessage = vi.fn(), onRetry = vi.fn();
    const signal = new AbortController().signal;
    await monitorAssetMeasurements(asset.id, { signal, lastEventId: 8, onOpen, onMessage, onRetry });
    expect(fetch).toHaveBeenCalledWith("/api/assets/asset%2Fid/measurements/stream", { headers: { Authorization: "Bearer asset-token", "Last-Event-ID": "8" }, credentials: "include", signal });
    expect(onOpen).toHaveBeenCalledOnce();
    expect(onRetry).toHaveBeenCalledWith(2500);
    expect(onMessage).toHaveBeenCalledExactlyOnceWith(reading);
  });
});
