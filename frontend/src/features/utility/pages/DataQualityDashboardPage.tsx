import { Activity, CircleAlert, Clock3, DatabaseZap, LoaderCircle, RadioTower } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import { getAssetMeasurements, listAssets } from "../../../services/asset.service";
import { getEnergyOverview } from "../../../services/energy.service";
import { listPlugins } from "../../../services/plugin.service";
import type { Asset, AssetMeasurementProjection, AssetMeasurementSnapshot } from "../../../types/asset";
import type { EnergyOverviewResponse, PluginInstanceSummary } from "../../../types/plugin";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DataQualityDashboardPage.css";

interface AssetSnapshot { asset: Asset; snapshot: AssetMeasurementSnapshot }
interface EnergySnapshot { plugin: PluginInstanceSummary; overview: EnergyOverviewResponse }
interface QualityItem { key: string; kind: "missing" | "bad" | "stale" | "modbus" | "gap" | "calculation"; title: string; detail: string; href: string }
const staleMilliseconds = 5 * 60 * 1_000;

function readingTime(projection: AssetMeasurementProjection): number | null {
  const value = projection.latest.observed_at ?? projection.latest.emitted_at;
  if (!value) return null;
  const parsed = new Date(value).getTime();
  return Number.isFinite(parsed) ? parsed : null;
}

