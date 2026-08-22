// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createTag, listTags, validateTagExpression } from "../../../services/tag.service";
import type { CalculatedTag, Tag, TagListResponse } from "../../../types/tag";
import CalculatedTagForm from "./CalculatedTagForm";

vi.mock("../../../services/tag.service", () => ({
  createTag: vi.fn(),
  listTags: vi.fn(),
  validateTagExpression: vi.fn(),
}));

const mockedCreateTag = vi.mocked(createTag);
const mockedListTags = vi.mocked(listTags);
const mockedValidate = vi.mocked(validateTagExpression);

const reading: Tag = {
  id: "10000000-0000-4000-8000-000000000001",
  datasource_id: "source-1",
  name: "Simulator Register 0",
  type: "reading",
  data_type: "uint16",
  description: null,
  enabled: true,
  config: { decoder: { type: "binary_numeric", config: { byte_offset: 0, byte_order: "big_endian", bit_offset: 0 } } },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const constant: Tag = {
  id: "20000000-0000-4000-8000-000000000002",
  datasource_id: null,
  name: "Nominal Voltage",
  type: "constant",
  data_type: "float64",
  description: null,
  enabled: true,
  config: { value: 230.5 },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function listResponse(data: Tag[] = [reading, constant]): TagListResponse {
  return { data, pagination: { page: 1, per_page: 100, total: data.length, total_pages: data.length ? 1 : 0 } };
}

function renderForm(overrides?: Partial<{ onCancel: () => void; onChangeType: () => void; onCreated: () => void }>) {
  const props = { onCancel: vi.fn(), onChangeType: vi.fn(), onCreated: vi.fn(), ...overrides };
  render(<CalculatedTagForm {...props} />);
  return props;
}

describe("CalculatedTagForm", () => {
  beforeEach(() => {
    mockedCreateTag.mockReset();
    mockedListTags.mockReset();
    mockedValidate.mockReset();
    mockedListTags.mockResolvedValue(listResponse());
    mockedValidate.mockResolvedValue({ valid: true, dependencies: [reading.id, constant.id] });
  });

  afterEach(cleanup);

  it("inserts immutable Tag references and creates an exact Calculated Tag", async () => {
    const onCreated = vi.fn();
    const expression = `\${${reading.id}} + \${${constant.id}}`;
    const created: CalculatedTag = { id: "calculated-1", datasource_id: null, name: "Adjusted voltage", type: "calculated", data_type: "float64", description: "Reading plus nominal", enabled: true, config: { expression, trigger: { tag_id: reading.id, mode: "on_sample" } }, created_at: "2026-08-22T00:00:00Z", updated_at: "2026-08-22T00:00:00Z" };
    mockedCreateTag.mockResolvedValue(created);
    renderForm({ onCreated });

    expect(await screen.findByRole("button", { name: "Insert Simulator Register 0" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Insert Simulator Register 0" }));
    expect(screen.getByLabelText(/Expression/)).toHaveValue(`\${${reading.id}}`);
    fireEvent.change(screen.getByLabelText(/Expression/), { target: { value: expression } });

    await waitFor(() => expect(mockedValidate).toHaveBeenLastCalledWith({ expression }, expect.any(AbortSignal)), { timeout: 1200 });
    expect(await screen.findByText("Valid expression · 2 dependencies")).toBeInTheDocument();
    expect(screen.getAllByText("Simulator Register 0")).toHaveLength(2);
    expect(screen.getAllByText("Nominal Voltage")).toHaveLength(2);

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: " Adjusted voltage " } });
    fireEvent.change(screen.getByRole("combobox", { name: /Trigger Tag/ }), { target: { value: reading.id } });
    fireEvent.change(screen.getByLabelText("Description"), { target: { value: " Reading plus nominal " } });
    fireEvent.click(screen.getByRole("button", { name: "Create Calculated Tag" }));

    await waitFor(() => expect(mockedCreateTag).toHaveBeenCalledWith({ name: "Adjusted voltage", type: "calculated", data_type: "float64", description: "Reading plus nominal", enabled: true, config: { expression, trigger: { tag_id: reading.id, mode: "on_sample" } } }));
    expect(onCreated).toHaveBeenCalledOnce();
    expect(mockedListTags).toHaveBeenCalledWith({ page: 1, per_page: 100 }, expect.any(AbortSignal));
  });

  it("aborts stale debounced validation and keeps only the latest result", async () => {
    mockedValidate.mockResolvedValue({ valid: true, dependencies: [] });
    renderForm();
    const editor = screen.getByLabelText(/Expression/);

    fireEvent.change(editor, { target: { value: "1 + 1" } });
    await waitFor(() => expect(mockedValidate).toHaveBeenCalledTimes(1), { timeout: 1200 });
    const firstSignal = mockedValidate.mock.calls[0][1];
    fireEvent.change(editor, { target: { value: "2 + 2" } });
    expect(firstSignal?.aborted).toBe(true);
    await waitFor(() => expect(mockedValidate).toHaveBeenCalledTimes(2), { timeout: 1200 });
    expect(mockedValidate).toHaveBeenLastCalledWith({ expression: "2 + 2" }, expect.any(AbortSignal));
  });

  it("requires a Trigger Tag and never exposes an external-read preview", async () => {
    const expression = `\${${reading.id}} + 1`;
    mockedValidate.mockResolvedValue({ valid: true, dependencies: [reading.id] });
    renderForm();

    fireEvent.change(screen.getByLabelText(/Expression/), { target: { value: expression } });
    await screen.findByText("Valid expression · 1 dependency", {}, { timeout: 1200 });
    expect(screen.getByRole("button", { name: "Create Calculated Tag" })).toBeDisabled();
    fireEvent.change(screen.getByRole("combobox", { name: /Trigger Tag/ }), { target: { value: reading.id } });
    expect(screen.getByRole("button", { name: "Create Calculated Tag" })).toBeEnabled();
    expect(screen.queryByRole("button", { name: "Preview value" })).not.toBeInTheDocument();
  });

  it("shows sanitized validation failures and retries the current expression", async () => {
    mockedValidate
      .mockRejectedValueOnce({ isAxiosError: true, response: { data: { error: { message: "invalid expression: expected value" } } } })
      .mockResolvedValueOnce({ valid: true, dependencies: [] });
    renderForm();

    fireEvent.change(screen.getByLabelText(/Expression/), { target: { value: "1 +" } });
    expect(await screen.findByRole("alert", {}, { timeout: 1200 })).toHaveTextContent("invalid expression: expected value");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("Valid expression · 0 dependencies", {}, { timeout: 1200 })).toBeInTheDocument();
    expect(mockedValidate).toHaveBeenCalledTimes(2);
  });

  it("recovers Tag palette loading and preserves navigation callbacks", async () => {
    mockedListTags.mockRejectedValueOnce(new Error("offline")).mockResolvedValueOnce(listResponse([]));
    const props = renderForm();

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to load Tags");
    fireEvent.click(screen.getByRole("button", { name: "Retry" }));
    expect(await screen.findByText("No Tags are available yet. Literal-only expressions still work.")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Change type" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(props.onChangeType).toHaveBeenCalledOnce();
    expect(props.onCancel).toHaveBeenCalledOnce();
  });
});
