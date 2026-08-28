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
  Thermometer,
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
  Pie,
  PieChart,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { Link, useParams, useSearchParams } from "react-router-dom";

import { exportEnergyCSV, getEnergyHistory } from "../../../services/energy.service";
import { getAssetBindings, listAssets } from "../../../services/asset.service";
import { getPlugin } from "../../../services/plugin.service";
import type { Asset } from "../../../types/asset";
import type { DataLoggerQueryBucket } from "../../../types/datalogger";
import type { EnergyHistoryResponse, EnergyPeriodSummary, PluginInstance } from "../../../types/plugin";
import EnergyPluginTabs from "../components/EnergyPluginTabs";
import ProductionLineChart from "../components/ProductionLineChart";
import { previousEqualRange, updateUtilityFilters, validDashboardTimezone } from "../../utility/utils/dashboardFilters";
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
import "../components/ProductionLineChart.css";

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
  const [comparison, setComparison] = useState<EnergyHistoryResponse | null>(null);
  const [ownerAssets, setOwnerAssets] = useState<Asset[]>([]);
  const [hierarchyLoading, setHierarchyLoading] = useState(true);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [filterError, setFilterError] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [exporting, setExporting] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    const compare = searchParams.get("compare") === "previous";
    const previousRange = previousEqualRange(range.from, range.to);
    void Promise.all([
      getPlugin(id, controller.signal),
      getEnergyHistory(id, { from: range.from, to: range.to, bucket, page: 1, per_page: 500 }, controller.signal),
      compare ? getEnergyHistory(id, { from: previousRange.from, to: previousRange.to, bucket, page: 1, per_page: 500 }, controller.signal) : Promise.resolve(null),
    ]).then(([instance, response, previous]) => {
      if (controller.signal.aborted) return;
      setPlugin(instance);
      setHistory(response);
      setComparison(previous);
      setMessage("");
      setState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(readableError(error));
      setState("error");
    });
    return () => controller.abort();
  }, [bucket, id, range.from, range.to, refreshVersion, searchParams]);

  useEffect(() => {
    const controller = new AbortController();
    void listAssets({ enabled: true, page: 1, per_page: 100 }, controller.signal).then(async (response) => {
      const candidates = await Promise.all(response.data.map(async (asset) => ({ asset, bindings: await getAssetBindings(asset.id, controller.signal) })));
      if (!controller.signal.aborted) setOwnerAssets(candidates.filter(({ bindings }) => bindings.data.some((binding) => binding.source.kind === "plugin_output" && binding.source.plugin_instance_id === id)).map(({ asset }) => asset));
    }).catch(() => { if (!controller.signal.aborted) setOwnerAssets([]); }).finally(() => { if (!controller.signal.aborted) setHierarchyLoading(false); });
    return () => controller.abort();
  }, [id]);

  const summary = summarizeEnergyHistory(history?.data ?? []);
  const previousRows = [...(comparison?.data ?? [])].reverse();
  const chartData = [...(history?.data ?? [])].reverse().map((row, index) => ({
    at: formatPeriod(row.from, history?.timezone ?? "UTC", bucket),
    electrical: row.electrical.kilowatt_hours,
    thermal: row.thermal.kilowatt_hours,
    cost: row.cost.valid ? row.cost.value : null,
    cop: row.cop.valid ? row.cop.value : null,
    electricalCoverage: row.electrical.coverage_percent,
    thermalCoverage: row.thermal.coverage_percent,
    demand: row.electrical.covered_seconds > 0 ? row.electrical.kilowatt_hours / (row.electrical.covered_seconds / 3_600) : null,
    previousElectrical: previousRows[index]?.electrical.kilowatt_hours ?? null,
    previousElectricalDemand: previousRows[index]?.electrical.covered_seconds ? previousRows[index].electrical.kilowatt_hours / (previousRows[index].electrical.covered_seconds / 3_600) : null,
    thermalDemand: row.thermal.covered_seconds > 0 ? row.thermal.kilowatt_hours / (row.thermal.covered_seconds / 3_600) : null,
  }));
  const comparisonSummary = comparison ? summarizeEnergyHistory(comparison.data) : null;
  const costDistribution = chartData.filter((row) => row.cost !== null && row.cost > 0).map((row) => ({ name: row.at, value: row.cost }));
  const coveredAverage = (kilowattHours: number, coveredSeconds: number) => coveredSeconds > 0 ? kilowattHours / (coveredSeconds / 3_600) : null;
  const electricalCoveredSeconds = history?.data.reduce((sum, row) => sum + row.electrical.covered_seconds, 0) ?? 0;
  const thermalCoveredSeconds = history?.data.reduce((sum, row) => sum + row.thermal.covered_seconds, 0) ?? 0;
  const electricalAverage = coveredAverage(summary.electricalKWh, electricalCoveredSeconds);
  const thermalAverage = coveredAverage(summary.thermalKWh, thermalCoveredSeconds);
  const totalTablePages = Math.max(1, Math.ceil((history?.data.length ?? 0) / tablePageSize));
  const safePage = Math.min(page, totalTablePages);
  const tableRows = history?.data.slice((safePage - 1) * tablePageSize, safePage * tablePageSize) ?? [];
  const sourceTimezone = history?.timezone ?? (plugin?.config as { timezone?: string } | undefined)?.timezone ?? "UTC";
  const timezone = validDashboardTimezone(searchParams.get("timezone"), sourceTimezone);
  const assetID = searchParams.get("asset") ?? "all";
  const hierarchyUnavailable = assetID !== "all" && !ownerAssets.some((asset) => asset.id === assetID);

  function setRange(nextRange: { from: string; to: string }, nextBucket?: DataLoggerQueryBucket) {
    setState("loading");
    setMessage("");
    setSearchParams(updateUtilityFilters(searchParams, { from: nextRange.from, to: nextRange.to, bucket: nextBucket ?? automaticEnergyBucket(nextRange.from, nextRange.to) }), { replace: true });
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

  function toggleComparison(enabled: boolean) {
    setState("loading");
    setSearchParams(updateUtilityFilters(searchParams, { compare: enabled ? "previous" : null }), { replace: true });
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
          <div className="energy-history-global-filters" aria-label="Global dashboard filters"><label><span>Display timezone</span><select aria-label="Display timezone" value={timezone} onChange={(event) => setSearchParams(updateUtilityFilters(searchParams, { timezone: event.target.value }), { replace: true })}><option value={sourceTimezone}>{sourceTimezone} (source)</option>{sourceTimezone !== "UTC" && <option value="UTC">UTC</option>}{sourceTimezone !== "Asia/Bangkok" && <option value="Asia/Bangkok">Asia/Bangkok</option>}</select></label><label><span>Hierarchy</span><select aria-label="Dashboard hierarchy" disabled={hierarchyLoading} value={assetID} onChange={(event) => setSearchParams(updateUtilityFilters(searchParams, { asset: event.target.value === "all" ? null : event.target.value }), { replace: true })}><option value="all">All mapped output</option>{ownerAssets.map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label></div>
          <label className="energy-history-compare"><input type="checkbox" checked={searchParams.get("compare") === "previous"} onChange={(event) => toggleComparison(event.target.checked)} />Compare previous equal period</label>
          {filterError && <div className="energy-history-filter-error" role="alert"><CircleAlert />{filterError}</div>}
        </section>

        {state === "loading" && history && <div className="energy-history-update" role="status" aria-live="polite"><LoaderCircle className="is-spinning" /><span><strong>Updating Energy history…</strong><small>Current charts remain visible until the Plugin returns the replacement range.</small></span></div>}
        {message && (history || state !== "error") && <div className="energy-history-alert" role="alert"><CircleAlert />{message}</div>}
        {state === "loading" && !history && <div className="energy-history-state" role="status"><LoaderCircle className="is-spinning" /><strong>Calculating Energy history…</strong><span>The Plugin is integrating synchronized Logger batches.</span></div>}
        {state === "error" && !history && <div className="energy-history-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Energy history</strong><span>{message}</span><button type="button" onClick={() => { setState("loading"); setRefreshVersion((current) => current + 1); }}>Try again</button></div>}

        {history && hierarchyUnavailable && !hierarchyLoading && <div className="energy-history-state"><CircleAlert /><strong>No Energy data for this hierarchy</strong><span>The selected Asset does not own an output from this Plugin instance, so instance totals are intentionally excluded.</span></div>}
        {history && !hierarchyUnavailable && <>
          <section className="energy-history-summary" aria-label="Selected range summary">
            <article><Zap /><span>Electrical energy</span><strong>{formatNumber(summary.electricalKWh)} kWh</strong><small>{formatNumber(summary.electricalCoverage, 1)}% coverage</small></article>
            <article><Activity /><span>Thermal energy</span><strong>{formatNumber(summary.thermalKWh)} kWh</strong><small>{formatNumber(summary.thermalCoverage, 1)}% coverage</small></article>
            <article><WalletCards /><span>Estimated cost</span><strong>{summary.cost === null ? "Unavailable" : `${formatNumber(summary.cost)} ${history.currency}`}</strong><small>{history.tariff_mode === "tag" ? "Synchronized tariff Tag" : `${formatNumber(history.rate_per_kwh)} ${history.currency}/kWh`}</small></article>
            <article><Gauge /><span>Covered-period COP</span><strong>{summary.cop === null ? "Unavailable" : formatNumber(summary.cop)}</strong><small>Thermal kWh ÷ electrical kWh</small></article>
            <article className={summary.affectedBuckets > 0 ? "has-issues" : ""}><CircleAlert /><span>Affected buckets</span><strong>{summary.affectedBuckets}</strong><small>Unavailable metrics or integration issues</small></article>
          </section>

          <section className="energy-history-provenance" aria-label="Electricity source period"><div><span>Source period</span><strong>{formatPeriod(range.from, timezone, bucket)} — {formatPeriod(range.to, timezone, bucket)}</strong></div><div><span>Persisted source</span><strong>Logger {history.logger_id}</strong></div><div><span>Comparison</span><strong>{comparisonSummary ? `${formatNumber(comparisonSummary.electricalKWh)} kWh previous period` : "Off"}</strong></div></section>

          <section className="thermal-dashboard" aria-labelledby="thermal-dashboard-heading">
            <header><div><Thermometer /><span><h2 id="thermal-dashboard-heading">Thermal Energy &amp; COP</h2><small>Logger-derived covered-period performance</small></span></div><Link to={`/plugins/${encodeURIComponent(id)}/energy`}>Open live thermal circuit</Link></header>
            <div className="thermal-dashboard-kpis" role="region" aria-label="Thermal performance summary">
              <article><span>Thermal energy</span><strong>{formatNumber(summary.thermalKWh)} kWh</strong><small>{formatNumber(summary.thermalCoverage, 1)}% coverage</small></article>
              <article><span>Average thermal output</span><strong>{thermalAverage === null ? "Unavailable" : `${formatNumber(thermalAverage)} kW`}</strong><small>Thermal kWh ÷ covered hours</small></article>
              <article><span>Average electrical input</span><strong>{electricalAverage === null ? "Unavailable" : `${formatNumber(electricalAverage)} kW`}</strong><small>Electrical kWh ÷ covered hours</small></article>
              <article><span>Covered-period COP</span><strong>{summary.cop === null ? "Unavailable" : formatNumber(summary.cop)}</strong><small>Thermal kWh ÷ electrical kWh</small></article>
            </div>
            <div className="thermal-source-boundary" role="region" aria-label="Thermal circuit history availability"><span>Supply temperature <strong>Live circuit only</strong></span><span>Return temperature / ΔT <strong>Live circuit only</strong></span><span>Flow rate <strong>Live circuit only</strong></span><p>The Energy history schema does not persist these circuit inputs yet, so this dashboard does not reconstruct or interpolate them.</p></div>
          </section>

          {history.data.length === 0 ? <div className="energy-history-state"><BarChart3 /><strong>No Energy history in this range</strong><span>Choose another window or wait for synchronized Logger batches.</span></div> : <>
            <section className="energy-history-chart is-wide" aria-label="Electrical and thermal Energy chart">
              <header><div><BarChart3 /><span><h2>Energy by {bucketLabels[bucket]}</h2><small>Electrical and thermal integration per Plugin bucket</small></span></div><code>kWh</code></header>
              <div role="img" aria-label="Grouped bars comparing electrical and thermal kilowatt-hours over time"><ResponsiveContainer width="100%" height="100%"><BarChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 11 }} /><YAxis tick={{ fill: "#8295a4", fontSize: 11 }} /><Tooltip /><Legend /><Bar dataKey="electrical" name="Electrical kWh" fill="#34c3ff" radius={[3, 3, 0, 0]} />{comparison && <Bar dataKey="previousElectrical" name="Previous electrical kWh" fill="#567082" radius={[3, 3, 0, 0]} />}<Bar dataKey="thermal" name="Thermal kWh" fill="#8d7cf6" radius={[3, 3, 0, 0]} /></BarChart></ResponsiveContainer></div>
            </section>

            <div className="energy-history-chart-grid electricity-cost-grid">
              <section className="energy-history-chart" aria-label="Electrical demand profile"><header><div><Gauge /><span><h2>Demand profile</h2><small>Covered-period average demand; gaps are not interpolated</small></span></div><code>kW</code></header><div role="img" aria-label="Line chart of average electrical demand over time"><ResponsiveContainer width="100%" height="100%"><LineChart data={chartData}><CartesianGrid stroke="rgba(92,118,136,.18)" vertical={false} /><XAxis dataKey="at" minTickGap={28} tick={{ fill: "#8295a4", fontSize: 10 }} /><YAxis tick={{ fill: "#8295a4", fontSize: 10 }} /><Tooltip /><Line type="monotone" dataKey="demand" name="Average demand (kW)" stroke="#34c3ff" strokeWidth={2} dot={false} connectNulls={false} /></LineChart></ResponsiveContainer></div></section>
              <section className="energy-history-chart" aria-label="Cost distribution"><header><div><WalletCards /><span><h2>Cost distribution</h2><small>Valid tariff cost contribution by source bucket</small></span></div><code>{history.currency}</code></header><div role="img" aria-label={`Donut chart of cost contribution in ${history.currency}`}><ResponsiveContainer width="100%" height="100%"><PieChart><Tooltip /><Pie data={costDistribution} dataKey="value" nameKey="name" innerRadius="48%" outerRadius="78%" fill="#34c3ff" /></PieChart></ResponsiveContainer></div></section>
            </div>

            <section className="energy-history-chart is-wide production-chart-panel" aria-label="Thermal and electrical input trend"><header><div><Thermometer /><span><h2>Thermal output and electrical input</h2><small>Covered-bucket averages preserve unavailable periods as gaps</small></span></div><code>kW</code></header><ProductionLineChart data={chartData} xKey="at" unit="kW" ariaLabel="Interactive line chart comparing average thermal output and electrical input" series={[{ key: "thermalDemand", label: "Average thermal output", color: "#8d7cf6" }, { key: "demand", label: "Average electrical input", color: "#34c3ff" }, ...(comparison ? [{ key: "previousElectricalDemand", label: "Previous electrical input", color: "#8295a4", dashed: true }] : [])]} /></section>

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