function DataQualityDashboardPage() {
  const [assets, setAssets] = useState<AssetSnapshot[]>([]);
  const [energy, setEnergy] = useState<EnergySnapshot[]>([]);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      try {
        const [assetList, pluginList] = await Promise.all([
          listAssets({ enabled: true, page: 1, per_page: 100 }, controller.signal),
          listPlugins({ type: "energy_management", enabled: true, page: 1, per_page: 100 }, controller.signal),
        ]);
        const [assetSnapshots, energySnapshots] = await Promise.all([
          Promise.all(assetList.data.map(async (asset) => ({ asset, snapshot: await getAssetMeasurements(asset.id, controller.signal) }))),
          Promise.all(pluginList.data.map(async (plugin) => ({ plugin, overview: await getEnergyOverview(plugin.id, controller.signal) }))),
        ]);
        if (!controller.signal.aborted) { setAssets(assetSnapshots); setEnergy(energySnapshots); setState("ready"); }
      } catch { if (!controller.signal.aborted) setState("error"); }
    }
    void load();
    return () => controller.abort();
  }, []);

  const report = useMemo(() => {
    const items: QualityItem[] = [];
    let measurementMissing = 0; let measurementBad = 0; let measurementStale = 0;
    for (const { asset, snapshot } of assets) {
      const captured = new Date(snapshot.captured_at).getTime();
      for (const projection of snapshot.measurements) {
        const reading = projection.latest;
        const label = `${reading.semantic.resource} / ${reading.semantic.quantity}`;
        const href = `/assets?asset=${encodeURIComponent(asset.id)}`;
        if (!reading.available) measurementMissing += 1;
        if (reading.quality === "bad") measurementBad += 1;
        const isModbus = reading.error?.toLowerCase().includes("modbus") ?? false;
        if (!reading.available || reading.quality === "bad") items.push({ key: `${projection.binding.id}-quality`, kind: isModbus ? "modbus" : !reading.available ? "missing" : "bad", title: `${asset.name}: ${label}`, detail: reading.error ?? (!reading.available ? "No source value is available." : "Source reported bad quality."), href });
        else {
          const observed = readingTime(projection);
          if (observed === null || (Number.isFinite(captured) && captured - observed > staleMilliseconds)) { measurementStale += 1; items.push({ key: `${projection.binding.id}-stale`, kind: "stale", title: `${asset.name}: ${label}`, detail: observed === null ? "Source timestamp is unavailable." : "Latest good value is older than the five-minute dashboard threshold.", href }); }
        }
      }
    }
    let skipped = 0; let affected = 0; let coverageTotal = 0; let coverageCount = 0;
    for (const { plugin, overview } of energy) {
      const period = overview.today; const href = `/plugins/${encodeURIComponent(plugin.id)}/energy/history`;
      skipped += period.electrical.skipped_segments + period.thermal.skipped_segments;
      coverageTotal += period.electrical.coverage_percent + period.thermal.coverage_percent; coverageCount += 2;
      for (const issue of [...(period.electrical.issues ?? []), ...(period.thermal.issues ?? [])]) {
        const errors = issue.errors?.length ? issue.errors : [{ code: issue.code, message: `Logger segment ${issue.from} — ${issue.to} was skipped.` }];
        errors.forEach((error, index) => items.push({ key: `${plugin.id}-${issue.from}-${error.code}-${index}`, kind: error.message.toLowerCase().includes("modbus") ? "modbus" : "gap", title: `${plugin.name}: ${error.code}`, detail: error.message, href }));
      }
      if (!period.cost.valid) { affected += 1; items.push({ key: `${plugin.id}-cost`, kind: "calculation", title: `${plugin.name}: cost unavailable`, detail: period.cost.error ?? "Tariff or electrical coverage is incomplete.", href }); }
      if (!period.cop.valid) { affected += 1; items.push({ key: `${plugin.id}-cop`, kind: "calculation", title: `${plugin.name}: COP unavailable`, detail: period.cop.error ?? "Thermal or electrical coverage is incomplete.", href }); }
    }
    return { items, missing: measurementMissing, bad: measurementBad, stale: measurementStale, modbus: items.filter((item) => item.kind === "modbus").length, skipped, affected, coverage: coverageCount ? coverageTotal / coverageCount : null };
  }, [assets, energy]);

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Data Quality &amp; Coverage</strong></>}><main className="quality-dashboard">
    <header><div><p>Utilities</p><h1>Data Quality &amp; Coverage</h1><span>Prioritize source failures, Logger gaps, and calculations affected by incomplete evidence.</span></div><span className={`quality-health ${report.items.length ? "has-issues" : "is-good"}`}><i />{report.items.length ? `${report.items.length} actionable issues` : "All inspected sources healthy"}</span></header>
    {state === "loading" && <div className="quality-state" role="status"><LoaderCircle className="is-spinning" /><strong>Inspecting persisted and projected data quality…</strong></div>}
    {state === "error" && <div className="quality-state" role="alert"><CircleAlert /><strong>Data-quality sources unavailable</strong><span>Refresh after the Asset and Plugin APIs recover.</span></div>}
    {state === "ready" && <>
      <section className="quality-kpis" aria-label="Data quality summary"><article><RadioTower /><span>Missing</span><strong>{report.missing}</strong></article><article><CircleAlert /><span>Bad quality</span><strong>{report.bad}</strong></article><article><Clock3 /><span>Stale</span><strong>{report.stale}</strong></article><article><Activity /><span>Modbus errors</span><strong>{report.modbus}</strong></article><article><DatabaseZap /><span>Logger gaps</span><strong>{report.skipped}</strong></article><article><span>Affected calculations</span><strong>{report.affected}</strong></article><article><span>Energy coverage</span><strong>{report.coverage === null ? "Unavailable" : `${report.coverage.toLocaleString(undefined, { maximumFractionDigits: 1 })}%`}</strong></article></section>
      <section className="quality-policy" aria-label="Quality interpretation"><div><strong>Missing is not zero</strong><span>Unavailable measurements never contribute a numeric value.</span></div><div><strong>Stale threshold</strong><span>Good Asset readings older than five minutes relative to snapshot capture are flagged for triage.</span></div><div><strong>Coverage provenance</strong><span>Energy coverage and gaps come from synchronized persisted Logger segments.</span></div></section>
      <section className="quality-issues" aria-labelledby="quality-issues-heading"><header><div><h2 id="quality-issues-heading">Actionable issues</h2><p>Open the owning Asset or Plugin history to inspect provenance and repair the mapping or source.</p></div><strong>{report.items.length}</strong></header>{report.items.length === 0 ? <div className="quality-empty"><Activity /><strong>No quality issues detected</strong><span>All currently inspected readings and calculations have usable evidence.</span></div> : <ol>{report.items.map((item) => <li key={item.key}><span className={`is-${item.kind}`}>{item.kind}</span><div><strong>{item.title}</strong><small>{item.detail}</small></div><Link to={item.href}>Investigate</Link></li>)}</ol>}</section>
    </>}
  </main></VGatewayShell>;
}

export default DataQualityDashboardPage;
