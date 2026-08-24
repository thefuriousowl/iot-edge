import axios from "axios";
import {
  ArchiveRestore,
  CalendarClock,
  CheckCircle2,
  CircleAlert,
  DatabaseZap,
  ExternalLink,
  FileChartColumn,
  HardDrive,
  LoaderCircle,
  RefreshCw,
  SearchCheck,
  Settings2,
  Trash2,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import {
  cleanupDataLoggerRetention,
  getDataLoggerRetention,
  getDataManagementOverview,
  listDataLoggers,
  previewDataLoggerRetention,
} from "../../../services/datalogger.service";
import type {
  DataLogger,
  DataManagementOverview,
  RetentionCleanupResult,
  RetentionMetrics,
  RetentionPlan,
  RetentionStatus,
} from "../../../types/datalogger";
import { formatEstimatedDuration, formatStorageBytes } from "../../datalogger/utils/storage";
import SettingsWorkspace from "../components/SettingsWorkspace";

type LoadState = "loading" | "ready" | "error";

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Data Management. Check the API connection and try again.";
}

function formatDateTime(value: string | null): string {
  if (!value) return "No history";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Unavailable";
  return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

function historyDuration(metrics: RetentionMetrics): string {
  if (!metrics.oldest_batch_at || !metrics.newest_batch_at) return "No retained history";
  const seconds = Math.max(0, (new Date(metrics.newest_batch_at).getTime() - new Date(metrics.oldest_batch_at).getTime()) / 1000);
  return seconds === 0 ? "Single capture boundary" : formatEstimatedDuration(seconds);
}

function policySummary(logger: DataLogger): string {
  const policies: string[] = [];
  if (logger.max_size_bytes !== null) policies.push(formatStorageBytes(logger.max_size_bytes));
  if (logger.max_age_seconds != null) policies.push(formatEstimatedDuration(logger.max_age_seconds));
  return policies.length > 0 ? policies.join(" · ") : "No automatic retention";
}

function countLabel(count: number, singular: string, plural = `${singular}s`): string {
  return `${count.toLocaleString()} ${count === 1 ? singular : plural}`;
}

async function loadAllDataLoggers(signal: AbortSignal): Promise<DataLogger[]> {
  const loggers: DataLogger[] = [];
  let page = 1;
  while (true) {
    const response = await listDataLoggers({ page, per_page: 100 }, signal);
    loggers.push(...response.data);
    if (page >= response.pagination.total_pages) return loggers;
    page += 1;
  }
}

function Metrics({ label, metrics }: { label: string; metrics: RetentionMetrics }) {
  return <article className="settings-metrics-card"><span>{label}</span><strong>{countLabel(metrics.row_count, "row")}</strong><small>{countLabel(metrics.batch_count, "synchronized batch", "synchronized batches")} · {formatStorageBytes(metrics.estimated_size_bytes)}</small><dl><div><dt>Oldest</dt><dd>{formatDateTime(metrics.oldest_batch_at)}</dd></div><div><dt>Newest</dt><dd>{formatDateTime(metrics.newest_batch_at)}</dd></div></dl></article>;
}

function DataManagementSettingsPage() {
  const [overview, setOverview] = useState<DataManagementOverview | null>(null);
  const [loggers, setLoggers] = useState<DataLogger[]>([]);
  const [selectedLoggerID, setSelectedLoggerID] = useState("");
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState("");
  const [status, setStatus] = useState<RetentionStatus | null>(null);
  const [statusState, setStatusState] = useState<LoadState>("loading");
  const [statusError, setStatusError] = useState("");
  const [preview, setPreview] = useState<RetentionPlan | null>(null);
  const [previewing, setPreviewing] = useState(false);
  const [previewError, setPreviewError] = useState("");
  const [cleanupOpen, setCleanupOpen] = useState(false);
  const [cleanupAcknowledged, setCleanupAcknowledged] = useState(false);
  const [batchLimit, setBatchLimit] = useState("1000");
  const [cleaning, setCleaning] = useState(false);
  const [cleanupError, setCleanupError] = useState("");
  const [cleanupResult, setCleanupResult] = useState<RetentionCleanupResult | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([getDataManagementOverview(controller.signal), loadAllDataLoggers(controller.signal)]).then(([overviewResponse, loggerResponse]) => {
      if (controller.signal.aborted) return;
      setOverview(overviewResponse);
      setLoggers(loggerResponse);
      setSelectedLoggerID((current) => loggerResponse.some((logger) => logger.id === current) ? current : loggerResponse[0]?.id ?? "");
      setLoadState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setLoadError(errorMessage(error));
      setLoadState("error");
    });
    return () => controller.abort();
  }, [refreshVersion]);

  useEffect(() => {
    if (!selectedLoggerID) return;
    const controller = new AbortController();
    void getDataLoggerRetention(selectedLoggerID, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      setStatus(response);
      setStatusState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setStatusError(errorMessage(error));
      setStatusState("error");
    });
    return () => controller.abort();
  }, [refreshVersion, selectedLoggerID]);

  const selectedLogger = useMemo(() => loggers.find((logger) => logger.id === selectedLoggerID) ?? null, [loggers, selectedLoggerID]);
  const plan = preview ?? status?.plan ?? null;
  const parsedBatchLimit = Number(batchLimit);
  const validBatchLimit = Number.isInteger(parsedBatchLimit) && parsedBatchLimit >= 1 && parsedBatchLimit <= 10_000;

  function refresh() {
    setLoadState("loading");
    setLoadError("");
    setStatus(null);
    setStatusState("loading");
    setStatusError("");
    setPreview(null);
    setPreviewError("");
    setCleanupResult(null);
    setCleanupOpen(false);
    setRefreshVersion((version) => version + 1);
  }

  async function runPreview() {
    if (!selectedLoggerID || previewing) return;
    setPreviewing(true);
    setPreviewError("");
    try {
      setPreview(await previewDataLoggerRetention(selectedLoggerID));
    } catch (error) {
      setPreviewError(errorMessage(error));
    } finally {
      setPreviewing(false);
    }
  }

  async function runCleanup() {
    if (!selectedLoggerID || !cleanupAcknowledged || !validBatchLimit || cleaning) return;
    setCleaning(true);
    setCleanupError("");
    try {
      const result = await cleanupDataLoggerRetention(selectedLoggerID, { confirm: true, batch_limit: parsedBatchLimit });
      setCleanupResult(result);
      setCleanupOpen(false);
      setCleanupAcknowledged(false);
      setRefreshVersion((version) => version + 1);
    } catch (error) {
      setCleanupError(errorMessage(error));
    } finally {
      setCleaning(false);
    }
  }

  return (
    <SettingsWorkspace section="Data Management">
      <section className="settings-section-heading">
        <div><p>Persisted Logger history</p><h2>Data Management</h2><span>Inspect storage, preview retention, and remove eligible history. Capture and retention policies remain owned by each Data Logger.</span></div>
        <button type="button" disabled={loadState === "loading"} onClick={refresh}><RefreshCw className={loadState === "loading" ? "is-spinning" : ""} aria-hidden="true" size={17} />Refresh</button>
      </section>

      <section className="settings-state-panel" aria-label="Data Management status">
        {loadState === "loading" && <div className="settings-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading storage workspace…</strong><span>Checking Data Logger ownership and retention availability.</span></div>}
        {loadState === "error" && <div className="settings-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Data Management</strong><span>{loadError}</span><button type="button" onClick={refresh}>Try again</button></div>}
        {loadState === "ready" && overview?.logger_count === 0 && <div className="settings-state"><DatabaseZap /><strong>No Data Loggers yet</strong><span>Create a Data Logger to persist synchronized Tag snapshots before configuring retention or querying history.</span><Link to="/data-loggers/new">Create Data Logger</Link></div>}
        {loadState === "ready" && overview && overview.logger_count > 0 && <div className="settings-ready"><div><DatabaseZap aria-hidden="true" /><span><strong>{overview.logger_count} Data Logger{overview.logger_count === 1 ? "" : "s"}</strong><small>{overview.enabled_logger_count} enabled · {overview.policy_logger_count} with retention policy</small></span></div><p>Evaluated {formatDateTime(overview.evaluated_at)}. Logical estimates and physical PostgreSQL allocation are intentionally reported separately.</p></div>}
      </section>

      {loadState === "ready" && overview && overview.logger_count > 0 && <>
        <section className="settings-storage-overview" aria-label="Storage overview">
          <Metrics label="Logical Logger history" metrics={overview.logical_history} />
          <article className="settings-allocation-card"><span>PostgreSQL physical allocation</span><strong>{formatStorageBytes(overview.postgresql_physical_allocation.total_bytes)}</strong><small>Raw history {formatStorageBytes(overview.postgresql_physical_allocation.raw_history_bytes)} · batch accounting {formatStorageBytes(overview.postgresql_physical_allocation.batch_accounting_bytes)}</small><p>Deleting rows frees space for PostgreSQL reuse; the database files may not shrink immediately.</p></article>
        </section>

        <section className="settings-retention-workspace" aria-labelledby="retention-heading">
          <header><div><p>Logger-scoped controls</p><h3 id="retention-heading">Retention workspace</h3><span>Select one Logger to inspect its current plan. Preview is read-only; cleanup permanently removes complete eligible batches.</span></div><label><span>Data Logger</span><select value={selectedLoggerID} onChange={(event) => { setStatus(null); setStatusState("loading"); setStatusError(""); setPreview(null); setPreviewError(""); setCleanupResult(null); setCleanupOpen(false); setSelectedLoggerID(event.target.value); }}>{loggers.map((logger) => <option key={logger.id} value={logger.id}>{logger.name}</option>)}</select></label></header>

          {selectedLogger && <div className="settings-logger-summary"><div><strong>{selectedLogger.name}</strong><small>{selectedLogger.enabled ? "Enabled" : "Paused"} · {selectedLogger.tag_count} Tag{selectedLogger.tag_count === 1 ? "" : "s"} · {policySummary(selectedLogger)}</small></div><nav><Link to={`/data-loggers/${selectedLogger.id}/edit`}><Settings2 size={15} />Edit policy</Link><Link to={`/data-loggers/${selectedLogger.id}/query`}><ExternalLink size={15} />Query history</Link></nav></div>}
          {cleanupResult && <div className="settings-operation-success" role="status"><CheckCircle2 />Cleanup removed {countLabel(cleanupResult.deleted.batch_count, "batch", "batches")} and {countLabel(cleanupResult.deleted.row_count, "row")}.</div>}

          {statusState === "loading" && <div className="settings-retention-state" role="status"><LoaderCircle className="is-spinning" />Loading retention status…</div>}
          {statusState === "error" && <div className="settings-retention-state is-error" role="alert"><CircleAlert />{statusError}</div>}
          {statusState === "ready" && plan && <>
            <div className="settings-policy-grid">
              <article><HardDrive /><span><small>Size policy</small><strong>{plan.policy.max_size_bytes === null ? "Unlimited" : formatStorageBytes(plan.policy.max_size_bytes)}</strong></span></article>
              <article><CalendarClock /><span><small>Age policy</small><strong>{plan.policy.max_age_seconds === null ? "Unlimited" : formatEstimatedDuration(plan.policy.max_age_seconds)}</strong></span></article>
              <article><ArchiveRestore /><span><small>Estimated retained duration</small><strong>{historyDuration(plan.estimated_retained)}</strong></span></article>
            </div>
            <div className="settings-retention-metrics"><Metrics label="Current history" metrics={plan.current} /><Metrics label="Eligible for removal" metrics={plan.remove} /><Metrics label="Estimated after cleanup" metrics={plan.estimated_retained} /></div>
            <div className="settings-retention-note"><SearchCheck /><span><strong>{preview ? "Fresh preview" : "Current retention plan"}</strong><small>Evaluated {formatDateTime(plan.evaluated_at)}{plan.cutoff_at ? ` · age cutoff ${formatDateTime(plan.cutoff_at)}` : " · no age cutoff"}</small></span></div>
            {status?.last_run && <div className={`settings-last-run ${status.last_run.error ? "is-error" : ""}`}>{status.last_run.error ? <CircleAlert /> : <CheckCircle2 />}<span><strong>{status.last_run.error ? "Latest cleanup failed" : "Latest cleanup completed"}</strong><small>{formatDateTime(status.last_run.completed_at)}{status.last_run.result ? ` · ${countLabel(status.last_run.result.deleted.batch_count, "batch", "batches")} removed` : ""}</small>{status.last_run.error && <small>{status.last_run.error}</small>}</span></div>}
            {previewError && <div className="settings-operation-error" role="alert"><CircleAlert />{previewError}</div>}
            <footer className="settings-retention-actions"><button type="button" disabled={previewing} onClick={() => void runPreview()}>{previewing ? <LoaderCircle className="is-spinning" /> : <SearchCheck />}Preview now</button><button className="is-danger" type="button" disabled={plan.remove.batch_count === 0} onClick={() => { setCleanupError(""); setCleanupOpen(true); }}><Trash2 />Review cleanup</button></footer>
          </>}
        </section>

        {cleanupOpen && selectedLogger && plan && <div className="settings-cleanup-backdrop" role="presentation"><section className="settings-cleanup-dialog" role="dialog" aria-modal="true" aria-labelledby="cleanup-title"><header><Trash2 /><div><p>Permanent history removal</p><h3 id="cleanup-title">Clean up {selectedLogger.name}?</h3></div></header><p>This run can delete up to the selected number of complete batches. It never splits a synchronized batch and cannot be undone.</p><div className="settings-cleanup-impact"><span><strong>{plan.remove.batch_count.toLocaleString()}</strong> eligible batches</span><span><strong>{plan.remove.row_count.toLocaleString()}</strong> eligible rows</span><span><strong>{formatStorageBytes(plan.remove.estimated_size_bytes)}</strong> logical estimate</span></div><div className="settings-batch-limit"><label htmlFor="retention-batch-limit">Maximum batches this run</label><input id="retention-batch-limit" type="number" min="1" max="10000" value={batchLimit} aria-invalid={!validBatchLimit} onChange={(event) => setBatchLimit(event.target.value)} /><small>1–10,000 batches. More eligible batches may remain afterward.</small></div><label className="settings-cleanup-confirm"><input type="checkbox" checked={cleanupAcknowledged} onChange={(event) => setCleanupAcknowledged(event.target.checked)} /><span>I understand this permanently deletes eligible Logger history.</span></label>{cleanupError && <div className="settings-operation-error" role="alert"><CircleAlert />{cleanupError}</div>}<footer><button type="button" disabled={cleaning} onClick={() => { setCleanupOpen(false); setCleanupAcknowledged(false); }}>Cancel</button><button className="is-danger" type="button" disabled={!cleanupAcknowledged || !validBatchLimit || cleaning} onClick={() => void runCleanup()}>{cleaning ? <LoaderCircle className="is-spinning" /> : <Trash2 />}{cleaning ? "Cleaning up…" : "Delete eligible batches"}</button></footer></section></div>}
      </>}

      <div className="settings-scope-grid">
        <article><HardDrive aria-hidden="true" /><div><h3>Storage health</h3><p>Logical Logger history and PostgreSQL allocation stay separately labeled so estimates are never presented as physical disk reclamation.</p></div></article>
        <article><Settings2 aria-hidden="true" /><div><h3>Policy ownership</h3><p>Intervals, schedules, size limits, and age retention belong to each Data Logger.</p><Link to="/data-loggers">Open Data Loggers</Link></div></article>
        <article><FileChartColumn aria-hidden="true" /><div><h3>Query and export</h3><p>Use Reports for custom columns, aggregation, exact date ranges, and full-range CSV export.</p><Link to="/reports">Open Reports</Link></div></article>
      </div>
    </SettingsWorkspace>
  );
}

export default DataManagementSettingsPage;
