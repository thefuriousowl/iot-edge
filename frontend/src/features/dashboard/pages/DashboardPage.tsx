import {
  Boxes,
  ChevronRight,
  CircleAlert,
  Clock3,
  Cpu,
  DatabaseZap,
  LoaderCircle,
  RadioTower,
  RefreshCw,
  Server,
  Tags,
} from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { listDataLoggers } from "../../../services/datalogger.service";
import { listDeviceInventory } from "../../../services/device.service";
import { getHealth, type HealthResponse } from "../../../services/health.service";
import { listTags } from "../../../services/tag.service";
import { listVGateways } from "../../../services/vgateway.service";
import type { DataLogger } from "../../../types/datalogger";
import type { Tag, TagRuntimeValue } from "../../../types/tag";
import type { VGatewayListItem } from "../../../types/vgateway";
import { useTagLiveStore } from "../../tag/stores/tagLive.store";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DashboardPage.css";

type DashboardTagState = "live" | "syncing" | "waiting" | "stale" | "error";

interface DashboardData {
  health: HealthResponse | null;
  gateways: VGatewayListItem[];
  gatewayTotal: number;
  deviceTotal: number;
  tags: Tag[];
  enabledTagTotal: number;
  loggers: DataLogger[];
  loggerTotal: number;
}

const emptyData: DashboardData = {
  health: null,
  gateways: [],
  gatewayTotal: 0,
  deviceTotal: 0,
  tags: [],
  enabledTagTotal: 0,
  loggers: [],
  loggerTotal: 0,
};

const runtimeDateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "medium",
});

function formatRuntimeTime(value: string | number): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown time" : runtimeDateFormatter.format(date);
}

