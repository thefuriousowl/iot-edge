// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { exportReportCSV, getReport, queryReport } from "../../../services/report.service";
import type { Report, ReportQueryResponse } from "../../../types/report";
import ReportDetailPage from "./ReportDetailPage";

vi.mock("../../../services/report.service", () => ({ exportReportCSV: vi.fn(), getReport: vi.fn(), queryReport: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));

const mockedExport = vi.mocked(exportReportCSV);
const mockedGet = vi.mocked(getReport);
const mockedQuery = vi.mocked(queryReport);
const entity: Report = { id: "report-1", name: "Energy report", description: "Shift", logger_id: "logger-1", logger_name: "Plant history", timezone: "Asia/Bangkok", mode: "aggregate", bucket: "1h", column_count: 2, columns: [{ tag_id: "tag-1", position: 0, name: "Demand kW", aggregate: "avg", tag_name: "Power", tag_type: "reading", data_type: "float64" }, { tag_id: "tag-2", position: 1, name: "Peak kW", aggregate: "max", tag_name: "Power peak", tag_type: "calculated", data_type: "float64" }], created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" };
const query: ReportQueryResponse = { report: entity, data: [{ at: "2026-08-23T01:00:00Z", values: { "tag-1": { tag_id: "tag-1", data_type: "float64", value: 42.5, good_count: 5, total_count: 6, bad_count: 1, error: "illegal address", supported: true }, "tag-2": { tag_id: "tag-2", data_type: "float64", value: 55, good_count: 6, total_count: 6, supported: true } } }], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } };

function Probe() { return <span data-testid="search">{useLocation().search}</span>; }
function renderPage(path = "/reports/report-1?from=2026-08-23T00%3A00%3A00.000Z&to=2026-08-24T00%3A00%3A00.000Z") { return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/reports/:id" element={<><ReportDetailPage /><Probe /></>} /></Routes></MemoryRouter>); }

describe("ReportDetailPage", () => {
  beforeEach(() => { mockedExport.mockReset(); mockedGet.mockReset(); mockedQuery.mockReset(); mockedGet.mockResolvedValue(entity); mockedQuery.mockResolvedValue(query); mockedExport.mockResolvedValue(new Blob(["csv"])); Object.defineProperty(URL, "createObjectURL", { configurable: true, value: vi.fn(() => "blob:report") }); Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: vi.fn() }); vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => {}); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders custom headers, per-column functions, quality, and values", async () => {
    renderPage();
    expect(await screen.findByRole("heading", { name: "Energy report" })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: /Demand kW.*Power.*AVG/ })).toBeInTheDocument();
    expect(screen.getByRole("columnheader", { name: /Peak kW.*Power peak.*MAX/ })).toBeInTheDocument();
    expect(screen.getByText("42.5")).toBeInTheDocument();
    expect(screen.getByText("5/6 good · 1 bad")).toBeInTheDocument();
    expect(screen.getByText("illegal address")).toBeInTheDocument();
    expect(mockedQuery).toHaveBeenCalledWith("report-1", { from: "2026-08-23T00:00:00.000Z", to: "2026-08-24T00:00:00.000Z", page: 1, per_page: 100 }, expect.any(AbortSignal));
  });

  it("updates timezone-aware range and exports the entire range", async () => {
    renderPage(); await screen.findByRole("heading", { name: "Energy report" });
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-23T08:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-23T09:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Run Report" }));
    await waitFor(() => expect(screen.getByTestId("search")).toHaveTextContent("from=2026-08-23T01%3A00%3A00.000Z"));
    fireEvent.click(screen.getByRole("button", { name: "Export full range CSV" }));
    await waitFor(() => expect(mockedExport).toHaveBeenCalledWith("report-1", expect.objectContaining({ from: "2026-08-23T01:00:00.000Z", to: "2026-08-23T02:00:00.000Z" })));
    expect(URL.createObjectURL).toHaveBeenCalled();
  });
});
