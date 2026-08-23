import type { AxiosResponse } from "axios";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { EnergyHistoryResponse, EnergyLiveEvent, EnergyOverviewResponse } from "../types/plugin";
import api from "./api";
import { exportEnergyCSV, getEnergyHistory, getEnergyOverview, monitorEnergy } from "./energy.service";

vi.mock("./api", () => ({ default: { get: vi.fn() }, getAccessToken: vi.fn(() => "access-token") }));
const mockedGet = vi.mocked(api.get);
const demand = { kilowatts: 12, valid: true, errors: null };
const ratio = { value: 3, valid: true };
const latest = { batch_at: "2026-08-23T00:00:00Z", electrical: demand, thermal: { ...demand, kilowatts: 36 }, tariff: { rate_per_kwh: 4.5, valid: true, errors: null }, cop: ratio };
const summary = { kilowatt_hours: 12, covered_seconds: 3600, skipped_seconds: 0, coverage_percent: 100, segments: 60, skipped_segments: 0, issues: null };
const period = { from: "2026-08-22T23:00:00Z", to: "2026-08-23T00:00:00Z", electrical: summary, thermal: { ...summary, kilowatt_hours: 36 }, cost: { value: 54, valid: true }, cop: ratio };
const overview: EnergyOverviewResponse = { instance_id: "plugin-1", logger_id: "logger-1", timezone: "UTC", currency: "THB", tariff_mode: "flat", rate_per_kwh: 4.5, as_of: latest.batch_at, latest, today: period, month: period };

function responseWith<T>(data: T): AxiosResponse<T> { return { data } as AxiosResponse<T>; }

describe("Energy service", () => {
  beforeEach(() => mockedGet.mockReset());
  afterEach(() => vi.unstubAllGlobals());

  it("loads overview/history and exports exact-range CSV through encoded paths", async () => {
    const controller = new AbortController();
    const params = { from: period.from, to: period.to, bucket: "1h" as const, page: 2, per_page: 100 };
    const history: EnergyHistoryResponse = { instance_id: overview.instance_id, logger_id: overview.logger_id, timezone: "UTC", bucket: "1h", currency: "THB", tariff_mode: "flat", rate_per_kwh: 4.5, data: [period], pagination: { page: 2, per_page: 100, total: 101, total_pages: 2 } };
    const blob = new Blob(["from,to,electrical_kwh"]);
    mockedGet.mockResolvedValueOnce(responseWith(overview)).mockResolvedValueOnce(responseWith(history)).mockResolvedValueOnce(responseWith(blob));
    await expect(getEnergyOverview("plugin / one", controller.signal)).resolves.toEqual(overview);
    expect(mockedGet).toHaveBeenNthCalledWith(1, "/plugins/plugin%20%2F%20one/energy/overview", { signal: controller.signal });
    await expect(getEnergyHistory("plugin / one", params, controller.signal)).resolves.toEqual(history);
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/plugins/plugin%20%2F%20one/energy/history", { params, signal: controller.signal });
    await expect(exportEnergyCSV("plugin / one", params)).resolves.toBe(blob);
    expect(mockedGet).toHaveBeenNthCalledWith(3, "/plugins/plugin%20%2F%20one/energy/export.csv", { params, responseType: "blob" });
  });

  it("resumes and parses reset/retry/fragmented Energy events", async () => {
    const event: EnergyLiveEvent = { id: "11111111-1111-1111-1111-111111111111:12", sequence: 12, instance_id: "plugin-1", metrics: latest };
    const payload = `retry: 4500\r\n: connected\r\n\r\nevent: energy_reset\r\ndata: {"reason":"cursor_unavailable"}\r\n\r\nevent: energy_metrics\r\ndata: not-json\r\n\r\nid: ${event.id}\r\nevent: energy_metrics\r\ndata: ${JSON.stringify(event)}\r\n\r\n`;
    const encoded = new TextEncoder().encode(payload);
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(new ReadableStream({ start(stream) { stream.enqueue(encoded.slice(0, 13)); stream.enqueue(encoded.slice(13, 67)); stream.enqueue(encoded.slice(67)); stream.close(); } }), { status: 200 })));
    const context = { signal: new AbortController().signal, lastEventId: "11111111-1111-1111-1111-111111111111:9", onOpen: vi.fn(), onMessage: vi.fn(), onReset: vi.fn(), onRetry: vi.fn() };
    await monitorEnergy("plugin / one", context);
    expect(context.onOpen).toHaveBeenCalledOnce();
    expect(context.onRetry).toHaveBeenCalledWith(4500);
    expect(context.onReset).toHaveBeenCalledWith("cursor_unavailable");
    expect(context.onMessage).toHaveBeenCalledOnce();
    expect(context.onMessage).toHaveBeenCalledWith(event);
    expect(fetch).toHaveBeenCalledWith("/api/plugins/plugin%20%2F%20one/energy/stream", { headers: { Authorization: "Bearer access-token", "Last-Event-ID": context.lastEventId }, credentials: "include", signal: context.signal });
  });

  it("rejects unavailable streams before reporting open", async () => {
    vi.stubGlobal("fetch", vi.fn().mockResolvedValue(new Response(null, { status: 503 })));
    const onOpen = vi.fn();
    await expect(monitorEnergy("plugin-1", { signal: new AbortController().signal, lastEventId: null, onOpen, onMessage: vi.fn(), onReset: vi.fn(), onRetry: vi.fn() })).rejects.toThrow("Energy live stream failed with status 503");
    expect(onOpen).not.toHaveBeenCalled();
  });
});
