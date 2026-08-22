// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { deleteDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import type { DataLogger, DataLoggerListResponse } from "../../../types/datalogger";
import DataLoggerListPage from "./DataLoggerListPage";

vi.mock("../../../services/datalogger.service", () => ({ deleteDataLogger: vi.fn(), listDataLoggers: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedDelete = vi.mocked(deleteDataLogger);
const mockedList = vi.mocked(listDataLoggers);
const loggers: DataLogger[] = [
  { id: "logger-1", name: "Fast history", description: "Plant values", enabled: true, timezone: "UTC", mode: "interval", start_at: "2030-01-01T00:00:00Z", end_at: null, max_size_bytes: null, config: { interval_seconds: 60 }, tag_count: 3, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
  { id: "logger-2", name: "Energy report", description: null, enabled: false, timezone: "Asia/Bangkok", mode: "schedule", start_at: "2030-01-01T00:00:00Z", end_at: null, max_size_bytes: null, config: { unit: "week", every: 1, weekdays: [1, 5], times: ["08:00"] }, tag_count: 2, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
];

function response(data: DataLogger[] = loggers, pagination: DataLoggerListResponse["pagination"] = { page: 1, per_page: 20, total: data.length, total_pages: data.length ? 1 : 0 }): DataLoggerListResponse {
  return { data, pagination };
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location-search">{location.search}</span>;
}

function renderPage(path = "/data-loggers") {
  return render(<MemoryRouter initialEntries={[path]}><DataLoggerListPage /><LocationProbe /></MemoryRouter>);
}

function params(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId("location-search").textContent ?? "");
}

describe("DataLoggerListPage", () => {
  beforeEach(() => {
    mockedDelete.mockReset();
    mockedList.mockReset();
    vi.spyOn(window, "confirm").mockReturnValue(true);
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("shows loading, summary, cadence, navigation, and definitions", async () => {
    let resolveList: ((value: DataLoggerListResponse) => void) | undefined;
    mockedList.mockReturnValue(new Promise((resolve) => { resolveList = resolve; }));
    renderPage();
    expect(screen.getByRole("status")).toHaveTextContent("Loading Data Loggers");
    resolveList?.(response());

    expect(await screen.findByText("Fast history")).toBeInTheDocument();
    expect(screen.getByText("Energy report")).toBeInTheDocument();
    expect(screen.getByText("Every 60 seconds")).toBeInTheDocument();
    expect(screen.getByText("Every 1 week · Mon, Fri · 08:00")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Data Loggers" })).toHaveClass("active");
    expect(screen.getByRole("link", { name: "New Data Logger" })).toHaveAttribute("href", "/data-loggers/new");
    expect(screen.getByRole("link", { name: "Fast history" })).toHaveAttribute("href", "/data-loggers/logger-1");
    expect(screen.getByRole("link", { name: "Edit Fast history" })).toHaveAttribute("href", "/data-loggers/logger-1/edit");
    const summary = screen.getByRole("region", { name: "Data Logger summary" });
    expect(within(summary).getByText("2")).toBeInTheDocument();
  });

  it("hydrates and changes URL-backed server filters", async () => {
    mockedList.mockResolvedValue(response());
    renderPage("/data-loggers?mode=schedule&enabled=false&search=energy&page=2");
    await screen.findByText("Energy report");
    expect(mockedList).toHaveBeenCalledWith({ mode: "schedule", enabled: false, search: "energy", page: 2, per_page: 20 }, expect.any(AbortSignal));

    fireEvent.change(screen.getByRole("combobox", { name: "Logger mode" }), { target: { value: "interval" } });
    await waitFor(() => expect(mockedList).toHaveBeenLastCalledWith(expect.objectContaining({ mode: "interval", page: 1 }), expect.any(AbortSignal)));
    expect(params().get("mode")).toBe("interval");
    expect(params().has("page")).toBe(false);

    fireEvent.change(screen.getByRole("searchbox", { name: "Search Data Loggers" }), { target: { value: " fast " } });
    fireEvent.click(screen.getByRole("button", { name: "Search" }));
    await waitFor(() => expect(params().get("search")).toBe("fast"));
  });

  it("confirms destructive deletion and refreshes the list", async () => {
    mockedList.mockResolvedValue(response());
    mockedDelete.mockResolvedValue();
    renderPage();
    await screen.findByText("Fast history");
    fireEvent.click(screen.getByRole("button", { name: "Delete Fast history" }));

    await waitFor(() => expect(mockedDelete).toHaveBeenCalledWith("logger-1"));
    await waitFor(() => expect(mockedList).toHaveBeenCalledTimes(2));
    expect(window.confirm).toHaveBeenCalledWith(expect.stringContaining("raw history"));
  });

  it("handles API errors, retries, and distinguishes filtered empty results", async () => {
    mockedList.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(response([]));
    renderPage("/data-loggers?search=missing");
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Data Loggers");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No matching Data Loggers")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Clear filters" }));
    await waitFor(() => expect(params().toString()).toBe(""));
  });

  it("aborts its in-flight request on unmount", async () => {
    mockedList.mockReturnValue(new Promise(() => {}));
    const rendered = renderPage();
    await waitFor(() => expect(mockedList).toHaveBeenCalledOnce());
    const signal = mockedList.mock.calls[0][1];
    rendered.unmount();
    expect(signal?.aborted).toBe(true);
  });
});
