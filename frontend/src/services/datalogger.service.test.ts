import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { DataLogger, DataLoggerHistoryResponse, DataLoggerListResponse, SaveDataLoggerRequest } from "../types/datalogger";
import api from "./api";
import { createDataLogger, deleteDataLogger, getDataLogger, getDataLoggerHistory, listDataLoggers, updateDataLogger } from "./datalogger.service";

vi.mock("./api", () => ({
  default: {
    delete: vi.fn(),
    get: vi.fn(),
    post: vi.fn(),
    put: vi.fn(),
  },
}));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);

const logger: DataLogger = {
  id: "logger-1",
  name: "Plant history",
  description: null,
  enabled: true,
  timezone: "Asia/Bangkok",
  mode: "interval",
  start_at: "2026-08-22T01:00:00Z",
  end_at: null,
  config: { interval_seconds: 60 },
  tag_count: 1,
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const request: SaveDataLoggerRequest = {
  name: logger.name,
  description: null,
  enabled: true,
  timezone: logger.timezone,
  mode: logger.mode,
  start_at: logger.start_at,
  end_at: null,
  config: logger.config,
  tag_ids: ["tag-1"],
};

function responseWith<T>(data: T): AxiosResponse<T> {
  return { data } as AxiosResponse<T>;
}

describe("Data Logger service", () => {
  beforeEach(() => {
    mockedDelete.mockReset();
    mockedGet.mockReset();
    mockedPost.mockReset();
    mockedPut.mockReset();
  });

  it("lists definitions with filters, pagination, and cancellation", async () => {
    const controller = new AbortController();
    const params = { mode: "schedule" as const, enabled: false, search: "energy", page: 2, per_page: 20 };
    const expected: DataLoggerListResponse = { data: [logger], pagination: { page: 2, per_page: 20, total: 21, total_pages: 2 } };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(listDataLoggers(params, controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/data-loggers", { params, signal: controller.signal });
  });

  it("creates and fetches definitions without transforming schedule data", async () => {
    const controller = new AbortController();
    mockedPost.mockResolvedValue(responseWith(logger));
    mockedGet.mockResolvedValue(responseWith(logger));

    await expect(createDataLogger(request)).resolves.toEqual(logger);
    expect(mockedPost).toHaveBeenCalledWith("/data-loggers", request);
    await expect(getDataLogger("logger with/slash", controller.signal)).resolves.toEqual(logger);
    expect(mockedGet).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash", { signal: controller.signal });
  });

  it("updates and deletes through an encoded resource path", async () => {
    mockedPut.mockResolvedValue(responseWith(logger));
    mockedDelete.mockResolvedValue(responseWith(undefined));

    await expect(updateDataLogger("logger with/slash", request)).resolves.toEqual(logger);
    expect(mockedPut).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash", request);
    await expect(deleteDataLogger("logger with/slash")).resolves.toBeUndefined();
    expect(mockedDelete).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash");
  });

  it("loads filtered paginated history through the encoded logger path", async () => {
    const controller = new AbortController();
    const params = { tag_id: "tag-1", from: "2026-08-22T00:00:00Z", to: "2026-08-23T00:00:00Z", page: 2, per_page: 100 };
    const expected: DataLoggerHistoryResponse = {
      data: [{ logger_id: logger.id, tag_id: "tag-1", batch_at: "2026-08-22T01:00:00Z", observed_at: "2026-08-22T00:59:59Z", data_type: "float64", value: 230.5, quality: "good", persisted_at: "2026-08-22T01:00:00Z" }],
      last_batch_at: "2026-08-22T01:00:00Z",
      pagination: { page: 2, per_page: 100, total: 101, total_pages: 2 },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(getDataLoggerHistory("logger with/slash", params, controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash/history", { params, signal: controller.signal });
  });
});
