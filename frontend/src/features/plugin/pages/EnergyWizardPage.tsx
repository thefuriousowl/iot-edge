import axios from "axios";
import { ArrowLeft, ArrowRight, Calculator, Check, CircleAlert, DatabaseZap, Gauge, LoaderCircle, Plus, Trash2, WalletCards, Zap } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { getDataLogger, listDataLoggers } from "../../../services/datalogger.service";
import { createPlugin, getPlugin, updatePlugin } from "../../../services/plugin.service";
import type { DataLogger, DataLoggerTagReference } from "../../../types/datalogger";
import type { EnergyConfig, EnergyPowerTag, EnergyPowerUnit, PluginInstance } from "../../../types/plugin";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import { isValidTimezone } from "../../datalogger/utils/schedule";
import "./EnergyWizardPage.css";

const steps = ["Source", "Power Tags", "Tariff", "Review"];
const powerUnits: EnergyPowerUnit[] = ["W", "kW", "MW"];
const numericTypes = new Set(["int16", "uint16", "int32", "uint32", "float32", "float64"]);

interface EnergyForm {
  name: string;
  enabled: boolean;
  loggerID: string;
  electrical: EnergyPowerTag[];
  thermal: EnergyPowerTag[];
  timezone: string;
  maxGapSeconds: number;
  currency: string;
  tariffMode: "flat" | "tag";
  ratePerKWh: number;
  tariffTagID: string;
}

function initialForm(): EnergyForm {
  return {
    name: "",
    enabled: false,
    loggerID: "",
    electrical: [{ tag_id: "", unit: "kW" }],
    thermal: [],
    timezone: Intl.DateTimeFormat().resolvedOptions().timeZone || "UTC",
    maxGapSeconds: 120,
    currency: "THB",
    tariffMode: "flat",
    ratePerKWh: 4,
    tariffTagID: "",
  };
}

function formFromPlugin(instance: PluginInstance): EnergyForm {
  const config = instance.config as EnergyConfig;
  return {
    name: instance.name,
    enabled: instance.enabled,
    loggerID: config.logger_id,
    electrical: config.electrical_power_tags.map((mapping) => ({ ...mapping })),
    thermal: (config.thermal_power_tags ?? []).map((mapping) => ({ ...mapping })),
    timezone: config.timezone,
    maxGapSeconds: config.max_gap_seconds,
    currency: config.tariff.currency,
    tariffMode: config.tariff.mode === "tag" ? "tag" : "flat",
    ratePerKWh: config.tariff.mode === "tag" ? 0 : config.tariff.rate_per_kwh,
    tariffTagID: config.tariff.mode === "tag" ? config.tariff.tag_id : "",
  };
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return error instanceof Error ? error.message : "Unable to save Energy Plugin";
}

async function loadAllLoggers(signal: AbortSignal): Promise<DataLogger[]> {
  const result: DataLogger[] = [];
  let page = 1;
  while (true) {
    const response = await listDataLoggers({ page, per_page: 100 }, signal);
    result.push(...response.data);
    if (page >= response.pagination.total_pages) return result;
    page += 1;
  }
}

