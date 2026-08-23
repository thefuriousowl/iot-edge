import axios from "axios";
import {
  Activity,
  BarChart3,
  CalendarRange,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Download,
  Gauge,
  LoaderCircle,
  RefreshCw,
  Sigma,
  WalletCards,
  Zap,
} from "lucide-react";
import { type FormEvent, useEffect, useState } from "react";
import {
  Area,
  AreaChart,
  Bar,
  BarChart,
  CartesianGrid,
  Legend,
  Line,
  LineChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { exportEnergyCSV, getEnergyHistory } from "../../../services/energy.service";
import { getPlugin } from "../../../services/plugin.service";
import type { DataLoggerQueryBucket } from "../../../types/datalogger";
import type { EnergyHistoryResponse, EnergyPeriodSummary, PluginInstance } from "../../../types/plugin";
import EnergyPluginTabs from "../components/EnergyPluginTabs";
import {
  automaticEnergyBucket,
  availableEnergyBuckets,
  defaultEnergyRange,
  energyLocalDate,
  energyLocalInput,
  type EnergyRangePreset,
  presetEnergyRange,
  summarizeEnergyHistory,
  validEnergyRange,
} from "../utils/energyHistory";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./EnergyHistoryPage.css";

const tablePageSize = 10;
const bucketLabels: Record<DataLoggerQueryBucket, string> = {
  "1m": "1 minute",
  "5m": "5 minutes",
  "15m": "15 minutes",
  "1h": "1 hour",
  "6h": "6 hours",
  "1d": "1 day",
  "1w": "1 week",
};
const presets: Array<{ value: EnergyRangePreset; label: string }> = [
  { value: "today", label: "Today" },
  { value: "24h", label: "24h" },
  { value: "7d", label: "7d" },
  { value: "30d", label: "30d" },
  { value: "month", label: "This month" },
];

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function readableError(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Energy history. Check the Plugin API connection and try again.";
}

function formatNumber(value: number, maximumFractionDigits = 2): string {
  if (!Number.isFinite(value)) return "—";
  return new Intl.NumberFormat(undefined, { minimumFractionDigits: Math.min(2, maximumFractionDigits), maximumFractionDigits }).format(value);
}

function formatPeriod(value: string, timezone: string, bucket: DataLoggerQueryBucket): string {
  const date = new Date(value);
  try {
    return new Intl.DateTimeFormat(undefined, bucket === "1d" || bucket === "1w"
      ? { timeZone: timezone, month: "short", day: "numeric", year: "numeric" }
      : { timeZone: timezone, month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
  } catch {
    return date.toLocaleString();
  }
}

function issueCount(row: EnergyPeriodSummary): number {
  return (row.electrical.issues?.length ?? 0) + (row.thermal.issues?.length ?? 0) + (row.cost.valid ? 0 : 1) + (row.cop.valid ? 0 : 1);
}

function requestedRange(searchParams: URLSearchParams, fallback: { from: string; to: string }) {
  const from = searchParams.get("from") ?? fallback.from;
  const to = searchParams.get("to") ?? fallback.to;
  return validEnergyRange(from, to) ? { from, to } : fallback;
}

function EnergyHistoryPage() {
  const { id = "" } = useParams();
  const [searchParams, setSearchParams] = useSearchParams();
  const [fallbackRange] = useState(() => defaultEnergyRange());
  const range = requestedRange(searchParams, fallbackRange);
  const availableBuckets = availableEnergyBuckets(range.from, range.to);
  const requestedBucket = searchParams.get("bucket") as DataLoggerQueryBucket | null;
  const bucket = requestedBucket && availableBuckets.includes(requestedBucket) ? requestedBucket : automaticEnergyBucket(range.from, range.to);
  const page = positivePage(searchParams.get("page"));
  const [plugin, setPlugin] = useState<PluginInstance | null>(null);
  const [history, setHistory] = useState<EnergyHistoryResponse | null>(null);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [filterError, setFilterError] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      getPlugin(id, controller.signal),
      getEnergyHistory(id, { from: range.from, to: range.to, bucket, page: 1, per_page: 500 }, controller.signal),
    ]).then(([instance, response]) => {
      if (controller.signal.aborted) return;
      setPlugin(instance);
      setHistory(response);
      setMessage("");
      setState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(readableError(error));
      setState("error");
    });
    return () => controller.abort();
  }, [bucket, id, range.from, range.to, refreshVersion]);

  const summary = summarizeEnergyHistory(history?.data ?? []);
  const chartData = [...(history?.data ?? [])].reverse().map((row) => ({
    at: formatPeriod(row.from, history?.timezone ?? "UTC", bucket),
    electrical: row.electrical.kilowatt_hours,
    thermal: row.thermal.kilowatt_hours,
    cost: row.cost.valid ? row.cost.value : null,
    cop: row.cop.valid ? row.cop.value : null,
    electricalCoverage: row.electrical.coverage_percent,
    thermalCoverage: row.thermal.coverage_percent,
  }));
  const totalTablePages = Math.max(1, Math.ceil((history?.data.length ?? 0) / tablePageSize));
  const safePage = Math.min(page, totalTablePages);
  const tableRows = history?.data.slice((safePage - 1) * tablePageSize, safePage * tablePageSize) ?? [];
  const timezone = history?.timezone ?? (plugin?.config as { timezone?: string } | undefined)?.timezone ?? "UTC";

  function setRange(nextRange: { from: string; to: string }, nextBucket?: DataLoggerQueryBucket) {
    setState("loading");
    setMessage("");
    const next = new URLSearchParams();
    next.set("from", nextRange.from);
    next.set("to", nextRange.to);
    next.set("bucket", nextBucket ?? automaticEnergyBucket(nextRange.from, nextRange.to));
    setSearchParams(next, { replace: true });
  }

  function applyPreset(preset: EnergyRangePreset) {
    setFilterError("");
    setRange(presetEnergyRange(preset, timezone));
  }

  function applyCustomRange(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const form = new FormData(event.currentTarget);
    const from = energyLocalDate(String(form.get("from") ?? ""), timezone);
    const to = energyLocalDate(String(form.get("to") ?? ""), timezone);
    if (!from || !to || !validEnergyRange(from.toISOString(), to.toISOString())) {
      setFilterError("Enter a valid range where To is after From and the span is no more than 366 days.");
      return;
    }
    setFilterError("");
    setRange({ from: from.toISOString(), to: to.toISOString() });
  }

  function changeBucket(nextBucket: DataLoggerQueryBucket) {
    setState("loading");
    setMessage("");
    const next = new URLSearchParams(searchParams);
    next.set("from", range.from);
    next.set("to", range.to);
    next.set("bucket", nextBucket);
    next.delete("page");
    setSearchParams(next, { replace: true });
  }

  function changePage(nextPage: number) {
    const next = new URLSearchParams(searchParams);
    if (nextPage > 1) next.set("page", String(nextPage)); else next.delete("page");
    setSearchParams(next, { replace: true });
  }

  async function exportCSV() {
    setExporting(true);
    setMessage("");
    try {
      const blob = await exportEnergyCSV(id, { from: range.from, to: range.to, bucket });
      const url = URL.createObjectURL(blob);
      const anchor = document.createElement("a");
      anchor.href = url;
      const slug = (plugin?.name ?? "energy").replaceAll(/[^a-z0-9]+/gi, "-").replaceAll(/^-|-$/g, "").toLowerCase() || "energy";
      anchor.download = `${slug}-${bucket}-history.csv`;
      anchor.click();
      URL.revokeObjectURL(url);
    } catch (error) {
      setMessage(readableError(error));
    } finally {
      setExporting(false);
    }
  }

  return (
    <VGatewayShell breadcrumb={<>Plugins <span>/</span> <strong>{plugin?.name ?? "Energy history"}</strong></>}>
      <div className="energy-history-content" aria-busy={state === "loading"}>
        <header className="energy-history-heading">
          <div><p>Energy Management Plugin</p><h1>{plugin?.name ?? "Energy history"}</h1><span>Explore Plugin-calculated kWh, cost, COP, coverage, and quality over an exact time range.</span></div>
          <div><Link to={`/plugins/${encodeURIComponent(id)}/energy`}><Gauge size={17} />Live overview</Link><button type="button" disabled={exporting || state === "loading" || !history?.data.length} onClick={() => void exportCSV()}>{exporting ? <LoaderCircle className="is-spinning" /> : <Download />} {exporting ? "Exporting…" : "Export CSV"}</button></div>
        </header>

        <EnergyPluginTabs instanceID={id} />

        <section className="energy-history-range" aria-label="Energy history range">
          <header><div><CalendarRange /><span><h2>Analysis window</h2><small>Inputs and labels use {timezone}</small></span></div><div className="energy-history-presets">{presets.map((preset) => <button key={preset.value} type="button" onClick={() => applyPreset(preset.value)}>{preset.label}</button>)}</div></header>
          <form onSubmit={applyCustomRange} key={`${range.from}-${range.to}-${timezone}`}>
            <label><span>From <small>{timezone}</small></span><input type="datetime-local" name="from" defaultValue={energyLocalInput(range.from, timezone)} required /></label>
            <label><span>To <small>{timezone}</small></span><input type="datetime-local" name="to" defaultValue={energyLocalInput(range.to, timezone)} required /></label>
            <label><span>Resolution <small>≤ 500 points</small></span><select aria-label="Chart resolution" value={bucket} onChange={(event) => changeBucket(event.target.value as DataLoggerQueryBucket)}>{availableBuckets.map((value) => <option key={value} value={value}>{bucketLabels[value]}</option>)}</select></label>
            <button type="submit">Run analysis</button>
            <button type="button" aria-label="Refresh Energy history" disabled={state === "loading"} onClick={() => { setState("loading"); setMessage(""); setRefreshVersion((current) => current + 1); }}><RefreshCw className={state === "loading" ? "is-spinning" : ""} /></button>
          </form>
          {filterError && <div className="energy-history-filter-error" role="alert"><CircleAlert />{filterError}</div>}
        </section>

        {state === "loading" && history && <div className="energy-history-update" role="status" aria-live="polite"><LoaderCircle className="is-spinning" /><span><strong>Updating Energy history…</strong><small>Current charts remain visible until the Plugin returns the replacement range.</small></span></div>}
        {message && (history || state !== "error") && <div className="energy-history-alert" role="alert"><CircleAlert />{message}</div>}
        {state === "loading" && !history && <div className="energy-history-state" role="status"><LoaderCircle className="is-spinning" /><strong>Calculating Energy history…</strong><span>The Plugin is integrating synchronized Logger batches.</span></div>}
        {state === "error" && !history && <div className="energy-history-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Energy history</strong><span>{message}</span><button type="button" onClick={() => { setState("loading"); setRefreshVersion((current) => current + 1); }}>Try again</button></div>}

        {history && <>
          <section className="energy-history-summary" aria-label="Selected range summary">
            <article><Zap /><span>Electrical energy</span><strong>{formatNumber(summary.electricalKWh)} kWh</strong><small>{formatNumber(summary.electricalCoverage, 1)}% coverage</small></article>
            <article><Activity /><span>Thermal energy</span><strong>{formatNumber(summary.thermalKWh)} kWh</strong><small>{formatNumber(summary.thermalCoverage, 1)}% coverage</small></article>
            <article><WalletCards /><span>Estimated cost</span><strong>{summary.cost === null ? "Unavailable" : `${formatNumber(summary.cost)} ${history.currency}`}</strong><small>{history.tariff_mode === "tag" ? "Synchronized tariff Tag" : `${formatNumber(history.rate_per_kwh)} ${history.currency}/kWh`}</small></article>
            <article><Gauge /><span>Covered-period COP</span><strong>{summary.cop === null ? "Unavailable" : formatNumber(summary.cop)}</strong><small>Thermal kWh ÷ electrical kWh</small></article>
            <article className={summary.affectedBuckets > 0 ? "has-issues" : ""}><CircleAlert /><span>Affected buckets</span><strong>{summary.affectedBuckets}</strong><small>Unavailable metrics or integration issues</small></article>
          </section>

          {history.data.length === 0 ? <div className="energy-history-state"><BarChart3 /><strong>No Energy history in this range</strong><span>Choose another window or wait for synchronized Logger batches.</span></div> : <>
            <section className="energy-history-chart is-wide" aria-label="Electrical and thermal Energy chart">
              <header><div><BarChart3 /><span><h2>Energy by {bucketLabels[bucket]}</h2><small>Electrical and thermal integration per Plugin bucket</small></span></div><code>kWh</code></header>
              <div role="img" aria-label="Grouped bars comparing electrical and thermal kilowatt-hours over time"><ResponsiveContainer width="100%" height="100%"><BarChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 11 }} /><YAxis tick={{ fill: "#8295a4", fontSize: 11 }} /><Tooltip /><Legend /><Bar dataKey="electrical" name="Electrical kWh" fill="#34c3ff" radius={[3, 3, 0, 0]} /><Bar dataKey="thermal" name="Thermal kWh" fill="#8d7cf6" radius={[3, 3, 0, 0]} /></BarChart></ResponsiveContainer></div>
            </section>

            <div className="energy-history-chart-grid">
              <section className="energy-history-chart" aria-label="COP chart"><header><div><Sigma /><span><h2>Coefficient of performance</h2><small>Unavailable buckets remain visible as gaps</small></span></div><code>COP</code></header><div role="img" aria-label="Line chart of coefficient of performance over time"><ResponsiveContainer width="100%" height="100%"><LineChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip /><Line type="monotone" dataKey="cop" name="COP" stroke="#8d7cf6" strokeWidth={2} dot={false} connectNulls={false} /></LineChart></ResponsiveContainer></div></section>
              <section className="energy-history-chart" aria-label="Estimated cost chart"><header><div><WalletCards /><span><h2>Estimated cost</h2><small>Calculated only where tariff coverage is valid</small></span></div><code>{history.currency}</code></header><div role="img" aria-label={`Area chart of estimated cost in ${history.currency} over time`}><ResponsiveContainer width="100%" height="100%"><AreaChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip /><Area type="monotone" dataKey="cost" name={`Cost (${history.currency})`} stroke="#34c3ff" fill="rgba(52,195,255,.15)" connectNulls={false} /></AreaChart></ResponsiveContainer></div></section>
            </div>

            <section className="energy-history-chart is-compact" aria-label="Coverage chart"><header><div><Activity /><span><h2>Data coverage</h2><small>Independent electrical and thermal completeness</small></span></div><code>%</code></header><div role="img" aria-label="Line chart of electrical and thermal data coverage over time"><ResponsiveContainer width="100%" height="100%"><LineChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis domain={[0, 100]} tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip /><Legend /><Line type="monotone" dataKey="electricalCoverage" name="Electrical coverage" stroke="#34c3ff" strokeWidth={2} dot={false} /><Line type="monotone" dataKey="thermalCoverage" name="Thermal coverage" stroke="#8d7cf6" strokeWidth={2} dot={false} /></LineChart></ResponsiveContainer></div></section>

            <section className="energy-history-table" aria-labelledby="energy-history-table-heading">
              <header><div><h2 id="energy-history-table-heading">Bucket details</h2><p>Plugin-calculated values for the exact selected window.</p></div><span>{history.pagination.total} buckets</span></header>
              <div><table><thead><tr><th>Period</th><th>Electrical</th><th>Thermal</th><th>Cost</th><th>COP</th><th>Coverage</th><th>Issues</th></tr></thead><tbody>{tableRows.map((row) => <tr key={`${row.from}-${row.to}`}><td data-label="Period"><strong>{formatPeriod(row.from, history.timezone, bucket)}</strong><small>to {formatPeriod(row.to, history.timezone, bucket)}</small></td><td data-label="Electrical">{formatNumber(row.electrical.kilowatt_hours)} kWh</td><td data-label="Thermal">{formatNumber(row.thermal.kilowatt_hours)} kWh</td><td data-label="Cost">{row.cost.valid ? `${formatNumber(row.cost.value)} ${history.currency}` : <span className="is-unavailable">Unavailable</span>}</td><td data-label="COP">{row.cop.valid ? formatNumber(row.cop.value) : <span className="is-unavailable">Unavailable</span>}</td><td data-label="Coverage"><strong>{formatNumber(row.electrical.coverage_percent, 1)}%</strong><small>thermal {formatNumber(row.thermal.coverage_percent, 1)}%</small></td><td data-label="Issues"><span className={issueCount(row) > 0 ? "has-issues" : ""}>{issueCount(row)}</span></td></tr>)}</tbody></table></div>
              <footer><p>Showing {tableRows.length} of {history.data.length} loaded Plugin buckets</p><div><button aria-label="Previous history page" disabled={safePage <= 1} onClick={() => changePage(safePage - 1)}><ChevronLeft /></button><span>{safePage} / {totalTablePages}</span><button aria-label="Next history page" disabled={safePage >= totalTablePages} onClick={() => changePage(safePage + 1)}><ChevronRight /></button></div></footer>
            </section>
          </>}
        </>}
      </div>
    </VGatewayShell>
  );
}

export default EnergyHistoryPage;
