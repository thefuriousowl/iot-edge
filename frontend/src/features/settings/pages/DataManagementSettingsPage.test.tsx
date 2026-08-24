// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import {
  cleanupDataLoggerRetention,
  getDataLoggerRetention,
  getDataManagementOverview,
  listDataLoggers,
  previewDataLoggerRetention,
} from "../../../services/datalogger.service";
import { expectNoAxeViolations } from "../../../test/axe";
import type { DataLogger, DataManagementOverview, RetentionCleanupResult, RetentionPlan, RetentionStatus } from "../../../types/datalogger";
import DataManagementSettingsPage from "./DataManagementSettingsPage";

vi.mock("../../../services/datalogger.service", () => ({
  cleanupDataLoggerRetention: vi.fn(),
  getDataLoggerRetention: vi.fn(),
  getDataManagementOverview: vi.fn(),
  listDataLoggers: vi.fn(),
  previewDataLoggerRetention: vi.fn(),
}));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedCleanup = vi.mocked(cleanupDataLoggerRetention);
const mockedRetention = vi.mocked(getDataLoggerRetention);
const mockedOverview = vi.mocked(getDataManagementOverview);
const mockedList = vi.mocked(listDataLoggers);
const mockedPreview = vi.mocked(previewDataLoggerRetention);

const overview: DataManagementOverview = {
  evaluated_at: "2026-08-24T08:00:00Z",
  logger_count: 2,
  enabled_logger_count: 1,
  policy_logger_count: 1,
  logical_history: { row_count: 100, batch_count: 20, estimated_size_bytes: 4096, oldest_batch_at: "2026-08-23T08:00:00Z", newest_batch_at: "2026-08-24T08:00:00Z" },
  postgresql_physical_allocation: { raw_history_bytes: 8192, batch_accounting_bytes: 4096, total_bytes: 12288 },
};