function EnergyWizardPage() {
  const { id } = useParams();
  const editing = Boolean(id);
  const navigate = useNavigate();
  const [step, setStep] = useState(0);
  const [form, setForm] = useState<EnergyForm>(initialForm);
  const [loggers, setLoggers] = useState<DataLogger[]>([]);
  const [logger, setLogger] = useState<DataLogger | null>(null);
  const [loadState, setLoadState] = useState<"loading" | "ready" | "error">("loading");
  const [loggerLoading, setLoggerLoading] = useState(false);
  const [message, setMessage] = useState("");
  const [submitting, setSubmitting] = useState(false);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([loadAllLoggers(controller.signal), id ? getPlugin(id, controller.signal) : Promise.resolve(null)]).then(([availableLoggers, instance]) => {
      if (controller.signal.aborted) return;
      if (instance && instance.type !== "energy_management") throw new Error("This Plugin is not an Energy Management instance");
      setLoggers(availableLoggers);
      if (instance) {
        setLoggerLoading(true);
        setForm(formFromPlugin(instance));
      }
      setLoadState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(errorMessage(error));
      setLoadState("error");
    });
    return () => controller.abort();
  }, [id]);

  useEffect(() => {
    if (!form.loggerID) {
      return;
    }
    const controller = new AbortController();
    void getDataLogger(form.loggerID, controller.signal).then((selected) => {
      if (!controller.signal.aborted) setLogger(selected);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setLogger(null);
        setMessage(errorMessage(error));
      }
    }).finally(() => {
      if (!controller.signal.aborted) setLoggerLoading(false);
    });
    return () => controller.abort();
  }, [form.loggerID]);

  const numericTags = useMemo(() => (logger?.tags ?? []).filter((tag) => numericTypes.has(tag.data_type)), [logger]);
  const mappedIDs = useMemo(() => new Set([...form.electrical, ...form.thermal].map((mapping) => mapping.tag_id).filter(Boolean).concat(form.tariffTagID || [])), [form.electrical, form.tariffTagID, form.thermal]);

  function chooseLogger(loggerID: string) {
    const selected = loggers.find((candidate) => candidate.id === loggerID);
    setMessage("");
    setLogger(null);
    setLoggerLoading(Boolean(loggerID));
    setForm((current) => ({ ...current, loggerID, electrical: [{ tag_id: "", unit: "kW" }], thermal: [], tariffTagID: "", timezone: selected?.timezone || current.timezone }));
  }

  function updateMapping(group: "electrical" | "thermal", index: number, key: keyof EnergyPowerTag, value: string) {
    setForm((current) => ({ ...current, [group]: current[group].map((mapping, mappingIndex) => mappingIndex === index ? { ...mapping, [key]: value } : mapping) }));
  }

  function addMapping(group: "electrical" | "thermal") {
    setForm((current) => ({ ...current, [group]: [...current[group], { tag_id: "", unit: "kW" }] }));
  }

  function removeMapping(group: "electrical" | "thermal", index: number) {
    setForm((current) => ({ ...current, [group]: current[group].filter((_, mappingIndex) => mappingIndex !== index) }));
  }

  function availableTags(currentID: string): DataLoggerTagReference[] {
    return numericTags.filter((tag) => tag.id === currentID || !mappedIDs.has(tag.id));
  }

  function validateCurrentStep(): string | null {
    if (step === 0) {
      if (!form.name.trim()) return "Instance name is required";
      if (!form.loggerID) return "Select a Data Logger";
      if (loggerLoading || !logger) return "Wait for the selected Data Logger to finish loading";
    }
    if (step === 1) {
      if (form.electrical.length === 0 || form.electrical.some((mapping) => !mapping.tag_id)) return "Select at least one electrical power Tag";
      if (form.thermal.some((mapping) => !mapping.tag_id)) return "Complete or remove every thermal power mapping";
      const selected = [...form.electrical, ...form.thermal].map((mapping) => mapping.tag_id);
      if (new Set(selected).size !== selected.length) return "A power Tag can only be mapped once";
      if (selected.length > 100) return "Select no more than 100 power Tags";
    }
    if (step === 2) {
      if (!isValidTimezone(form.timezone)) return "Enter a valid IANA timezone";
      if (!Number.isInteger(form.maxGapSeconds) || form.maxGapSeconds < 1) return "Maximum gap must be a positive whole number";
      if (!/^[A-Z]{3}$/.test(form.currency)) return "Use a three-letter currency code";
      if (form.tariffMode === "flat" && (!Number.isFinite(form.ratePerKWh) || form.ratePerKWh < 0)) return "Flat rate cannot be negative";
      if (form.tariffMode === "tag" && !form.tariffTagID) return "Select a tariff Tag";
    }
    return null;
  }

  function continueWizard() {
    const validationError = validateCurrentStep();
    if (validationError) {
      setMessage(validationError);
      return;
    }
    setMessage("");
    setStep((current) => Math.min(3, current + 1));
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (step !== 3) return;
    const config: EnergyConfig = {
      logger_id: form.loggerID,
      electrical_power_tags: form.electrical,
      ...(form.thermal.length > 0 ? { thermal_power_tags: form.thermal } : {}),
      timezone: form.timezone,
      max_gap_seconds: form.maxGapSeconds,
      tariff: form.tariffMode === "tag" ? { mode: "tag", currency: form.currency, tag_id: form.tariffTagID } : { mode: "flat", currency: form.currency, rate_per_kwh: form.ratePerKWh },
    };
    setSubmitting(true);
    setMessage("");
    try {
      if (id) await updatePlugin(id, { name: form.name.trim(), enabled: form.enabled, config });
      else await createPlugin({ type: "energy_management", name: form.name.trim(), enabled: form.enabled, config });
      navigate("/plugins");
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setSubmitting(false);
    }
  }

  function mappingRows(group: "electrical" | "thermal") {
    return form[group].map((mapping, index) => <div className="energy-mapping-row" key={`${group}-${index}`}><label><span>{group === "electrical" ? "Electrical power" : "Thermal power"} Tag {index + 1}</span><select aria-label={`${group === "electrical" ? "Electrical" : "Thermal"} power Tag ${index + 1}`} value={mapping.tag_id} onChange={(event) => updateMapping(group, index, "tag_id", event.target.value)}><option value="">Select numeric Tag</option>{availableTags(mapping.tag_id).map((tag) => <option key={tag.id} value={tag.id}>{tag.name} · {tag.data_type}</option>)}</select></label><label><span>Source unit</span><select aria-label={`${group === "electrical" ? "Electrical" : "Thermal"} source unit ${index + 1}`} value={mapping.unit} onChange={(event) => updateMapping(group, index, "unit", event.target.value)}>{powerUnits.map((unit) => <option key={unit}>{unit}</option>)}</select></label><button type="button" aria-label={`Remove ${group} mapping ${index + 1}`} disabled={group === "electrical" && form.electrical.length === 1} onClick={() => removeMapping(group, index)}><Trash2 size={16} /></button></div>);
  }

  if (loadState !== "ready") return <VGatewayShell breadcrumb={<>Plugins <span>/</span> <strong>Energy</strong></>}><div className="energy-wizard-content"><div className={`energy-wizard-state ${loadState === "error" ? "is-error" : ""}`} role={loadState === "error" ? "alert" : "status"}>{loadState === "loading" ? <LoaderCircle className="is-spinning" /> : <CircleAlert />}<strong>{loadState === "loading" ? "Loading Energy configuration…" : "Couldn’t load Energy configuration"}</strong>{message && <span>{message}</span>}</div></div></VGatewayShell>;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Plugins <span>/</span> <strong>{editing ? "Configure" : "New Energy"}</strong></>}><div className="energy-wizard-content"><header className="energy-wizard-heading"><Link to="/plugins"><ArrowLeft size={17} /> Plugins</Link><p>Energy Management</p><h1>{editing ? "Configure Energy" : "New Energy instance"}</h1><span>Map synchronized power Tags. kWh, cost, and period COP are calculated from timestamped kW history—no kWh Tag is required.</span></header><ol className="energy-steps">{steps.map((label, index) => <li key={label} className={index === step ? "is-current" : index < step ? "is-complete" : ""}><span>{index < step ? <Check size={15} /> : index + 1}</span><strong>{label}</strong></li>)}</ol>
    <form className="energy-wizard-form" onSubmit={(event) => void submit(event)}>
      {step === 0 && <section className="energy-panel"><div className="energy-panel-heading"><DatabaseZap /><div><h2>Choose synchronized history</h2><p>The Plugin reads committed Logger batches only. It never polls a Datasource or changes Logger timing.</p></div></div><div className="energy-form-grid"><label><span>Instance name</span><input autoFocus value={form.name} onChange={(event) => setForm((current) => ({ ...current, name: event.target.value }))} /></label><label><span>Data Logger</span><select value={form.loggerID} onChange={(event) => chooseLogger(event.target.value)}><option value="">Select a Data Logger</option>{loggers.map((candidate) => <option key={candidate.id} value={candidate.id}>{candidate.name} · {candidate.tag_count} Tags</option>)}</select></label><label className="energy-enable"><input type="checkbox" checked={form.enabled} onChange={(event) => setForm((current) => ({ ...current, enabled: event.target.checked }))} /><span><strong>Enable after save</strong><small>Start calculating after backend validation succeeds.</small></span></label></div>{loggers.length === 0 && <div className="energy-guidance"><CircleAlert /><span>No Data Logger is available. <Link to="/data-loggers/new">Create one with the demo Tags first.</Link></span></div>}{loggerLoading && <p className="energy-inline-state"><LoaderCircle className="is-spinning" /> Loading Logger Tags…</p>}{logger && <div className="energy-source-summary"><span><strong>{logger.tag_count}</strong> synchronized Tags</span><span><strong>{numericTags.length}</strong> numeric candidates</span><span><strong>{logger.mode}</strong> Logger mode</span><small>All Logger Tags remain available for the dashboard live-value panel, even when they are not mapped into Energy calculations.</small></div>}</section>}
      {step === 1 && <section className="energy-panel"><div className="energy-panel-heading"><Gauge /><div><h2>Map power Tags</h2><p>Power inputs are summed per synchronized batch, normalized to kW, then integrated over time.</p></div></div><div className="energy-mapping-group"><header><div><h3>Electrical input</h3><p>Use ActivePowerTotal_kW for the demo.</p></div><button type="button" onClick={() => addMapping("electrical")}><Plus size={16} /> Add meter</button></header>{mappingRows("electrical")}</div><div className="energy-mapping-group"><header><div><h3>Thermal output</h3><p>Optional. Select either the W or kW representation—not both.</p></div><button type="button" onClick={() => addMapping("thermal")}><Plus size={16} /> Add thermal Tag</button></header>{form.thermal.length > 0 ? mappingRows("thermal") : <div className="energy-empty-mapping">No thermal power Tag mapped. COP will remain unavailable.</div>}</div><div className="energy-guidance"><Calculator /><span>If thermal power is derived from Flow_m3h × 1.163 × (TempReturn − TempSupply), create a Calculated Tag, include it in this Logger, and select that kW Tag here.</span></div></section>}
      {step === 2 && <section className="energy-panel"><div className="energy-panel-heading"><WalletCards /><div><h2>Integration and tariff</h2><p>Define local calendar boundaries, permitted sample gaps, and either a fixed or synchronized Tag rate.</p></div></div><div className="energy-form-grid"><label><span>Timezone</span><input value={form.timezone} onChange={(event) => setForm((current) => ({ ...current, timezone: event.target.value }))} /></label><label><span>Maximum sample gap (seconds)</span><input type="number" min="1" step="1" value={form.maxGapSeconds} onChange={(event) => setForm((current) => ({ ...current, maxGapSeconds: Number(event.target.value) }))} /></label><label><span>Currency</span><input maxLength={3} value={form.currency} onChange={(event) => setForm((current) => ({ ...current, currency: event.target.value.trim().toUpperCase() }))} /></label><label><span>Tariff source</span><select value={form.tariffMode} onChange={(event) => setForm((current) => ({ ...current, tariffMode: event.target.value as "flat" | "tag", tariffTagID: "" }))}><option value="flat">Fixed flat rate</option><option value="tag">Rate from Logger Tag</option></select></label>{form.tariffMode === "flat" ? <label><span>Flat rate per kWh</span><input type="number" min="0" step="0.0001" value={form.ratePerKWh} onChange={(event) => setForm((current) => ({ ...current, ratePerKWh: Number(event.target.value) }))} /></label> : <label><span>Tariff Tag</span><select value={form.tariffTagID} onChange={(event) => setForm((current) => ({ ...current, tariffTagID: event.target.value }))}><option value="">Select numeric rate Tag</option>{numericTags.filter((tag) => tag.id === form.tariffTagID || !mappedIDs.has(tag.id)).map((tag) => <option key={tag.id} value={tag.id}>{tag.name} · {tag.data_type}</option>)}</select><small>Expected unit: {form.currency}/kWh. Values are read from the same committed Logger batch.</small></label>}</div><div className="energy-equation-grid"><article><Zap /><span>Electrical energy</span><strong>∫ Electrical kW dt</strong><small>Result: kWh</small></article><article><Gauge /><span>Period COP</span><strong>Thermal kWh ÷ Electrical kWh</strong><small>Same selected period</small></article><article><WalletCards /><span>Estimated cost</span><strong>∫ Electrical kW × rate dt</strong><small>{form.tariffMode === "tag" ? "Synchronized Tag rate" : `${form.currency} ${form.ratePerKWh}/kWh`}</small></article></div></section>}
      {step === 3 && <section className="energy-panel"><div className="energy-panel-heading"><Check /><div><h2>Review Energy configuration</h2><p>Saving validates Logger membership and numeric Tag types again on the backend.</p></div></div><div className="energy-review-grid"><article><span>Instance</span><strong>{form.name}</strong><small>{form.enabled ? "Enabled after save" : "Saved disabled"}</small></article><article><span>Data Logger</span><strong>{logger?.name ?? "Unavailable"}</strong><small>{logger?.tag_count ?? 0} Tags available for live monitoring</small></article><article className="is-wide"><span>Electrical power</span>{form.electrical.map((mapping) => <strong key={mapping.tag_id}>{numericTags.find((tag) => tag.id === mapping.tag_id)?.name ?? "Unresolved Tag"}<small>{mapping.unit}</small></strong>)}</article><article className="is-wide"><span>Thermal power</span>{form.thermal.length > 0 ? form.thermal.map((mapping) => <strong key={mapping.tag_id}>{numericTags.find((tag) => tag.id === mapping.tag_id)?.name ?? "Unresolved Tag"}<small>{mapping.unit}</small></strong>) : <strong>Not configured<small>COP unavailable</small></strong>}</article><article><span>Integration</span><strong>{form.timezone}</strong><small>Skip gaps over {form.maxGapSeconds} seconds</small></article><article><span>Tariff</span><strong>{form.tariffMode === "tag" ? numericTags.find((tag) => tag.id === form.tariffTagID)?.name ?? "Unresolved Tag" : `${form.currency} ${form.ratePerKWh}/kWh`}</strong><small>{form.tariffMode === "tag" ? `Synchronized ${form.currency}/kWh Tag` : "Fixed rate applied to covered electrical energy"}</small></article><article className="is-wide energy-review-callout"><Calculator /><div><strong>No kWh Tag required</strong><small>Hourly, daily, weekly, and custom-period kWh are calculated from timestamped power samples. The Plugin does not create acquisition requests.</small></div></article></div></section>}
      {message && <div className="energy-submit-error" role="alert"><CircleAlert />{message}</div>}<footer className="energy-wizard-actions"><button type="button" disabled={step === 0 || submitting} onClick={() => { setMessage(""); setStep((current) => Math.max(0, current - 1)); }}><ArrowLeft size={17} /> Back</button>{step < 3 ? <button key="continue" className="is-primary" type="button" onClick={continueWizard}>Continue <ArrowRight size={17} /></button> : <button key="submit" className="is-primary" type="submit" disabled={submitting}>{submitting ? <LoaderCircle className="is-spinning" /> : <Check size={17} />}{editing ? "Save configuration" : "Create Energy instance"}</button>}</footer>
    </form></div></VGatewayShell>;
}

export default EnergyWizardPage;
