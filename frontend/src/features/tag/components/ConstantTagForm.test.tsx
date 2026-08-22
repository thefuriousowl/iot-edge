// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { createTag, previewTag } from "../../../services/tag.service";
import type { ConstantTag, TagPreviewResult } from "../../../types/tag";
import ConstantTagForm from "./ConstantTagForm";

vi.mock("../../../services/tag.service", () => ({
  createTag: vi.fn(),
  previewTag: vi.fn(),
}));

const mockedCreateTag = vi.mocked(createTag);
const mockedPreviewTag = vi.mocked(previewTag);

const createdTag: ConstantTag = {
  id: "constant-1",
  datasource_id: null,
  name: "nominal_voltage",
  type: "constant",
  data_type: "float64",
  description: "Nominal line voltage",
  enabled: true,
  config: { value: 230.5 },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

const previewResult: TagPreviewResult = {
  tag_id: "00000000-0000-0000-0000-000000000000",
  observed_at: "2026-08-22T04:00:00Z",
  quality: "good",
  data_type: "float64",
  value: 230.5,
};

function renderForm(overrides?: Partial<{ onCancel: () => void; onChangeType: () => void; onCreated: () => void }>) {
  const props = {
    onCancel: vi.fn(),
    onChangeType: vi.fn(),
    onCreated: vi.fn(),
    ...overrides,
  };
  render(<ConstantTagForm {...props} />);
  return props;
}

describe("ConstantTagForm", () => {
  beforeEach(() => {
    mockedCreateTag.mockReset();
    mockedPreviewTag.mockReset();
  });

  afterEach(cleanup);

  it("creates an exact typed Constant Tag and preserves enabled semantics", async () => {
    const onCreated = vi.fn();
    mockedCreateTag.mockResolvedValue({ ...createdTag, data_type: "uint32", config: { value: 4_294_967_295 }, enabled: false });
    renderForm({ onCreated });

    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: " max_counter " } });
    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "uint32" } });
    fireEvent.change(screen.getByLabelText(/Description/), { target: { value: " Maximum counter " } });
    fireEvent.change(screen.getByLabelText(/Value/), { target: { value: "4294967295" } });
    fireEvent.click(screen.getByRole("checkbox", { name: /Enabled/ }));
    fireEvent.click(screen.getByRole("button", { name: "Create Constant Tag" }));

    await waitFor(() => expect(mockedCreateTag).toHaveBeenCalledWith({
      name: "max_counter",
      type: "constant",
      data_type: "uint32",
      description: "Maximum counter",
      enabled: false,
      config: { value: 4_294_967_295 },
    }));
    expect(onCreated).toHaveBeenCalledOnce();
  });

  it("switches between backend-aligned numeric ranges and boolean values", () => {
    renderForm();
    const dataType = screen.getByLabelText(/Data type/);

    fireEvent.change(dataType, { target: { value: "int16" } });
    expect(screen.getByLabelText(/Value/)).toHaveAttribute("min", "-32768");
    expect(screen.getByLabelText(/Value/)).toHaveAttribute("max", "32767");
    expect(screen.getByLabelText(/Value/)).toHaveAttribute("step", "1");

    fireEvent.change(dataType, { target: { value: "float32" } });
    expect(screen.getByLabelText(/Value/)).toHaveAttribute("min", "-3.4028235e38");
    expect(screen.getByLabelText(/Value/)).toHaveAttribute("step", "any");

    fireEvent.change(dataType, { target: { value: "bool" } });
    expect(screen.getByLabelText(/Value/).tagName).toBe("SELECT");
    expect(screen.getByLabelText(/Value/)).toHaveValue("false");
    expect(screen.getByText("Boolean true or false")).toBeInTheDocument();
  });

  it("previews boolean and numeric values without requiring a Tag name", async () => {
    mockedPreviewTag
      .mockResolvedValueOnce(previewResult)
      .mockResolvedValueOnce({ ...previewResult, data_type: "bool", value: true });
    renderForm();

    fireEvent.change(screen.getByLabelText(/Value/), { target: { value: "230.5" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    await waitFor(() => expect(mockedPreviewTag).toHaveBeenLastCalledWith({ type: "constant", data_type: "float64", config: { value: 230.5 } }));
    expect(await screen.findByText("230.5")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "bool" } });
    expect(screen.queryByLabelText("Tag preview result")).not.toBeInTheDocument();
    fireEvent.change(screen.getByLabelText(/Value/), { target: { value: "true" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    await waitFor(() => expect(mockedPreviewTag).toHaveBeenLastCalledWith({ type: "constant", data_type: "bool", config: { value: true } }));
    const previewOutput = await screen.findByLabelText("Tag preview result");
    expect(previewOutput).toHaveTextContent("true");
    expect(previewOutput).toHaveTextContent("bool · good");
    expect(mockedCreateTag).not.toHaveBeenCalled();
  });

  it("clears stale results and blocks out-of-range previews", async () => {
    mockedPreviewTag.mockResolvedValue(previewResult);
    renderForm();
    fireEvent.change(screen.getByLabelText(/Value/), { target: { value: "1" } });
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(await screen.findByLabelText("Tag preview result")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/Data type/), { target: { value: "uint16" } });
    fireEvent.change(screen.getByLabelText(/Value/), { target: { value: "70000" } });
    expect(screen.queryByLabelText("Tag preview result")).not.toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(mockedPreviewTag).toHaveBeenCalledOnce();
  });

  it("shows sanitized API failures and keeps navigation actions available", async () => {
    const onCancel = vi.fn();
    const onChangeType = vi.fn();
    mockedPreviewTag.mockRejectedValue({ isAxiosError: true, response: { data: { error: { message: "Invalid constant value" } } } });
    mockedCreateTag.mockRejectedValue({ isAxiosError: true, response: { data: { error: { message: "Tag name already exists" } } } });
    renderForm({ onCancel, onChangeType });

    fireEvent.click(screen.getByRole("button", { name: "Preview value" }));
    expect(await screen.findByRole("alert")).toHaveTextContent("Invalid constant value");
    fireEvent.change(screen.getByLabelText(/Name/), { target: { value: "duplicate" } });
    fireEvent.click(screen.getByRole("button", { name: "Create Constant Tag" }));
    expect(await screen.findByText("Tag name already exists")).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: "Dismiss error" }));
    fireEvent.click(screen.getByRole("button", { name: "Change type" }));
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    expect(onChangeType).toHaveBeenCalledOnce();
    expect(onCancel).toHaveBeenCalledOnce();
  });
});
