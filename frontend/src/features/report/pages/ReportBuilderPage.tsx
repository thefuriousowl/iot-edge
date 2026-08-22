import axios from "axios";
import { ArrowDown, ArrowLeft, ArrowUp, Check, CircleAlert, Columns3, FileChartColumn, LoaderCircle, Save, Sigma } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { getDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import { createReport, getReport, updateReport } from "../../../services/report.service";
import type { DataLogger, DataLoggerAggregate, DataLoggerQueryBucket, DataLoggerQueryMode } from "../../../types/datalogger";
import type { ReportColumnRequest, SaveReportRequest } from "../../../types/report";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./ReportPages.css";

const buckets: DataLoggerQueryBucket[] = ["1m", "5m", "15m", "1h", "6h", "1d", "1w"];
const numericAggregates: DataLoggerAggregate[] = ["min", "max", "avg", "sum", "count", "first", "last"];
const boolAggregates: DataLoggerAggregate[] = ["count", "first", "last"];

function localTimezone(): string {
  return Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC";
}

function timezoneValues(): string[] {
  const extended = Intl as typeof Intl & { supportedValuesOf?: (key: "timeZone") => string[] };
  return extended.supportedValuesOf?.("timeZone") ?? ["UTC", "Asia/Bangkok"];
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return error instanceof Error ? error.message : "Unable to save Report";
}

async function loadAllLoggers(signal: AbortSignal): Promise<DataLogger[]> {
  const values: DataLogger[] = [];
  let page = 1;
  while (true) {
    const result = await listDataLoggers({ page, per_page: 100 }, signal);
    values.push(...result.data);
    if (page >= result.pagination.total_pages) return values;
    page++;
  }
}

function ReportBuilderPage() {
  const { id } = useParams();
  const editing = Boolean(id);
  const navigate = useNavigate();
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [loggers, setLoggers] = useState<DataLogger[]>([]);
  const [source, setSource] = useState<DataLogger | null>(null);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [loggerID, setLoggerID] = useState("");
  const [timezone, setTimezone] = useState(localTimezone());
  const [mode, setMode] = useState<DataLoggerQueryMode>("raw");
  const [bucket, setBucket] = useState<DataLoggerQueryBucket>("1h");
  const [columns, setColumns] = useState<ReportColumnRequest[]>([]);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([loadAllLoggers(controller.signal), id ? getReport(id, controller.signal) : Promise.resolve(null)]).then(async ([loggerList, entity]) => {
      if (controller.signal.aborted) return;
      setLoggers(loggerList);
      const selectedLoggerID = entity?.logger_id ?? loggerList[0]?.id ?? "";
      if (entity) {
        setName(entity.name); setDescription(entity.description ?? ""); setTimezone(entity.timezone); setMode(entity.mode); setBucket(entity.bucket ?? "1h");
        setColumns((entity.columns ?? []).map((column) => ({ tag_id: column.tag_id, name: column.name, aggregate: column.aggregate })));
      }
      setLoggerID(selectedLoggerID);
      if (selectedLoggerID) setSource(await getDataLogger(selectedLoggerID, controller.signal));
      if (!controller.signal.aborted) setState("ready");
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) { setMessage(errorMessage(error)); setState("error"); }
    });
    return () => controller.abort();
  }, [id]);

  const selectedIDs = useMemo(() => new Set(columns.map((column) => column.tag_id)), [columns]);

  async function changeSource(nextID: string) {
    setLoggerID(nextID);
    setColumns([]);
    setMessage("");
    if (!nextID) { setSource(null); return; }
    try { setSource(await getDataLogger(nextID)); } catch (error) { setMessage(errorMessage(error)); }
  }

  function toggleTag(tagID: string, checked: boolean) {
    if (!source) return;
    if (!checked) { setColumns((values) => values.filter((column) => column.tag_id !== tagID)); return; }
    const tag = source.tags?.find((candidate) => candidate.id === tagID);
    if (!tag) return;
    const aggregate = tag.data_type === "bool" ? "count" : "avg";
    setColumns((values) => [...values, { tag_id: tag.id, name: tag.name, aggregate }]);
  }

  function updateColumn(index: number, patch: Partial<ReportColumnRequest>) {
    setColumns((values) => values.map((column, current) => current === index ? { ...column, ...patch } : column));
  }

  function moveColumn(index: number, direction: number) {
    const target = index + direction;
    if (target < 0 || target >= columns.length) return;
    setColumns((values) => { const next = [...values]; [next[index], next[target]] = [next[target], next[index]]; return next; });
  }

  async function save(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const aliases = columns.map((column) => column.name.trim().toLowerCase());
    if (!name.trim() || !loggerID || columns.length === 0) { setMessage("Name, source Data Logger, and at least one column are required"); return; }
    if (columns.some((column) => !column.name.trim()) || new Set(aliases).size !== aliases.length) { setMessage("Column names are required and must be unique"); return; }
    const request: SaveReportRequest = { name: name.trim(), description: description.trim() || null, logger_id: loggerID, timezone, mode, ...(mode === "aggregate" ? { bucket } : {}), columns: columns.map((column) => ({ ...column, name: column.name.trim(), ...(mode === "raw" ? { aggregate: undefined } : {}) })) };
    setSaving(true); setMessage("");
    try {
      const saved = editing && id ? await updateReport(id, request) : await createReport(request);
      navigate(`/reports/${saved.id}`, { replace: true });
    } catch (error) { setMessage(errorMessage(error)); } finally { setSaving(false); }
  }

  if (state !== "ready") return <VGatewayShell breadcrumb={<>Reports <span>/</span> <strong>{editing ? "Edit" : "New"}</strong></>}><div className="report-content"><div className={`report-state ${state === "error" ? "is-error" : ""}`} role={state === "error" ? "alert" : "status"}>{state === "loading" ? <LoaderCircle className="is-spinning" /> : <CircleAlert />}<strong>{state === "loading" ? "Loading Report builder…" : "Couldn’t load Report"}</strong><span>{message}</span></div></div></VGatewayShell>;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Reports <span>/</span> <strong>{editing ? "Edit" : "New"}</strong></>}>
    <form className="report-builder" onSubmit={save}>
      <header className="report-builder-heading"><div><Link to="/reports"><ArrowLeft size={17} /> Reports</Link><h1>{editing ? "Edit Report" : "New Report"}</h1><p>Map Data Logger Tags into ordered, reusable report columns.</p></div><button type="submit" disabled={saving}>{saving ? <LoaderCircle className="is-spinning" /> : <Save />} {saving ? "Saving…" : "Save Report"}</button></header>
      {message && <div className="report-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      <section className="report-builder-card"><header><FileChartColumn /><div><h2>Definition</h2><p>One Report reads from one synchronized Data Logger.</p></div></header><div className="report-definition-grid"><label><span>Name</span><input value={name} maxLength={100} onChange={(event) => setName(event.target.value)} required /></label><label><span>Source Data Logger</span><select value={loggerID} onChange={(event) => void changeSource(event.target.value)} required><option value="">Select Logger</option>{loggers.map((logger) => <option key={logger.id} value={logger.id}>{logger.name}</option>)}</select></label><label><span>Timezone</span><select value={timezone} onChange={(event) => setTimezone(event.target.value)}>{timezoneValues().map((value) => <option key={value} value={value}>{value}</option>)}</select></label><label><span>Mode</span><select value={mode} onChange={(event) => setMode(event.target.value as DataLoggerQueryMode)}><option value="raw">Raw synchronized batches</option><option value="aggregate">Time-bucketed aggregation</option></select></label>{mode === "aggregate" && <label><span>Bucket</span><select value={bucket} onChange={(event) => setBucket(event.target.value as DataLoggerQueryBucket)}>{buckets.map((value) => <option key={value} value={value}>{value}</option>)}</select></label>}<label className="is-wide"><span>Description</span><textarea value={description} onChange={(event) => setDescription(event.target.value)} /></label></div></section>
      <section className="report-builder-card"><header><Columns3 /><div><h2>Columns</h2><p>Select Tags, customize headers, choose aggregation, and arrange output order.</p></div><strong>{columns.length} selected</strong></header>{!source ? <div className="report-inline-empty">Select a source Data Logger first.</div> : <div className="report-tag-picker">{(source.tags ?? []).map((tag) => <label key={tag.id}><input type="checkbox" checked={selectedIDs.has(tag.id)} onChange={(event) => toggleTag(tag.id, event.target.checked)} /><span><strong>{tag.name}</strong><small>{tag.type} · {tag.data_type}</small></span></label>)}</div>}</section>
      {columns.length > 0 && <section className="report-column-editor"><header><Sigma /><div><h2>Output schema</h2><p>Column aliases become CSV headers exactly as shown.</p></div></header><div className="report-column-list">{columns.map((column, index) => { const tag = source?.tags?.find((candidate) => candidate.id === column.tag_id); const aggregateOptions = tag?.data_type === "bool" ? boolAggregates : numericAggregates; return <article key={column.tag_id}><span className="report-column-position">{index + 1}</span><div><strong>{tag?.name ?? column.tag_id}</strong><small>{tag?.data_type}</small></div><label><span>Column name</span><input aria-label={`Column name for ${tag?.name ?? column.tag_id}`} value={column.name} maxLength={100} onChange={(event) => updateColumn(index, { name: event.target.value })} /></label>{mode === "aggregate" ? <label><span>Aggregation</span><select aria-label={`Aggregation for ${tag?.name ?? column.tag_id}`} value={column.aggregate} onChange={(event) => updateColumn(index, { aggregate: event.target.value as DataLoggerAggregate })}>{aggregateOptions.map((value) => <option key={value} value={value}>{value.toUpperCase()}</option>)}</select></label> : <span className="report-raw-value"><Check size={16} /> Raw value</span>}<div className="report-column-actions"><button type="button" aria-label={`Move ${tag?.name} up`} disabled={index === 0} onClick={() => moveColumn(index, -1)}><ArrowUp /></button><button type="button" aria-label={`Move ${tag?.name} down`} disabled={index === columns.length - 1} onClick={() => moveColumn(index, 1)}><ArrowDown /></button></div></article>; })}</div></section>}
    </form>
  </VGatewayShell>;
}

export default ReportBuilderPage;
