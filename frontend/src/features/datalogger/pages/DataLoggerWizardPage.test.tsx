// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createDataLogger, getDataLogger, updateDataLogger } from "../../../services/datalogger.service";
import { listTags } from "../../../services/tag.service";
import type { DataLogger } from "../../../types/datalogger";
import type { Tag } from "../../../types/tag";
import DataLoggerWizardPage from "./DataLoggerWizardPage";

vi.mock("../../../services/datalogger.service", () => ({ createDataLogger: vi.fn(), getDataLogger: vi.fn(), updateDataLogger: vi.fn() }));
vi.mock("../../../services/tag.service", () => ({ listTags: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedCreate = vi.mocked(createDataLogger);
const mockedGet = vi.mocked(getDataLogger);
const mockedUpdate = vi.mocked(updateDataLogger);
const mockedListTags = vi.mocked(listTags);

const tags: Tag[] = [
  { id: "tag-1", datasource_id: "source-1", name: "Line voltage", type: "reading", data_type: "float64", description: null, enabled: true, config: { decoder: { type: "binary_numeric", config: { byte_offset: 0, byte_order: "big_endian", bit_offset: 0 } } }, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
  { id: "tag-2", datasource_id: null, name: "Nominal voltage", type: "constant", data_type: "float64", description: null, enabled: false, config: { value: 230 }, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" },
];

const existing: DataLogger = {
  id: "logger-1",
  name: "Existing history",
  description: "Original",
  enabled: false,
  timezone: "UTC",
  mode: "schedule",
  start_at: "2026-08-24T08:00:00Z",
  end_at: null,
  max_size_bytes: null,
  max_age_seconds: 14 * 24 * 60 * 60,
  config: { unit: "week", every: 2, weekdays: [1, 5], times: ["08:00", "17:00"] },
  tag_count: 1,
  tags: [{ id: "tag-1", name: "Line voltage", type: "reading", data_type: "float64", enabled: true }],
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function renderWizard(path = "/data-loggers/new") {
  return render(<MemoryRouter initialEntries={[path]}><Routes><Route path="/data-loggers" element={<h1>Logger list</h1>} /><Route path="/data-loggers/new" element={<DataLoggerWizardPage />} /><Route path="/data-loggers/:id/edit" element={<DataLoggerWizardPage />} /></Routes></MemoryRouter>);
}

async function continueToTags(name = "Plant history") {
  fireEvent.change(screen.getByLabelText("Name"), { target: { value: name } });
  fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
  expect(await screen.findByRole("heading", { name: "Select Tags" })).toBeInTheDocument();
}

async function selectTagAndContinue() {
  fireEvent.click(await screen.findByRole("checkbox", { name: /Line voltage/ }));
  fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
  expect(await screen.findByRole("heading", { name: "Capture schedule" })).toBeInTheDocument();
}

describe("DataLoggerWizardPage", () => {
  beforeEach(() => {
    mockedCreate.mockReset();
    mockedGet.mockReset();
    mockedUpdate.mockReset();
    mockedListTags.mockReset();
    mockedListTags.mockResolvedValue({ data: tags, pagination: { page: 1, per_page: 100, total: tags.length, total_pages: 1 } });
    mockedCreate.mockResolvedValue(existing);
    mockedUpdate.mockResolvedValue(existing);
  });

  afterEach(cleanup);

  it("creates an interval logger with a synchronized Tag snapshot payload", async () => {
    renderWizard();
    await continueToTags();
    await selectTagAndContinue();
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "Asia/Bangkok" } });
    fireEvent.change(screen.getByLabelText("Start date & time"), { target: { value: "2026-08-22T08:00" } });
    fireEvent.change(screen.getByLabelText("Capture every (seconds)"), { target: { value: "15" } });
    expect(screen.getByRole("region", { name: "Rolling retention policy" })).toHaveTextContent("273,066 complete batches");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));

    expect(await screen.findByRole("heading", { name: "Review and save" })).toBeInTheDocument();
    expect(screen.getByText("Latest Tag snapshot → raw history")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Create Data Logger" }));

    await waitFor(() => expect(mockedCreate).toHaveBeenCalledWith({
      name: "Plant history",
      description: null,
      enabled: true,
      timezone: "Asia/Bangkok",
      mode: "interval",
      start_at: "2026-08-22T01:00:00.000Z",
      end_at: null,
      max_size_bytes: 100 * 1024 * 1024,
      max_age_seconds: null,
      config: { interval_seconds: 15 },
      tag_ids: ["tag-1"],
    }));
    expect(await screen.findByRole("heading", { name: "Logger list" })).toBeInTheDocument();
  });

  it("creates a weekly calendar definition with sorted times and weekdays", async () => {
    renderWizard();
    await continueToTags("Weekly energy");
    await selectTagAndContinue();
    fireEvent.click(screen.getByRole("radio", { name: /Calendar schedule/ }));
    fireEvent.change(screen.getByLabelText("Timezone"), { target: { value: "UTC" } });
    fireEvent.change(screen.getByLabelText("Start date & time"), { target: { value: "2026-08-24T08:00" } });
    fireEvent.change(screen.getByLabelText("Calendar unit"), { target: { value: "week" } });
    fireEvent.click(screen.getByRole("checkbox", { name: "Sun" }));
    fireEvent.change(screen.getByLabelText("Schedule time 1"), { target: { value: "17:00" } });
    fireEvent.click(screen.getByRole("button", { name: "Add time" }));
    fireEvent.change(screen.getByLabelText("Schedule time 2"), { target: { value: "08:00" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await screen.findByRole("heading", { name: "Review and save" });
    fireEvent.click(screen.getByRole("button", { name: "Create Data Logger" }));

    await waitFor(() => expect(mockedCreate).toHaveBeenCalledWith(expect.objectContaining({
      name: "Weekly energy",
      mode: "schedule",
      config: { unit: "week", every: 1, times: ["08:00", "17:00"], weekdays: [1, 2, 3, 4, 5, 7] },
    })));
  });

  it("requires at least one Tag before scheduling", async () => {
    renderWizard();
    await continueToTags();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Select at least one Tag");
    expect(screen.getByRole("heading", { name: "Select Tags" })).toBeInTheDocument();
  });

  it("hydrates an existing definition and submits a full update", async () => {
    mockedGet.mockResolvedValue(existing);
    renderWizard("/data-loggers/logger-1/edit");
    expect(await screen.findByDisplayValue("Existing history")).toBeInTheDocument();
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Updated history" } });
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByText("1 selected")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    expect(await screen.findByLabelText("Repeat every")).toHaveValue(2);
    expect(screen.getByText("Age retention · keep approximately 14 days")).toBeInTheDocument();
    expect(screen.getByLabelText("Maximum age")).toHaveValue(2);
    expect(screen.getByLabelText("Unit")).toHaveValue("week");
    fireEvent.click(screen.getByRole("button", { name: /Continue/ }));
    await screen.findByRole("heading", { name: "Review and save" });
    fireEvent.click(screen.getByRole("button", { name: "Save changes" }));

    await waitFor(() => expect(mockedUpdate).toHaveBeenCalledWith("logger-1", expect.objectContaining({
      name: "Updated history",
      enabled: false,
      mode: "schedule",
      max_size_bytes: null,
      max_age_seconds: 14 * 24 * 60 * 60,
      config: { unit: "week", every: 2, times: ["08:00", "17:00"], weekdays: [1, 5] },
      tag_ids: ["tag-1"],
    })));
  });
});