function formatRuntimeValue(value: TagRuntimeValue["value"]): string {
  if (value === null) return "—";
  if (typeof value === "boolean") return String(value);
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function dashboardTagState(value: TagRuntimeValue | null, connectionState: string): DashboardTagState {
  if (!value) return "waiting";
  if (value.quality === "bad") return "error";
  if (connectionState === "disconnected") return "stale";
  if (connectionState === "live") return "live";
  return "syncing";
}

function DashboardTagRow({ entity }: { entity: Tag }) {
  const latest = useTagLiveStore((state) => state.entries[entity.id]?.value ?? null);
  const connectionState = useTagLiveStore((state) => state.connectionState);
  const state = dashboardTagState(latest, connectionState);

  return (
    <tr>
      <td data-label="Tag">
        <RadioTower aria-hidden="true" size={17} />
        <span>
          <Link to={`/tags/${entity.id}`}>{entity.name}</Link>
          <small>{entity.type}</small>
        </span>
      </td>
      <td data-label="Type"><code>{latest?.data_type ?? entity.data_type}</code></td>
      <td data-label="Value" className={`dashboard-live-value dashboard-live-value--${state}`}>
        <strong>{latest ? formatRuntimeValue(latest.value) : "No sample"}</strong>
        {latest?.error && <small>{latest.error}</small>}
      </td>
      <td data-label="Source time">
        <time dateTime={latest?.observed_at}>{latest ? formatRuntimeTime(latest.observed_at) : "Waiting for publication"}</time>
      </td>
      <td data-label="State">
        <span className={`dashboard-tag-state dashboard-tag-state--${state}`}><i />{state}</span>
      </td>
    </tr>
  );
}

function DashboardPage() {
  const [data, setData] = useState<DashboardData>(emptyData);
  const [loading, setLoading] = useState(true);
  const [failures, setFailures] = useState<string[]>([]);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const streamState = useTagLiveStore((state) => state.connectionState);
  const streamError = useTagLiveStore((state) => state.connectionError);
  const lastEventAt = useTagLiveStore((state) => state.lastEventAt);
  const lastSequence = useTagLiveStore((state) => state.lastSequence);
  const requestReconnect = useTagLiveStore((state) => state.requestReconnect);

  useEffect(() => {
    const controller = new AbortController();

    async function loadDashboard() {
      setLoading(true);
      const results = await Promise.allSettled([
        getHealth(controller.signal),
        listVGateways({ page: 1, per_page: 100 }, controller.signal),
        listDeviceInventory({ page: 1, per_page: 1 }, controller.signal),
        listTags({ enabled: true, page: 1, per_page: 8 }, controller.signal),
        listDataLoggers({ page: 1, per_page: 100 }, controller.signal),
      ]);
      if (controller.signal.aborted) return;

      const [health, gateways, devices, tags, loggers] = results;
      setData((current) => ({
        health: health.status === "fulfilled" ? health.value : current.health,
        gateways: gateways.status === "fulfilled" ? gateways.value.data : current.gateways,
        gatewayTotal: gateways.status === "fulfilled" ? gateways.value.pagination.total : current.gatewayTotal,
        deviceTotal: devices.status === "fulfilled" ? devices.value.pagination.total : current.deviceTotal,
        tags: tags.status === "fulfilled" ? tags.value.data : current.tags,
        enabledTagTotal: tags.status === "fulfilled" ? tags.value.pagination.total : current.enabledTagTotal,
        loggers: loggers.status === "fulfilled" ? loggers.value.data : current.loggers,
        loggerTotal: loggers.status === "fulfilled" ? loggers.value.pagination.total : current.loggerTotal,
      }));
      const labels = ["Backend", "vGateways", "Devices", "Tags", "Data Loggers"];
      setFailures(results.flatMap((result, index) => result.status === "rejected" ? [labels[index]] : []));
      setLoading(false);
    }

    void loadDashboard();
    return () => controller.abort();
  }, [refreshVersion]);

  const tagIDs = useMemo(() => data.tags.map((entity) => entity.id), [data.tags]);
  const goodTagCount = useTagLiveStore((state) => tagIDs.reduce((count, tagID) => count + (state.entries[tagID]?.value.quality === "good" ? 1 : 0), 0));
  const errorTagCount = useTagLiveStore((state) => tagIDs.reduce((count, tagID) => count + (state.entries[tagID]?.value.quality === "bad" ? 1 : 0), 0));
  const connectedGatewayCount = data.gateways.filter((gateway) => gateway.status === "connected").length;
  const enabledLoggerCount = data.loggers.filter((logger) => logger.enabled).length;
  const backendFailed = failures.includes("Backend");
  const streamLabel = streamState === "live" ? "Connected" : streamState === "disconnected" ? "Disconnected" : "Synchronizing";

  return (
    <VGatewayShell breadcrumb={<strong>Dashboard</strong>}>
      <div className="dashboard-content">
        <section className="dashboard-heading">
          <div>
            <h1>Operations overview</h1>
            <p>Monitor acquisition health and the latest normalized values.</p>
          </div>
          <button type="button" disabled={loading} onClick={() => setRefreshVersion((current) => current + 1)}>
            <RefreshCw className={loading ? "is-spinning" : ""} aria-hidden="true" size={18} />
            Refresh
          </button>
        </section>

        {failures.length > 0 && (
          <div className="dashboard-alert" role="alert">
            <CircleAlert aria-hidden="true" size={19} />
            <span><strong>{backendFailed ? "Backend unavailable." : "Dashboard refresh incomplete."}</strong> Couldn’t refresh {failures.join(", ")}.</span>
            <button type="button" onClick={() => setRefreshVersion((current) => current + 1)}>Try again</button>
          </div>
        )}

        <section className="dashboard-status-band" aria-label="Runtime health">
          <article role={loading && !data.health ? "status" : undefined}>
            <Server aria-hidden="true" size={27} />
            <div>
              <span>Backend</span>
              <strong>{data.health ? "Backend connected" : backendFailed ? "Backend unavailable" : "Checking backend connection…"}</strong>
              <small>{data.health ? `${data.health.status} · version ${data.health.version}` : "API health and version"}</small>
            </div>
          </article>
          <article>
            <RadioTower aria-hidden="true" size={27} />
            <div>
              <span>Tag stream</span>
              <strong className={`dashboard-stream dashboard-stream--${streamState}`}><i />{streamLabel}</strong>
              {streamState === "disconnected" ? <button type="button" title={streamError ?? undefined} onClick={requestReconnect}>Reconnect</button> : <small>One shared authenticated stream</small>}
            </div>
          </article>
          <article>
            <Clock3 aria-hidden="true" size={27} />
            <div>
              <span>Latest event</span>
              <strong>{lastEventAt ? formatRuntimeTime(lastEventAt) : "No runtime event yet"}</strong>
              <small>{lastSequence > 0 ? `Sequence ${lastSequence}` : "Waiting for Tag publication"}</small>
            </div>
          </article>
        </section>

        <section className="dashboard-metrics" aria-label="Inventory summary">
          <article><Cpu aria-hidden="true" /><div><span>vGateways</span><strong>{data.gatewayTotal}</strong><small>{connectedGatewayCount} connected</small></div></article>
          <article><Boxes aria-hidden="true" /><div><span>Devices</span><strong>{data.deviceTotal}</strong><small>Across all gateways</small></div></article>
          <article><Tags aria-hidden="true" /><div><span>Enabled Tags</span><strong>{data.enabledTagTotal}</strong><small>{goodTagCount} good · {errorTagCount} error shown</small></div></article>
          <article><DatabaseZap aria-hidden="true" /><div><span>Data Loggers</span><strong>{data.loggerTotal}</strong><small>{enabledLoggerCount} enabled</small></div></article>
        </section>

        <div className="dashboard-main-grid">
          <section className="dashboard-live-panel" aria-labelledby="dashboard-live-heading">
            <header><div><h2 id="dashboard-live-heading">Live Tags</h2><p>Latest normalized values from the shared runtime stream.</p></div><Link to="/tags">View all Tags <ChevronRight aria-hidden="true" size={17} /></Link></header>
            {loading && data.tags.length === 0 && <div className="dashboard-panel-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading enabled Tags…</strong><span>Preparing the live operational view.</span></div>}
            {!loading && data.tags.length === 0 && <div className="dashboard-panel-state"><Tags /><strong>No enabled Tags yet</strong><span>Create or enable a Tag to publish normalized values here.</span><Link to="/tags/new">Add Tag</Link></div>}
            {data.tags.length > 0 && <div className="dashboard-live-table"><table><thead><tr><th>Tag</th><th>Type</th><th>Value</th><th>Source time</th><th>State</th></tr></thead><tbody>{data.tags.map((entity) => <DashboardTagRow key={entity.id} entity={entity} />)}</tbody></table></div>}
            {data.tags.length > 0 && <footer>Showing {data.tags.length} of {data.enabledTagTotal} enabled Tags <span>Updates arrive without definition polling</span></footer>}
          </section>

          <aside className="dashboard-activity" aria-labelledby="dashboard-activity-heading">
            <header><h2 id="dashboard-activity-heading">Acquisition activity</h2><p>Current runtime coverage at a glance.</p></header>
            <nav aria-label="Acquisition activity links">
              <Link to="/vgateways"><Cpu /><span><strong>Connected vGateways</strong><small>Transport sessions available</small></span><b>{connectedGatewayCount} / {data.gatewayTotal}</b><ChevronRight /></Link>
              <Link to="/tags"><RadioTower /><span><strong>Live / Good Tags</strong><small>Visible enabled Tags</small></span><b>{goodTagCount} / {data.tags.length}</b><ChevronRight /></Link>
              <Link className={errorTagCount > 0 ? "dashboard-activity-error" : ""} to="/tags?enabled=true"><CircleAlert /><span><strong>Error Tags</strong><small>Explicit runtime failures shown</small></span><b>{errorTagCount}</b><ChevronRight /></Link>
              <Link to="/data-loggers"><DatabaseZap /><span><strong>Enabled Data Loggers</strong><small>Independent capture engines</small></span><b>{enabledLoggerCount} / {data.loggerTotal}</b><ChevronRight /></Link>
            </nav>
            <div className="dashboard-quick-links"><Link to="/tags">View all Tags <ChevronRight /></Link><Link to="/data-loggers">Open Data Loggers <ChevronRight /></Link></div>
          </aside>
        </div>
      </div>
    </VGatewayShell>
  );
}

export default DashboardPage;
