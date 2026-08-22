import { useEffect, useState } from "react";
import axios from "axios";
import { Activity, AlertTriangle, ChevronDown, ChevronRight, Cpu, LoaderCircle, Pencil, Plus, Radio, RefreshCw, Trash2, X } from "lucide-react";

import {
  createDatasource,
  createDevice,
  deleteDatasource,
  deleteDevice,
  listDatasources,
  listDevices,
  monitorDatasource,
  previewDatasource,
  previewSavedDatasource,
  updateDatasource,
  updateDevice,
} from "../../../services/device.service";
import type { CreateDatasourceRequest, Datasource, DatasourceSample, Device } from "../../../types/device";
import "./DeviceWorkspace.css";

interface Props { vgatewayId: string }
type ErrorSetter = (message: string | null) => void;

function modbusQuantityLimit(functionCode: number): number {
  return functionCode === 1 || functionCode === 2 ? 2000 : 125;
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return error instanceof Error ? error.message : "Unexpected operation failure";
}

function DeviceWorkspace({ vgatewayId }: Props) {
  const [devices, setDevices] = useState<Device[]>([]);
  const [expanded, setExpanded] = useState<string | null>(null);
  const [datasources, setDatasources] = useState<Record<string, Datasource[]>>({});
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [showDeviceForm, setShowDeviceForm] = useState(false);
  const [editingDevice, setEditingDevice] = useState<Device | null>(null);
  const [datasourceFormFor, setDatasourceFormFor] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void listDevices(vgatewayId, controller.signal)
      .then(setDevices)
      .catch((caught: unknown) => {
        if (!controller.signal.aborted) setError(errorMessage(caught));
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
  }, [vgatewayId]);

  async function toggleDevice(id: string) {
    if (expanded === id) { setExpanded(null); return; }
    setError(null);
    setExpanded(id);
    try { setDatasources((current) => ({ ...current, [id]: current[id] ?? [] })); setDatasources((current) => ({ ...current, [id]: current[id]?.length ? current[id] : [] })); const result = await listDatasources(id); setDatasources((current) => ({ ...current, [id]: result })); }
    catch (caught) { setError(errorMessage(caught)); }
  }

  async function removeDevice(entity: Device) {
    if (!window.confirm(`Delete ${entity.name} and all of its datasources?`)) return;
    setError(null);
    try { await deleteDevice(entity.id); setDevices((current) => current.filter(({ id }) => id !== entity.id)); }
    catch (caught) { setError(errorMessage(caught)); }
  }

  async function removeDatasource(entity: Datasource) {
    if (!window.confirm(`Delete datasource ${entity.name}?`)) return;
    setError(null);
    try { await deleteDatasource(entity.id); setDatasources((current) => ({ ...current, [entity.device_id]: current[entity.device_id].filter(({ id }) => id !== entity.id) })); }
    catch (caught) { setError(errorMessage(caught)); }
  }

  return (
    <section className="device-workspace" id="devices">
      <header>
        <div><span>Acquisition topology</span><h2>Devices &amp; datasources</h2><p>Configure protocol endpoints and inspect live raw samples.</p></div>
        <button type="button" className="is-primary" onClick={() => { setError(null); setEditingDevice(null); setShowDeviceForm(true); }}><Plus size={17} /> Add device</button>
      </header>
      {error && <div className="device-workspace-alert" role="alert"><AlertTriangle size={18} /><span>{error}</span><button type="button" aria-label="Dismiss error" onClick={() => setError(null)}><X size={16} /></button></div>}
      {showDeviceForm && <DeviceForm vgatewayId={vgatewayId} initial={editingDevice} onCancel={() => { setShowDeviceForm(false); setEditingDevice(null); }} onSaved={(entity) => { setDevices((current) => editingDevice ? current.map((item) => item.id === entity.id ? { ...item, ...entity } : item) : [...current, entity]); setShowDeviceForm(false); setEditingDevice(null); }} onError={setError} />}
      {loading ? <div className="device-workspace-empty"><LoaderCircle className="is-spinning" /><strong>Loading devices…</strong></div> : devices.length === 0 ? <div className="device-workspace-empty"><Cpu /><strong>No devices configured</strong><p>Add a protocol device to start defining datasources.</p></div> : (
        <div className="device-stack">{devices.map((entity) => (
          <article key={entity.id} className="device-card">
            <div className="device-row">
              <button type="button" className="device-expand" onClick={() => void toggleDevice(entity.id)}>{expanded === entity.id ? <ChevronDown /> : <ChevronRight />}<Cpu /><span><strong>{entity.name}</strong><small>Modbus Unit {entity.config.unit_id}</small></span></button>
              <div><span className={entity.enabled ? "is-enabled" : "is-paused"}>{entity.enabled ? "Enabled" : "Paused"}</span><button type="button" aria-label={`Edit ${entity.name}`} onClick={() => { setError(null); setEditingDevice(entity); setShowDeviceForm(true); }}><Pencil size={17} /></button><button type="button" aria-label={`Delete ${entity.name}`} onClick={() => void removeDevice(entity)}><Trash2 size={17} /></button></div>
            </div>
            {expanded === entity.id && <div className="datasource-panel">
              <div className="datasource-heading"><div><strong>Datasources</strong><small>{datasources[entity.id]?.length ?? 0} configured</small></div><button type="button" onClick={() => { setError(null); setDatasourceFormFor(entity.id); }}><Plus size={16} /> Add datasource</button></div>
              {datasourceFormFor === entity.id && <DatasourceForm deviceId={entity.id} onCancel={() => setDatasourceFormFor(null)} onCreated={(created) => { setDatasources((current) => ({ ...current, [entity.id]: [...(current[entity.id] ?? []), created] })); setDatasourceFormFor(null); }} onError={setError} />}
              {(datasources[entity.id] ?? []).length === 0 ? <p className="datasource-empty">No datasource reads configured.</p> : (datasources[entity.id] ?? []).map((datasource) => <DatasourceRow key={datasource.id} datasource={datasource} monitoringEnabled={entity.enabled && datasource.enabled} onUpdated={(updated) => setDatasources((current) => ({ ...current, [entity.id]: current[entity.id].map((item) => item.id === updated.id ? updated : item) }))} onDelete={() => void removeDatasource(datasource)} onError={setError} />)}
            </div>}
          </article>
        ))}</div>
      )}
    </section>
  );
}

