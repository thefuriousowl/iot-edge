import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type {
  CreateDataPublisherRequest,
  DataPublisher,
  MQTTDiagnosticEvent,
  PublisherPayloadValidation,
  PublisherRuntimeStatus,
  PublisherSourceCatalogEntry,
} from "../types/publisher";
import api from "./api";
import {
  createDataPublisher,
  deleteDataPublisher,
  disableDataPublisher,
  enableDataPublisher,
  getDataPublisher,
  getDataPublisherStatus,
  listDataPublishers,
  listPublisherDiagnostics,
  listPublisherSources,
  probeHTTPServerListener,
  restartDataPublisher,
  testMQTTConnection,
  updateDataPublisher,
  validatePublisherPayload,
} from "./publisher.service";

vi.mock("./api", () => ({ default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() } }));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);

function responseWith<T>(data: T): AxiosResponse<T> { return { data } as AxiosResponse<T>; }

const runtime: PublisherRuntimeStatus = {
  publisher_id: "publisher-1", type: "mqtt", state: "stopped", config_version: 1,
  last_transition_at: "2026-08-23T00:00:00Z", request_count: 0, publish_count: 0,
  failure_count: 0, queue_depth: 0, drop_count: 0, reconnect_count: 0, connected: false,
  connection_count: 0, delivery_count: 0, delivery_failure_count: 0, transport_queue_depth: 0,
  transport_drop_count: 0, diagnostic_count: 0, diagnostic_drop_count: 0,
};

const publisher: DataPublisher = {
  id: "publisher-1", type: "mqtt", name: "Plant telemetry", enabled: false,
  config_version: 1, source_count: 1, runtime,
  created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z",
};

