import axios from "axios";
import { BarChart3, ChevronLeft, ChevronRight, CircleAlert, Columns3, FileChartColumn, LoaderCircle, Pencil, Plus, RefreshCw, Search, Trash2 } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { deleteReport, listReports } from "../../../services/report.service";
import type { DataLoggerPagination } from "../../../types/datalogger";
import type { Report } from "../../../types/report";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./ReportPages.css";

const perPage = 20;
const emptyPagination: DataLoggerPagination = { page: 1, per_page: perPage, total: 0, total_pages: 0 };

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Reports. Check the API connection and try again.";
}

function ReportListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const search = searchParams.get("search")?.trim() ?? "";
  const rawMode = searchParams.get("mode");
  const mode = rawMode === "raw" || rawMode === "aggregate" ? rawMode : undefined;
  const page = positivePage(searchParams.get("page"));
  const [reports, setReports] = useState<Report[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [deletingID, setDeletingID] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void listReports({ search: search || undefined, mode, page, per_page: perPage }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      setReports(response.data);
      setPagination(response.pagination);
      setMessage("");
      setState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(errorMessage(error));
      setState("error");
    });
    return () => controller.abort();
  }, [mode, page, refresh, search]);

  const updateFilter = useCallback((name: string, value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(name, value); else next.delete(name);
    if (name !== "page") next.delete("page");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const aggregateCount = reports.filter((report) => report.mode === "aggregate").length;
  const visibleColumns = useMemo(() => reports.reduce((total, report) => total + report.column_count, 0), [reports]);

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    updateFilter("search", String(new FormData(event.currentTarget).get("search") ?? "").trim() || undefined);
  }

  async function remove(entity: Report) {
    if (!window.confirm(`Delete “${entity.name}”? Data Logger history will not be removed.`)) return;
    setDeletingID(entity.id);
    try {
      await deleteReport(entity.id);
      setRefresh((value) => value + 1);
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setDeletingID(null);
    }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Reports</strong></>}>
    <div className="report-content">
      <section className="report-heading"><div><h1>Reports</h1><p>Build reusable column layouts from synchronized Data Logger history.</p></div><Link className="report-primary-action" to="/reports/new"><Plus size={18} /> New Report</Link></section>
      <section className="report-metrics" aria-label="Report summary"><article><FileChartColumn /><span>Total reports</span><strong>{pagination.total}</strong></article><article><BarChart3 /><span>Aggregated on page</span><strong>{aggregateCount}</strong></article><article><Columns3 /><span>Columns on page</span><strong>{visibleColumns}</strong></article></section>
      <section className="report-toolbar" aria-label="Report filters"><form role="search" onSubmit={submitSearch}><Search size={18} /><label className="sr-only" htmlFor="report-search">Search Reports</label><input id="report-search" name="search" type="search" key={search} defaultValue={search} placeholder="Search report or logger…" /><button type="submit">Search</button></form><select aria-label="Report mode" value={mode ?? "all"} onChange={(event) => updateFilter("mode", event.target.value === "all" ? undefined : event.target.value)}><option value="all">All modes</option><option value="raw">Raw</option><option value="aggregate">Aggregated</option></select><button type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw className={state === "loading" ? "is-spinning" : ""} size={17} /> Refresh</button></section>
      {message && state !== "error" && <div className="report-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      <section className="report-list" aria-label="Reports">
        {state === "loading" && <div className="report-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Reports…</strong></div>}
        {state === "error" && <div className="report-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Reports</strong><span>{message}</span><button type="button" onClick={() => setRefresh((value) => value + 1)}>Try again</button></div>}
        {state === "ready" && reports.length === 0 && <div className="report-state"><FileChartColumn /><strong>No Reports found</strong><span>Create a saved view over one Data Logger.</span></div>}
        {state === "ready" && reports.length > 0 && <><div className="report-table-scroll"><table><thead><tr><th>Name</th><th>Source</th><th>Mode</th><th>Columns</th><th>Timezone</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{reports.map((entity) => <tr key={entity.id}><td data-label="Name"><Link to={`/reports/${entity.id}`}>{entity.name}</Link><small>{entity.description ?? "Saved report"}</small></td><td data-label="Source">{entity.logger_name}</td><td data-label="Mode"><span className={`report-mode is-${entity.mode}`}>{entity.mode === "aggregate" ? `${entity.bucket} aggregated` : "Raw batches"}</span></td><td data-label="Columns">{entity.column_count}</td><td data-label="Timezone">{entity.timezone}</td><td data-label="Actions"><div className="report-row-actions"><Link aria-label={`Edit ${entity.name}`} to={`/reports/${entity.id}/edit`}><Pencil size={16} /></Link><button aria-label={`Delete ${entity.name}`} disabled={deletingID === entity.id} onClick={() => void remove(entity)}>{deletingID === entity.id ? <LoaderCircle className="is-spinning" size={16} /> : <Trash2 size={16} />}</button></div></td></tr>)}</tbody></table></div><footer className="report-pagination"><p>Showing {reports.length} of {pagination.total} Reports</p><div><button aria-label="Previous page" disabled={page <= 1} onClick={() => updateFilter("page", String(page - 1))}><ChevronLeft /></button><span>{pagination.page}</span><button aria-label="Next page" disabled={page >= pagination.total_pages} onClick={() => updateFilter("page", String(page + 1))}><ChevronRight /></button></div></footer></>}
      </section>
    </div>
  </VGatewayShell>;
}

export default ReportListPage;