function DeviceForm({ vgatewayId, initial, onCancel, onSaved, onError }: { vgatewayId: string; initial: Device | null; onCancel: () => void; onSaved: (device: Device) => void; onError: ErrorSetter }) {
  const [pending, setPending] = useState(false);
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault(); const data = new FormData(event.currentTarget); setPending(true); onError(null);
    try { const input = { name: String(data.get("name")), description: String(data.get("description") || "") || null, enabled: data.get("enabled") === "on", config: { unit_id: Number(data.get("unit_id")) } }; onSaved(initial ? await updateDevice(initial.id, input) : await createDevice(vgatewayId, { ...input, type: "modbus_device" })); }
    catch (caught) { onError(errorMessage(caught)); } finally { setPending(false); }
  }
  return <form className="topology-form" onSubmit={(event) => void submit(event)}><label>Name<input name="name" required maxLength={100} defaultValue={initial?.name} /></label><label>Unit ID<input name="unit_id" type="number" min="0" max="255" defaultValue={initial?.config.unit_id ?? 1} required /></label><label className="is-wide">Description<input name="description" defaultValue={initial?.description ?? ""} /></label><label className="topology-toggle"><input name="enabled" type="checkbox" defaultChecked={initial?.enabled ?? true} /> Enabled</label><div><button type="button" onClick={onCancel}>Cancel</button><button type="submit" className="is-primary" disabled={pending}>{pending ? "Saving…" : initial ? "Update device" : "Save device"}</button></div></form>;
}

function DatasourceForm({ deviceId, initial = null, onCancel, onCreated, onError }: { deviceId: string; initial?: Datasource | null; onCancel: () => void; onCreated: (datasource: Datasource) => void; onError: ErrorSetter }) {
  const [pending, setPending] = useState<"preview" | "save" | null>(null);
  const [sample, setSample] = useState<DatasourceSample | null>(null);
  const [functionCode, setFunctionCode] = useState(initial?.config.function_code ?? 3);
  function request(form: HTMLFormElement): CreateDatasourceRequest { const data = new FormData(form); return { name: String(data.get("name")), type: "modbus_read", description: String(data.get("description") || "") || null, enabled: data.get("enabled") === "on", config: { function_code: Number(data.get("function_code")) as 1 | 2 | 3 | 4, start_address: Number(data.get("start_address")), quantity: Number(data.get("quantity")), poll_interval_ms: Number(data.get("poll_interval_ms")) } }; }
  async function preview(form: HTMLFormElement) {
    if (!form.reportValidity()) return;
    setPending("preview"); setSample(null); onError(null);
    try { setSample(await previewDatasource(deviceId, request(form))); }
    catch (caught) { onError(errorMessage(caught)); }
    finally { setPending(null); }
  }
  async function submit(event: React.FormEvent<HTMLFormElement>) { event.preventDefault(); setPending("save"); onError(null); try { const input = request(event.currentTarget); onCreated(initial ? await updateDatasource(initial.id, { name: input.name, description: input.description, enabled: input.enabled, config: input.config }) : await createDatasource(deviceId, input)); } catch (caught) { onError(errorMessage(caught)); } finally { setPending(null); } }
  return <form className="topology-form datasource-form" onSubmit={(event) => void submit(event)}><label>Name<input name="name" required maxLength={100} defaultValue={initial?.name} /></label><label>Function<select name="function_code" value={functionCode} onChange={(event) => setFunctionCode(Number(event.target.value) as 1 | 2 | 3 | 4)}><option value="1">01 · Coils</option><option value="2">02 · Discrete inputs</option><option value="3">03 · Holding registers</option><option value="4">04 · Input registers</option></select></label><label>Start address<input name="start_address" type="number" min="0" max="65535" defaultValue={initial?.config.start_address ?? 0} required /></label><label>Quantity<input name="quantity" type="number" min="1" max={modbusQuantityLimit(functionCode)} defaultValue={initial?.config.quantity ?? 1} required /></label><label>Poll interval (ms)<input name="poll_interval_ms" type="number" min="100" max="86400000" defaultValue={initial?.config.poll_interval_ms ?? 60000} required /></label><label>Description<input name="description" defaultValue={initial?.description ?? ""} /></label><label className="topology-toggle"><input name="enabled" type="checkbox" defaultChecked={initial?.enabled ?? true} /> Enabled</label><div><button type="button" onClick={() => { onError(null); onCancel(); }}>Cancel</button><button type="button" disabled={pending !== null} onClick={(event) => void preview(event.currentTarget.form!)}><RefreshCw size={15} /> {pending === "preview" ? "Reading…" : "Preview"}</button><button type="submit" className="is-primary" disabled={pending !== null}>{pending === "save" ? "Saving…" : initial ? "Update" : "Save"}</button></div>{sample && <SampleView sample={sample} />}</form>;
}

