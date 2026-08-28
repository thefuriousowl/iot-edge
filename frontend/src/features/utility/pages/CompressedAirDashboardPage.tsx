import { Activity, CircleAlert, Gauge, LoaderCircle, Wind, Zap } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { getAssetMeasurements, listAssets } from "../../../services/asset.service";
import type { Asset, AssetMeasurementProjection, AssetMeasurementSnapshot } from "../../../types/asset";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./CompressedAirDashboardPage.css";

const keys = {
  power: "compressed_air.live_power", flow: "compressed_air.live_flow", pressure: "compressed_air.live_pressure",
  energy: "compressed_air.energy", volume: "compressed_air.volume", sec: "compressed_air.sec", cost: "compressed_air.cost",
  costPerVolume: "compressed_air.cost_per_volume", runtime: "compressed_air.runtime_ratio", load: "compressed_air.load_ratio",
  pressureAverage: "compressed_air.pressure_average", pressureStdDev: "compressed_air.pressure_stddev", pressureDrop: "compressed_air.pressure_drop",
  baselineFlow: "compressed_air.estimated_leak_flow", baselineEnergy: "compressed_air.estimated_leak_energy", baselineCost: "compressed_air.estimated_leak_cost",
} as const;

function outputKey(projection: AssetMeasurementProjection): string | null { return projection.latest.source.kind === "plugin_output" ? projection.latest.source.output_key : null; }
function numeric(projection: AssetMeasurementProjection | undefined): number | null { const reading = projection?.latest; return reading?.available && reading.quality === "good" && typeof reading.value === "number" && Number.isFinite(reading.value) ? reading.value : null; }
const formatted = (value: number | null, unit = "") => value === null ? "Unavailable" : `${new Intl.NumberFormat(undefined, { maximumFractionDigits: 3 }).format(value)}${unit ? ` ${unit}` : ""}`;

function CompressedAirDashboardPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const [assets, setAssets] = useState<Asset[]>([]);
  const [snapshot, setSnapshot] = useState<AssetMeasurementSnapshot | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState(false);
  const assetID = searchParams.get("asset") ?? "";

  useEffect(() => {
    const controller = new AbortController();
    void listAssets({ enabled: true, page: 1, per_page: 100 }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      setAssets(response.data);
      if (!assetID && response.data[0]) setSearchParams({ asset: response.data[0].id }, { replace: true });
      if (!response.data[0]) setLoading(false);
    }).catch(() => { if (!controller.signal.aborted) { setError(true); setLoading(false); } });
    return () => controller.abort();
  }, [assetID, setSearchParams]);

  useEffect(() => {
    if (!assetID) return;
    const controller = new AbortController();
    void getAssetMeasurements(assetID, controller.signal).then((response) => { if (!controller.signal.aborted) setSnapshot(response); }).catch(() => { if (!controller.signal.aborted) setError(true); }).finally(() => { if (!controller.signal.aborted) setLoading(false); });
    return () => controller.abort();
  }, [assetID]);

  const outputs = useMemo(() => new Map((snapshot?.measurements ?? []).filter((projection) => projection.binding.semantic.resource === "compressed_air").map((projection) => [outputKey(projection), projection])), [snapshot]);
  const get = (key: string) => outputs.get(key);
  const value = (key: string) => numeric(get(key));
  const unit = (key: string) => get(key)?.latest.semantic.unit ?? "";
  const runtime = value(keys.runtime); const load = value(keys.load);
  const unload = load === null ? null : Math.max(0, 1 - load);
  const baselineEnergy = value(keys.baselineEnergy); const energy = value(keys.energy);
  const baselineShare = baselineEnergy !== null && energy !== null && energy > 0 ? baselineEnergy / energy : null;
  const period = get(keys.energy)?.latest;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Compressed Air Efficiency</strong></>}>
    <main className="compressed-dashboard">
      <header><div><p>Utilities</p><h1>Compressed Air Efficiency</h1><span>Asset-bound Plugin outputs with explicit coverage and baseline evidence.</span></div><label>Asset<select aria-label="Compressed-air Asset" value={assetID} onChange={(event) => { setLoading(true); setError(false); setSnapshot(null); setSearchParams({ asset: event.target.value }, { replace: true }); }}><option value="">Choose Asset</option>{assets.map((asset) => <option value={asset.id} key={asset.id}>{asset.name}</option>)}</select></label></header>
      {loading && <div className="compressed-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading compressed-air outputs…</strong></div>}
      {error && <div className="compressed-state" role="alert"><CircleAlert /><strong>Compressed-air measurements unavailable</strong><span>Check the Asset measurement API and try again.</span></div>}
      {!loading && !error && outputs.size === 0 && <div className="compressed-state"><Wind /><strong>No compressed-air outputs bound</strong><span>Bind compressed-air Plugin outputs to this Asset before using the dashboard.</span><Link to={`/assets?asset=${encodeURIComponent(assetID)}`}>Open Asset workspace</Link></div>}
      {!loading && !error && outputs.size > 0 && <>
        <section className="compressed-provenance" aria-label="Compressed-air source period"><div><span>Source period</span><strong>{period?.period_start && period.period_end ? `${period.period_start} — ${period.period_end}` : "Instantaneous/latest only"}</strong></div><div><span>Captured</span><strong>{snapshot?.captured_at ?? "Unavailable"}</strong></div><div><span>Retention</span><strong>Latest Plugin output</strong></div></section>
        <section className="compressed-kpis" aria-label="Compressed-air performance summary">
          <article><Zap /><span>Power</span><strong>{formatted(value(keys.power), unit(keys.power))}</strong></article><article><Wind /><span>Flow</span><strong>{formatted(value(keys.flow), unit(keys.flow))}</strong></article><article><Gauge /><span>Pressure</span><strong>{formatted(value(keys.pressure), unit(keys.pressure))}</strong></article><article><Activity /><span>SEC</span><strong>{formatted(value(keys.sec), unit(keys.sec))}</strong></article>
          <article><span>Consumption</span><strong>{formatted(energy, unit(keys.energy))}</strong></article><article><span>Air volume</span><strong>{formatted(value(keys.volume), unit(keys.volume))}</strong></article><article><span>Cost</span><strong>{formatted(value(keys.cost), unit(keys.cost))}</strong></article><article><span>Cost / volume</span><strong>{formatted(value(keys.costPerVolume), unit(keys.costPerVolume))}</strong></article>
        </section>
        <div className="compressed-detail-grid">
          <section aria-labelledby="load-profile-heading"><h2 id="load-profile-heading">Load / unload</h2><div className="compressed-ratio"><span style={{ width: `${Math.max(0, Math.min(100, (load ?? 0) * 100))}%` }}>Load</span></div><dl><div><dt>Runtime ratio</dt><dd>{runtime === null ? "Unavailable" : `${formatted(runtime * 100)}%`}</dd></div><div><dt>Load ratio</dt><dd>{load === null ? "Unavailable" : `${formatted(load * 100)}%`}</dd></div><div><dt>Unload share</dt><dd>{unload === null ? "Unavailable" : `${formatted(unload * 100)}%`}</dd></div></dl></section>
          <section aria-labelledby="pressure-heading"><h2 id="pressure-heading">Pressure stability</h2><dl><div><dt>Average</dt><dd>{formatted(value(keys.pressureAverage), unit(keys.pressureAverage))}</dd></div><div><dt>Standard deviation</dt><dd>{formatted(value(keys.pressureStdDev), unit(keys.pressureStdDev))}</dd></div><div><dt>Drop</dt><dd>{formatted(value(keys.pressureDrop), unit(keys.pressureDrop))}</dd></div></dl></section>
          <section aria-labelledby="baseline-heading"><h2 id="baseline-heading">Confirmed baseline estimate</h2><dl><div><dt>Flow</dt><dd>{formatted(value(keys.baselineFlow), unit(keys.baselineFlow))}</dd></div><div><dt>Energy</dt><dd>{formatted(baselineEnergy, unit(keys.baselineEnergy))}</dd></div><div><dt>Cost</dt><dd>{formatted(value(keys.baselineCost), unit(keys.baselineCost))}</dd></div><div><dt>Share of energy</dt><dd>{baselineShare === null ? "Unavailable" : `${formatted(baselineShare * 100)}%`}</dd></div></dl><p>This is observed demand during an explicitly confirmed baseline period. It is not an automatic leakage diagnosis.</p></section>
        </div>
        <section className="compressed-comparison" aria-label="Compressed-air comparison availability"><CircleAlert /><div><strong>Time comparison unavailable</strong><span>The current Asset projection retains only the latest Plugin output. No previous period or trend is fabricated; a bounded compressed-air history endpoint is required.</span></div></section>
      </>}
    </main>
  </VGatewayShell>;
}

export default CompressedAirDashboardPage;
