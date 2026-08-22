// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getDataLogger, queryDataLogger } from "../../../services/datalogger.service";
import type { DataLogger, DataLoggerQueryResponse } from "../../../types/datalogger";
import DataLoggerQueryPage from "./DataLoggerQueryPage";

vi.mock("../../../services/datalogger.service", () => ({ getDataLogger: vi.fn(), queryDataLogger: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedGetLogger = vi.mocked(getDataLogger);
const mockedQueryLogger = vi.mocked(queryDataLogger);

const logger: DataLogger = {
  id: "logger-1",
  name: "Plant historian",
  description: "Synchronized process values",
  enabled: true,
  timezone: "Asia/Bangkok",
  mode: "interval",
  start_at: "2026-08-22T01:00:00Z",
  end_at: null,
  max_size_bytes: null,
  config: { interval_seconds: 60 },
  tag_count: 3,
  tags: [
    { id: "tag-1", name: "Power", type: "reading", data_type: "float64", enabled: true },
    { id: "tag-2", name: "Running", type: "reading", data_type: "bool", enabled: true },
    { id: "tag-3", name: "COP", type: "calculated", data_type: "float64", enabled: true },
  ],
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function rawResult(overrides: Partial<DataLoggerQueryResponse> = {}): DataLoggerQueryResponse {
  return {
    data: [{
      at: "2026-08-22T01:00:00Z",
      values: {
        "tag-1": { tag_id: "tag-1", data_type: "float64", value: 42.5, quality: "good", observed_at: "2026-08-22T00:59:59Z" },
        "tag-2": { tag_id: "tag-2", data_type: "bool", value: null, quality: "bad", error: "illegal data address", observed_at: "2026-08-22T00:59:59Z" },
      },
    }],
    mode: "raw",
    pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 },
    ...overrides,
  };
}

function aggregateResult(): DataLoggerQueryResponse {
  return {
    data: [{
      at: "2026-08-22T01:00:00Z",
      values: {
        "tag-1": { tag_id: "tag-1", data_type: "float64", value: 18.5, good_count: 2, bad_count: 1, total_count: 3, error: "timeout", supported: true },
        "tag-2": { tag_id: "tag-2", data_type: "bool", value: null, good_count: 3, total_count: 3, supported: false },
      },
    }],
    mode: "aggregate",
    bucket: "5m",
    aggregate: "avg",
    pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 },
  };
}

function LocationProbe() {
  const location = useLocation();
  return <span data-testid="location-search">{location.search}</span>;
}

function renderPage(path = "/data-loggers/logger-1/query?tag_ids=tag-1%2Ctag-2&from=2026-08-22T00%3A00%3A00.000Z&to=2026-08-22T02%3A00%3A00.000Z") {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/data-loggers/:id/query" element={<DataLoggerQueryPage />} /></Routes><LocationProbe /></MemoryRouter>);
}

function currentParams(): URLSearchParams {
  return new URLSearchParams(screen.getByTestId("location-search").textContent ?? "");
}

describe("DataLoggerQueryPage", () => {
  beforeEach(() => {
    mockedGetLogger.mockReset();
    mockedQueryLogger.mockReset();
    mockedGetLogger.mockResolvedValue(logger);
    mockedQueryLogger.mockResolvedValue(rawResult());
  });

  afterEach(() => {
    vi.useRealTimers();
    cleanup();
    vi.restoreAllMocks();
  });

  it("renders raw batches as a wide Tag table with explicit errors", async () => {
    renderPage();
    expect(screen.getByRole("status")).toHaveTextContent("Querying Data Logger values");
    expect(await screen.findByRole("heading", { name: "Plant historian" })).toBeInTheDocument();
    const table = screen.getByRole("table");
    expect(within(table).getByRole("columnheader", { name: /Power/ })).toBeInTheDocument();
    expect(within(table).getByRole("columnheader", { name: /Running/ })).toBeInTheDocument();
    expect(within(table).queryByRole("columnheader", { name: /COP/ })).not.toBeInTheDocument();
    expect(within(table).getByText("42.5")).toBeInTheDocument();
    expect(within(table).getByText("illegal data address")).toBeInTheDocument();
    expect(mockedQueryLogger).toHaveBeenCalledWith("logger-1", {
      mode: "raw",
      tag_ids: "tag-1,tag-2",
      from: "2026-08-22T00:00:00.000Z",
      to: "2026-08-22T02:00:00.000Z",
      bucket: undefined,
      aggregate: undefined,
      page: 1,
      per_page: 100,
    }, expect.any(AbortSignal));
  });

  it("rounds the implicit upper boundary up so rerunning keeps the current minute", async () => {
    vi.useFakeTimers();
    vi.setSystemTime(new Date("2026-08-22T11:24:37.000Z"));
    renderPage("/data-loggers/logger-1/query");
    await vi.waitFor(() => expect(mockedQueryLogger).toHaveBeenCalled());
    expect(mockedQueryLogger).toHaveBeenCalledWith("logger-1", expect.objectContaining({
      from: "2026-08-21T11:25:00.000Z",
      to: "2026-08-22T11:25:00.000Z",
    }), expect.any(AbortSignal));
  });

  it("renders aggregate quality and unsupported boolean functions", async () => {
    mockedQueryLogger.mockResolvedValue(aggregateResult());
    renderPage("/data-loggers/logger-1/query?mode=aggregate&bucket=5m&aggregate=avg&tag_ids=tag-1%2Ctag-2&from=2026-08-22T00%3A00%3A00.000Z&to=2026-08-22T02%3A00%3A00.000Z");
    await screen.findByRole("heading", { name: "Plant historian" });
    expect(screen.getByRole("heading", { name: "AVG by 5m bucket" })).toBeInTheDocument();
    expect(screen.getByText("18.5")).toBeInTheDocument();
    expect(screen.getByText("2/3 good · 1 bad")).toBeInTheDocument();
    expect(screen.getByText("timeout")).toBeInTheDocument();
    expect(screen.getByText("Unavailable")).toBeInTheDocument();
    expect(screen.getByText("avg is not supported for bool")).toBeInTheDocument();
    expect(screen.getByText(/SUM adds stored samples/)).toBeInTheDocument();
  });

  it("applies timezone-aware range, Tag, bucket, and function controls to the URL", async () => {
    mockedQueryLogger.mockResolvedValue(aggregateResult());
    renderPage();
    await screen.findByRole("heading", { name: "Plant historian" });
    fireEvent.click(screen.getByRole("button", { name: /Aggregated/ }));
    await waitFor(() => expect(currentParams().get("mode")).toBe("aggregate"));
    await waitFor(() => expect(mockedQueryLogger).toHaveBeenLastCalledWith("logger-1", expect.objectContaining({ mode: "aggregate", bucket: "1h", aggregate: "avg" }), expect.any(AbortSignal)));

    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-22T08:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-22T09:00" } });
    fireEvent.change(screen.getByLabelText("Bucket"), { target: { value: "15m" } });
    fireEvent.change(screen.getByLabelText("Function"), { target: { value: "max" } });
    fireEvent.click(screen.getByLabelText(/Running/));
    fireEvent.click(screen.getByLabelText(/COP/));
    fireEvent.click(screen.getByRole("button", { name: "Run query" }));

    await waitFor(() => expect(currentParams().get("tag_ids")).toBe("tag-1,tag-3"));
    expect(currentParams().get("from")).toBe("2026-08-22T01:00:00.000Z");
    expect(currentParams().get("to")).toBe("2026-08-22T02:00:00.000Z");
    expect(currentParams().get("bucket")).toBe("15m");
    expect(currentParams().get("aggregate")).toBe("max");
    fireEvent.click(screen.getByRole("button", { name: "Raw batches" }));
    await waitFor(() => expect(currentParams().get("mode")).toBeNull());
    expect(currentParams().get("bucket")).toBeNull();
    expect(currentParams().get("aggregate")).toBeNull();
  });

  it("rejects empty Tag selection and invalid ranges before querying", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Plant historian" });
    fireEvent.click(screen.getByRole("button", { name: "Clear" }));
    fireEvent.click(screen.getByRole("button", { name: "Run query" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Select at least one Tag");
    expect(mockedQueryLogger).toHaveBeenCalledOnce();

    fireEvent.click(screen.getByRole("button", { name: "Select all" }));
    fireEvent.change(screen.getByLabelText(/From/), { target: { value: "2026-08-22T10:00" } });
    fireEvent.change(screen.getByLabelText(/To/), { target: { value: "2026-08-22T09:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Run query" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("To must be after From");
    expect(mockedQueryLogger).toHaveBeenCalledOnce();
  });

  it("paginates, exports the current page, retries failures, and aborts pending queries", async () => {
    mockedQueryLogger.mockResolvedValue(rawResult({ pagination: { page: 1, per_page: 100, total: 201, total_pages: 3 } }));
    const createObjectURL = vi.fn(() => "blob:query");
    const revokeObjectURL = vi.fn();
    Object.defineProperty(URL, "createObjectURL", { configurable: true, value: createObjectURL });
    Object.defineProperty(URL, "revokeObjectURL", { configurable: true, value: revokeObjectURL });
    const click = vi.spyOn(HTMLAnchorElement.prototype, "click").mockImplementation(() => undefined);
    renderPage();
    await screen.findByRole("heading", { name: "Plant historian" });
    fireEvent.click(screen.getByRole("button", { name: "Export page CSV" }));
    expect(createObjectURL).toHaveBeenCalledWith(expect.any(Blob));
    expect(click).toHaveBeenCalledOnce();
    expect(revokeObjectURL).toHaveBeenCalledWith("blob:query");
    fireEvent.click(screen.getByRole("button", { name: "Next query page" }));
    await waitFor(() => expect(currentParams().get("page")).toBe("2"));

    cleanup();
    mockedQueryLogger.mockRejectedValueOnce(new Error("offline")).mockResolvedValue(rawResult({ data: [] }));
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to query Data Logger values");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("No values in this range")).toBeInTheDocument();

    cleanup();
    mockedGetLogger.mockReturnValue(new Promise(() => {}));
    mockedQueryLogger.mockReturnValue(new Promise(() => {}));
    const pending = renderPage();
    await waitFor(() => expect(mockedQueryLogger).toHaveBeenCalled());
    const signal = mockedQueryLogger.mock.calls.at(-1)?.[2];
    pending.unmount();
    expect(signal?.aborted).toBe(true);
  });
});
