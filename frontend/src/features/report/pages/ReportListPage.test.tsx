// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { deleteReport, listReports } from "../../../services/report.service";
import type { Report } from "../../../types/report";
import ReportListPage from "./ReportListPage";

vi.mock("../../../services/report.service", () => ({ deleteReport: vi.fn(), listReports: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet</span> }));

const mockedDelete = vi.mocked(deleteReport);
const mockedList = vi.mocked(listReports);
const reports: Report[] = [
  { id: "report-1", name: "Energy shift", description: "Operations", logger_id: "logger-1", logger_name: "Plant", timezone: "Asia/Bangkok", mode: "aggregate", bucket: "1h", column_count: 3, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" },
  { id: "report-2", name: "Raw values", description: null, logger_id: "logger-2", logger_name: "Utility", timezone: "UTC", mode: "raw", column_count: 2, created_at: "2026-08-23T00:00:00Z", updated_at: "2026-08-23T00:00:00Z" },
];

function Probe() { return <span data-testid="search">{useLocation().search}</span>; }
function renderPage(path = "/reports") { return render(<MemoryRouter initialEntries={[path]}><ReportListPage /><Probe /></MemoryRouter>); }

describe("ReportListPage", () => {
  beforeEach(() => { mockedDelete.mockReset(); mockedList.mockReset(); mockedList.mockResolvedValue({ data: reports, pagination: { page: 1, per_page: 20, total: 2, total_pages: 1 } }); vi.spyOn(window, "confirm").mockReturnValue(true); });
  afterEach(() => { cleanup(); vi.restoreAllMocks(); });

  it("renders source, modes, custom column totals, and navigation", async () => {
    renderPage();
    expect(await screen.findByText("Energy shift")).toBeInTheDocument();
    expect(screen.getByText("1h aggregated")).toBeInTheDocument();
    expect(screen.getByText("Raw batches")).toBeInTheDocument();
    expect(screen.getByText("5")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Reports" })).toHaveClass("active");
    expect(screen.getByRole("link", { name: "New Report" })).toHaveAttribute("href", "/reports/new");
  });

  it("uses URL-backed filters and confirms non-destructive deletion", async () => {
    mockedDelete.mockResolvedValue();
    renderPage("/reports?mode=aggregate&search=energy");
    await screen.findByText("Energy shift");
    expect(mockedList).toHaveBeenCalledWith({ search: "energy", mode: "aggregate", page: 1, per_page: 20 }, expect.any(AbortSignal));
    fireEvent.change(screen.getByRole("combobox", { name: "Report mode" }), { target: { value: "raw" } });
    await waitFor(() => expect(screen.getByTestId("search")).toHaveTextContent("mode=raw"));
    fireEvent.click(screen.getByRole("button", { name: "Delete Energy shift" }));
    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith("report-1"));
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining("history will not be removed"));
  });

  it("shows API error and retries", async () => {
    mockedList.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce({ data: [], pagination: { page: 1, per_page: 20, total: 0, total_pages: 0 } });
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Reports");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No Reports found")).toBeInTheDocument();
  });
});
