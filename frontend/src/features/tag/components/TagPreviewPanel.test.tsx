// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { TagPreviewResult } from "../../../types/tag";
import TagPreviewPanel from "./TagPreviewPanel";

const result: TagPreviewResult = {
  tag_id: "00000000-0000-0000-0000-000000000000",
  observed_at: "2026-08-22T04:00:00Z",
  quality: "good",
  data_type: "float32",
  value: 1234.5,
};

describe("TagPreviewPanel", () => {
  afterEach(cleanup);

  it("renders reusable idle and loading states", () => {
    const rendered = render(<TagPreviewPanel state={{ status: "idle" }} />);
    expect(screen.getByLabelText("Tag preview")).toHaveTextContent("without saving");
    rendered.rerender(<TagPreviewPanel state={{ status: "loading" }} />);
    expect(screen.getByRole("status")).toHaveTextContent("Executing the datasource and decoder pipeline");
  });

  it("renders sanitized preview errors", () => {
    render(<TagPreviewPanel state={{ status: "error", message: "Datasource is paused" }} />);
    expect(screen.getByRole("alert")).toHaveTextContent("Preview failed");
    expect(screen.getByRole("alert")).toHaveTextContent("Datasource is paused");
  });

  it("renders typed values, quality, and observation metadata", () => {
    render(<TagPreviewPanel state={{ status: "success", result }} />);
    const output = screen.getByLabelText("Tag preview result");
    expect(output).toHaveTextContent("1234.5");
    expect(output).toHaveTextContent("float32 · good");
    expect(output).toHaveTextContent("Decoded value");
  });

  it("formats boolean and uncertain values without numeric coercion", () => {
    render(<TagPreviewPanel state={{ status: "success", result: { ...result, quality: "uncertain", data_type: "bool", value: false } }} />);
    const output = screen.getByLabelText("Tag preview result");
    expect(output).toHaveTextContent("false");
    expect(output).toHaveTextContent("bool · uncertain");
    expect(output).toHaveClass("is-uncertain");
  });
});
