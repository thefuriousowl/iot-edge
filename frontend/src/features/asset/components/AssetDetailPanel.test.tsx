// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { act, cleanup, fireEvent, render, screen } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Asset, AssetMeasurementReading } from "../../../types/asset";
import AssetDetailPanel from "./AssetDetailPanel";

const mocks = vi.hoisted(() => ({ measurements: vi.fn(), connectivity: vi.fn(), sse: vi.fn() }));
vi.mock("../../../hooks/useAssets", () => ({ useAssetMeasurements: mocks.measurements, useAssetConnectivity: mocks.connectivity }));
vi.mock("../../../hooks/useSSE", () => ({ useSSE: mocks.sse }));

const asset: Asset = { id: "asset-1", parent_id: "site-1", name: "Main Meter", kind: "meter", description: "Revenue meter", enabled: true, timezone: null, position: 2, metadata: {}, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const reading: AssetMeasurementReading = { binding_id: "binding-1", source: { kind: "tag", tag_id: "tag-1" }, semantic: { resource: "electricity", quantity: "energy", unit: "kWh", precision: 2 }, available: true, sequence: 7, value: 123.45, quality: "good", observed_at: "2026-08-28T01:00:00Z" };

describe("Asset detail panel", () => {
  beforeEach(() => {
    mocks.sse.mockReset();
    mocks.measurements.mockReturnValue({ isLoading: false, isError: false, data: { asset_id: asset.id, captured_at: "2026-08-28T01:00:01Z", measurements: [{ binding: { id: "binding-1", owner_asset_id: asset.id, boundary_asset_id: "site-1", source_key: "tag:tag-1", source: reading.source, semantic: reading.semantic, meter_role: "main", rollup_policy: "include" }, latest: reading, history: [{ ...reading, sequence: 6, value: 120 }], history_retention: "runtime_memory" }] } });
    mocks.connectivity.mockReturnValue({ isLoading: false, isError: false, data: { asset_id: asset.id, links: [{ binding_id: "binding-1", source: reading.source, tag: { id: "tag-1", name: "Energy total", kind: "tag", enabled: true }, datasource: { id: "ds-1", name: "Holding registers", kind: "datasource", enabled: true }, device: { id: "dev-1", name: "Power meter", kind: "device", enabled: true }, vgateway: { id: "gw-1", name: "Plant gateway", kind: "vgateway", enabled: true } }] } });
  });
  afterEach(() => cleanup());

  it("renders latest-10 retention, source history link, quality and physical chain", () => {
    render(<MemoryRouter><AssetDetailPanel asset={asset} /></MemoryRouter>);
    fireEvent.click(screen.getByRole("button", { name: /Measurements/ }));
    expect(screen.getByText("123.45")).toBeInTheDocument();
    expect(screen.getByText(/latest 2 runtime samples/)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: "Open Tag history" })).toHaveAttribute("href", "/tags/tag-1");
    fireEvent.click(screen.getByRole("button", { name: "Connectivity" }));
    expect(screen.getByText("Power meter")).toBeInTheDocument();
    expect(screen.getByText("Plant gateway")).toBeInTheDocument();
  });

  it("marks cached readings stale, ingests SSE and exposes reconnect", () => {
    render(<MemoryRouter><AssetDetailPanel asset={asset} /></MemoryRouter>);
    const options = mocks.sse.mock.calls[0][0];
    act(() => options.onStateChange("disconnected"));
    expect(screen.getByRole("button", { name: "Reconnect" })).toBeInTheDocument();
    fireEvent.click(screen.getByRole("button", { name: /Measurements/ }));
    expect(screen.getByText("stale")).toBeInTheDocument();
    act(() => { options.onMessage({ ...reading, sequence: 8, value: 130 }); options.onStateChange("live"); });
    expect(screen.getByText("130")).toBeInTheDocument();
    expect(screen.getAllByText("good").length).toBeGreaterThan(0);
  });
});
