import type { AxiosResponse } from "axios";
import { beforeEach, describe, expect, it, vi } from "vitest";

import type { Report, ReportListResponse, ReportQueryResponse, SaveReportRequest } from "../types/report";
import api from "./api";
import { createReport, deleteReport, exportReportCSV, getReport, listReports, queryReport, updateReport } from "./report.service";

vi.mock("./api", () => ({ default: { delete: vi.fn(), get: vi.fn(), post: vi.fn(), put: vi.fn() } }));

const mockedDelete = vi.mocked(api.delete);
const mockedGet = vi.mocked(api.get);
const mockedPost = vi.mocked(api.post);
const mockedPut = vi.mocked(api.put);
const entity: Report = { id: "report-1", name: "Energy", description: null, logger_id: "logger-1", logger_name: "Plant", timezone: "UTC", mode: "aggregate", bucket: "1h", column_count: 1, columns: [{ tag_id: "tag-1", position: 0, name: "Demand", aggregate: "avg", tag_name: "Power", tag_type: "reading", data_type: "float64" }], created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" };
const request: SaveReportRequest = { name: "Energy", description: null, logger_id: "logger-1", timezone: "UTC", mode: "aggregate", bucket: "1h", columns: [{ tag_id: "tag-1", name: "Demand", aggregate: "avg" }] };

function responseWith<T>(data: T): AxiosResponse<T> { return { data } as AxiosResponse<T>; }

describe("Report service", () => {
  beforeEach(() => { mockedDelete.mockReset(); mockedGet.mockReset(); mockedPost.mockReset(); mockedPut.mockReset(); });

  it("transports list filters and encoded resource CRUD", async () => {
    const controller = new AbortController();
    const list: ReportListResponse = { data: [entity], pagination: { page: 2, per_page: 20, total: 21, total_pages: 2 } };
    mockedGet.mockResolvedValueOnce(responseWith(list)).mockResolvedValueOnce(responseWith(entity));
    mockedPost.mockResolvedValue(responseWith(entity)); mockedPut.mockResolvedValue(responseWith(entity)); mockedDelete.mockResolvedValue(responseWith(undefined));
    await expect(listReports({ mode: "aggregate", search: "energy", page: 2 }, controller.signal)).resolves.toEqual(list);
    expect(mockedGet).toHaveBeenNthCalledWith(1, "/reports", { params: { mode: "aggregate", search: "energy", page: 2 }, signal: controller.signal });
    await expect(createReport(request)).resolves.toEqual(entity);
    await expect(getReport("report / one", controller.signal)).resolves.toEqual(entity);
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/reports/report%20%2F%20one", { signal: controller.signal });
    await expect(updateReport("report / one", request)).resolves.toEqual(entity);
    await expect(deleteReport("report / one")).resolves.toBeUndefined();
    expect(mockedPost).toHaveBeenCalledWith("/reports", request);
    expect(mockedPut).toHaveBeenCalledWith("/reports/report%20%2F%20one", request);
    expect(mockedDelete).toHaveBeenCalledWith("/reports/report%20%2F%20one");
  });

  it("queries pages and requests full-range CSV as a blob", async () => {
    const params = { from: "2026-08-22T00:00:00Z", to: "2026-08-23T00:00:00Z", page: 2, per_page: 100 };
    const query: ReportQueryResponse = { report: entity, data: [], pagination: { page: 2, per_page: 100, total: 101, total_pages: 2 } };
    const blob = new Blob(["timestamp,Demand"]);
    mockedGet.mockResolvedValueOnce(responseWith(query)).mockResolvedValueOnce(responseWith(blob));
    await expect(queryReport("report/1", params)).resolves.toEqual(query);
    expect(mockedGet).toHaveBeenNthCalledWith(1, "/reports/report%2F1/query", { params, signal: undefined });
    await expect(exportReportCSV("report/1", params)).resolves.toBe(blob);
    expect(mockedGet).toHaveBeenNthCalledWith(2, "/reports/report%2F1/export.csv", { params, responseType: "blob" });
  });
});
