import axios from "axios";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Download,
  Info,
  LoaderCircle,
  RefreshCw,
  Sigma,
  TableProperties,
} from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { getDataLogger, queryDataLogger } from "../../../services/datalogger.service";
import type {
  DataLogger,
  DataLoggerAggregate,
  DataLoggerQueryBucket,
  DataLoggerQueryMode,
  DataLoggerQueryResponse,
  DataLoggerQueryValue,
} from "../../../types/datalogger";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { dateToLocalInput, formatInTimezone, zonedDateTimeToDate } from "../utils/schedule";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DataLoggerDetailPage.css";
import "./DataLoggerQueryPage.css";

type LoadState = "loading" | "ready" | "error";

const perPage = 100;
const buckets: { value: DataLoggerQueryBucket; label: string }[] = [
  { value: "1m", label: "1 minute" },
  { value: "5m", label: "5 minutes" },
  { value: "15m", label: "15 minutes" },
  { value: "1h", label: "1 hour" },
  { value: "6h", label: "6 hours" },
  { value: "1d", label: "1 day" },
  { value: "1w", label: "1 week" },
];
const aggregates: DataLoggerAggregate[] = ["min", "max", "avg", "sum", "count", "first", "last"];
const emptyQuery: DataLoggerQueryResponse = {
  data: [],
  mode: "raw",
  pagination: { page: 1, per_page: perPage, total: 0, total_pages: 0 },
};

function validMode(value: string | null): DataLoggerQueryMode {
  return value === "aggregate" ? "aggregate" : "raw";
}

function validBucket(value: string | null): DataLoggerQueryBucket {
  return buckets.some((bucket) => bucket.value === value) ? value as DataLoggerQueryBucket : "1h";
}

function validAggregate(value: string | null): DataLoggerAggregate {
  return aggregates.includes(value as DataLoggerAggregate) ? value as DataLoggerAggregate : "avg";
}

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function formatValue(value: DataLoggerQueryValue["value"]): string {
  if (value === null || value === undefined) return "—";
  if (typeof value === "boolean") return String(value);
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to query Data Logger values. Check the API connection and try again.";
}

function csvCell(value: unknown): string {
  const text = value === null || value === undefined ? "" : String(value);
  return `"${text.replaceAll('"', '""')}"`;
}

function queryCellSummary(value: DataLoggerQueryValue | undefined, mode: DataLoggerQueryMode, aggregate: DataLoggerAggregate): string {
  if (!value) return "No sample";
  if (mode === "raw") return value.error || value.quality || "";
  if (value.supported === false) return `${aggregate} unsupported for ${value.data_type}`;
  return `${value.good_count ?? 0}/${value.total_count ?? 0} good${value.error ? `; ${value.error}` : ""}`;
}

