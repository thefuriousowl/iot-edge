// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import { createReport, getReport, updateReport } from "../../../services/report.service";
import type { DataLogger } from "../../../types/datalogger";
import type { Report } from "../../../types/report";
import ReportBuilderPage from "./ReportBuilderPage";

vi.mock("../../../services/datalogger.service", () => ({ getDataLogger: vi.fn(), listDataLoggers: vi.fn() }));
vi.mock("../../../services/report.service", () => ({ createReport: vi.fn(), getReport: vi.fn(), updateReport: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));

const mockedGetLogger = vi.mocked(getDataLogger);
const mockedListLoggers = vi.mocked(listDataLoggers);
const mockedCreate = vi.mocked(createReport);
const mockedGetReport = vi.mocked(getReport);
const mockedUpdate = vi.mocked(updateReport);
const logger: DataLogger = { id: "logger-1", name: "Plant history", description: null, enabled: true, timezone: "UTC", mode: "interval", start_at: "2026-08-23T00:00:00Z", end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: 2, tags: [{ id: "tag-power", name: "Power", type: "reading", data_type: "float64", enabled: true }, { id: "tag-running", name: "Running", type: "reading", data_type: "bool", enabled: true }], created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" };
const existing: Report = { id: "report-1", name: "Existing", description: "Saved", logger_id: logger.id, logger_name: logger.name, timezone: "UTC", mode: "aggregate", bucket: "5m", column_count: 1, columns: [{ tag_id: "tag-power", position: 0, name: "Demand kW", aggregate: "max", tag_name: "Power", tag_type: "reading", data_type: "float64" }], created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" };

function renderBuilder(path = "/reports/new") { return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/reports/new" element={<ReportBuilderPage />} /><Route path="/reports/:id/edit" element={<ReportBuilderPage />} /><Route path="/reports/:id" element={<h1>Saved Report</h1>} /></Routes></MemoryRouter>); }

describe("ReportBuilderPage", () => {
  beforeEach(() => {
    mockedGetLogger.mockReset(); mockedListLoggers.mockReset(); mockedCreate.mockReset(); mockedGetReport.mockReset(); mockedUpdate.mockReset();
    mockedListLoggers.mockResolvedValue({ data: [logger], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } });
    mockedGetLogger.mockResolvedValue(logger); mockedCreate.mockResolvedValue(existing); mockedUpdate.mockResolvedValue(existing);
  });
  afterEach(cleanup);

  it("creates ordered custom columns with per-column aggregation", async () => {
    renderBuilder();
    expect(await screen.findByRole("heading", { name: "New Report" })).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Shift summary" } });
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "Asia/Bangkok" } });
    fireEvent.change(screen.getByLabelText("Mode"), { target: { value: "aggregate" } });
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "15m" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Power/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Running/ }));
    fireEvent.change(screen.getByLabelText("Column name for Power"), { target: { value: "Demand kW" } });
    fireEvent.change(screen.getByLabelText("Aggregation for Power"), { target: { value: "max" } });
    expect(screen.getByLabelText("Aggregation for Running")).not.toHaveTextContent("AVG");
    fireEvent.click(screen.getByRole("button", { name: "Move Running up" }));
    fireEvent.click(screen.getByRole("button", { name: "Save Report" }));
    await waitFor(() => expect(mockedCreate).toHaveBeenCalledWith({ name: "Shift summary", description: null, logger_id: "logger-1", timezone: "Asia/Bangkok", mode: "aggregate", bucket: "15m", columns: [{ tag_id: "tag-running", name: "Running", aggregate: "count" }, { tag_id: "tag-power", name: "Demand kW", aggregate: "max" }] }));
    expect(await screen.findByRole("heading", { name: "Saved Report" })).toBeInTheDocument();
  });

  it("blocks duplicate aliases before submission", async () => {
    renderBuilder(); await screen.findByRole("heading", { name: "New Report" });
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Invalid" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Power/ }));
    fireEvent.click(screen.getByRole("checkbox", { name: /Running/ }));
    fireEvent.change(screen.getByLabelText("Column name for Power"), { target: { value: "Value" } });
    fireEvent.change(screen.getByLabelText("Column name for Running"), { target: { value: " value " } });
    fireEvent.click(screen.getByRole("button", { name: "Save Report" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("must be unique");
    expect(mockedCreate).not.toHaveBeenCalled();
  });

  it("hydrates and updates an existing definition", async () => {
    mockedGetReport.mockResolvedValue(existing);
    renderBuilder("/reports/report-1/edit");
    expect(await screen.findByDisplayValue("Existing")).toBeInTheDocument();
    expect(screen.getByDisplayValue("Demand kW")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Updated" } });
    fireEvent.change(screen.getByLabelText("Mode"), { target: { value: "raw" } });
    fireEvent.click(screen.getByRole("button", { name: "Save Report" }));
    await waitFor(() => expect(mockedUpdate).toHaveBeenCalledWith("report-1", expect.objectContaining({ name: "Updated", mode: "raw", columns: [{ tag_id: "tag-power", name: "Demand kW", aggregate: undefined }] })));
  });
});
