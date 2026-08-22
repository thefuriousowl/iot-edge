import axios from "axios";
import {
  ArrowLeft,
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Clock3,
  DatabaseZap,
  Download,
  HardDrive,
  LoaderCircle,
  Pencil,
  RefreshCw,
  TableProperties,
  Tags,
} from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { getDataLogger, getDataLoggerHistory } from "../../../services/datalogger.service";
import type { DataLogger, DataLoggerHistoryResponse, DataLoggerRawValue } from "../../../types/datalogger";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { dateToLocalInput, formatInTimezone, nextDataLoggerRun, scheduleSummary, zonedDateTimeToDate } from "../utils/schedule";
import { defaultEstimatedRowBytes, estimateDataLoggerStorage, formatEstimatedDuration, formatStorageBytes } from "../utils/storage";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DataLoggerListPage.css";
import "./DataLoggerDetailPage.css";

type LoadState = "loading" | "ready" | "error";
type DefinitionState = "active" | "scheduled" | "paused" | "completed";

const perPage = 100;
const emptyHistory: DataLoggerHistoryResponse = {
  data: [],
  last_batch_at: null,
  pagination: { page: 1, per_page: perPage, total: 0, total_pages: 0 },
};

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function validDateQuery(value: string | null): string | undefined {
  return value && !Number.isNaN(new Date(value).getTime()) ? value : undefined;
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Data Logger history. Check the API connection and try again.";
}

function formatValue(value: DataLoggerRawValue["value"]): string {
  if (value === null) return "—";
  if (typeof value === "boolean") return String(value);
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  return `"${text.replaceAll('"', '""')}"`;
}

function definitionState(logger: DataLogger, nextRun: Date | null): DefinitionState {
  if (!logger.enabled) return "paused";
  if (logger.end_at && new Date(logger.end_at) < new Date() && !nextRun) return "completed";
  if (new Date(logger.start_at) > new Date()) return "scheduled";
  return "active";
}

