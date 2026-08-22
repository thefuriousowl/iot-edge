import axios from "axios";
import {
  CalendarClock,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Clock3,
  DatabaseZap,
  LoaderCircle,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Tags,
  Trash2,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { deleteDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import type { DataLogger, DataLoggerPagination } from "../../../types/datalogger";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { formatInTimezone, nextDataLoggerRun, scheduleSummary } from "../utils/schedule";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DataLoggerListPage.css";

type LoadState = "loading" | "ready" | "error";
type EnabledFilter = "all" | "enabled" | "disabled";

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
  return "Unable to load Data Loggers. Check the API connection and try again.";
}

function nextRunLabel(logger: DataLogger): string {
  if (!logger.enabled) return "Paused";
  const next = nextDataLoggerRun(logger);
  if (next) return formatInTimezone(next, logger.timezone);
  return logger.end_at && new Date(logger.end_at) < new Date() ? "Completed" : "No future run";
}

function DataLoggerListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const querySearch = searchParams.get("search")?.trim() ?? "";
  const rawMode = searchParams.get("mode");
  const mode = rawMode === "interval" || rawMode === "schedule" ? rawMode : undefined;
  const rawEnabled = searchParams.get("enabled");
  const enabledFilter: EnabledFilter = rawEnabled === "true" ? "enabled" : rawEnabled === "false" ? "disabled" : "all";
  const page = positivePage(searchParams.get("page"));
  const [loggers, setLoggers] = useState<DataLogger[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [deletingID, setDeletingID] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();

    async function load() {
      setLoadState("loading");
      setLoadError(null);
      try {
        const response = await listDataLoggers({
          mode,
          enabled: enabledFilter === "all" ? undefined : enabledFilter === "enabled",
          search: querySearch || undefined,
          page,
          per_page: perPage,
        }, controller.signal);
        if (!controller.signal.aborted) {
          setLoggers(response.data);
          setPagination(response.pagination);
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
  }, [enabledFilter, mode, page, querySearch, refreshVersion]);

  const updateFilter = useCallback((name: string, value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(name, value); else next.delete(name);
    if (name !== "page") next.delete("page");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const activeFilters = useMemo(() => [mode, enabledFilter !== "all", querySearch].filter(Boolean).length, [enabledFilter, mode, querySearch]);
  const enabledCount = loggers.filter((logger) => logger.enabled).length;
  const scheduledCount = loggers.filter((logger) => logger.mode === "schedule").length;

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = new FormData(event.currentTarget).get("search");
    updateFilter("search", typeof value === "string" ? value.trim() || undefined : undefined);
  }

  async function remove(logger: DataLogger) {
    if (!window.confirm(`Delete “${logger.name}”? Existing raw history will also be removed.`)) return;
    setDeletingID(logger.id);
    setActionError(null);
    try {
      await deleteDataLogger(logger.id);
      setRefreshVersion((current) => current + 1);
    } catch (error) {
      setActionError(errorMessage(error));
    } finally {
      setDeletingID(null);
    }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Data Loggers</strong></>}>
    <div className="datalogger-content">
      <section className="datalogger-heading">
        <div><h1>Data Loggers</h1><p>Capture synchronized Tag snapshots into durable time-series history.</p></div>
        <Link className="datalogger-primary-action" to="/data-loggers/new"><Plus size={18} /> New Data Logger</Link>
      </section>

      <section className="datalogger-metrics" aria-label="Data Logger summary">
        <article><DatabaseZap /><span>Total loggers</span><strong>{pagination.total}</strong></article>
        <article><Clock3 /><span>Enabled on page</span><strong>{enabledCount}</strong></article>
        <article><CalendarClock /><span>Calendar on page</span><strong>{scheduledCount}</strong></article>
      </section>

      <section className="datalogger-toolbar" aria-label="Data Logger filters">
        <form role="search" onSubmit={submitSearch}><Search size={18} /><label className="sr-only" htmlFor="datalogger-search">Search Data Loggers</label><input id="datalogger-search" key={querySearch} name="search" type="search" defaultValue={querySearch} placeholder="Search logger names…" /><button type="submit">Search</button></form>
        <select aria-label="Logger mode" value={mode ?? "all"} onChange={(event) => updateFilter("mode", event.target.value === "all" ? undefined : event.target.value)}><option value="all">All modes</option><option value="interval">Interval</option><option value="schedule">Calendar</option></select>
        <select aria-label="Enabled state" value={enabledFilter} onChange={(event) => { const value = event.target.value as EnabledFilter; updateFilter("enabled", value === "all" ? undefined : String(value === "enabled")); }}><option value="all">All states</option><option value="enabled">Enabled</option><option value="disabled">Disabled</option></select>
        <button className="datalogger-refresh" type="button" disabled={loadState === "loading"} onClick={() => setRefreshVersion((current) => current + 1)}><RefreshCw className={loadState === "loading" ? "is-spinning" : ""} size={17} /> Refresh</button>
      </section>
      {activeFilters > 0 && <div className="datalogger-active-filters"><span>{activeFilters} active {activeFilters === 1 ? "filter" : "filters"}</span><button type="button" onClick={() => setSearchParams({}, { replace: true })}>Clear all</button></div>}
      {actionError && <div className="datalogger-alert" role="alert"><CircleAlert size={18} />{actionError}</div>}

      <section className="datalogger-list-shell" aria-label="Data Loggers">
        {loadState === "loading" && <div className="datalogger-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Data Loggers…</strong><span>Resolving schedule definitions and runtime state.</span></div>}
        {loadState === "error" && <div className="datalogger-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Data Loggers</strong><span>{loadError}</span><button type="button" onClick={() => setRefreshVersion((current) => current + 1)}>Try again</button></div>}
        {loadState === "ready" && loggers.length === 0 && <div className="datalogger-state"><DatabaseZap /><strong>{activeFilters ? "No matching Data Loggers" : "No Data Loggers yet"}</strong><span>{activeFilters ? "Adjust or clear the filters to broaden the result." : "Create an engine to begin capturing Tag snapshots."}</span>{activeFilters > 0 && <button type="button" onClick={() => setSearchParams({}, { replace: true })}>Clear filters</button>}</div>}
        {loadState === "ready" && loggers.length > 0 && <>
          <div className="datalogger-table-scroll"><table><thead><tr><th>Name</th><th>Mode</th><th>Cadence</th><th>Tags</th><th>Next run</th><th>Status</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{loggers.map((logger) => <tr key={logger.id}>
            <td data-label="Name"><strong>{logger.name}</strong><small>{logger.description ?? logger.timezone}</small></td>
            <td data-label="Mode"><span className={`datalogger-mode is-${logger.mode}`}>{logger.mode === "interval" ? <Clock3 size={15} /> : <CalendarClock size={15} />}{logger.mode === "interval" ? "Interval" : "Calendar"}</span></td>
            <td data-label="Cadence">{scheduleSummary(logger.mode, logger.config)}</td>
            <td data-label="Tags"><span className="datalogger-tag-count"><Tags size={15} />{logger.tag_count}</span></td>
            <td data-label="Next run"><span className="datalogger-next-run">{nextRunLabel(logger)}</span><small>{logger.timezone}</small></td>
            <td data-label="Status"><span className={logger.enabled ? "datalogger-enabled is-enabled" : "datalogger-enabled"}><span />{logger.enabled ? "Enabled" : "Paused"}</span></td>
            <td data-label="Actions"><div className="datalogger-row-actions"><Link aria-label={`Edit ${logger.name}`} to={`/data-loggers/${logger.id}/edit`}><Pencil size={16} /></Link><button type="button" aria-label={`Delete ${logger.name}`} disabled={deletingID === logger.id} onClick={() => void remove(logger)}>{deletingID === logger.id ? <LoaderCircle className="is-spinning" size={16} /> : <Trash2 size={16} />}</button></div></td>
          </tr>)}</tbody></table></div>
          <footer className="datalogger-pagination"><p>Showing {loggers.length} of {pagination.total} Data Loggers</p><div><button type="button" aria-label="Previous page" disabled={page <= 1} onClick={() => updateFilter("page", String(Math.max(1, page - 1)))}><ChevronLeft size={19} /></button><span aria-label={`Page ${pagination.page}`}>{pagination.page}</span><button type="button" aria-label="Next page" disabled={pagination.total_pages === 0 || page >= pagination.total_pages} onClick={() => updateFilter("page", String(page + 1))}><ChevronRight size={19} /></button></div></footer>
        </>}
      </section>
    </div>
  </VGatewayShell>;
}

export default DataLoggerListPage;
