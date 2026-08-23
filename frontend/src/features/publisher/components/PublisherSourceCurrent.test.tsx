// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import type { PublisherSourceCatalogEntry } from "../../../types/publisher";
import PublisherSourceCurrent from "./PublisherSourceCurrent";

const windowedEntry: PublisherSourceCatalogEntry = {
  descriptor: {
    reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.covered_estimated_cost" },
    name: "Today covered cost",
    owner_name: "Energy Management",
    schema_version: 1,
    data_type: "float64",
    unit: "THB",
    period_kind: "windowed",
    enabled: true,
  },
  current: {
    quality: "partial",
    sequence: 18,
    observed_at: "2026-08-23T14:07:06Z",
    period_start: "2026-08-22T17:00:00Z",
    period_end: "2026-08-23T14:07:06Z",
    coverage_percent: 6.793,
  },
};

describe("PublisherSourceCurrent", () => {
  afterEach(cleanup);

  it("shows window bounds and coverage for a Plugin output", () => {
    render(<PublisherSourceCurrent entry={windowedEntry} />);

    expect(screen.getByText("partial")).toBeInTheDocument();
    expect(screen.getByRole("progressbar", { name: "Coverage 6.8 percent" })).toHaveAttribute("aria-valuenow", "6.793");
    expect(screen.getByLabelText("Period from 2026-08-22T17:00:00Z to 2026-08-23T14:07:06Z")).toBeInTheDocument();
  });

  it("shows an explicit unavailable state when a windowed output has no coverage", () => {
    render(<PublisherSourceCurrent entry={{ ...windowedEntry, current: { quality: "unavailable" } }} />);

    expect(screen.getByRole("progressbar", { name: "Coverage unavailable" })).not.toHaveAttribute("aria-valuenow");
  });

  it("shows observation time without inventing coverage for an instantaneous Tag", () => {
    render(<PublisherSourceCurrent entry={{
      descriptor: {
        reference: { kind: "tag", tag_id: "tag-1" }, name: "Demand", schema_version: 1,
        data_type: "float64", unit: "kW", period_kind: "instantaneous", enabled: true,
      },
      current: { quality: "good", observed_at: "2026-08-23T14:07:06Z" },
    }} />);

    expect(screen.queryByRole("progressbar")).not.toBeInTheDocument();
    expect(screen.getByLabelText("Observed at 2026-08-23T14:07:06Z")).toBeInTheDocument();
  });
});
