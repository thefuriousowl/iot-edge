import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import api, { getAccessToken } from "./api";
import { createDatasource, createDevice, deleteDatasource, deleteDevice, listDatasources, listDevices, monitorDatasource, previewDatasource, previewSavedDatasource, updateDatasource, updateDevice } from "./device.service";
import type { CreateDatasourceRequest, Datasource, DatasourceSample, Device } from "../types/device";

vi.mock("./api", () => ({
  default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() },
  getAccessToken: vi.fn(),
}));

const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);
const mockedDelete = vi.mocked(api.delete);
const mockedGetAccessToken = vi.mocked(getAccessToken);
function responseWith<T>(data: T): AxiosResponse<T> { return { data } as AxiosResponse<T>; }

const device: Device = { id: "device-1", vgateway_id: "gateway-1", name: "Meter", type: "modbus_device", description: null, enabled: true, config: { unit_id: 7, poll_interval_ms: 1000, request_timeout_ms: null }, datasource_count: 0, tag_count: 0, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-21T00:00:00Z" };
const datasource: Datasource = { id: "source-1", device_id: device.id, name: "Registers", type: "modbus_read", description: null, enabled: true, status: "idle", config: { function_code: 3, start_address: 10, quantity: 2, poll_interval_ms: null }, created_at: "2026-08-21T00:00:00Z", updated_at: "2026-08-21T00:00:00Z" };
const request: CreateDatasourceRequest = { name: datasource.name, type: "modbus_read", config: { function_code: 3, start_address: 10, quantity: 2 } };

describe("device service", () => {
  beforeEach(() => { mockedGet.mockReset(); mockedPost.mockReset(); mockedPut.mockReset(); mockedDelete.mockReset(); mockedGetAccessToken.mockReset(); vi.unstubAllGlobals(); });

  it("lists and creates devices through nested gateway routes", async () => {
    const signal = new AbortController().signal;
    mockedGet.mockResolvedValue(responseWith({ data: [device] }));
    mockedPost.mockResolvedValue(responseWith(device));
    await expect(listDevices("gateway-1", signal)).resolves.toEqual([device]);
    expect(mockedGet).toHaveBeenCalledWith("/vgateways/gateway-1/devices", { signal });
    const createRequest = { name: "Meter", type: "modbus_device" as const, config: { unit_id: 7 } };
    await expect(createDevice("gateway-1", createRequest)).resolves.toEqual(device);
    expect(mockedPost).toHaveBeenCalledWith("/vgateways/gateway-1/devices", createRequest);
  });

  it("lists datasources and previews current unsaved config", async () => {
    mockedGet.mockResolvedValue(responseWith({ data: [datasource] }));
    const sample: DatasourceSample = { datasource_id: "", sequence: 1, observed_at: "2026-08-21T00:00:00Z", latency_ms: 2, quality: "good", raw_hex: "1234", data: { registers: [] } };
    mockedPost.mockResolvedValue(responseWith(sample));
    await expect(listDatasources(device.id)).resolves.toEqual([datasource]);
    await expect(previewDatasource(device.id, request)).resolves.toEqual(sample);
    expect(mockedPost).toHaveBeenCalledWith(`/devices/${device.id}/datasources/preview`, { type: "modbus_read", config: request.config });
    mockedPost.mockResolvedValue(responseWith(datasource));
    await expect(createDatasource(device.id, request)).resolves.toEqual(datasource);
  });

  it("updates and deletes devices and datasources through flat resource routes", async () => {
    const deviceUpdate = { name: "Meter paused", enabled: false };
    const datasourceUpdate = { name: "Registers paused", enabled: false };
    mockedPut.mockResolvedValueOnce(responseWith({ ...device, ...deviceUpdate })).mockResolvedValueOnce(responseWith({ ...datasource, ...datasourceUpdate }));
    mockedDelete.mockResolvedValue(responseWith(undefined));

    await expect(updateDevice(device.id, deviceUpdate)).resolves.toEqual({ ...device, ...deviceUpdate });
    expect(mockedPut).toHaveBeenNthCalledWith(1, `/devices/${device.id}`, deviceUpdate);
    await expect(updateDatasource(datasource.id, datasourceUpdate)).resolves.toEqual({ ...datasource, ...datasourceUpdate });
    expect(mockedPut).toHaveBeenNthCalledWith(2, `/datasources/${datasource.id}`, datasourceUpdate);
    await deleteDevice(device.id);
    await deleteDatasource(datasource.id);
    expect(mockedDelete).toHaveBeenNthCalledWith(1, `/devices/${device.id}`);
    expect(mockedDelete).toHaveBeenNthCalledWith(2, `/datasources/${datasource.id}`);
  });

  it("previews a saved datasource through its resource route", async () => {
    const sample: DatasourceSample = { datasource_id: datasource.id, sequence: 1, observed_at: "2026-08-21T00:00:00Z", latency_ms: 2, quality: "good", raw_hex: "1234", data: { registers: [] } };
    mockedPost.mockResolvedValue(responseWith(sample));
    await expect(previewSavedDatasource(datasource.id)).resolves.toEqual(sample);
    expect(mockedPost).toHaveBeenCalledWith(`/datasources/${datasource.id}/preview`);
  });

  it("parses fragmented SSE samples using the current bearer token", async () => {
    mockedGetAccessToken.mockReturnValue("access-token");
    const sample = { datasource_id: "source-1", sequence: 2, observed_at: "2026-08-21T00:00:00Z", latency_ms: 1, quality: "good", raw_hex: "0001", data: { registers: [] } };
    const encoded = new TextEncoder().encode(`: connected\n\nevent: sample\ndata: ${JSON.stringify(sample)}\n\n`);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(controller) { controller.enqueue(encoded.slice(0, 12)); controller.enqueue(encoded.slice(12)); controller.close(); } }), { status: 200 })));
    const received: DatasourceSample[] = [];
    await monitorDatasource("source-1", (value) => received.push(value), new AbortController().signal);
    expect(received).toEqual([sample]);
    expect(fetch).toHaveBeenCalledWith("/api/datasources/source-1/stream", expect.objectContaining({ headers: { Authorization: "Bearer access-token" }, credentials: "include" }));
  });
});
