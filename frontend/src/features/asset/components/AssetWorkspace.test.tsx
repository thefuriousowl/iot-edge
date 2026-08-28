// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import type { Asset } from "../../../types/asset";
import AssetWorkspace from "./AssetWorkspace";

const mutations = vi.hoisted(() => ({ create: vi.fn(), update: vi.fn(), move: vi.fn(), remove: vi.fn(), bindings: vi.fn() }));
vi.mock("../../../hooks/useAssets", () => ({
  useCreateAsset: () => ({ mutateAsync: mutations.create }), useUpdateAsset: () => ({ mutateAsync: mutations.update }),
  useMoveAsset: () => ({ mutateAsync: mutations.move }), useDeleteAsset: () => ({ mutateAsync: mutations.remove }),
  useReplaceAssetBindings: () => ({ mutateAsync: mutations.bindings }),
}));

const asset: Asset = { id: "asset-1", parent_id: "site-1", name: "Main Meter", kind: "meter", description: "Revenue meter", enabled: true, timezone: null, position: 2, metadata: { line: 1 }, created_at: "2026-08-28T00:00:00Z", updated_at: "2026-08-28T00:00:00Z" };

describe("Asset workspace", () => {
  beforeEach(() => Object.values(mutations).forEach((mock) => mock.mockReset()));
  afterEach(() => cleanup());

  it("creates a child with explicit hierarchy metadata", async () => {
    mutations.create.mockResolvedValue({ ...asset, id: "child-1", name: "Pump" });
    const onCreated = vi.fn();
    render(<AssetWorkspace selected={asset} onCreated={onCreated} onDeleted={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "New asset" }));
    fireEvent.change(screen.getByLabelText("Name"), { target: { value: "Pump" } });
    fireEvent.change(screen.getByLabelText("Type"), { target: { value: "equipment" } });
    fireEvent.submit(screen.getByRole("button", { name: "Save asset" }).closest("form")!);
    expect(mutations.create).toHaveBeenCalledWith(expect.objectContaining({ parent_id: asset.id, name: "Pump", kind: "equipment", enabled: true, metadata: {} }));
    await waitFor(() => expect(onCreated).toHaveBeenCalledWith(expect.objectContaining({ id: "child-1" })));
  });

  it("keeps parent read-only during edit and moves through the dedicated mutation", async () => {
    mutations.update.mockResolvedValue(asset); mutations.move.mockResolvedValue(asset);
    render(<AssetWorkspace selected={asset} onCreated={vi.fn()} onDeleted={vi.fn()} />);
    fireEvent.click(screen.getByRole("button", { name: "Edit" }));
    expect(screen.getByLabelText("Parent ID")).toHaveAttribute("readonly");
    fireEvent.click(screen.getByRole("button", { name: "Cancel" }));
    fireEvent.click(screen.getByRole("button", { name: "Move" }));
    fireEvent.change(screen.getByLabelText("New parent ID"), { target: { value: "site-2" } });
    fireEvent.change(screen.getByLabelText("Position"), { target: { value: "4" } });
    fireEvent.submit(screen.getByRole("button", { name: "Move asset" }).closest("form")!);
    expect(mutations.move).toHaveBeenCalledWith({ id: asset.id, data: { parent_id: "site-2", position: 4 } });
  });

  it("previews and replaces Tag/Plugin semantic bindings and confirms destructive delete", async () => {
    mutations.bindings.mockResolvedValue({ data: [] }); mutations.remove.mockResolvedValue(undefined);
    const onDeleted = vi.fn();
    render(<AssetWorkspace selected={asset} onCreated={vi.fn()} onDeleted={onDeleted} />);
    fireEvent.click(screen.getByRole("button", { name: "Bindings" }));
    fireEvent.change(screen.getByLabelText("Source type"), { target: { value: "plugin_output" } });
    fireEvent.change(screen.getByLabelText("Plugin instance ID"), { target: { value: "plugin-1" } });
    fireEvent.change(screen.getByLabelText("Output key"), { target: { value: "energy_total" } });
    expect(screen.getByLabelText("Semantic preview")).toHaveTextContent("electricity / energy");
    fireEvent.submit(screen.getByRole("button", { name: "Replace bindings" }).closest("form")!);
    expect(mutations.bindings).toHaveBeenCalledWith({ id: asset.id, data: { bindings: [expect.objectContaining({ source: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "energy_total" }, boundary_asset_id: asset.id, meter_role: "main", rollup_policy: "include" })] } });

    fireEvent.click(screen.getByRole("button", { name: "Delete" }));
    fireEvent.change(screen.getByLabelText("Asset name"), { target: { value: asset.name } });
    fireEvent.submit(screen.getByRole("button", { name: "Delete permanently" }).closest("form")!);
    expect(mutations.remove).toHaveBeenCalledWith(asset.id);
    await waitFor(() => expect(onDeleted).toHaveBeenCalledOnce());
  });
});
