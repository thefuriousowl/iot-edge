import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { DataLogger, DataLoggerHistoryResponse, DataLoggerListResponse, DataLoggerQueryResponse, DataManagementOverview, RetentionCleanupResult, RetentionPlan, RetentionStatus, SaveDataLoggerRequest } from "../types/datalogger";
import api from "./api";
import { cleanupDataLoggerRetention, createDataLogger, deleteDataLogger, getDataLogger, getDataLoggerHistory, getDataLoggerRetention, getDataManagementOverview, listDataLoggers, previewDataLoggerRetention, queryDataLogger, updateDataLogger } from "./datalogger.service";

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
  max_size_bytes: null,
  max_age_seconds: 86400,
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
  max_size_bytes: null,
  max_age_seconds: 86400,
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

  it("loads the protected Data Management overview with cancellation", async () => {
    const controller = new AbortController();
    const expected: DataManagementOverview = {
      evaluated_at: "2026-08-24T08:00:00Z",
      logger_count: 2,
      enabled_logger_count: 1,
      policy_logger_count: 1,
      logical_history: { row_count: 20, batch_count: 10, estimated_size_bytes: 4096, oldest_batch_at: null, newest_batch_at: null },
      postgresql_physical_allocation: { raw_history_bytes: 8192, batch_accounting_bytes: 4096, total_bytes: 12288 },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(getDataManagementOverview(controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/data-management/overview", { signal: controller.signal });
  });

  it("loads retention status and a fresh preview through encoded paths", async () => {
    const controller = new AbortController();
    const metrics = { row_count: 20, batch_count: 10, estimated_size_bytes: 4096, oldest_batch_at: null, newest_batch_at: null };
    const plan: RetentionPlan = { evaluated_at: "2026-08-24T08:00:00Z", cutoff_at: null, policy: { max_size_bytes: null, max_age_seconds: 86400 }, current: metrics, remove: { ...metrics, row_count: 4, batch_count: 2 }, estimated_retained: { ...metrics, row_count: 16, batch_count: 8 } };
    const status: RetentionStatus = { plan, last_run: null };
    mockedGet.mockResolvedValueOnce(responseWith(status)).mockResolvedValueOnce(responseWith(plan));

    await expect(getDataLoggerRetention("logger with/slash", controller.signal)).resolves.toEqual(status);
    expect(mockedGet).toHaveBeenNthCalledWith(1, "/data-loggers/logger%20with%2Fslash/retention", { signal: controller.signal });
    await expect(previewDataLoggerRetention("logger with/slash", controller.signal)).resolves.toEqual(plan);
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/data-loggers/logger%20with%2Fslash/retention/preview", { signal: controller.signal });
  });

  it("sends an explicit confirmed retention cleanup request", async () => {
    const metrics = { row_count: 0, batch_count: 0, estimated_size_bytes: 0, oldest_batch_at: null, newest_batch_at: null };
    const result: RetentionCleanupResult = { started_at: "2026-08-24T08:00:00Z", completed_at: "2026-08-24T08:00:01Z", evaluated_at: "2026-08-24T08:00:00Z", cutoff_at: null, policy: { max_size_bytes: null, max_age_seconds: 86400 }, deleted: metrics, retained: metrics, remaining_removal: metrics, complete: true };
    mockedPost.mockResolvedValue(responseWith(result));

    await expect(cleanupDataLoggerRetention("logger with/slash", { confirm: true, batch_limit: 250 })).resolves.toEqual(result);
    expect(mockedPost).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash/retention/cleanup", { confirm: true, batch_limit: 250 });
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

  it("queries raw or aggregated rows through the encoded logger path", async () => {
    const controller = new AbortController();
    const params = { mode: "aggregate" as const, tag_ids: "tag-1,tag-2", from: "2026-08-22T00:00:00Z", to: "2026-08-23T00:00:00Z", bucket: "5m" as const, aggregate: "avg" as const, page: 2, per_page: 100 };
    const expected: DataLoggerQueryResponse = {
      data: [{ at: "2026-08-22T01:00:00Z", values: { "tag-1": { tag_id: "tag-1", data_type: "float64", value: 230.5, good_count: 5, total_count: 5, supported: true } } }],
      mode: "aggregate",
      bucket: "5m",
      aggregate: "avg",
      pagination: { page: 2, per_page: 100, total: 101, total_pages: 2 },
    };
    mockedGet.mockResolvedValue(responseWith(expected));

    await expect(queryDataLogger("logger with/slash", params, controller.signal)).resolves.toEqual(expected);
    expect(mockedGet).toHaveBeenCalledWith("/data-loggers/logger%20with%2Fslash/query", { params, signal: controller.signal });
  });
});