const loggers: DataLogger[] = [
  { id: "logger-1", name: "Energy history", description: null, enabled: true, timezone: "Asia/Bangkok", mode: "interval", start_at: "2026-08-22T00:00:00Z", end_at: null, max_size_bytes: 100 * 1024 * 1024, max_age_seconds: 24 * 60 * 60, config: { interval_seconds: 60 }, tag_count: 3, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
  { id: "logger-2", name: "Unlimited history", description: null, enabled: false, timezone: "UTC", mode: "schedule", start_at: "2026-08-22T00:00:00Z", end_at: null, max_size_bytes: null, max_age_seconds: null, config: { unit: "day", every: 1, times: ["08:00"] }, tag_count: 1, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
];

const current = { row_count: 100, batch_count: 20, estimated_size_bytes: 4096, oldest_batch_at: "2026-08-23T08:00:00Z", newest_batch_at: "2026-08-24T08:00:00Z" };
const remove = { row_count: 25, batch_count: 5, estimated_size_bytes: 1024, oldest_batch_at: "2026-08-23T08:00:00Z", newest_batch_at: "2026-08-23T12:00:00Z" };
const retained = { row_count: 75, batch_count: 15, estimated_size_bytes: 3072, oldest_batch_at: "2026-08-23T13:00:00Z", newest_batch_at: "2026-08-24T08:00:00Z" };
const plan: RetentionPlan = { evaluated_at: "2026-08-24T08:00:00Z", cutoff_at: "2026-08-23T08:00:00Z", policy: { max_size_bytes: 100 * 1024 * 1024, max_age_seconds: 86400 }, current, remove, estimated_retained: retained };
const status: RetentionStatus = { plan, last_run: null };
const cleanupResult: RetentionCleanupResult = { started_at: "2026-08-24T08:00:00Z", completed_at: "2026-08-24T08:00:01Z", evaluated_at: "2026-08-24T08:00:00Z", cutoff_at: plan.cutoff_at, policy: plan.policy, deleted: remove, retained, remaining_removal: { ...remove, row_count: 0, batch_count: 0, estimated_size_bytes: 0 }, complete: true };

function renderPage() {
  return render(<MemoryRouter initialEntries={["/settings/data-management"]}><DataManagementSettingsPage /></MemoryRouter>);
}

describe("DataManagementSettingsPage", () => {
  beforeEach(() => {
    mockedCleanup.mockReset();
    mockedRetention.mockReset();
    mockedOverview.mockReset();
    mockedList.mockReset();
    mockedPreview.mockReset();
    mockedOverview.mockResolvedValue(overview);
    mockedList.mockResolvedValue({ data: loggers, pagination: { page: 1, per_page: 100, total: 2, total_pages: 1 } });
    mockedRetention.mockResolvedValue(status);
    mockedPreview.mockResolvedValue({ ...plan, evaluated_at: "2026-08-24T08:05:00Z" });
    mockedCleanup.mockResolvedValue(cleanupResult);
  });
  afterEach(() => cleanup());

  it("shows an accessible overview, physical allocation, and selected Logger retention", async () => {
    const { container } = renderPage();

    expect(screen.getByRole("status")).toHaveTextContent("Loading storage workspace");
    expect(await screen.findByText("2 Data Loggers")).toBeInTheDocument();
    expect(await screen.findByRole("heading", { name: "Retention workspace" })).toBeInTheDocument();
    expect(screen.getByText("PostgreSQL physical allocation")).toBeInTheDocument();
    expect(screen.getByText(/database files may not shrink immediately/i)).toBeInTheDocument();
    expect(screen.getByText("Eligible for removal").closest("article")).toHaveTextContent("25 rows");
    expect(screen.getByText("Estimated retained duration").closest("article")).toHaveTextContent("19 hours");
    expect(screen.getByRole("link", { name: /Edit policy/ })).toHaveAttribute("href", "/data-loggers/logger-1/edit");
    expect(screen.getByRole("link", { name: /Query history/ })).toHaveAttribute("href", "/data-loggers/logger-1/query");
    expect(screen.getByRole("link", { name: /Data Management/ })).toHaveClass("active");
    await expectNoAxeViolations(container);
  });

  it("loads every Logger page and switches the scoped status", async () => {
    mockedList.mockReset();
    mockedList.mockResolvedValueOnce({ data: [loggers[0]], pagination: { page: 1, per_page: 100, total: 2, total_pages: 2 } }).mockResolvedValueOnce({ data: [loggers[1]], pagination: { page: 2, per_page: 100, total: 2, total_pages: 2 } });
    mockedRetention.mockResolvedValueOnce(status).mockResolvedValueOnce({ ...status, plan: { ...plan, policy: { max_size_bytes: null, max_age_seconds: null }, remove: { ...remove, row_count: 0, batch_count: 0, estimated_size_bytes: 0 } } });
    renderPage();
    await screen.findByText("Energy history", { selector: "strong" });

    fireEvent.change(screen.getByLabelText("Data Logger"), { target: { value: "logger-2" } });
    expect(await screen.findByText("Unlimited history", { selector: "strong" })).toBeInTheDocument();
    expect(mockedList).toHaveBeenNthCalledWith(2, { page: 2, per_page: 100 }, expect.any(AbortSignal));
    expect(mockedRetention).toHaveBeenLastCalledWith("logger-2", expect.any(AbortSignal));
  });

  it("runs a read-only fresh preview", async () => {
    renderPage();
    await screen.findByText("Eligible for removal");
    fireEvent.click(screen.getByRole("button", { name: "Preview now" }));

    expect(await screen.findByText("Fresh preview")).toBeInTheDocument();
    expect(mockedPreview).toHaveBeenCalledWith("logger-1");
    expect(mockedCleanup).not.toHaveBeenCalled();
  });

  it("keeps a fast retention response when the overview refresh finishes later", async () => {
    let resolveOverview: ((value: DataManagementOverview) => void) | undefined;
    mockedOverview.mockResolvedValueOnce(overview).mockReturnValueOnce(new Promise((resolve) => { resolveOverview = resolve; }));
    renderPage();
    await screen.findByText("Eligible for removal");

    fireEvent.click(screen.getByRole("button", { name: "Refresh" }));
    await waitFor(() => expect(mockedRetention).toHaveBeenCalledTimes(2));
    resolveOverview?.(overview);

    expect(await screen.findByText("Eligible for removal")).toBeInTheDocument();
    expect(screen.getByRole("button", { name: "Review cleanup" })).toBeEnabled();
  });

  it("requires an accessible confirmation, valid limits, and explicit acknowledgment before cleanup", async () => {
    const { container } = renderPage();
    await screen.findByText("Eligible for removal");
    const reviewButton = screen.getByRole("button", { name: "Review cleanup" });
    reviewButton.focus();
    fireEvent.click(reviewButton);

    const deleteButton = screen.getByRole("button", { name: "Delete eligible batches" });
    expect(screen.getByRole("dialog")).toHaveTextContent("cannot be undone");
    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus());
    await expectNoAxeViolations(container);
    expect(deleteButton).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Maximum batches this run"), { target: { value: "10001" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /permanently deletes/ }));
    expect(deleteButton).toBeDisabled();
    fireEvent.change(screen.getByLabelText("Maximum batches this run"), { target: { value: "250" } });
    fireEvent.click(deleteButton);

    await waitFor(() => expect(mockedCleanup).toHaveBeenCalledWith("logger-1", { confirm: true, batch_limit: 250 }));
    await waitFor(() => expect(screen.getByText((_, element) => element?.classList.contains("settings-operation-success") === true && element.textContent === "Cleanup removed 5 batches and 25 rows.")).toBeInTheDocument());
  });

  it("closes cleanup with Escape and returns focus to its trigger", async () => {
    renderPage();
    await screen.findByText("Eligible for removal");
    const reviewButton = screen.getByRole("button", { name: "Review cleanup" });
    reviewButton.focus();
    fireEvent.click(reviewButton);

    await waitFor(() => expect(screen.getByRole("button", { name: "Cancel" })).toHaveFocus());
    fireEvent.keyDown(document, { key: "Escape" });

    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
    await waitFor(() => expect(reviewButton).toHaveFocus());
  });

  it("keeps Tab navigation inside the cleanup dialog", async () => {
    renderPage();
    await screen.findByText("Eligible for removal");
    fireEvent.click(screen.getByRole("button", { name: "Review cleanup" }));

    const cancelButton = screen.getByRole("button", { name: "Cancel" });
    const limitInput = screen.getByLabelText("Maximum batches this run");
    await waitFor(() => expect(cancelButton).toHaveFocus());
    fireEvent.keyDown(cancelButton, { key: "Tab" });
    expect(limitInput).toHaveFocus();
    fireEvent.keyDown(limitInput, { key: "Tab", shiftKey: true });
    expect(cancelButton).toHaveFocus();
  });

  it("uses singular labels for one retained batch and cleanup row", async () => {
    const singularMetrics = { ...remove, row_count: 1, batch_count: 1 };
    const singularResult = { ...cleanupResult, deleted: singularMetrics };
    mockedRetention.mockResolvedValue({
      plan: { ...plan, current: singularMetrics, remove: singularMetrics },
      last_run: { started_at: singularResult.started_at, completed_at: singularResult.completed_at, result: singularResult, error: null },
    });
    mockedCleanup.mockResolvedValue(singularResult);
    renderPage();

    expect((await screen.findAllByText("1 synchronized batch", { exact: false })).length).toBeGreaterThan(0);
    expect(screen.getByText(/1 batch removed/)).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Review cleanup" }));
    fireEvent.click(screen.getByRole("checkbox", { name: /permanently deletes/ }));
    fireEvent.click(screen.getByRole("button", { name: "Delete eligible batches" }));

    await waitFor(() => expect(screen.getByText((_, element) => element?.classList.contains("settings-operation-success") === true && element.textContent === "Cleanup removed 1 batch and 1 row.")).toBeInTheDocument());
  });

  it("keeps cleanup failures inside the confirmation dialog", async () => {
    mockedCleanup.mockRejectedValue({ isAxiosError: true, response: { data: { error: { message: "Cleanup is already running" } } } });
    renderPage();
    await screen.findByText("Eligible for removal");
    fireEvent.click(screen.getByRole("button", { name: "Review cleanup" }));
    fireEvent.click(screen.getByRole("checkbox", { name: /permanently deletes/ }));
    fireEvent.click(screen.getByRole("button", { name: "Delete eligible batches" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Cleanup is already running");
    expect(screen.getByRole("dialog")).toBeInTheDocument();
  });

  it("disables cleanup when no batches are eligible", async () => {
    mockedRetention.mockResolvedValue({ ...status, plan: { ...plan, remove: { ...remove, row_count: 0, batch_count: 0, estimated_size_bytes: 0 } } });
    renderPage();

    expect(await screen.findByRole("button", { name: "Review cleanup" })).toBeDisabled();
    expect(screen.queryByRole("dialog")).not.toBeInTheDocument();
  });

  it("shows safe empty, failure, and retry states", async () => {
    mockedOverview.mockResolvedValueOnce({ ...overview, logger_count: 0, enabled_logger_count: 0, policy_logger_count: 0 });
    mockedList.mockResolvedValueOnce({ data: [], pagination: { page: 1, per_page: 100, total: 0, total_pages: 0 } });
    const empty = renderPage();
    expect(await screen.findByText("No Data Loggers yet")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Create Data Logger" })).toHaveAttribute("href", "/data-loggers/new");
    empty.unmount();

    mockedOverview.mockRejectedValueOnce(new Error("database detail")).mockResolvedValueOnce(overview);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Data Management");
    expect(screen.getByRole("alert")).not.toHaveTextContent("database detail");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByText("2 Data Loggers")).toBeInTheDocument();
  });
});