function DatasourceRow({ datasource, monitoringEnabled, onUpdated, onDelete, onError }: { datasource: Datasource; monitoringEnabled: boolean; onUpdated: (datasource: Datasource) => void; onDelete: () => void; onError: ErrorSetter }) {
  const [sample, setSample] = useState<DatasourceSample | null>(null); const [monitoring, setMonitoring] = useState(false); const [controller, setController] = useState<AbortController | null>(null);
  const [editing, setEditing] = useState(false);
  useEffect(() => () => controller?.abort(), [controller]);
  async function preview() { onError(null); setSample(null); try { setSample(await previewSavedDatasource(datasource.id)); } catch (caught) { onError(errorMessage(caught)); } }
  function toggleMonitor() { if (controller) { controller.abort(); setController(null); setMonitoring(false); return; } onError(null); const next = new AbortController(); setController(next); setMonitoring(true); void monitorDatasource(datasource.id, setSample, next.signal).catch((caught) => { if (!next.signal.aborted) onError(errorMessage(caught)); }).finally(() => { setMonitoring(false); setController((current) => current === next ? null : current); }); }
  const config = datasource.config;
  return <div className="datasource-row"><div className="datasource-summary"><Radio size={17} /><span><strong>{datasource.name}</strong><small>FC{String(config.function_code).padStart(2, "0")} · {config.start_address}–{config.start_address + config.quantity - 1} · {config.poll_interval_ms} ms</small></span><span className={monitoring ? "is-live" : monitoringEnabled ? "is-idle" : "is-paused"}>{monitoring ? "Live" : monitoringEnabled ? "Ready" : "Paused"}</span><button type="button" onClick={() => void preview()}><RefreshCw size={15} /> Read once</button><button type="button" disabled={!monitoringEnabled} className={monitoring ? "is-monitoring" : ""} onClick={toggleMonitor}><Activity size={15} /> {monitoring ? "Stop" : "Monitor"}</button><button type="button" aria-label={`Edit ${datasource.name}`} onClick={() => { onError(null); setEditing(true); }}><Pencil size={16} /></button><button type="button" aria-label={`Delete ${datasource.name}`} onClick={onDelete}><Trash2 size={16} /></button></div>{editing && <DatasourceForm deviceId={datasource.device_id} initial={datasource} onCancel={() => setEditing(false)} onCreated={(updated) => { onUpdated(updated); setEditing(false); }} onError={onError} />}{sample && <SampleView sample={sample} />}</div>;
}

function SampleView({ sample }: { sample: DatasourceSample }) {
  const values = sample.data.registers ?? sample.data.bits ?? [];
  return <div className={`sample-view is-${sample.quality}`}><div><strong>{sample.quality === "good" ? "Live sample" : sample.error ?? "Read error"}</strong><span>{new Date(sample.observed_at).toLocaleTimeString()} · {sample.latency_ms.toFixed(2)} ms · sequence {sample.sequence}</span></div><code>{sample.raw_hex || "No raw payload"}</code>{values.length > 0 && <div className="sample-values">{values.map((value) => <span key={value.address}><small>{value.address}</small><strong>{typeof value.value === "boolean" ? String(value.value) : value.value}</strong>{"hex" in value && <em>{value.hex}</em>}</span>)}</div>}</div>;
}

export default DeviceWorkspace;