function DataLoggerDetailPage() {
  const { id = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const tagID = searchParams.get("tag_id") ?? undefined;
  const from = validDateQuery(searchParams.get("from"));
  const to = validDateQuery(searchParams.get("to"));
  const page = positivePage(searchParams.get("page"));
  const [logger, setLogger] = useState<DataLogger | null>(null);
  const [history, setHistory] = useState<DataLoggerHistoryResponse>(emptyHistory);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState("");
  const [filterError, setFilterError] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      setLoadState("loading");
      setLoadError("");
      try {
        const [definition, values] = await Promise.all([
          getDataLogger(id, controller.signal),
          getDataLoggerHistory(id, { tag_id: tagID, from, to, page, per_page: perPage }, controller.signal),
        ]);
        if (!controller.signal.aborted) {
          setLogger(definition);
          setHistory(values);
          setLoadState("ready");
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setLoadError(errorMessage(error));
          setLoadState("error");
        }
      }
    }
    void load();
    return () => controller.abort();
  }, [from, id, page, refreshVersion, tagID, to]);

  const tagNames = useMemo(() => new Map((logger?.tags ?? []).map((tag) => [tag.id, tag.name])), [logger]);
  const visibleBatches = useMemo(() => {
    const batches = new Map<string, { count: number; bad: number }>();
    history.data.forEach((value) => {
      const batch = batches.get(value.batch_at) ?? { count: 0, bad: 0 };
      batch.count += 1;
      if (value.quality === "bad") batch.bad += 1;
      batches.set(value.batch_at, batch);
    });
    return batches;
  }, [history.data]);
  const badOnPage = history.data.filter((value) => value.quality === "bad").length;
  const nextRun = logger ? nextDataLoggerRun(logger) : null;
  const state = logger ? definitionState(logger, nextRun) : "paused";
  const activeFilters = [tagID, from, to].filter(Boolean).length;
  const storageEstimate = useMemo(() => logger ? estimateDataLoggerStorage({ maxSizeBytes: logger.max_size_bytes, tagCount: logger.tag_count, averageRowBytes: logger.storage?.average_row_bytes ?? defaultEstimatedRowBytes, batchCount: logger.storage?.batch_count ?? 0, mode: logger.mode, config: logger.config }) : null, [logger]);
  const storagePercent = logger?.max_size_bytes && logger.storage ? Math.min(100, logger.storage.estimated_size_bytes / logger.max_size_bytes * 100) : 0;

  function updatePage(nextPage: number) {
    const next = new URLSearchParams(searchParams);
    if (nextPage > 1) next.set("page", String(nextPage)); else next.delete("page");
    setSearchParams(next, { replace: true });
  }

  function applyFilters(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!logger) return;
    const form = new FormData(event.currentTarget);
    const nextTagID = String(form.get("tag_id") ?? "");
    const fromLocal = String(form.get("from") ?? "");
    const toLocal = String(form.get("to") ?? "");
    const fromDate = fromLocal ? zonedDateTimeToDate(fromLocal, logger.timezone) : null;
    const toDate = toLocal ? zonedDateTimeToDate(toLocal, logger.timezone) : null;
    if (fromLocal && !fromDate || toLocal && !toDate) {
      setFilterError("Enter valid local date and time boundaries");
      return;
    }
    if (fromDate && toDate && toDate <= fromDate) {
      setFilterError("To must be after From");
      return;
    }
    setFilterError("");
    const next = new URLSearchParams();
    if (nextTagID) next.set("tag_id", nextTagID);
    if (fromDate) next.set("from", fromDate.toISOString());
    if (toDate) next.set("to", toDate.toISOString());
    setSearchParams(next, { replace: true });
  }

  function filterTag(nextTagID: string) {
    const next = new URLSearchParams(searchParams);
    if (nextTagID) next.set("tag_id", nextTagID); else next.delete("tag_id");
    next.delete("page");
    setSearchParams(next, { replace: true });
  }

  function exportCurrentPage() {
    if (!logger || history.data.length === 0) return;
    const header = ["logger_id", "logger_name", "tag_id", "tag_name", "batch_at", "observed_at", "data_type", "value", "quality", "error", "persisted_at"];
    const rows = history.data.map((value) => [value.logger_id, logger.name, value.tag_id, tagNames.get(value.tag_id) ?? value.tag_id, value.batch_at, value.observed_at, value.data_type, value.value, value.quality, value.error ?? "", value.persisted_at]);
    const blob = new Blob([[header, ...rows].map((row) => row.map(csvCell).join(",")).join("\n")], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${logger.name.replaceAll(/[^a-z0-9]+/gi, "-").replaceAll(/^-|-$/g, "").toLowerCase() || "data-logger"}-page-${history.pagination.page}.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Loggers <span>/</span> <strong>{logger?.name ?? "History"}</strong></>}>
    <div className="datalogger-detail-content">
      <header className="datalogger-detail-heading">
        <div><Link to="/data-loggers"><ArrowLeft size={17} /> Back to Data Loggers</Link><h1>{logger?.name ?? "Data Logger history"}</h1><p>{logger?.description ?? "Definition, schedule, and durable synchronized Tag history."}</p></div>
        {logger && <div className="datalogger-detail-actions"><Link to={`/data-loggers/${logger.id}/query`}><TableProperties size={17} /> Query data</Link><button type="button" disabled={history.data.length === 0} onClick={exportCurrentPage}><Download size={17} /> Export page CSV</button><Link to={`/data-loggers/${logger.id}/edit`}><Pencil size={17} /> Edit</Link></div>}
      </header>

      {loadState === "loading" && <section className="datalogger-detail-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Data Logger history…</strong></section>}
      {loadState === "error" && <section className="datalogger-detail-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Data Logger history</strong><span>{loadError}</span><button type="button" onClick={() => setRefreshVersion((value) => value + 1)}>Try again</button></section>}

      {loadState === "ready" && logger && <>
        <section className="datalogger-detail-metrics" aria-label="Data Logger runtime summary">
          <article><DatabaseZap /><span>Definition state</span><strong className={`is-${state}`}>{state}</strong><small>{logger.enabled ? "Runtime reconciliation enabled" : "Definition is paused"}</small></article>
          <article><Clock3 /><span>Last capture</span><strong>{history.last_batch_at ? formatInTimezone(new Date(history.last_batch_at), logger.timezone) : "No captures yet"}</strong><small>{logger.timezone}</small></article>
          <article><CalendarClock /><span>Next run</span><strong>{nextRun ? formatInTimezone(nextRun, logger.timezone) : "No future run"}</strong><small>{scheduleSummary(logger.mode, logger.config)}</small></article>
          <article><Tags /><span>Selected Tags</span><strong>{logger.tag_count}</strong><small>{history.pagination.total} matching stored values</small></article>
        </section>

        <section className="datalogger-storage-card" aria-labelledby="datalogger-storage-summary-heading">
          <header><div><HardDrive /><div><h2 id="datalogger-storage-summary-heading">Rolling storage retention</h2><p>Quota accounting preserves complete synchronized batches. PostgreSQL disk allocation may be higher.</p></div></div><strong>{logger.max_size_bytes === null ? "Unlimited" : `${formatStorageBytes(logger.storage?.estimated_size_bytes ?? 0)} / ${formatStorageBytes(logger.max_size_bytes)}`}</strong></header>
          {logger.max_size_bytes !== null && <div className="datalogger-storage-progress" role="progressbar" aria-label="Estimated Data Logger storage usage" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.round(storagePercent)}><span style={{ width: `${storagePercent}%` }} /></div>}
          <div className="datalogger-storage-summary">
            <span><small>Stored rows</small><strong>{(logger.storage?.row_count ?? 0).toLocaleString()}</strong><em>{(logger.storage?.batch_count ?? 0).toLocaleString()} complete batches</em></span>
            <span><small>Estimated capacity</small><strong>{storageEstimate ? storageEstimate.capacityRows.toLocaleString() : "Unlimited"}</strong><em>{storageEstimate ? `${storageEstimate.capacityBatches.toLocaleString()} batches` : "No automatic pruning"}</em></span>
            <span><small>Retained history</small><strong>{storageEstimate ? formatEstimatedDuration(storageEstimate.estimatedRetentionSeconds) : "Unlimited"}</strong><em>{storageEstimate && (logger.storage?.batch_count ?? 0) >= storageEstimate.capacityBatches ? "Oldest batches roll off automatically" : storageEstimate ? `First rollover in about ${formatEstimatedDuration(storageEstimate.estimatedSecondsUntilRollover)}` : "Subject to available database storage"}</em></span>
            <span><small>Current window</small><strong>{logger.storage?.oldest_batch_at ? formatInTimezone(new Date(logger.storage.oldest_batch_at), logger.timezone) : "No captures yet"}</strong><em>{logger.storage?.newest_batch_at ? `through ${formatInTimezone(new Date(logger.storage.newest_batch_at), logger.timezone)}` : logger.timezone}</em></span>
          </div>
        </section>

        <div className="datalogger-detail-grid">
          <section className="datalogger-detail-card"><header><div><CalendarClock /><h2>Schedule definition</h2></div><span>{logger.mode}</span></header><dl>
            <div><dt>Cadence</dt><dd>{scheduleSummary(logger.mode, logger.config)}</dd></div>
            <div><dt>Timezone</dt><dd>{logger.timezone}</dd></div>
            <div><dt>Starts</dt><dd>{formatInTimezone(new Date(logger.start_at), logger.timezone)}</dd></div>
            <div><dt>Ends</dt><dd>{logger.end_at ? formatInTimezone(new Date(logger.end_at), logger.timezone) : "No end boundary"}</dd></div>
          </dl></section>
          <section className="datalogger-detail-card"><header><div><Tags /><h2>Selected Tags</h2></div><button type="button" className={!tagID ? "is-selected" : ""} onClick={() => filterTag("")}>All</button></header><div className="datalogger-detail-tags">{(logger.tags ?? []).map((tag) => <button type="button" key={tag.id} className={tagID === tag.id ? "is-selected" : ""} onClick={() => filterTag(tag.id)}><span><strong>{tag.name}</strong><small>{tag.type} · {tag.data_type}{tag.enabled ? "" : " · disabled"}</small></span><i /></button>)}</div></section>
        </div>

        <section className="datalogger-history-section">
          <header><div><h2>Persistent history</h2><p>Each row preserves the shared Logger batch time and original Tag observation time.</p></div><button type="button" onClick={() => setRefreshVersion((value) => value + 1)}><RefreshCw size={16} /> Refresh</button></header>
          <form className="datalogger-history-filters" onSubmit={applyFilters}>
            <label><span>Tag</span><select name="tag_id" key={tagID ?? "all"} defaultValue={tagID ?? ""}><option value="">All selected Tags</option>{(logger.tags ?? []).map((tag) => <option key={tag.id} value={tag.id}>{tag.name}</option>)}</select></label>
            <label><span>From <small>{logger.timezone}</small></span><input type="datetime-local" name="from" key={from ?? "from"} defaultValue={from ? dateToLocalInput(from, logger.timezone) : ""} /></label>
            <label><span>To <small>{logger.timezone}</small></span><input type="datetime-local" name="to" key={to ?? "to"} defaultValue={to ? dateToLocalInput(to, logger.timezone) : ""} /></label>
            <button type="submit">Apply filters</button>
          </form>
          {filterError && <div className="datalogger-history-filter-error" role="alert"><CircleAlert size={16} />{filterError}</div>}
          {activeFilters > 0 && <div className="datalogger-active-filters"><span>{activeFilters} active {activeFilters === 1 ? "filter" : "filters"}</span><button type="button" onClick={() => setSearchParams({}, { replace: true })}>Clear all</button></div>}

          <div className="datalogger-history-strip" role="region" aria-label="Visible history summary"><span><strong>{visibleBatches.size}</strong> visible batches</span><span><strong>{history.data.length}</strong> values on page</span><span className={badOnPage ? "is-bad" : ""}><strong>{badOnPage}</strong> bad on page</span></div>

          {history.data.length === 0 ? <div className="datalogger-history-empty"><DatabaseZap /><strong>{activeFilters ? "No matching history" : "No persistent history yet"}</strong><span>{activeFilters ? "Adjust the Tag or time range filters." : "The runtime will store the first synchronized snapshot at the next eligible run."}</span></div> : <div className="datalogger-history-scroll"><table><thead><tr><th>Batch</th><th>Tag</th><th>Value</th><th>Quality</th><th>Visible batch</th><th>Observed</th></tr></thead><tbody>{history.data.map((value) => {
            const batch = visibleBatches.get(value.batch_at)!;
            const expected = tagID ? 1 : logger.tag_count;
            const batchQuality = batch.bad === batch.count ? "failed" : batch.bad > 0 || batch.count < expected ? "partial" : "good";
            return <tr key={`${value.batch_at}-${value.tag_id}`}><td data-label="Batch"><strong>{formatInTimezone(new Date(value.batch_at), logger.timezone)}</strong><small>Stored {formatInTimezone(new Date(value.persisted_at), logger.timezone)}</small></td><td data-label="Tag"><strong>{tagNames.get(value.tag_id) ?? "Unknown Tag"}</strong><small>{value.data_type}</small></td><td data-label="Value">{formatValue(value.value)}</td><td data-label="Quality"><span className={`datalogger-history-quality is-${value.quality}`}>{value.quality}</span>{value.error && <small className="is-error">{value.error}</small>}</td><td data-label="Visible batch"><span className={`datalogger-batch-quality is-${batchQuality}`}>{batchQuality}</span><small>{batch.count - batch.bad}/{batch.count} good visible values</small></td><td data-label="Observed">{formatInTimezone(new Date(value.observed_at), logger.timezone)}</td></tr>;
          })}</tbody></table></div>}

          <footer className="datalogger-history-pagination"><p>Showing {history.data.length} of {history.pagination.total} matching values · CSV exports this page only</p><div><button type="button" aria-label="Previous history page" disabled={page <= 1} onClick={() => updatePage(Math.max(1, page - 1))}><ChevronLeft size={19} /></button><span aria-label={`History page ${history.pagination.page}`}>{history.pagination.page}</span><button type="button" aria-label="Next history page" disabled={history.pagination.total_pages === 0 || page >= history.pagination.total_pages} onClick={() => updatePage(page + 1)}><ChevronRight size={19} /></button></div></footer>
        </section>
      </>}
    </div>
  </VGatewayShell>;
}

export default DataLoggerDetailPage;
