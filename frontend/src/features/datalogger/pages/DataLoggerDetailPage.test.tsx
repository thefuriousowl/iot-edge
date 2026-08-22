// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getDataLogger, getDataLoggerHistory } from "../../../services/datalogger.service";
import type { DataLogger, DataLoggerHistoryResponse, DataLoggerRawValue } from "../../../types/datalogger";
import DataLoggerDetailPage from "./DataLoggerDetailPage";

vi.mock("../../../services/datalogger.service", () => ({ getDataLogger: vi.fn(), getDataLoggerHistory: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedGetLogger = vi.mocked(getDataLogger);
const mockedGetHistory = vi.mocked(getDataLoggerHistory);

const logger: DataLogger = {
  id: "logger-1",
  name: "Plant history",
  description: "Synchronized process values",
  enabled: true,
  timezone: "Asia/Bangkok",
  mode: "interval",
  start_at: "2030-08-22T01:00:00Z",
  end_at: null,
  max_size_bytes: 100 * 1024 * 1024,
  config: { interval_seconds: 60 },
  tag_count: 2,
  storage: { row_count: 200, batch_count: 100, estimated_size_bytes: 80_000, average_row_bytes: 400, estimated_capacity_rows: 262_144, estimated_capacity_batches: 131_072, oldest_batch_at: "2026-08-22T10:00:00Z", newest_batch_at: "2026-08-22T11:40:00Z" },
  tags: [
    { id: "tag-1", name: "Power", type: "reading", data_type: "float64", enabled: true },
    { id: "tag-2", name: "Energy", type: "calculated", data_type: "float64", enabled: true },
  ],
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function raw(overrides: Partial<DataLoggerRawValue> = {}): DataLoggerRawValue {
  return {
    logger_id: logger.id,
    tag_id: "tag-1",
    batch_at: "2026-08-22T01:00:00Z",
    observed_at: "2026-08-22T00:59:59Z",
    data_type: "float64",
    value: 42.5,
    quality: "good",
    persisted_at: "2026-08-22T01:00:01Z",
    ...overrides,
  };
}

function history(data: DataLoggerRawValue[] = [raw(), raw({ tag_id: "tag-2", value: null, quality: "bad", error: "dependency unavailable" })], overrides: Partial<DataLoggerHistoryResponse> = {}): DataLoggerHistoryResponse {
  return {
    data,
    last_batch_at: "2026-08-22T01:00:00Z",
    pagination: { page: 1, per_page: 100, total: data.length, total_pages: data.length ? 1 : 0 },
    ...overrides,
  };
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location-search">{location.search}</span>;
}

function renderPage(path = "/data-loggers/logger-1") {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/data-loggers/:id" element={<DataLoggerDetailPage />} /></Routes><LocationProbe /></MemoryRouter>);
}

function currentParams(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId("location-search").textContent ?? "");
}

describe("DataLoggerDetailPage", () => {
  beforeEach(() => {
    mockedGetLogger.mockReset();
    mockedGetHistory.mockReset();
    mockedGetLogger.mockResolvedValue(logger);
    mockedGetHistory.mockResolvedValue(history());
  });

  afterEach(() => {
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders definition state, selected Tags, persistent values, and visible batch quality", async () => {
    renderPage();
    expect(screen.getByRole("status")).toHaveTextContent("Loading Data Logger history");
    expect(await screen.findByRole("heading", { name: "Plant history" })).toBeInTheDocument();
    expect(screen.getByText("scheduled")).toBeInTheDocument();
    expect(screen.getAllByText("Every 60 seconds")).toHaveLength(2);
    expect(screen.getAllByText("Power").length).toBeGreaterThan(0);
    expect(screen.getAllByText("Energy").length).toBeGreaterThan(0);
    expect(screen.getByText("42.5")).toBeInTheDocument();
    expect(screen.getByText("dependency unavailable")).toBeInTheDocument();
    expect(screen.getAllByText("partial")).toHaveLength(2);
    const storage = screen.getByRole("heading", { name: "Rolling storage retention" }).closest("section")!;
    expect(within(storage).getByRole("progressbar")).toHaveAttribute("aria-valuenow", "0");
    expect(within(storage).getByText("200")).toBeInTheDocument();
    expect(within(storage).getByText("262,144")).toBeInTheDocument();
    const summary = screen.getByRole("region", { name: "Visible history summary" });
    expect(within(summary).getByText(/visible batches/)).toHaveTextContent("1");
    expect(mockedGetHistory).toHaveBeenCalledWith("logger-1", { tag_id: undefined, from: undefined, to: undefined, page: 1, per_page: 100 }, expect.any(AbortSignal));
  });

  it("keeps Tag and timezone-aware range filters in the URL and API query", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant history" });
    fireEvent.change(screen.getByLabelText("Tag"), { target: { value: "tag-2" } });
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-22T08:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-22T09:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));

    await waitFor(() => expect(mockedGetHistory).toHaveBeenLastCalledWith("logger-1", {
      tag_id: "tag-2",
      from: "2026-08-22T01:00:00.000Z",
      to: "2026-08-22T02:00:00.000Z",
      page: 1,
      per_page: 100,
    }, expect.any(AbortSignal)));
    expect(currentParams().get("tag_id")).toBe("tag-2");
    expect(currentParams().get("from")).toBe("2026-08-22T01:00:00.000Z");
    expect(screen.getByText("3 active filters")).toBeInTheDocument();
  });

  it("rejects invalid local ranges before changing the query", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant history" });
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-22T10:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-22T09:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Apply filters" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("To must be after From");
    expect(mockedGetHistory).toHaveBeenCalledOnce();
    expect(currentParams().toString()).toBe("");
  });

  it("supports quick Tag filtering and history pagination", async () => {
    mockedGetHistory.mockResolvedValue(history([raw()], { pagination: { page: 1, per_page: 100, total: 201, total_pages: 3 } }));
    renderPage();
    await screen.findByRole("heading", { name: "Plant history" });
    const tagsCard = screen.getByRole("heading", { name: "Selected Tags" }).closest("section")!;
    fireEvent.click(within(tagsCard).getByRole("button", { name: /Power/ }));
    await waitFor(() => expect(currentParams().get("tag_id")).toBe("tag-1"));
    fireEvent.click(screen.getByRole("button", { name: "Next history page" }));
    await waitFor(() => expect(currentParams().get("page")).toBe("2"));
    expect(mockedGetHistory).toHaveBeenLastCalledWith("logger-1", expect.objectContaining({ tag_id: "tag-1", page: 2 }), expect.any(AbortSignal));
  });

  it("exports only the loaded page as CSV", async () => {
    const createObjectURL = vi.fn(() => "blob:history");
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    renderPage();
    await screen.findByRole("heading", { name: "Plant history" });
    fireEvent.click(screen.getByRole("button", { name: "Export page CSV" }));
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob));
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:history");
  });

  it("shows empty filtered history, retries failures, and aborts in-flight requests", async () => {
    mockedGetHistory.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(history([]));
    const rendered = renderPage("/data-loggers/logger-1?tag_id=tag-1");
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Data Logger history");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No matching history")).toBeInTheDocument();
    rendered.unmount();

    mockedGetLogger.mockReturnValue(new Promise(() => {}));
    mockedGetHistory.mockReturnValue(new Promise(() => {}));
    const pending = renderPage();
    await waitFor(() => expect(mockedGetHistory).toHaveBeenCalled());
    const signal = mockedGetHistory.mock.calls.at(-1)?.[2];
    pending.unmount();
    expect(signal?.aborted).toBe(true);
  });
});
