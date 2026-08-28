import { Activity, CircleAlert, Droplets, Gauge, LoaderCircle, Zap } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { listAssets } from "../../../services/asset.service";
import { getEnergyOverview } from "../../../services/energy.service";
import { listPlugins } from "../../../services/plugin.service";
import type { Asset } from "../../../types/asset";
import type { EnergyOverviewResponse, PluginInstanceSummary } from "../../../types/plugin";

type Period = "today" | "month";

interface LoadedEnergy {
  plugin: PluginInstanceSummary;
  overview: EnergyOverviewResponse;
}

const number = new Intl.NumberFormat(undefined, { maximumFractionDigits: 2 });

function UtilitiesOverview() {
  const [assets, setAssets] = useState<Asset[]>([]);
  const [instances, setInstances] = useState<LoadedEnergy[]>([]);
  const [assetID, setAssetID] = useState("all");
  const [period, setPeriod] = useState<Period>("today");
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      setLoading(true);
      setError(false);
      try {
        const [assetResult, pluginResult] = await Promise.all([
          listAssets({ enabled: true, page: 1, per_page: 100 }, controller.signal),
          listPlugins({ type: "energy_management", enabled: true, page: 1, per_page: 100 }, controller.signal),
        ]);
        const overviews = await Promise.all(pluginResult.data.map(async (plugin) => ({
          plugin,
          overview: await getEnergyOverview(plugin.id, controller.signal),
        })));
        if (!controller.signal.aborted) {
          setAssets(assetResult.data);
          setInstances(overviews);
        }
      } catch {
        if (!controller.signal.aborted) setError(true);
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    void load();
    return () => controller.abort();
  }, []);

  const metrics = useMemo(() => {
    if (assetID !== "all") return null;
    const rows = instances.map((entry) => ({ ...entry, summary: entry.overview[period] }));
    const consumption = rows.reduce((sum, row) => sum + row.summary.electrical.kilowatt_hours, 0);
    const demand = rows.reduce((sum, row) => sum + (row.overview.latest?.electrical.valid ? row.overview.latest.electrical.kilowatts : 0), 0);
    const thermal = rows.reduce((sum, row) => sum + row.summary.thermal.kilowatt_hours, 0);
    const cost = rows.reduce((sum, row) => sum + (row.summary.cost.valid ? row.summary.cost.value : 0), 0);
    const coverage = rows.length ? rows.reduce((sum, row) => sum + row.summary.electrical.coverage_percent, 0) / rows.length : 0;
    const skipped = rows.reduce((sum, row) => sum + row.summary.electrical.skipped_segments, 0);
    return { rows: rows.sort((a, b) => b.summary.electrical.kilowatt_hours - a.summary.electrical.kilowatt_hours), consumption, demand, thermal, cost, coverage, skipped };
  }, [assetID, instances, period]);

  const noData = !loading && !error && (!metrics || metrics.rows.length === 0);

  return (
    <section className="utility-overview" aria-labelledby="utility-overview-heading">
      <header>
        <div><h2 id="utility-overview-heading">Energy &amp; Utilities</h2><p>Consumption, demand, cost, resource mix, and data confidence.</p><nav aria-label="Utility dashboards"><Link to="/utilities/compressed-air">Compressed Air Efficiency</Link><Link to="/utilities/data-quality">Data Quality &amp; Coverage</Link></nav></div>
        <div className="utility-overview-filters">
          <label>Hierarchy<select aria-label="Hierarchy" value={assetID} onChange={(event) => setAssetID(event.target.value)}><option value="all">All Assets</option>{assets.map((asset) => <option key={asset.id} value={asset.id}>{asset.name}</option>)}</select></label>
          <label>Period<select aria-label="Period" value={period} onChange={(event) => setPeriod(event.target.value as Period)}><option value="today">Today</option><option value="month">This month</option></select></label>
        </div>
      </header>

      {loading && <div className="utility-overview-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading utility analytics…</strong></div>}
      {error && <div className="utility-overview-state" role="alert"><CircleAlert /><strong>Utility analytics unavailable</strong><span>Refresh the dashboard to try again.</span></div>}
      {noData && <div className="utility-overview-state"><Droplets /><strong>No utility data for this hierarchy</strong><span>{assetID === "all" ? "Configure and enable an Energy instance to populate this view." : "Energy instances are not yet mapped to individual Assets, so plant totals are intentionally excluded."}</span></div>}

      {!loading && !error && metrics && metrics.rows.length > 0 && <>
        <div className="utility-kpis" role="region" aria-label="Utility summary">
          <article><Zap /><span>Consumption</span><strong>{number.format(metrics.consumption)} kWh</strong></article>
          <article><Gauge /><span>Current demand</span><strong>{number.format(metrics.demand)} kW</strong></article>
          <article><span>Cost</span><strong>{number.format(metrics.cost)} {metrics.rows[0].overview.currency}</strong></article>
          <article><Activity /><span>Data coverage</span><strong>{number.format(metrics.coverage)}%</strong><small>{metrics.skipped} skipped segments</small></article>
        </div>
        <div className="utility-detail-grid">
          <article><h3>Resource mix</h3><dl><div><dt>Electricity</dt><dd>{number.format(metrics.consumption)} kWh</dd></div><div><dt>Thermal</dt><dd>{number.format(metrics.thermal)} kWh</dd></div></dl></article>
          <article><h3>Top contributors</h3><ol>{metrics.rows.slice(0, 5).map((row) => <li key={row.plugin.id}><span>{row.plugin.name}</span><strong>{number.format(row.summary.electrical.kilowatt_hours)} kWh</strong></li>)}</ol></article>
          <article><h3>Current status</h3><dl>{metrics.rows.map((row) => <div key={row.plugin.id}><dt>{row.plugin.name}</dt><dd className={row.overview.latest ? "is-good" : "is-muted"}>{row.overview.latest ? "Reporting" : "No current sample"}</dd></div>)}</dl></article>
        </div>
      </>}
    </section>
  );
}

export default UtilitiesOverview;
