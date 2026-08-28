import { CircleAlert, Link2, Pencil, Plus, Save, Trash2, X } from "lucide-react";
import { type FormEvent, useState } from "react";

import { AssetAPIError } from "../../../services/asset.service";
import { useCreateAsset, useDeleteAsset, useMoveAsset, useReplaceAssetBindings, useUpdateAsset } from "../../../hooks/useAssets";
import type { Asset, AssetKind, AssetQuantity, AssetResource, AssetUnit, MeasurementBindingInput } from "../../../types/asset";

type EditorMode = "create" | "edit" | "move" | "binding" | "delete" | null;
const kinds: AssetKind[] = ["site", "building", "area", "system", "equipment", "meter", "custom"];
const resources: AssetResource[] = ["electricity", "thermal", "compressed_air", "steam", "gas", "water", "solar", "custom"];
const quantities: AssetQuantity[] = ["power", "energy", "flow_rate", "volume", "pressure", "temperature", "ratio", "state", "cost"];
const units: AssetUnit[] = ["W", "kW", "MW", "Wh", "kWh", "MWh", "m3", "Nm3", "m3/s", "m3/h", "Nm3/h", "Pa", "kPa", "bar", "K", "degC", "degF", "1", "%", "bool"];

function message(error: unknown): string { return error instanceof AssetAPIError ? error.message : "The Asset request could not be completed."; }

interface AssetWorkspaceProps { selected: Asset | null; onCreated: (asset: Asset) => void; onDeleted: () => void }

function AssetWorkspace({ selected, onCreated, onDeleted }: AssetWorkspaceProps) {
  const [mode, setMode] = useState<EditorMode>(null);
  const [error, setError] = useState("");
  const create = useCreateAsset(), update = useUpdateAsset(), move = useMoveAsset(), remove = useDeleteAsset(), replaceBindings = useReplaceAssetBindings();
  function close() { setMode(null); setError(""); }
  async function submitAsset(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); setError("");
    const data = new FormData(event.currentTarget);
    try {
      if (mode === "create") {
        const created = await create.mutateAsync({ parent_id: (data.get("parent_id") as string) || null, name: String(data.get("name")), kind: data.get("kind") as AssetKind, description: (data.get("description") as string) || null, enabled: data.get("enabled") === "on", timezone: (data.get("timezone") as string) || null, position: Number(data.get("position")), metadata: {} });
        onCreated(created);
      } else if (mode === "edit" && selected) {
        await update.mutateAsync({ id: selected.id, data: { name: String(data.get("name")), kind: data.get("kind") as AssetKind, description: (data.get("description") as string) || null, enabled: data.get("enabled") === "on", timezone: (data.get("timezone") as string) || null, position: Number(data.get("position")), metadata: selected.metadata } });
      }
      close();
    } catch (reason) { setError(message(reason)); }
  }

  async function submitMove(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!selected) return; setError(""); const data = new FormData(event.currentTarget);
    try { await move.mutateAsync({ id: selected.id, data: { parent_id: (data.get("parent_id") as string) || null, position: Number(data.get("position")) } }); close(); }
    catch (reason) { setError(message(reason)); }
  }

  async function submitBinding(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!selected) return; setError(""); const data = new FormData(event.currentTarget);
    const sourceType = data.get("source_type");
    const source = sourceType === "tag" ? { kind: "tag" as const, tag_id: String(data.get("tag_id")) } : { kind: "plugin_output" as const, plugin_instance_id: String(data.get("plugin_instance_id")), output_key: String(data.get("output_key")) };
    const meterRole = data.get("meter_role") as MeasurementBindingInput["meter_role"];
    const virtualInputs = String(data.get("virtual_inputs") ?? "").split(",").map((value) => value.trim()).filter(Boolean);
    const binding: MeasurementBindingInput = { boundary_asset_id: String(data.get("boundary_asset_id")), source, semantic: { resource: data.get("resource") as AssetResource, quantity: data.get("quantity") as AssetQuantity, unit: data.get("unit") as AssetUnit, precision: Number(data.get("precision")) }, meter_role: meterRole, rollup_policy: data.get("rollup_policy") as MeasurementBindingInput["rollup_policy"], ...(meterRole === "virtual" ? { virtual_inputs: virtualInputs } : {}) };
    try { await replaceBindings.mutateAsync({ id: selected.id, data: { bindings: [binding] } }); close(); }
    catch (reason) { setError(message(reason)); }
  }

  async function confirmDelete(event: FormEvent<HTMLFormElement>) {
    event.preventDefault(); if (!selected) return; const data = new FormData(event.currentTarget); if (data.get("confirmation") !== selected.name) { setError("Enter the Asset name exactly to confirm deletion."); return; }
    try { await remove.mutateAsync(selected.id); close(); onDeleted(); } catch (reason) { setError(message(reason)); }
  }

  return <>
    <div className="asset-workspace-actions">
      <button type="button" onClick={() => setMode("create")}><Plus size={16} />New asset</button>
      {selected && <><button type="button" onClick={() => setMode("edit")}><Pencil size={16} />Edit</button><button type="button" onClick={() => setMode("move")}><Link2 size={16} />Move</button><button type="button" onClick={() => setMode("binding")}><Save size={16} />Bindings</button><button className="is-danger" type="button" onClick={() => setMode("delete")}><Trash2 size={16} />Delete</button></>}
    </div>
    {mode && <div className="asset-editor-backdrop" role="presentation" onMouseDown={(event) => { if (event.target === event.currentTarget) close(); }}><section className="asset-editor" role="dialog" aria-modal="true" aria-labelledby="asset-editor-title"><header><div><p className="asset-eyebrow">Asset workspace</p><h2 id="asset-editor-title">{mode === "create" ? "Create asset" : mode === "edit" ? "Edit asset" : mode === "move" ? "Move asset" : mode === "binding" ? "Replace measurement binding" : "Delete asset"}</h2></div><button type="button" aria-label="Close Asset workspace" onClick={close}><X /></button></header>
      {error && <div className="asset-editor-error" role="alert"><CircleAlert size={18} />{error}</div>}
      {(mode === "create" || mode === "edit") && <form onSubmit={submitAsset}><div className="asset-editor-grid"><label>Name<input name="name" required maxLength={100} defaultValue={mode === "edit" ? selected?.name : ""} /></label><label>Type<select name="kind" defaultValue={mode === "edit" ? selected?.kind : "equipment"}>{kinds.map((kind) => <option key={kind}>{kind}</option>)}</select></label><label className="is-wide">Description<textarea name="description" defaultValue={mode === "edit" ? selected?.description ?? "" : ""} /></label><label>Parent ID<input name="parent_id" readOnly={mode === "edit"} defaultValue={mode === "edit" ? selected?.parent_id ?? "Root (use Move)" : selected?.id ?? ""} placeholder="Blank for root site" /></label><label>Timezone<input name="timezone" defaultValue={mode === "edit" ? selected?.timezone ?? "" : ""} placeholder="Inherited when blank" /></label><label>Position<input name="position" type="number" min="0" defaultValue={mode === "edit" ? selected?.position : 0} /></label><label className="asset-editor-check"><input name="enabled" type="checkbox" defaultChecked={mode === "edit" ? selected?.enabled : true} />Enabled</label></div><footer><button type="button" onClick={close}>Cancel</button><button className="is-primary" type="submit">Save asset</button></footer></form>}
      {mode === "move" && selected && <form onSubmit={submitMove}><p className="asset-editor-help">Moving is the only operation that changes hierarchy ownership. The backend rejects cycles and unsafe depth.</p><div className="asset-editor-grid"><label className="is-wide">New parent ID<input name="parent_id" defaultValue={selected.parent_id ?? ""} placeholder="Blank only for a site root" /></label><label>Position<input name="position" type="number" min="0" defaultValue={selected.position} /></label></div><footer><button type="button" onClick={close}>Cancel</button><button className="is-primary" type="submit">Move asset</button></footer></form>}
      {mode === "binding" && selected && <BindingForm selected={selected} onSubmit={submitBinding} onCancel={close} />}
      {mode === "delete" && selected && <form onSubmit={confirmDelete}><p className="asset-editor-help">Deletion fails safely while this Asset has children or measurement bindings. Enter <strong>{selected.name}</strong> to confirm.</p><label>Asset name<input name="confirmation" autoComplete="off" /></label><footer><button type="button" onClick={close}>Cancel</button><button className="is-danger" type="submit">Delete permanently</button></footer></form>}
    </section></div>}
  </>;
}

