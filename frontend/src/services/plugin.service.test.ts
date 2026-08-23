import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { CreatePluginRequest, PluginInstance, PluginListResponse, PluginStatusResponse, PluginTypeListResponse } from "../types/plugin";
import api from "./api";
import { createPlugin, deletePlugin, disablePlugin, enablePlugin, getPlugin, getPluginStatus, listPlugins, listPluginTypes, restartPlugin, updatePlugin } from "./plugin.service";

vi.mock("./api", () => ({ default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() } }));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);
const runtime = { state: "running" as const, started_at: "2026-08-23T00:00:00Z", last_transition_at: "2026-08-23T00:00:00Z", error: null };
const instance: PluginInstance = { id: "plugin-1", type: "energy_management", name: "Plant Energy", enabled: true, config_version: 1, runtime, config: { logger_id: "logger-1" }, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" };
const request: CreatePluginRequest = { type: "energy_management", name: "Plant Energy", config: { logger_id: "logger-1" } };

function responseWith<T>(data: T): AxiosResponse<T> { return { data } as AxiosResponse<T>; }

describe("Plugin service", () => {
  beforeEach(() => { mockedDelete.mockReset(); mockedGet.mockReset(); mockedPost.mockReset(); mockedPut.mockReset(); });

  it("lists manifests and filtered instances with cancellation", async () => {
    const controller = new AbortController();
    const types: PluginTypeListResponse = { data: [{ type: "energy_management", name: "Energy Management", description: "Energy", version: "0.1.0", config_version: 1, multiple_instances: true, capabilities: ["logger.committed_batches"] }] };
    const list: PluginListResponse = { data: [instance], pagination: { page: 2, per_page: 20, total: 21, total_pages: 2 } };
    mockedGet.mockResolvedValueOnce(responseWith(types)).mockResolvedValueOnce(responseWith(list));
    await expect(listPluginTypes(controller.signal)).resolves.toEqual(types);
    expect(mockedGet).toHaveBeenNthCalledWith(1, "/plugin-types", { signal: controller.signal });
    const params = { type: "energy_management" as const, enabled: true, search: "plant", page: 2, per_page: 20 };
    await expect(listPlugins(params, controller.signal)).resolves.toEqual(list);
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/plugins", { params, signal: controller.signal });
  });

  it("uses encoded paths for CRUD without transforming config", async () => {
    const controller = new AbortController();
    mockedPost.mockResolvedValue(responseWith(instance)); mockedGet.mockResolvedValue(responseWith(instance)); mockedPut.mockResolvedValue(responseWith(instance)); mockedDelete.mockResolvedValue(responseWith(undefined));
    await expect(createPlugin(request)).resolves.toEqual(instance);
    expect(mockedPost).toHaveBeenCalledWith("/plugins", request);
    await expect(getPlugin("plugin / one", controller.signal)).resolves.toEqual(instance);
    expect(mockedGet).toHaveBeenCalledWith("/plugins/plugin%20%2F%20one", { signal: controller.signal });
    await expect(updatePlugin("plugin / one", { name: "Renamed", config: request.config })).resolves.toEqual(instance);
    expect(mockedPut).toHaveBeenCalledWith("/plugins/plugin%20%2F%20one", { name: "Renamed", config: request.config });
    await expect(deletePlugin("plugin / one")).resolves.toBeUndefined();
  });

  it("transports explicit lifecycle actions and status", async () => {
    const status: PluginStatusResponse = { id: instance.id, type: instance.type, enabled: true, runtime };
    mockedPost.mockResolvedValueOnce(responseWith(instance)).mockResolvedValueOnce(responseWith(instance)).mockResolvedValueOnce(responseWith(status));
    mockedGet.mockResolvedValue(responseWith(status));
    await expect(enablePlugin("plugin/1")).resolves.toEqual(instance);
    expect(mockedPost).toHaveBeenNthCalledWith(1, "/plugins/plugin%2F1/enable");
    await expect(disablePlugin("plugin/1")).resolves.toEqual(instance);
    expect(mockedPost).toHaveBeenNthCalledWith(2, "/plugins/plugin%2F1/disable");
    await expect(restartPlugin("plugin/1")).resolves.toEqual(status);
    expect(mockedPost).toHaveBeenNthCalledWith(3, "/plugins/plugin%2F1/restart");
    await expect(getPluginStatus("plugin/1")).resolves.toEqual(status);
  });
});
