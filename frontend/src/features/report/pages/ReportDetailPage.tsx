import axios from "axios";
import { ArrowLeft, ChevronLeft, ChevronRight, CircleAlert, Download, FileChartColumn, LoaderCircle, Pencil, RefreshCw } from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { exportReportCSV, getReport, queryReport } from "../../../services/report.service";
import type { DataLoggerQueryValue } from "../../../types/datalogger";
import type { Report, ReportQueryResponse } from "../../../types/report";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { dateToLocalInput, formatInTimezone, zonedDateTimeToDate } from "../../datalogger/utils/schedule";
import "../../vgateway/pages/VGatewayListPage.css";
import "../../datalogger/pages/DataLoggerDetailPage.css";
import "./ReportPages.css";

const perPage = 100;

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to query Report. Check the API connection and try again.";
}

function formatValue(value: DataLoggerQueryValue | undefined): string {
  if (!value || value.value === null || value.supported === false || value.quality === "bad") return "—";
  if (typeof value.value === "boolean") return String(value.value);
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value.value);
}

function ReportDetailPage() {
  const { id = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [defaultRange] = useState(() => {
    const to = new Date(); to.setSeconds(0, 0); to.setMinutes(to.getMinutes() + 1);
    return { from: new Date(to.getTime() - 24 * 60 * 60 * 1000).toISOString(), to: to.toISOString() };
  });
  const from = searchParams.get("from") ?? defaultRange.from;
  const to = searchParams.get("to") ?? defaultRange.to;
  const page = positivePage(searchParams.get("page"));
  const [report, setReport] = useState<Report | null>(null);
  const [result, setResult] = useState<ReportQueryResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [filterError, setFilterError] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([getReport(id, controller.signal), queryReport(id, { from, to, page, per_page: perPage }, controller.signal)]).then(([definition, query]) => {
      if (controller.signal.aborted) return;
      setReport(definition); setResult(query); setMessage(""); setState("ready");
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) { setMessage(errorMessage(error)); setState("error"); }
    });
    return () => controller.abort();
  }, [from, id, page, refresh, to]);

  function applyRange(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!report) return;
    const form = new FormData(event.currentTarget);
    const fromDate = zonedDateTimeToDate(String(form.get("from") ?? ""), report.timezone);
    const toDate = zonedDateTimeToDate(String(form.get("to") ?? ""), report.timezone);
    if (!fromDate || !toDate || toDate <= fromDate) { setFilterError("Enter a valid range where To is after From"); return; }
    if (toDate.getTime() - fromDate.getTime() > 366 * 24 * 60 * 60 * 1000) { setFilterError("Report range cannot exceed 366 days"); return; }
    setFilterError("");
    setSearchParams({ from: fromDate.toISOString(), to: toDate.toISOString() }, { replace: true });
  }

  function updatePage(nextPage: number) {
    const next = new URLSearchParams(searchParams);
    if (nextPage > 1) next.set("page", String(nextPage)); else next.delete("page");
    setSearchParams(next, { replace: true });
  }

  async function exportCSV() {
    if (!report) return;
    setExporting(true); setMessage("");
    try {
      const blob = await exportReportCSV(id, { from, to });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      anchor.download = `${report.name.replaceAll(/[^a-z0-9]+/gi, "-").replaceAll(/^-|-$/g, "").toLowerCase() || "report"}.csv`;
      anchor.click(); URL.revokeObjectURL(url);
    } catch (error) { setMessage(errorMessage(error)); } finally { setExporting(false); }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Reports <span>/</span> <strong>{report?.name ?? "View"}</strong></>}>
    <div className="report-detail">
      <header className="report-detail-heading"><div><Link to="/reports"><ArrowLeft size={17} /> Back to Reports</Link><h1>{report?.name ?? "Report"}</h1><p>{report?.description ?? "Saved Data Logger column view."}</p></div><div><button type="button" disabled={!report || exporting} onClick={() => void exportCSV()}>{exporting ? <LoaderCircle className="is-spinning" /> : <Download />} {exporting ? "Exporting…" : "Export full range CSV"}</button>{report && <Link to={`/reports/${report.id}/edit`}><Pencil /> Edit</Link>}<button type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw /> Refresh</button></div></header>
      {message && state !== "error" && <div className="report-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      {state === "loading" && <div className="report-state" role="status"><LoaderCircle className="is-spinning" /><strong>Building Report…</strong></div>}
      {state === "error" && <div className="report-state is-error" role="alert"><CircleAlert /><strong>Couldn’t build Report</strong><span>{message}</span><button type="button" onClick={() => setRefresh((value) => value + 1)}>Try again</button></div>}
      {state === "ready" && report && result && <>
        <section className="report-definition-strip"><article><span>Source</span><strong>{report.logger_name}</strong><small>Synchronized Data Logger</small></article><article><span>Layout</span><strong>{report.column_count} columns</strong><small>Custom ordered headers</small></article><article><span>Mode</span><strong>{report.mode === "aggregate" ? `${report.bucket} buckets` : "Raw batches"}</strong><small>{report.mode === "aggregate" ? "Per-column functions" : "No aggregation"}</small></article><article><span>Timezone</span><strong>{report.timezone}</strong><small>Display and CSV timestamps</small></article></section>
        <section className="report-query-card"><header><FileChartColumn /><div><h2>Report range</h2><p>Preview is paginated; CSV exports every matching row in the selected range.</p></div></header><form onSubmit={applyRange} key={`${from}-${to}`}><label><span>From <small>{report.timezone}</small></span><input name="from" type="datetime-local" defaultValue={dateToLocalInput(from, report.timezone)} /></label><label><span>To <small>{report.timezone}</small></span><input name="to" type="datetime-local" defaultValue={dateToLocalInput(to, report.timezone)} /></label><button type="submit">Run Report</button></form>{filterError && <div className="report-filter-error" role="alert"><CircleAlert />{filterError}</div>}</section>
        <section className="report-result"><header><div><h2>{report.mode === "aggregate" ? `Aggregated by ${report.bucket}` : "Raw synchronized batches"}</h2><p>{result.pagination.total} matching rows</p></div><span>{result.data.length} on page</span></header>{result.data.length === 0 ? <div className="report-state"><FileChartColumn /><strong>No values in this range</strong></div> : <div className="report-result-scroll"><table><thead><tr><th>Timestamp</th>{(report.columns ?? []).map((column) => <th key={column.tag_id}><strong>{column.name}</strong><small>{column.tag_name} · {report.mode === "aggregate" ? column.aggregate?.toUpperCase() : column.data_type}</small></th>)}</tr></thead><tbody>{result.data.map((row) => <tr key={row.at}><td><strong>{formatInTimezone(new Date(row.at), report.timezone)}</strong><small>{report.timezone}</small></td>{(report.columns ?? []).map((column) => { const value = row.values[column.tag_id]; const bad = value?.quality === "bad" || (value?.bad_count ?? 0) > 0; return <td key={column.tag_id} className={bad ? "is-error" : ""}><strong>{formatValue(value)}</strong>{report.mode === "aggregate" && value && <small>{value.good_count ?? 0}/{value.total_count ?? 0} good{(value.bad_count ?? 0) > 0 ? ` · ${value.bad_count} bad` : ""}</small>}{value?.error && <small className="is-error-message">{value.error}</small>}{!value && <small>No sample</small>}</td>; })}</tr>)}</tbody></table></div>}<footer className="report-pagination"><p>CSV exports all {result.pagination.total} matching rows</p><div><button aria-label="Previous Report page" disabled={page <= 1} onClick={() => updatePage(page - 1)}><ChevronLeft /></button><span>{result.pagination.page}</span><button aria-label="Next Report page" disabled={page >= result.pagination.total_pages} onClick={() => updatePage(page + 1)}><ChevronRight /></button></div></footer></section>
      </>}
    </div>
  </VGatewayShell>;
}

export default ReportDetailPage;
