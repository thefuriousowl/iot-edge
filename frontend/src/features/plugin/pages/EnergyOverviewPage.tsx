import axios from "axios";
import {
  Activity,
  CalendarDays,
  CheckCircle2,
  ChevronRight,
  CircleAlert,
  Clock3,
  Coins,
  Gauge,
  LoaderCircle,
  RadioTower,
  RefreshCw,
  RotateCcw,
  Settings2,
  Thermometer,
  Zap,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { getDataLogger } from "../../../services/datalogger.service";
import { exportEnergyArchiveCSV, getEnergyArchives, getEnergyOverview, monitorEnergy, resetEnergyMeasurement } from "../../../services/energy.service";
import { getPlugin } from "../../../services/plugin.service";
import type { DataLogger, DataLoggerTagReference } from "../../../types/datalogger";
import type {
  EnergyBatchMetrics,
  EnergyConfig,
  EnergyOverviewResponse,
  EnergyMeasurementRun,
  EnergyPeriodSummary,
  PluginInstance,
} from "../../../types/plugin";
import type { TagRuntimeValue } from "../../../types/tag";
import { useTagLiveStore } from "../../tag/stores/tagLive.store";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import EnergyPluginTabs from "../components/EnergyPluginTabs";
import { downloadBlob } from "../utils/dashboardExport";
import "../../vgateway/pages/VGatewayListPage.css";
import "./EnergyOverviewPage.css";
import "./EnergyOverviewThermal.css";

type LoadState = "loading" | "ready" | "error";
type EnergyStreamState = "connecting" | "live" | "retrying" | "disconnected";

interface LoadedOverview {
  plugin: PluginInstance;
  logger: DataLogger;
  overview: EnergyOverviewResponse;
}

interface EnergyQualityIssue {
  key: string;
  code: string;
  message: string;
  at: string;
}

function readableError(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load the Energy overview. Check the API connection and try again.";
}

function formatNumber(value: number, maximumFractionDigits = 2): string {
  if (!Number.isFinite(value)) return "—";
  return new Intl.NumberFormat(undefined, {
    maximumFractionDigits,
    minimumFractionDigits: Math.min(2, maximumFractionDigits),
  }).format(value);
}

function formatDateTime(value: string | undefined, timezone?: string): string {
  if (!value) return "Not available";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Unknown time";
  try {
    return new Intl.DateTimeFormat(undefined, {
      dateStyle: "medium",
      timeStyle: "medium",
      timeZone: timezone,
    }).format(date);
  } catch {
    return date.toLocaleString();
  }
}

function formatPeriodDate(value: string, timezone: string): string {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "Unknown date";
  try {
    return new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeZone: timezone }).format(date);
  } catch {
    return date.toLocaleDateString();
  }
}