function BindingForm({ selected, onSubmit, onCancel }: { selected: Asset; onSubmit: (event: FormEvent<HTMLFormElement>) => void; onCancel: () => void }) {
  const [source, setSource] = useState("tag"), [resource, setResource] = useState<AssetResource>("electricity"), [quantity, setQuantity] = useState<AssetQuantity>("energy"), [unit, setUnit] = useState<AssetUnit>("kWh"), [role, setRole] = useState("main");
  return <form onSubmit={onSubmit}><p className="asset-editor-help">Saving replaces the complete binding set for this Asset. Source ownership and semantic compatibility are validated atomically.</p><div className="asset-editor-grid"><label>Source type<select name="source_type" value={source} onChange={(event) => setSource(event.target.value)}><option value="tag">Tag</option><option value="plugin_output">Plugin output</option></select></label>{source === "tag" ? <label>Tag ID<input name="tag_id" required /></label> : <><label>Plugin instance ID<input name="plugin_instance_id" required /></label><label>Output key<input name="output_key" required /></label></>}<label>Boundary Asset ID<input name="boundary_asset_id" required defaultValue={selected.id} /></label><label>Resource<select name="resource" value={resource} onChange={(event) => setResource(event.target.value as AssetResource)}>{resources.map((value) => <option key={value}>{value}</option>)}</select></label><label>Quantity<select name="quantity" value={quantity} onChange={(event) => setQuantity(event.target.value as AssetQuantity)}>{quantities.map((value) => <option key={value}>{value}</option>)}</select></label><label>Unit<select name="unit" value={unit} onChange={(event) => setUnit(event.target.value as AssetUnit)}>{units.map((value) => <option key={value}>{value}</option>)}</select></label><label>Precision<input name="precision" type="number" min="0" max="12" defaultValue="3" /></label><label>Meter role<select name="meter_role" value={role} onChange={(event) => setRole(event.target.value)}><option>direct</option><option>main</option><option>submeter</option><option>virtual</option></select></label><label>Rollup<select name="rollup_policy"><option>include</option><option>exclude</option></select></label>{role === "virtual" && <label className="is-wide">Virtual input binding IDs<input name="virtual_inputs" required placeholder="UUID, UUID" /></label>}</div><aside className="asset-semantic-preview" aria-label="Semantic preview"><span>Semantic preview</span><strong>{resource} / {quantity}</strong><code>{unit} · {role} meter</code></aside><footer><button type="button" onClick={onCancel}>Cancel</button><button className="is-primary" type="submit">Replace bindings</button></footer></form>;
}

export default AssetWorkspace;
