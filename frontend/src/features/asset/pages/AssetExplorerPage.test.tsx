// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Asset } from "../../../types/asset";
import AssetExplorerPage from "./AssetExplorerPage";

const hooks = vi.hoisted(() => ({ roots: vi.fn(), children: vi.fn(), assets: vi.fn() }));
vi.mock("../../../hooks/useAssets", () => ({
  useAssetRoots: hooks.roots, useAssetChildren: hooks.children, useAssets: hooks.assets,
  useCreateAsset: () => ({ mutateAsync: vi.fn() }), useUpdateAsset: () => ({ mutateAsync: vi.fn() }),
  useMoveAsset: () => ({ mutateAsync: vi.fn() }), useDeleteAsset: () => ({ mutateAsync: vi.fn() }),
  useReplaceAssetBindings: () => ({ mutateAsync: vi.fn() }),
  useAssetMeasurements: () => ({ data: undefined, isLoading: false, isError: false }),
  useAssetConnectivity: () => ({ data: undefined, isLoading: false, isError: false }),
}));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Online</span> }));

const site: Asset = { id: "site-1", parent_id: null, name: "Bangkok Plant", kind: "site", description: "Production campus", enabled: true, timezone: "Asia/Bangkok", position: 0, metadata: {}, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };
const meter: Asset = { ...site, id: "meter-1", parent_id: site.id, name: "Main incomer", kind: "meter", description: null, timezone: null };
const queryResult = (data: unknown) => ({ data, isLoading: false, isFetching: false, isError: false, isSuccess: true, refetch: vi.fn() });

function LocationProbe() { return <output data-testid="location">{useLocation().search}</output>; }
function renderPage(path = "/assets") { return render(<MemoryRouter initialEntries={[path]}><AssetExplorerPage /><LocationProbe /></MemoryRouter>); }

describe("Asset Explorer", () => {
  beforeEach(() => {
    hooks.roots.mockReturnValue(queryResult({ data: [site] }));
    hooks.children.mockImplementation((id: string) => queryResult({ data: id === site.id ? [meter] : [] }));
    hooks.assets.mockReturnValue(queryResult({ data: [meter], pagination: { page: 1, per_page: 100, total: 1, total_pages: 1 } }));
  });
  afterEach(() => cleanup());

  it("lazily expands nodes, selects by route, and exposes status", () => {
    renderPage();
    const tree = screen.getByRole("tree", { name: "Asset hierarchy" });
    const plant = within(tree).getByRole("treeitem", { name: /Bangkok Plant/ });
    expect(plant).toHaveAttribute("aria-expanded", "false");
    expect(screen.queryByText("Main incomer")).not.toBeInTheDocument();

    fireEvent.keyDown(plant, { key: "ArrowRight" });
    expect(plant).toHaveAttribute("aria-expanded", "true");
    const child = within(tree).getByRole("treeitem", { name: /Main incomer/ });
    fireEvent.click(child);
    expect(screen.getByTestId("location")).toHaveTextContent("asset=meter-1");
    expect(screen.getByRole("heading", { name: "Main incomer" })).toBeInTheDocument();
    expect(within(child).getByText("Enabled")).toBeInTheDocument();
  });

  it("supports tree focus navigation and URL-backed search", () => {
    renderPage();
    const plant = screen.getByRole("treeitem", { name: /Bangkok Plant/ });
    fireEvent.keyDown(plant, { key: "ArrowRight" });
    const child = screen.getByRole("treeitem", { name: /Main incomer/ });
    plant.focus();
    fireEvent.keyDown(plant, { key: "ArrowDown" });
    expect(child).toHaveFocus();
    fireEvent.keyDown(child, { key: "ArrowLeft" });
    expect(plant).toHaveFocus();

    fireEvent.change(screen.getByRole("searchbox", { name: "Search assets" }), { target: { value: "incomer" } });
    fireEvent.submit(screen.getByRole("search"));
    expect(screen.getByTestId("location")).toHaveTextContent("q=incomer");
    expect(screen.getByRole("tree", { name: "Asset search results" })).toBeInTheDocument();
  });
});
