// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter, Route, Routes } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getTag, getTagValues, monitorTagValues } from "../../../services/tag.service";
import type { ReadingTag, TagRuntimeValue } from "../../../types/tag";
import TagDetailPage from "./TagDetailPage";

vi.mock("../../../services/tag.service", () => ({ getTag: vi.fn(), getTagValues: vi.fn(), monitorTagValues: vi.fn() }));

const mockedGetTag = vi.mocked(getTag);
const mockedGetTagValues = vi.mocked(getTagValues);
const mockedMonitor = vi.mocked(monitorTagValues);
const tagID = "11111111-1111-4111-8111-111111111111";
const tag: ReadingTag = {
  id: tagID,
  datasource_id: "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa",
  name: "Line pressure",
  type: "reading",
  data_type: "float64",
  description: "Scaled pressure",
  enabled: true,
  config: { decoder: { type: "binary_numeric", data_type: "uint16", config: { byte_offset: 2, byte_order: "big_endian", bit_offset: 0 } }, transform: { type: "linear", config: { gain: 0.1, offset: -5 } } },
  created_at: "2026-08-22T00:00:00Z",
  updated_at: "2026-08-22T00:00:00Z",
};

function runtimeValue(sequence: number, quality: "good" | "bad" = "good"): TagRuntimeValue {
  return { tag_id: tagID, sequence, observed_at: `2026-08-22T00:00:${String(sequence).padStart(2, "0")}Z`, stored_at: `2026-08-22T00:00:${String(sequence).padStart(2, "0")}Z`, quality, data_type: "float64", value: quality === "good" ? sequence * 10 : null, ...(quality === "bad" ? { error: "illegal data address" } : {}) };
}

function renderPage() {
  return render(<MemoryRouter initialEntries={[`/tags/${tagID}`]}><Routes><Route path="/tags/:id" element={<TagDetailPage />} /></Routes></MemoryRouter>);
}

describe("TagDetailPage", () => {
  let publish: ((value: TagRuntimeValue) => void) | undefined;

  beforeEach(() => {
    mockedGetTag.mockReset();
    mockedGetTagValues.mockReset();
    mockedMonitor.mockReset();
    mockedGetTag.mockResolvedValue(tag);
    const history = Array.from({ length: 10 }, (_, index) => runtimeValue(index + 1));
    mockedGetTagValues.mockResolvedValue({ latest: history[9], history, latest_retention: "persistent", history_retention: "runtime_memory" });
    mockedMonitor.mockImplementation(async (_id, onValue, _signal, onOpen) => {
      publish = onValue;
      onOpen?.();
      await new Promise(() => {});
    });
  });

  afterEach(cleanup);

  it("shows configured scaling, current value, and ten runtime samples", async () => {
    renderPage();

    expect(await screen.findByRole("heading", { name: "Line pressure" })).toBeInTheDocument();
    expect(screen.getByText("uint16 → float64")).toBeInTheDocument();
    expect(screen.getByText("× 0.1 + -5")).toBeInTheDocument();
    expect(screen.getByLabelText("Current Tag value")).toHaveTextContent("100");
    expect(screen.getByText("Memory only")).toBeInTheDocument();
    const historySection = screen.getByRole("heading", { name: "Latest 10 runtime values" }).closest("section")!;
    expect(within(historySection).getAllByRole("row")).toHaveLength(11);
    expect(mockedGetTagValues).toHaveBeenCalledWith(tagID, 10, expect.any(AbortSignal));
    expect(mockedMonitor).toHaveBeenCalledWith(tagID, expect.any(Function), expect.any(AbortSignal), expect.any(Function));
  });

  it("merges live values by sequence, trims old samples, and exposes errors", async () => {
    renderPage();
    await screen.findByRole("heading", { name: "Line pressure" });
    publish?.(runtimeValue(11, "bad"));

    await waitFor(() => expect(screen.getByLabelText("Current Tag value")).toHaveTextContent("illegal data address"));
    expect(screen.getByLabelText("Current Tag value")).toHaveTextContent("bad");
    const historySection = screen.getByRole("heading", { name: "Latest 10 runtime values" }).closest("section")!;
    expect(within(historySection).getAllByRole("row")).toHaveLength(11);
    expect(within(historySection).queryByText("10", { selector: "td:first-child" })).not.toBeInTheDocument();
  });

  it("shows load failures and retries both definition and history", async () => {
    mockedGetTag.mockRejectedValueOnce({ isAxiosError: true, response: { data: { error: { message: "Tag not found" } } } }).mockResolvedValueOnce(tag);
    renderPage();
    expect(await screen.findByRole("alert")).toHaveTextContent("Tag not found");
    fireEvent.click(screen.getByRole("button", { name: "Try again" }));
    expect(await screen.findByRole("heading", { name: "Line pressure" })).toBeInTheDocument();
    expect(mockedGetTag).toHaveBeenCalledTimes(2);
  });

  it("renders an empty state when an older backend returns null history", async () => {
    mockedGetTagValues.mockResolvedValueOnce({ latest: null, history: null, latest_retention: "persistent", history_retention: "runtime_memory" } as unknown as Awaited<ReturnType<typeof getTagValues>>);
    renderPage();

    expect(await screen.findByRole("heading", { name: "Line pressure" })).toBeInTheDocument();
    expect(screen.getByText("No runtime samples yet.")).toBeInTheDocument();
  });
});