function DataLoggerQueryPage() {
  const { id = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [defaultRange] = useState(() => {
    const to = new Date();
    to.setSeconds(0, 0);
    to.setMinutes(to.getMinutes() + 1);
    return { from: new Date(to.getTime() - 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };
  });
  const mode = validMode(searchParams.get("mode"));
  const bucket = validBucket(searchParams.get("bucket"));
  const aggregate = validAggregate(searchParams.get("aggregate"));
  const from = searchParams.get("from") ?? defaultRange.from;
  const to = searchParams.get("to") ?? defaultRange.to;
  const page = positivePage(searchParams.get("page"));
  const tagIDsParam = searchParams.get("tag_ids") ?? "";
  const requestedTagIDs = useMemo(() => tagIDsParam.split(",").map((value) => value.trim()).filter(Boolean), [tagIDsParam]);
  const [logger, setLogger] = useState<DataLogger | null>(null);
  const [result, setResult] = useState<DataLoggerQueryResponse>(emptyQuery);
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
          queryDataLogger(id, {
            mode,
            tag_ids: tagIDsParam || undefined,
            from,
            to,
            bucket: mode === "aggregate" ? bucket : undefined,
            aggregate: mode === "aggregate" ? aggregate : undefined,
            page,
            per_page: perPage,
          }, controller.signal),
        ]);
        if (!controller.signal.aborted) {
          setLogger(definition);
          setResult(values);
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
  }, [aggregate, bucket, from, id, mode, page, refreshVersion, tagIDsParam, to]);

  const selectedTags = useMemo(() => {
    const tags = logger?.tags ?? [];
    if (requestedTagIDs.length === 0) return tags;
    const selected = new Set(requestedTagIDs);
    return tags.filter((tag) => selected.has(tag.id));
  }, [logger, requestedTagIDs]);
  const badCells = result.data.reduce((total, row) => total + Object.values(row.values).filter((value) => value.quality === "bad" || (value.bad_count ?? 0) > 0).length, 0);

  function updatePage(nextPage: number) {
    const next = new URLSearchParams(searchParams);
    if (nextPage > 1) next.set("page", String(nextPage)); else next.delete("page");
    setSearchParams(next, { replace: true });
  }

  function changeMode(nextMode: DataLoggerQueryMode) {
    const next = new URLSearchParams(searchParams);
    if (nextMode === "aggregate") {
      next.set("mode", "aggregate");
    } else {
      next.delete("mode");
      next.delete("bucket");
      next.delete("aggregate");
    }
    next.delete("page");
    setSearchParams(next, { replace: true });
  }

  function applyQuery(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!logger) return;
    const form = new FormData(event.currentTarget);
    const selected = form.getAll("tag_ids").map(String);
    const fromLocal = String(form.get("from") ?? "");
    const toLocal = String(form.get("to") ?? "");
    const fromDate = zonedDateTimeToDate(fromLocal, logger.timezone);
    const toDate = zonedDateTimeToDate(toLocal, logger.timezone);
    if (selected.length === 0) {
      setFilterError("Select at least one Tag");
      return;
    }
    if (!fromDate || !toDate) {
      setFilterError("Enter valid local date and time boundaries");
      return;
    }
    if (toDate <= fromDate) {
      setFilterError("To must be after From");
      return;
    }
    if (toDate.getTime() - fromDate.getTime() > 366 * 24 * 60 * 60 * 1000) {
      setFilterError("Query range cannot exceed 366 days");
      return;
    }
    setFilterError("");
    const next = new URLSearchParams();
    if (mode === "aggregate") {
      next.set("mode", "aggregate");
      next.set("bucket", String(form.get("bucket") ?? bucket));
      next.set("aggregate", String(form.get("aggregate") ?? aggregate));
    }
    next.set("tag_ids", selected.join(","));
    next.set("from", fromDate.toISOString());
    next.set("to", toDate.toISOString());
    setSearchParams(next, { replace: true });
  }

  function setAllTags(form: HTMLFormElement, checked: boolean) {
    form.querySelectorAll<HTMLInputElement>('input[name="tag_ids"]').forEach((input) => { input.checked = checked; });
  }

  function exportCurrentPage() {
    if (!logger || result.data.length === 0) return;
    const header = [mode === "raw" ? "batch_at" : "bucket_at"];
    selectedTags.forEach((tag) => header.push(tag.name, `${tag.name} status`));
    const rows = result.data.map((row) => {
      const cells: unknown[] = [row.at];
      selectedTags.forEach((tag) => {
        const value = row.values[tag.id];
        cells.push(value?.value ?? "", queryCellSummary(value, mode, aggregate));
      });
      return cells;
    });
    const blob = new Blob([[header, ...rows].map((row) => row.map(csvCell).join(",")).join("\n")], { type: "text/csv;charset=utf-8" });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = `${logger.name.replaceAll(/[^a-z0-9]+/gi, "-").replaceAll(/^-|-$/g, "").toLowerCase() || "data-logger"}-${mode}-page-${result.pagination.page}.csv`;
    anchor.click();
    URL.revokeObjectURL(url);
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Loggers <span>/</span> <strong>Query</strong></>}>
    <div className="datalogger-query-content">
      <header className="datalogger-detail-heading">
        <div><Link to={`/data-loggers/${id}`}><ArrowLeft size={17} /> Back to history</Link><h1>{logger?.name ?? "Data Logger query"}</h1><p>Compare synchronized Tag values as raw batches or time-bucketed aggregates.</p></div>
        <div className="datalogger-detail-actions"><button type="button" disabled={result.data.length === 0} onClick={exportCurrentPage}><Download size={17} /> Export page CSV</button><button type="button" onClick={() => setRefreshVersion((value) => value + 1)}><RefreshCw size={17} /> Refresh</button></div>
      </header>

      {loadState === "loading" && <section className="datalogger-detail-state" role="status"><LoaderCircle className="is-spinning" /><strong>Querying Data Logger values…</strong></section>}
      {loadState === "error" && <section className="datalogger-detail-state is-error" role="alert"><CircleAlert /><strong>Couldn’t query Data Logger values</strong><span>{loadError}</span><button type="button" onClick={() => setRefreshVersion((value) => value + 1)}>Try again</button></section>}

      {loadState === "ready" && logger && <>
        <section className="datalogger-query-workspace">
          <header><div><TableProperties /><div><h2>Query workspace</h2><p>Times are entered and displayed in {logger.timezone}.</p></div></div><div className="datalogger-query-mode" role="group" aria-label="Query mode"><button type="button" className={mode === "raw" ? "is-selected" : ""} onClick={() => changeMode("raw")}>Raw batches</button><button type="button" className={mode === "aggregate" ? "is-selected" : ""} onClick={() => changeMode("aggregate")}><Sigma size={15} /> Aggregated</button></div></header>
          <form onSubmit={applyQuery} key={`${tagIDsParam}-${from}-${to}-${mode}`}>
            <div className="datalogger-query-range">
              <label><span>From <small>{logger.timezone}</small></span><input type="datetime-local" name="from" defaultValue={dateToLocalInput(from, logger.timezone)} required /></label>
              <label><span>To <small>{logger.timezone}</small></span><input type="datetime-local" name="to" defaultValue={dateToLocalInput(to, logger.timezone)} required /></label>
              {mode === "aggregate" && <><label><span>Bucket</span><select name="bucket" defaultValue={bucket}>{buckets.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label><label><span>Function</span><select name="aggregate" defaultValue={aggregate}>{aggregates.map((option) => <option key={option} value={option}>{option.toUpperCase()}</option>)}</select></label></>}
            </div>
            <fieldset><legend>Tags <small>{selectedTags.length || logger.tags?.length || 0} selected</small></legend><div className="datalogger-query-tag-actions"><button type="button" onClick={(event) => setAllTags(event.currentTarget.form!, true)}>Select all</button><button type="button" onClick={(event) => setAllTags(event.currentTarget.form!, false)}>Clear</button></div><div className="datalogger-query-tags">{(logger.tags ?? []).map((tag) => <label key={tag.id}><input type="checkbox" name="tag_ids" value={tag.id} defaultChecked={requestedTagIDs.length === 0 || requestedTagIDs.includes(tag.id)} /><span><strong>{tag.name}</strong><small>{tag.type} · {tag.data_type}</small></span></label>)}</div></fieldset>
            {filterError && <div className="datalogger-history-filter-error" role="alert"><CircleAlert size={16} />{filterError}</div>}
            <footer><span>Maximum range: 366 days · Maximum 100 Tags</span><button type="submit">Run query</button></footer>
          </form>
        </section>

        {mode === "aggregate" && <aside className="datalogger-query-note"><Info size={18} /><span><strong>Aggregation is generic.</strong> SUM adds stored samples; it does not integrate kW over time into kWh. Energy conversion belongs in the Energy Management plugin.</span></aside>}

        <section className="datalogger-query-results">
          <header><div><h2>{mode === "raw" ? "Raw batch table" : `${aggregate.toUpperCase()} by ${bucket} bucket`}</h2><p>One row per {mode === "raw" ? "synchronized Logger batch" : "fixed UTC-aligned bucket"}; displayed in {logger.timezone}.</p></div><span>{result.pagination.total} rows</span></header>
          <div className="datalogger-history-strip" role="region" aria-label="Query result summary"><span><strong>{result.data.length}</strong> rows on page</span><span><strong>{selectedTags.length}</strong> Tag columns</span><span className={badCells ? "is-bad" : ""}><strong>{badCells}</strong> cells with errors</span></div>
          {result.data.length === 0 ? <div className="datalogger-history-empty"><TableProperties /><strong>No values in this range</strong><span>Adjust the time range, selected Tags, or aggregation bucket.</span></div> : <div className="datalogger-query-table-scroll"><table><thead><tr><th>{mode === "raw" ? "Batch" : "Bucket start"}</th>{selectedTags.map((tag) => <th key={tag.id}><strong>{tag.name}</strong><small>{tag.data_type}</small></th>)}</tr></thead><tbody>{result.data.map((row) => <tr key={row.at}><td><strong>{formatInTimezone(new Date(row.at), logger.timezone)}</strong><small>{logger.timezone}</small></td>{selectedTags.map((tag) => {
            const value = row.values[tag.id];
            const unsupported = value?.supported === false;
            const hasError = value?.quality === "bad" || (value?.bad_count ?? 0) > 0;
            return <td key={tag.id} className={unsupported ? "is-unsupported" : hasError ? "is-error" : ""}><strong>{unsupported ? "Unavailable" : formatValue(value?.value ?? null)}</strong>{value && mode === "raw" && <small>{value.quality}{value.observed_at ? ` · observed ${formatInTimezone(new Date(value.observed_at), logger.timezone)}` : ""}</small>}{value && mode === "aggregate" && <small>{value.good_count ?? 0}/{value.total_count ?? 0} good{(value.bad_count ?? 0) > 0 ? ` · ${value.bad_count} bad` : ""}</small>}{unsupported && <small>{aggregate} is not supported for {value.data_type}</small>}{value?.error && <small className="is-error-message">{value.error}</small>}{!value && <small>No sample</small>}</td>;
          })}</tr>)}</tbody></table></div>}
          <footer className="datalogger-history-pagination"><p>Showing {result.data.length} of {result.pagination.total} rows · CSV exports this page only</p><div><button type="button" aria-label="Previous query page" disabled={page <= 1} onClick={() => updatePage(Math.max(1, page - 1))}><ChevronLeft size={19} /></button><span aria-label={`Query page ${result.pagination.page}`}>{result.pagination.page}</span><button type="button" aria-label="Next query page" disabled={result.pagination.total_pages === 0 || page >= result.pagination.total_pages} onClick={() => updatePage(page + 1)}><ChevronRight size={19} /></button></div></footer>
        </section>
      </>}
    </div>
  </VGatewayShell>;
}

export default DataLoggerQueryPage;