describe("Publisher service", () => {
  beforeEach(() => {
    mockedDelete.mockReset();
    mockedGet.mockReset();
    mockedPost.mockReset();
    mockedPut.mockReset();
  });

  it("loads the searchable typed source catalog with cancellation", async () => {
    const controller = new AbortController();
    const catalog: PublisherSourceCatalogEntry[] = [{
      descriptor: {
        reference: { kind: "tag", tag_id: "tag-1" }, name: "Active power", owner_name: "Power meter",
        schema_version: 1, data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
      },
      current: { quality: "good", sequence: 42, observed_at: "2026-08-23T08:35:25.573Z" },
    }];
    mockedGet.mockResolvedValue(responseWith({ data: catalog }));

    await expect(listPublisherSources({ kind: "tag", enabled: true, search: "power" }, controller.signal)).resolves.toEqual(catalog);
    expect(mockedGet).toHaveBeenCalledWith("/publisher-sources", {
      params: { kind: "tag", enabled: true, search: "power" }, signal: controller.signal,
    });
  });

  it("sends immutable references to authoritative payload validation", async () => {
    const sources = [{ alias: "power_kw", reference: { kind: "tag" as const, tag_id: "tag-1" } }];
    const validation: PublisherPayloadValidation = {
      referenced_aliases: ["power_kw"], helper_calls: 1, good: { power: 42 }, unavailable: { power: null }, windowed: { power: 42 },
    };
    mockedPost.mockResolvedValue(responseWith(validation));

    await expect(validatePublisherPayload('{{value "power_kw"}}', sources)).resolves.toEqual(validation);
    expect(mockedPost).toHaveBeenCalledWith("/publisher-payloads/validate", {
      payload_template: '{{value "power_kw"}}', sources,
    });
  });

  it("covers Publisher list, detail, create, update, and delete endpoints", async () => {
    const request = {
      type: "mqtt", name: "Plant telemetry", description: null, enabled: false,
      config: { trigger: { mode: "interval", interval_ms: 60_000 }, mqtt: {} },
      sources: [{ alias: "power_kw", reference: { kind: "tag", tag_id: "tag-1" } }],
      credential_id: null,
    } as CreateDataPublisherRequest;
    const list = { data: [publisher], pagination: { page: 1, per_page: 20, total: 1, total_pages: 1 } };
    const controller = new AbortController();
    mockedGet.mockResolvedValueOnce(responseWith(list)).mockResolvedValueOnce(responseWith(publisher));
    mockedPost.mockResolvedValueOnce(responseWith(publisher));
    mockedPut.mockResolvedValueOnce(responseWith(publisher));
    mockedDelete.mockResolvedValueOnce(responseWith(undefined));

    await expect(listDataPublishers({ type: "mqtt", enabled: false, search: "plant", page: 1, per_page: 20 }, controller.signal)).resolves.toEqual(list);
    await expect(getDataPublisher("publisher/1", controller.signal)).resolves.toEqual(publisher);
    await expect(createDataPublisher(request)).resolves.toEqual(publisher);
    await expect(updateDataPublisher("publisher/1", { name: "Updated" })).resolves.toEqual(publisher);
    await expect(deleteDataPublisher("publisher/1")).resolves.toBeUndefined();

    expect(mockedGet).toHaveBeenNthCalledWith(1, "/data-publishers", { params: { type: "mqtt", enabled: false, search: "plant", page: 1, per_page: 20 }, signal: controller.signal });
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/data-publishers/publisher%2F1", { signal: controller.signal });
    expect(mockedPost).toHaveBeenCalledWith("/data-publishers", request);
    expect(mockedPut).toHaveBeenCalledWith("/data-publishers/publisher%2F1", { name: "Updated" });
    expect(mockedDelete).toHaveBeenCalledWith("/data-publishers/publisher%2F1");
    expect(JSON.stringify(request)).not.toContain("password_value");
  });

  it("covers lifecycle and runtime monitoring endpoints", async () => {
    const enabled = { ...publisher, enabled: true, runtime: { ...runtime, state: "running" as const, connected: true } };
    const restarted = { id: publisher.id, type: publisher.type, enabled: true, runtime: enabled.runtime };
    const diagnostic: MQTTDiagnosticEvent = {
      sequence: 1, label: "ack", topic: "site/ack", qos: 1, retained: false, duplicate: false,
      received_at: "2026-08-23T00:00:00Z", format: "json", payload: '{"status":"success"}', truncated: false,
    };
    mockedPost.mockResolvedValueOnce(responseWith(enabled)).mockResolvedValueOnce(responseWith(publisher)).mockResolvedValueOnce(responseWith(restarted));
    mockedGet.mockResolvedValueOnce(responseWith({ runtime: enabled.runtime })).mockResolvedValueOnce(responseWith({ data: [diagnostic] }));

    await expect(enableDataPublisher(publisher.id)).resolves.toEqual(enabled);
    await expect(disableDataPublisher(publisher.id)).resolves.toEqual(publisher);
    await expect(restartDataPublisher(publisher.id)).resolves.toEqual(restarted);
    await expect(getDataPublisherStatus(publisher.id)).resolves.toEqual(enabled.runtime);
    await expect(listPublisherDiagnostics(publisher.id)).resolves.toEqual([diagnostic]);

    expect(mockedPost.mock.calls.map(([path]) => path)).toEqual([
      "/data-publishers/publisher-1/enable", "/data-publishers/publisher-1/disable", "/data-publishers/publisher-1/restart",
    ]);
    expect(mockedGet.mock.calls.map(([path]) => path)).toEqual([
      "/data-publishers/publisher-1/status", "/data-publishers/publisher-1/diagnostics",
    ]);
  });

  it("probes an HTTP Server listener without sending endpoint credentials", async () => {
    const result = { reachable: true, probed_at: "2026-08-23T00:00:00Z", latency_ms: 1.25, endpoint: { network: "tcp" as const, scheme: "http" as const, bind_address: "127.0.0.1", port: 8088, path: "/snapshot", access_mode: "anonymous" as const, quality_policy: "payload" as const } };
    mockedPost.mockResolvedValueOnce(responseWith(result));

    await expect(probeHTTPServerListener(publisher.id)).resolves.toEqual(result);
    expect(mockedPost).toHaveBeenCalledWith("/data-publishers/publisher-1/probe-listener");
  });

  it("tests the stored Publisher configuration without transient secrets", async () => {
    mockedPost.mockResolvedValueOnce(responseWith({ connected: true, connected_at: "2026-08-23T00:00:00Z", latency_ms: 18 }));

    await expect(testMQTTConnection(publisher.id)).resolves.toMatchObject({ connected: true, latency_ms: 18 });

    expect(mockedPost).toHaveBeenCalledWith("/data-publishers/publisher-1/test-connection");
  });

  it("normalizes a legacy null diagnostics response to an empty array", async () => {
    mockedGet.mockResolvedValueOnce(responseWith({ data: null }));

    await expect(listPublisherDiagnostics(publisher.id)).resolves.toEqual([]);
  });
});
