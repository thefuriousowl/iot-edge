// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import PublisherRuntimeSources from "./PublisherRuntimeSources";

describe("PublisherRuntimeSources", () => {
  afterEach(cleanup);

  it("shows exact window provenance, coverage, quality, and source identity", () => {
    render(<PublisherRuntimeSources sources={[{
      alias: "today_cost", available: true, quality: "partial", sequence: 18, coverage_percent: 6.793,
      period_start: "2026-08-22T17:00:00Z", period_end: "2026-08-23T14:07:06Z",
      reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.covered_estimated_cost" },
    }]} />);

    expect(screen.getByText("today_cost")).toBeInTheDocument();
    expect(screen.getByText("Plugin output · today.covered_estimated_cost")).toBeInTheDocument();
    expect(screen.getByText("partial")).toBeInTheDocument();
    expect(screen.getByText("6.8%")).toBeInTheDocument();
    expect(screen.getByLabelText("Period from 2026-08-22T17:00:00Z to 2026-08-23T14:07:06Z")).toBeInTheDocument();
  });

  it("fails unavailable and does not invent observation provenance", () => {
    render(<PublisherRuntimeSources sources={[{
      alias: "flow", available: false, quality: "good",
      reference: { kind: "tag", tag_id: "tag-1" },
    }]} />);

    expect(screen.getByText("unavailable")).toBeInTheDocument();
    expect(screen.getByText("Tag · tag-1")).toBeInTheDocument();
    expect(screen.getByLabelText("Observation unavailable")).toBeInTheDocument();
    expect(screen.getByText("Unavailable", { selector: "dd" })).toBeInTheDocument();
  });

  it("renders zero-duration provenance as an observation instead of a window", () => {
    render(<PublisherRuntimeSources sources={[{
      alias: "instantaneous_cop", available: true, quality: "good", sequence: 19,
      period_start: "2026-08-23T14:07:06Z", period_end: "2026-08-23T14:07:06Z",
      reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "instantaneous_cop" },
    }]} />);

    expect(screen.getByLabelText("Observed at 2026-08-23T14:07:06Z")).toBeInTheDocument();
    expect(screen.queryByLabelText(/Period from/)).not.toBeInTheDocument();
  });

  it("shows an explicit pre-snapshot empty state", () => {
    render(<PublisherRuntimeSources />);

    expect(screen.getByText("No runtime source snapshot")).toBeInTheDocument();
    expect(screen.getByText("0 sources")).toBeInTheDocument();
  });
});