function formatRuntimeValue(value: TagRuntimeValue["value"]): string {
  if (value === null) return "—";
  if (typeof value === "boolean") return value ? "true" : "false";
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function segmentIssueMessage(code: string): string {
  if (code === "stale_gap") return "Data gap exceeded the configured maximum sample gap.";
  if (code === "no_coverage") return "No synchronized samples cover this integration segment.";
  if (code === "missing_sample") return "A required synchronized Tag sample is missing.";
  if (code === "source_bad") return "A source Tag reported bad quality.";
  return "An invalid value prevented this integration segment from being calculated.";
}

function periodIssues(period: EnergyPeriodSummary): EnergyQualityIssue[] {
  const metricIssues: EnergyQualityIssue[] = [];
  if (!period.cost.valid) metricIssues.push({ key: `cost-${period.from}`, code: "cost_unavailable", message: period.cost.error ?? "Cost is unavailable for this period.", at: period.from });
  if (!period.cop.valid) metricIssues.push({ key: `cop-${period.from}`, code: "cop_unavailable", message: period.cop.error ?? "Period COP is unavailable.", at: period.from });
  const segmentIssues = [...(period.electrical.issues ?? []), ...(period.thermal.issues ?? [])]
    .flatMap((issue, issueIndex) => issue.errors?.length
      ? issue.errors.map((error, errorIndex) => ({
        key: `${issue.from}-${issue.to}-${error.code}-${error.tag_id ?? "metric"}-${issueIndex}-${errorIndex}`,
        code: error.code,
        message: error.message,
        at: error.at,
      }))
      : [{
        key: `${issue.from}-${issue.to}-${issue.code}-${issueIndex}`,
        code: issue.code,
        message: segmentIssueMessage(issue.code),
        at: issue.from,
      }]);
  return [...metricIssues, ...segmentIssues];
}

function latestErrors(metrics: EnergyBatchMetrics | null): EnergyQualityIssue[] {
  if (!metrics) return [];
  return [
    ...(metrics.electrical.errors ?? []),
    ...(metrics.thermal.errors ?? []),
    ...(metrics.tariff.errors ?? []),
  ].map((error, index) => ({ key: `latest-${error.code}-${error.tag_id ?? "metric"}-${error.at}-${index}`, code: error.code, message: error.message, at: error.at }));
}

function inferredTagUnit(tag: DataLoggerTagReference, config: EnergyConfig, currency: string): string {
  const powerMapping = [...config.electrical_power_tags, ...(config.thermal_power_tags ?? [])]
    .find((mapping) => mapping.tag_id === tag.id);
  if (powerMapping) return powerMapping.unit;
  if (config.tariff.mode === "tag" && config.tariff.tag_id === tag.id) return `${currency}/kWh`;

  const name = tag.name.toLowerCase();
  if (name === "hz" || name.endsWith("_hz")) return "Hz";
  if (name.includes("temp")) return "°C";
  if (name.includes("flow_m3h") || name.includes("flow m3h")) return "m³/h";
  if (name.includes("voltage")) return "V";
  if (name.includes("current")) return "A";
  return tag.data_type;
}

function normalizedTagName(name: string): string {
  return name.toLowerCase().replace(/[^a-z0-9]/g, "");
}

function findThermalTag(tags: DataLoggerTagReference[], role: "supply" | "return" | "flow"): DataLoggerTagReference | undefined {
  const aliases = {
    supply: ["tempsupply", "supplytemp", "supplytemperature"],
    return: ["tempreturn", "returntemp", "returntemperature"],
    flow: ["flowm3h", "flowrate", "flowratem3h"],
  }[role];
  return tags.find((tag) => aliases.includes(normalizedTagName(tag.name)));
}

function numericGoodValue(value: TagRuntimeValue | null): number | null {
  return value?.quality === "good" && typeof value.value === "number" && Number.isFinite(value.value)
    ? value.value
    : null;
}

function ThermalCircuitBand({ tags }: { tags: DataLoggerTagReference[] }) {
  const supplyTag = findThermalTag(tags, "supply");
  const returnTag = findThermalTag(tags, "return");
  const flowTag = findThermalTag(tags, "flow");
  const supplyValue = useTagLiveStore((state) => state.entries[supplyTag?.id ?? ""]?.value ?? null);
  const returnValue = useTagLiveStore((state) => state.entries[returnTag?.id ?? ""]?.value ?? null);
  const flowValue = useTagLiveStore((state) => state.entries[flowTag?.id ?? ""]?.value ?? null);

  if (!supplyTag && !returnTag && !flowTag) return null;

  const supply = numericGoodValue(supplyValue);
  const returnTemperature = numericGoodValue(returnValue);
  const flow = numericGoodValue(flowValue);
  const temperatureDifference = supply !== null && returnTemperature !== null
    ? Math.abs(returnTemperature - supply)
    : null;

  return (
    <section className="energy-thermal-band" aria-label="Thermal circuit">
      <header>
        <div><Thermometer aria-hidden="true" /><span><h2>Thermal circuit</h2><small>Live Logger values</small></span></div>
        <span>ΔT uses |TempReturn − TempSupply|</span>
      </header>
      <div>
        <article><span>Supply temperature</span><strong>{supply === null ? "Unavailable" : `${formatNumber(supply)} °C`}</strong><small>{supplyTag?.name ?? "TempSupply Tag not configured"}</small></article>
        <article><span>Return temperature</span><strong>{returnTemperature === null ? "Unavailable" : `${formatNumber(returnTemperature)} °C`}</strong><small>{returnTag?.name ?? "TempReturn Tag not configured"}</small></article>
        <article><span>Temperature difference</span><strong>{temperatureDifference === null ? "Unavailable" : `${formatNumber(temperatureDifference)} °C`}</strong><small>|TempReturn − TempSupply|</small></article>
        <article><span>Flow rate</span><strong>{flow === null ? "Unavailable" : `${formatNumber(flow)} m³/h`}</strong><small>{flowTag?.name ?? "Flow_m3h Tag not configured"}</small></article>
      </div>
    </section>
  );
}

function PeriodPanel({ label, period, timezone, currency }: {
  label: string;
  period: EnergyPeriodSummary;
  timezone: string;
  currency: string;
}) {
  const issueCount = (period.electrical.issues?.length ?? 0) + (period.thermal.issues?.length ?? 0);
  return (
    <article className="energy-period-panel" data-source="persisted-logger-history">
      <header>
        <h2>{label}<small>Persisted Logger history</small></h2>
        <span><CalendarDays aria-hidden="true" size={15} />{formatPeriodDate(period.from, timezone)}</span>
      </header>
      <div className="energy-period-metrics">
        <div><span>Electrical kWh</span><strong>{formatNumber(period.electrical.kilowatt_hours)} kWh</strong></div>
        <div><span>Thermal kWh</span><strong>{formatNumber(period.thermal.kilowatt_hours)} kWh</strong></div>
        <div><span>Estimated cost</span><strong>{period.cost.valid ? `${formatNumber(period.cost.value)} ${currency}` : "Unavailable"}</strong></div>
        <div><span>Period COP</span><strong>{period.cop.valid ? formatNumber(period.cop.value) : "Unavailable"}</strong></div>
      </div>
      <div className="energy-coverage-row">
        <span>Electrical coverage</span>
        <strong>{formatNumber(period.electrical.coverage_percent, 1)}%</strong>
        <div role="progressbar" aria-label="Electrical coverage" aria-valuemin={0} aria-valuemax={100} aria-valuenow={Math.max(0, Math.min(100, period.electrical.coverage_percent))}><i style={{ width: `${Math.max(0, Math.min(100, period.electrical.coverage_percent))}%` }} /></div>
        <small>{formatNumber(period.electrical.covered_seconds / 3600, 1)} h covered · {issueCount} issues</small>
      </div>
    </article>
  );
}

function LiveTagRow({ tag, config, currency }: {
  tag: DataLoggerTagReference;
  config: EnergyConfig;
  currency: string;
}) {
  const value = useTagLiveStore((state) => state.entries[tag.id]?.value ?? null);
  const connectionState = useTagLiveStore((state) => state.connectionState);
  const quality = value?.quality === "bad"
    ? "error"
    : !value
      ? "waiting"
      : connectionState === "disconnected"
        ? "stale"
        : "good";
  return (
    <tr>
      <td data-label="Tag"><Link to={`/tags/${encodeURIComponent(tag.id)}`}>{tag.name}</Link><small>{tag.type}</small></td>
      <td data-label="Live value"><strong>{value ? formatRuntimeValue(value.value) : "No sample"}</strong>{value?.error && <small title={value.error}>{value.error}</small>}</td>
      <td data-label="Unit"><code>{inferredTagUnit(tag, config, currency)}</code></td>
      <td data-label="Quality"><span className={`energy-tag-quality is-${quality}`}><i />{quality}</span></td>
      <td data-label="Timestamp"><time dateTime={value?.observed_at}>{value ? formatDateTime(value.observed_at) : "Waiting for publication"}</time></td>
      <td><Link aria-label={`Open ${tag.name}`} to={`/tags/${encodeURIComponent(tag.id)}`}><ChevronRight aria-hidden="true" /></Link></td>
    </tr>
  );
}

function EnergyOverviewPage() {
  const { id = "" } = useParams();
  const [loaded, setLoaded] = useState<LoadedOverview | null>(null);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [message, setMessage] = useState("");
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [liveMetrics, setLiveMetrics] = useState<EnergyBatchMetrics | null>(null);
  const [streamState, setStreamState] = useState<EnergyStreamState>("connecting");
  const [streamMessage, setStreamMessage] = useState("Connecting to synchronized Energy metrics…");
  const [streamVersion, setStreamVersion] = useState(0);
  const [resetOpen, setResetOpen] = useState(false);
  const [resetName, setResetName] = useState("");
  const [resetReason, setResetReason] = useState("");
  const [resetting, setResetting] = useState(false);
  const [archives, setArchives] = useState<EnergyMeasurementRun[]>([]);
  const [exportingArchive, setExportingArchive] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      getPlugin(id, controller.signal),
      getEnergyOverview(id, controller.signal),
    ]).then(async ([plugin, overview]) => {
      const logger = await getDataLogger(overview.logger_id, controller.signal);
      if (controller.signal.aborted) return;
      setLoaded({ plugin, overview, logger });
      setLiveMetrics((current) => {
        if (!current) return overview.latest;
        if (!overview.latest) return current;
        return new Date(current.batch_at).getTime() >= new Date(overview.latest.batch_at).getTime() ? current : overview.latest;
      });
      setLoadState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(readableError(error));
      setLoadState("error");
    });
    return () => controller.abort();
  }, [id, refreshVersion]);

  useEffect(() => {
    const controller = new AbortController();
    void getEnergyArchives(id, controller.signal).then((runs) => { if (!controller.signal.aborted) setArchives(runs); }).catch(() => { if (!controller.signal.aborted) setArchives([]); });
    return () => controller.abort();
  }, [id, refreshVersion]);

  useEffect(() => {
    const controller = new AbortController();
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    let retryDelay = 4_500;
    let lastEventId: string | null = null;

    const connect = async () => {
      setStreamState((current) => current === "connecting" ? "connecting" : "retrying");
      try {
        await monitorEnergy(id, {
          signal: controller.signal,
          lastEventId,
          onOpen: () => {
            setStreamState("live");
            setStreamMessage("Receiving synchronized Logger batches");
          },
          onMessage: (event) => {
            lastEventId = event.id;
            setLiveMetrics(event.metrics);
            setStreamState("live");
            setStreamMessage("Receiving synchronized Logger batches");
          },
          onReset: () => {
            lastEventId = null;
            setStreamMessage("Stream cursor reset; synchronized latest metrics restored");
          },
          onRetry: (milliseconds) => {
            retryDelay = Math.max(1_000, Math.min(30_000, milliseconds));
          },
        });
        if (!controller.signal.aborted) {
          setStreamState("disconnected");
          setStreamMessage("Energy stream ended; reconnecting…");
          retryTimer = setTimeout(() => void connect(), retryDelay);
        }
      } catch {
        if (controller.signal.aborted) return;
        setStreamState("disconnected");
        setStreamMessage("Energy stream unavailable; retrying automatically");
        retryTimer = setTimeout(() => void connect(), retryDelay);
      }
    };

    void connect();
    return () => {
      controller.abort();
      if (retryTimer) clearTimeout(retryTimer);
    };
  }, [id, streamVersion]);

  const config = loaded?.plugin.config as EnergyConfig | undefined;
  const displayedMetrics = liveMetrics ?? loaded?.overview.latest ?? null;
  const tagStreamState = useTagLiveStore((state) => state.connectionState);
  const qualityIssues = useMemo(() => {
    if (!loaded) return [];
    return [...latestErrors(displayedMetrics), ...periodIssues(loaded.overview.today)];
  }, [displayedMetrics, loaded]);
  const skippedSegments = loaded
    ? loaded.overview.today.electrical.skipped_segments + loaded.overview.today.thermal.skipped_segments
    : 0;

  const requestRefresh = () => {
    setLoadState("loading");
    setMessage("");
    setRefreshVersion((current) => current + 1);
  };

  const requestStreamReconnect = () => {
    setStreamState("connecting");
    setStreamMessage("Connecting to synchronized Energy metrics…");
    setStreamVersion((current) => current + 1);
  };

  const requestReset = async () => {
    if (!loaded?.overview.run || !resetName.trim()) return;
    setResetting(true);
    setMessage("");
    try {
      await resetEnergyMeasurement(id, { expected_run_id: loaded.overview.run.id, name: resetName.trim(), reason: resetReason.trim() });
      setResetOpen(false);
      setResetName("");
      setResetReason("");
      requestRefresh();
    } catch (error) {
      setMessage(readableError(error));
    } finally {
      setResetting(false);
    }
  };

  const requestArchiveExport = async (run: EnergyMeasurementRun) => {
    setExportingArchive(run.id);
    setMessage("");
    try {
      const blob = await exportEnergyArchiveCSV(id, run.id);
      downloadBlob(blob, `energy-${run.name.toLowerCase().replace(/[^a-z0-9]+/g, "-").replace(/^-|-$/g, "") || run.id}.csv`);
    } catch (error) {
      setMessage(readableError(error));
    } finally {
      setExportingArchive("");
    }
  };

  return (
    <VGatewayShell breadcrumb={<>Plugins <span>/</span> <strong>{loaded?.plugin.name ?? "Energy"}</strong></>}>
      <div className="energy-overview-content">
        {loadState === "loading" && !loaded && <div className="energy-overview-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Energy overview…</strong><span>Reading synchronized Logger history and Plugin state.</span></div>}
        {loadState === "error" && !loaded && <div className="energy-overview-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Energy overview</strong><span>{message}</span><button type="button" onClick={requestRefresh}>Try again</button></div>}
        {loaded && config && <>
          <section className="energy-overview-heading">
            <div><h1>{loaded.plugin.name}</h1><p>Committed-batch SSE powers live demand; persisted Logger history powers kWh, cost, and period COP.</p></div>
            <div><Link to={`/plugins/${encodeURIComponent(id)}/configure`}><Settings2 aria-hidden="true" size={17} />Configure</Link><button type="button" className="is-reset" disabled={!loaded.overview.run || resetting} onClick={() => { setResetName(""); setResetReason(""); setResetOpen(true); }}><RotateCcw aria-hidden="true" size={17} />Reset measurement</button><button type="button" disabled={loadState === "loading"} onClick={requestRefresh}><RefreshCw className={loadState === "loading" ? "is-spinning" : ""} aria-hidden="true" size={17} />Refresh</button></div>
          </section>

          <EnergyPluginTabs instanceID={id} />

          {message && <div className="energy-overview-alert" role="alert"><CircleAlert aria-hidden="true" />{message}</div>}

          {resetOpen && <section className="energy-reset-panel" role="dialog" aria-modal="true" aria-labelledby="energy-reset-title">
            <h2 id="energy-reset-title">Start measurement at a new point</h2>
            <p>This archives the current run and starts totals from the latest committed Logger batch. It does not reset the power meter or delete Logger data.</p>
            <label>New measurement point<input autoFocus maxLength={100} value={resetName} onChange={(event) => setResetName(event.target.value)} placeholder="e.g. Chiller 2 inlet" /></label>
            <label>Operator note (optional)<textarea maxLength={500} value={resetReason} onChange={(event) => setResetReason(event.target.value)} /></label>
            <div><button type="button" onClick={() => setResetOpen(false)} disabled={resetting}>Cancel</button><button type="button" onClick={() => void requestReset()} disabled={resetting || !resetName.trim()}>{resetting ? "Archiving…" : "Archive and start new run"}</button></div>
          </section>}

          <section className="energy-runtime-band" aria-label="Energy runtime">
            <div><span className={`energy-runtime-state is-${loaded.plugin.runtime.state}`}><i />{loaded.plugin.runtime.state}</span><small>{loaded.plugin.enabled ? "Desired enabled" : "Saved disabled"}</small></div>
            <div><span>Energy stream</span><strong className={`is-${streamState}`}><RadioTower aria-hidden="true" />{streamState}</strong><small>{streamMessage}</small></div>
            <div><span>Latest batch</span><strong><Clock3 aria-hidden="true" />{formatDateTime(displayedMetrics?.batch_at, loaded.overview.timezone)}</strong><small>{loaded.logger.name} · {loaded.overview.timezone}</small></div>
          </section>

          {streamState !== "live" && <div className="energy-stream-notice" role="status" aria-live="polite"><RadioTower aria-hidden="true" /><span><strong>Energy stream {streamState}</strong><small>Showing the last synchronized Logger batch while the Plugin stream reconnects. No live Tag value is substituted into Energy calculations.</small></span></div>}

          <section className={`energy-live-band ${streamState !== "live" ? "is-stale" : ""}`} data-source="energy-sse" aria-label="Live operational metrics" aria-busy={streamState === "connecting" || streamState === "retrying"}>
            <header><h2>Live operational</h2><span>{displayedMetrics ? "Energy SSE · synchronized batch" : "Energy SSE · waiting for first batch"}</span></header>
            <div>
              <article><Zap aria-hidden="true" /><span>Electrical demand</span><strong>{displayedMetrics?.electrical.valid ? `${formatNumber(displayedMetrics.electrical.kilowatts)} kW` : "Unavailable"}</strong></article>
              <article><Thermometer aria-hidden="true" /><span>Thermal output</span><strong>{displayedMetrics?.thermal.valid ? `${formatNumber(displayedMetrics.thermal.kilowatts)} kW` : "Unavailable"}</strong></article>
              <article><Gauge aria-hidden="true" /><span>Instantaneous COP</span><strong>{displayedMetrics?.cop.valid ? formatNumber(displayedMetrics.cop.value) : "Unavailable"}</strong></article>
              <article><Coins aria-hidden="true" /><span>Tariff</span><strong>{displayedMetrics?.tariff.valid ? `${formatNumber(displayedMetrics.tariff.rate_per_kwh)} ${loaded.overview.currency}/kWh` : "Unavailable"}</strong><small>{loaded.overview.tariff_mode === "tag" ? "Synchronized Tag rate" : "Fixed rate"}</small></article>
            </div>
          </section>

          <ThermalCircuitBand tags={loaded.logger.tags ?? []} />

          <div className="energy-period-grid">
            <PeriodPanel label="Today" period={loaded.overview.today} timezone={loaded.overview.timezone} currency={loaded.overview.currency} />
            <PeriodPanel label="This month" period={loaded.overview.month} timezone={loaded.overview.timezone} currency={loaded.overview.currency} />
          </div>

          <section className="energy-archive-panel" aria-labelledby="energy-archive-title">
            <header><div><h2 id="energy-archive-title">Measurement archives</h2><p>Completed measurement points remain exportable independently of Logger retention.</p></div><strong>{archives.length}</strong></header>
            {archives.length === 0 ? <p className="energy-archive-empty">No archived measurement runs yet.</p> : <ul>{archives.map((run) => <li key={run.id}><span><strong>{run.name}</strong><small>{formatDateTime(run.started_at, loaded.overview.timezone)} – {formatDateTime(run.ended_at, loaded.overview.timezone)}{run.reason ? ` · ${run.reason}` : ""}</small></span><button type="button" disabled={exportingArchive === run.id} onClick={() => void requestArchiveExport(run)}>{exportingArchive === run.id ? "Exporting…" : "Export CSV"}</button></li>)}</ul>}
          </section>

          <section className={`energy-quality-band ${qualityIssues.length > 0 || skippedSegments > 0 ? "has-issues" : ""}`} aria-labelledby="energy-quality-heading">
            <div>{qualityIssues.length > 0 || skippedSegments > 0 ? <CircleAlert aria-hidden="true" /> : <CheckCircle2 aria-hidden="true" />}<span><h2 id="energy-quality-heading">Data quality</h2><strong>{qualityIssues.length > 0 || skippedSegments > 0 ? "Coverage needs attention" : "All available sources healthy"}</strong><small>{qualityIssues.length > 0 ? qualityIssues[0].message : skippedSegments > 0 ? "One or more integration segments were skipped." : "No source errors in the current synchronized view."}</small></span></div>
            <dl><div><dt>Today coverage</dt><dd>{formatNumber(loaded.overview.today.electrical.coverage_percent, 1)}%</dd></div><div><dt>Skipped segments</dt><dd>{skippedSegments}</dd></div><div><dt>Quality issues</dt><dd>{qualityIssues.length}</dd></div></dl>
            {qualityIssues.length > 0 && <ul>{qualityIssues.slice(0, 4).map((issue) => <li key={issue.key}><code>{issue.code}</code><span>{issue.message}</span><time dateTime={issue.at}>{formatDateTime(issue.at, loaded.overview.timezone)}</time></li>)}</ul>}
          </section>

          <section className="energy-tag-panel" aria-labelledby="energy-live-tags-heading">
            <header><div><h2 id="energy-live-tags-heading">Live Logger Tags <span>({loaded.logger.tags?.length ?? 0})</span></h2><p>Current normalized values from the shared Tag stream. Energy does not create acquisition requests.</p></div><span className={`energy-global-stream is-${tagStreamState}`}><i />Tag stream {tagStreamState}</span></header>
            {!loaded.logger.tags?.length && <div className="energy-tag-empty"><Activity aria-hidden="true" /><strong>No Logger Tags available</strong><span>Update the Data Logger selection before using this dashboard.</span></div>}
            {!!loaded.logger.tags?.length && <div className="energy-tag-table"><table><thead><tr><th>Tag</th><th>Live value</th><th>Unit</th><th>Quality</th><th>Timestamp</th><th><span className="sr-only">Open Tag</span></th></tr></thead><tbody>{loaded.logger.tags.map((tag) => <LiveTagRow key={tag.id} tag={tag} config={config} currency={loaded.overview.currency} />)}</tbody></table></div>}
          </section>

          <footer className="energy-overview-footer"><span>Plugin ID <code>{loaded.plugin.id}</code></span><span>Logger <Link to={`/data-loggers/${encodeURIComponent(loaded.logger.id)}`}>{loaded.logger.name}</Link></span><button type="button" disabled={streamState === "connecting"} onClick={requestStreamReconnect}>Reconnect Energy stream</button></footer>
        </>}
      </div>
    </VGatewayShell>
  );
}

export default EnergyOverviewPage;
