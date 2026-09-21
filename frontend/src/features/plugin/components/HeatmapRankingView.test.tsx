// @vitest-environment jsdom
import "@testing-library/jest-dom/vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import HeatmapRankingView from "./HeatmapRankingView";
import { buildHeatmapCells, maximumHeatmapPoints } from "./heatmapData";

describe("HeatmapRankingView", () => {
  afterEach(cleanup);
  it("bounds and averages same hour×weekday cells without treating gaps as zero", () => { const points = Array.from({ length: 501 }, (_, index) => ({ at: index < 499 ? "invalid" : index === 499 ? "2026-08-24T01:00:00Z" : "2026-08-31T01:00:00Z", value: index < 499 ? null : index === 499 ? 10 : 20, coverage: 80 })); const cells = buildHeatmapCells(points, "UTC"); expect(maximumHeatmapPoints).toBe(500); expect(cells).toHaveLength(1); expect(cells[0]).toEqual(expect.objectContaining({ value: 15, coverage: 80, samples: 2 })); });
  it("renders heatmap table and KPI ranking with units, coverage and delta", () => { render(<HeatmapRankingView timezone="UTC" unit="kWh" points={[{ at: "2026-08-24T01:00:00Z", value: 10, coverage: 75 }, { at: "2026-08-24T02:00:00Z", value: null, coverage: 0 }]} ranking={[{ label: "Aug 24, 01:00", value: 10, previous: 8, unit: "kWh", coverage: 75 }]} />); expect(screen.getByTitle(/10 kWh.*75% coverage/)).toBeInTheDocument(); fireEvent.click(screen.getByText("Accessible heatmap table")); expect(screen.getByRole("table")).toHaveTextContent("Mon01:0010 kWh75%1"); const ranking = screen.getByRole("heading", { name: "Top consumption buckets" }).closest("section")!; expect(within(ranking).getByText("+25%")).toBeInTheDocument(); expect(ranking).toHaveTextContent("75% coverage"); });
  it("shows explicit no-data states", () => { render(<HeatmapRankingView timezone="UTC" unit="kWh" points={[]} ranking={[]} />); expect(screen.getAllByRole("status")).toHaveLength(2); });
});
