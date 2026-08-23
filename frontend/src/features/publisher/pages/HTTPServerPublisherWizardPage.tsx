import axios from "axios";
import { ArrowLeft, ArrowRight, Check, CircleAlert, Clock3, Code2, Copy, Database, FileKey2, Globe2, Info, LockKeyhole, Plus, RefreshCw, Server, ShieldCheck, Trash2 } from "lucide-react";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import { listCredentials } from "../../../services/credential.service";
import { createDataPublisher, getDataPublisher, listPublisherSources, updateDataPublisher, validatePublisherPayload } from "../../../services/publisher.service";
import type { CredentialProfile } from "../../../types/credential";
import type { DataPublisher, HTTPServerPublisherDraft, MQTTPayloadFixture, PublisherPayloadValidation, PublisherSourceCatalogEntry, PublisherSourceDraft, PublisherSourceKind, PublisherSourceReference, PublisherSourceSelection } from "../../../types/publisher";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import HTTPServerPublisherOperations from "../components/HTTPServerPublisherOperations";
import PublisherSourceCurrent from "../components/PublisherSourceCurrent";
import { exportHTTPServerPublisherConfig, generateHTTPServerPayloadTemplate, httpServerEndpoint, importHTTPServerPublisherDraft, initialHTTPServerPublisherDraft, normalizeHTTPServerErrorMessage, validateHTTPServerEndpoint, validateHTTPServerPublisherDraft } from "../utils/httpServer";
import { mqttPayloadSourceHelpers, mqttPayloadSourceSyntax, previewPayloadTemplate, validateSourceAliases } from "../utils/mqtt";
import type { MQTTPayloadSourceHelper } from "../utils/mqtt";
import "../../vgateway/pages/VGatewayListPage.css";
import "./MQTTPublisherWizardPage.css";

const steps = ["Endpoint", "Sources", "Response", "Runtime"];
const fixtures: Array<{ id: MQTTPayloadFixture; label: string }> = [
  { id: "good", label: "Good" },
  { id: "unavailable", label: "Unavailable" },
  { id: "windowed", label: "Windowed" },
];

function referenceKey(reference: PublisherSourceReference): string {
  return reference.kind === "tag" ? `tag:${reference.tag_id}` : `plugin_output:${reference.plugin_instance_id}:${reference.output_key}`;
}

function selectedSources(sources: PublisherSourceDraft[]): PublisherSourceSelection[] | null {
  if (sources.some((source) => !source.reference)) return null;
  return sources.map((source) => ({ alias: source.alias, reference: source.reference! }));
}

function sourceAlias(name: string, existing: PublisherSourceDraft[]): string {
  const base = name.trim().toLowerCase().replace(/[^a-z0-9_.-]+/g, "_").replace(/^[^a-z_]+/, "") || "source";
  let candidate = base.slice(0, 64);
  let suffix = 2;
  while (existing.some((source) => source.alias === candidate)) {
    const ending = `_${suffix}`;
    candidate = `${base.slice(0, 64 - ending.length)}${ending}`;
    suffix += 1;
  }
  return candidate;
}

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    return body?.error?.message ?? fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

function httpErrorMessage(error: unknown, fallback: string): string {
  return normalizeHTTPServerErrorMessage(errorMessage(error, fallback));
}

function loadDraft(): HTTPServerPublisherDraft {
  const initial = initialHTTPServerPublisherDraft();
  try {
    const raw = localStorage.getItem("iot-edge.http-server-publisher-draft.v1");
    if (!raw) return initial;
    const stored = JSON.parse(raw) as Partial<HTTPServerPublisherDraft>;
    if (!stored.trigger || !stored.http || !stored.response || !Array.isArray(stored.sources)) return initial;
    return {
      ...initial,
      ...stored,
      trigger: { ...initial.trigger, ...stored.trigger },
      http: { ...initial.http, ...stored.http },
      response: { ...initial.response, ...stored.response },
      sources: stored.sources,
      credential_slots: Array.isArray(stored.credential_slots) ? stored.credential_slots : [],
    };
  } catch {
    return initial;
  }
}

function HTTPServerPublisherWizardPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [publisher, setPublisher] = useState<DataPublisher | null>(null);
  const [draft, setDraft] = useState<HTTPServerPublisherDraft>(() => id ? initialHTTPServerPublisherDraft() : loadDraft());
  const [step, setStep] = useState(0);
  const [fixture, setFixture] = useState<MQTTPayloadFixture>("good");
  const [catalog, setCatalog] = useState<PublisherSourceCatalogEntry[]>([]);
  const [catalogKind, setCatalogKind] = useState<PublisherSourceKind | "">("");
  const [catalogSearch, setCatalogSearch] = useState("");
  const [catalogState, setCatalogState] = useState<"loading" | "ready" | "error">("loading");
  const [credentials, setCredentials] = useState<CredentialProfile[]>([]);
  const [payloadHelper, setPayloadHelper] = useState<MQTTPayloadSourceHelper>("value");
  const [serverValidation, setServerValidation] = useState<PublisherPayloadValidation | null>(null);
  const [detailState, setDetailState] = useState<"loading" | "ready" | "error">(id ? "loading" : "ready");
  const [saving, setSaving] = useState(false);
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const templateRef = useRef<HTMLTextAreaElement>(null);
  const configurationLocked = Boolean(publisher?.enabled);
  const endpoint = useMemo(() => httpServerEndpoint(draft, window.location.hostname || "localhost"), [draft]);
  const configJSON = useMemo(() => JSON.stringify(exportHTTPServerPublisherConfig(draft), null, 2), [draft]);
  const preview = useMemo(() => {
    try {
      return { result: previewPayloadTemplate(draft.response.payload_template, draft.sources, fixture), error: "" };
    } catch (error) {
      return { result: null, error: error instanceof Error ? error.message : "Payload preview failed" };
    }
  }, [draft.response.payload_template, draft.sources, fixture]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(() => void listPublisherSources({
      ...(catalogKind ? { kind: catalogKind } : {}),
      enabled: true,
      ...(catalogSearch.trim() ? { search: catalogSearch.trim() } : {}),
    }, controller.signal).then((entries) => {
      setCatalog(entries);
      setCatalogState("ready");
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setCatalogState("error");
        setMessage(httpErrorMessage(error, "Unable to load Publisher sources"));
      }
    }), 150);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [catalogKind, catalogSearch]);

  useEffect(() => {
    const controller = new AbortController();
    void listCredentials({ type: "http" }, controller.signal).then((profiles) => {
      if (!controller.signal.aborted) setCredentials(profiles);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setMessage(httpErrorMessage(error, "Unable to load HTTP Credential Profiles"));
    });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!id) return;
    const controller = new AbortController();
    void Promise.all([getDataPublisher(id, controller.signal), listPublisherSources(undefined, controller.signal), listCredentials({ type: "http" }, controller.signal)]).then(([entity, entries, profiles]) => {
      if (controller.signal.aborted) return;
      if (entity.type !== "http_server") throw new Error("This Data Publisher is not an HTTP Server");
      setPublisher(entity);
      setCredentials(profiles);
      setDraft(importHTTPServerPublisherDraft(entity, entries, profiles));
      setDetailState("ready");
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) {
        setDetailState("error");
        setMessage(httpErrorMessage(error, "Unable to load HTTP Server Publisher"));
      }
    });
    return () => controller.abort();
  }, [id]);

  function updateHTTP<Key extends keyof HTTPServerPublisherDraft["http"]>(key: Key, value: HTTPServerPublisherDraft["http"][Key]) {
    setDraft((current) => ({ ...current, http: { ...current.http, [key]: value } }));
  }

  function addSource(entry: PublisherSourceCatalogEntry) {
    setDraft((current) => {
      if (current.sources.some((source) => source.reference && referenceKey(source.reference) === referenceKey(entry.descriptor.reference))) return current;
      const source: PublisherSourceDraft = {
        id: crypto.randomUUID(), alias: sourceAlias(entry.descriptor.name, current.sources), reference: entry.descriptor.reference,
        name: entry.descriptor.name, owner_name: entry.descriptor.owner_name ?? "Core", kind: entry.descriptor.reference.kind,
        data_type: entry.descriptor.data_type, unit: entry.descriptor.unit ?? "", period_kind: entry.descriptor.period_kind,
      };
      return { ...current, sources: [...current.sources, source], trigger: current.trigger.source_alias ? current.trigger : { ...current.trigger, source_alias: source.alias } };
    });
    setServerValidation(null);
  }

  function updateSource(sourceID: string, alias: string) {
    setDraft((current) => {
      const previous = current.sources.find((source) => source.id === sourceID)?.alias;
      return {
        ...current,
        sources: current.sources.map((source) => source.id === sourceID ? { ...source, alias } : source),
        trigger: previous === current.trigger.source_alias ? { ...current.trigger, source_alias: alias } : current.trigger,
      };
    });
    setServerValidation(null);
  }

  function removeSource(sourceID: string) {
    setDraft((current) => {
      const removed = current.sources.find((source) => source.id === sourceID);
      const sources = current.sources.filter((source) => source.id !== sourceID);
      return { ...current, sources, trigger: removed?.alias === current.trigger.source_alias ? { ...current.trigger, source_alias: sources[0]?.alias ?? "" } : current.trigger };
    });
    setServerValidation(null);
  }

  function selectCredential(credentialID: string) {
    const profile = credentials.find((candidate) => candidate.id === credentialID);
    setDraft((current) => ({ ...current, credential_id: credentialID, credential_slots: profile?.secrets.map((secret) => secret.slot) ?? [] }));
  }

  function insertSyntax(syntax: string) {
    const template = draft.response.payload_template;
    const start = templateRef.current?.selectionStart ?? template.length;
    const end = templateRef.current?.selectionEnd ?? start;
    const next = `${template.slice(0, start)}${syntax}${template.slice(end)}`;
    setDraft((current) => ({ ...current, response: { payload_template: next } }));
    setServerValidation(null);
    setNotice(`Inserted ${syntax}`);
    window.requestAnimationFrame(() => {
      const cursor = start + syntax.length;
      templateRef.current?.focus();
      templateRef.current?.setSelectionRange(cursor, cursor);
    });
  }

  function generatePayloadTemplate() {
    const generated = generateHTTPServerPayloadTemplate(draft.sources);
    const initialTemplate = initialHTTPServerPublisherDraft().response.payload_template;
    const currentTemplate = draft.response.payload_template;
    if (currentTemplate !== initialTemplate && currentTemplate !== generated && !window.confirm("Replace the current HTTP response template with generated JSON?")) return;
    setDraft((current) => ({ ...current, response: { payload_template: generated } }));
    setServerValidation(null);
    setMessage("");
    setNotice(`Generated HTTP JSON from ${draft.sources.length} selected source${draft.sources.length === 1 ? "" : "s"}.`);
  }

  function validateStep(): string | null {
    if (step === 0) return validateHTTPServerEndpoint(draft)[0] ?? null;
    if (step === 1) {
      if (draft.sources.length === 0) return "Add at least one Publisher source alias";
      const aliasError = validateSourceAliases(draft.sources)[0];
      if (aliasError) return aliasError;
      if (!selectedSources(draft.sources)) return "Select every source from the Core catalog";
      if (draft.trigger.mode === "on_change" && !draft.sources.some((source) => source.alias === draft.trigger.source_alias)) return "Select a valid on-change source alias";
    }
    if (step === 2 && preview.error) return preview.error;
    if (step === 3) return validateHTTPServerPublisherDraft(draft)[0] ?? null;
    return null;
  }

  function nextStep() {
    const error = validateStep();
    if (error) return setMessage(error);
    setMessage("");
    setNotice("");
    setStep((current) => Math.min(steps.length - 1, current + 1));
  }

  async function validateOnServer(): Promise<boolean> {
    const sources = selectedSources(draft.sources);
    if (!sources) return false;
    try {
      const result = await validatePublisherPayload(draft.response.payload_template, sources);
      setServerValidation(result);
      setNotice(`Core validation passed: ${result.helper_calls} helpers, ${result.referenced_aliases.length} aliases.`);
      setMessage("");
      return true;
    } catch (error) {
      setServerValidation(null);
      setMessage(httpErrorMessage(error, "Core rejected the HTTP response template"));
      return false;
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (configurationLocked) return setMessage("Disable the HTTP Server before changing its configuration");
    const errors = validateHTTPServerPublisherDraft(draft);
    if (errors.length > 0) return setMessage(errors[0]);
    const sources = selectedSources(draft.sources);
    if (!sources) return setMessage("Select every source from the Core catalog");
    setSaving(true);
    try {
      if (!await validateOnServer()) return;
      const request = { name: draft.name.trim(), description: null, credential_id: draft.credential_id || null, config: exportHTTPServerPublisherConfig(draft), sources };
      const saved = publisher ? await updateDataPublisher(publisher.id, request) : await createDataPublisher({ type: "http_server", enabled: false, ...request });
      setPublisher(saved);
      localStorage.setItem("iot-edge.http-server-publisher-draft.v1", JSON.stringify(draft));
      setNotice(publisher ? "Disabled HTTP Server configuration updated." : "Disabled HTTP Server saved. Review the endpoint, then enable it.");
      if (!publisher) navigate(`/data-publishers/${encodeURIComponent(saved.id)}/http-server`, { replace: true });
    } catch (error) {
      setMessage(httpErrorMessage(error, "Unable to save HTTP Server Publisher"));
    } finally {
      setSaving(false);
    }
  }

  async function copyConfig() {
    try {
      await navigator.clipboard.writeText(configJSON);
      setNotice("HTTP Server configuration copied. No secret values are included.");
    } catch {
      setMessage("Clipboard access is unavailable in this browser");
    }
  }

  if (detailState === "loading") return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Publishers</>}><div className="mqtt-loading"><RefreshCw className="is-spinning" />Loading HTTP Server Publisher…</div></VGatewayShell>;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Publishers <span>/</span> <strong>HTTP Server</strong></>}>
    <div className="mqtt-wizard-content">
      <header className="mqtt-heading"><Link to="/data-publishers"><ArrowLeft size={17} />Data Publishers</Link><div className="mqtt-heading-row"><div><p>Core Data Publisher</p><h1>{publisher?.name || "HTTP Server Publisher"}</h1><span>Expose retained Tag and Plugin-output snapshots as operator-authored JSON over one exact GET endpoint.</span></div><div className="mqtt-contract-badge"><Server size={18} /><span><strong>Pull-only endpoint</strong><small>No acquisition on GET</small></span></div></div></header>
      <div className="mqtt-api-note"><ShieldCheck size={18} /><span><strong>Core boundary.</strong> The Manager refreshes one immutable snapshot on its trigger. External clients only render that snapshot; they cannot change Datasource polling.</span></div>
      {configurationLocked && <div className="mqtt-lock-note"><LockKeyhole size={17} /><span><strong>Configuration locked while enabled.</strong> Disable this HTTP Server from Runtime before editing.</span></div>}
      <ol className="mqtt-steps">{steps.map((label, index) => <li key={label} className={index === step ? "is-current" : index < step ? "is-complete" : ""}><span>{index < step ? <Check size={14} /> : index + 1}</span><strong>{label}</strong></li>)}</ol>
      <form onSubmit={submit}>
        <fieldset className="mqtt-config-fieldset" disabled={configurationLocked || detailState === "error"}>
          {step === 0 && <section className="mqtt-panel"><div className="mqtt-panel-heading"><Globe2 /><div><h2>Endpoint and access</h2><p>Bind one exact HTTP path. Authentication material comes only from an encrypted HTTP Credential Profile.</p></div></div><div className="mqtt-form-grid"><label><span>Publisher name</span><input aria-label="Publisher name" autoFocus value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} /></label><label><span>Bind address</span><input aria-label="Bind address" spellCheck={false} value={draft.http.bind_address} onChange={(event) => updateHTTP("bind_address", event.target.value)} /><small>Use 127.0.0.1 for local-only or 0.0.0.0 for all interfaces.</small></label><label><span>Port</span><input aria-label="HTTP Server port" type="number" value={draft.http.port} onChange={(event) => updateHTTP("port", Number(event.target.value))} /></label><label><span>Exact path</span><input aria-label="HTTP Server path" spellCheck={false} value={draft.http.path} onChange={(event) => updateHTTP("path", event.target.value)} /></label><label><span>Access mode</span><select aria-label="Access mode" value={draft.http.access_mode} onChange={(event) => updateHTTP("access_mode", event.target.value as HTTPServerPublisherDraft["http"]["access_mode"])}><option value="api_key">API key header</option><option value="basic">HTTP Basic</option><option value="bearer">Bearer token</option><option value="anonymous">Anonymous</option></select></label>{draft.http.access_mode === "api_key" && <label><span>API key header</span><input aria-label="API key header" value={draft.http.api_key_header} onChange={(event) => updateHTTP("api_key_header", event.target.value)} /></label>}<label><span>Credential Profile</span><select aria-label="HTTP Credential Profile" value={draft.credential_id} onChange={(event) => selectCredential(event.target.value)}><option value="">No Credential Profile</option>{credentials.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select><small>{draft.credential_id ? `${draft.credential_slots.length} encrypted HTTP slot(s)` : "Required for API key, Basic, and Bearer modes."}</small></label><label><span>Manage credentials</span><Link className="mqtt-inline-link" to="/credentials/new?type=http"><FileKey2 size={15} />Create HTTP Credential Profile</Link><small>Secrets never appear in Publisher configuration.</small></label></div>{draft.http.access_mode === "anonymous" && <label className="mqtt-warning-check"><input type="checkbox" checked={draft.http.anonymous_acknowledged} onChange={(event) => updateHTTP("anonymous_acknowledged", event.target.checked)} /><span><strong>I understand this endpoint has no authentication</strong><small>Network controls become the only access boundary.</small></span></label>}<div className="http-endpoint-preview"><Server size={17} /><span><small>Endpoint preview</small><code>{endpoint}</code></span></div></section>}

          {step === 1 && <section className="mqtt-panel"><div className="mqtt-panel-heading"><Database /><div><h2>Sources and snapshot trigger</h2><p>Select immutable Core descriptors. Different source timestamps remain explicit in the rendered response.</p></div></div><div className="mqtt-source-catalog"><header><div><h3>Core source catalog</h3><p>Enabled Tags and Plugin outputs available to Data Publisher.</p></div><span>{catalog.length} results</span></header><div className="mqtt-catalog-filters"><label><span>Search</span><input aria-label="Search Publisher sources" value={catalogSearch} onChange={(event) => { setCatalogState("loading"); setCatalogSearch(event.target.value); }} /></label><label><span>Kind</span><select aria-label="Publisher source kind" value={catalogKind} onChange={(event) => { setCatalogState("loading"); setCatalogKind(event.target.value as PublisherSourceKind | ""); }}><option value="">All sources</option><option value="tag">Core Tags</option><option value="plugin_output">Plugin outputs</option></select></label></div>{catalogState === "loading" && <div className="mqtt-catalog-empty">Loading source catalog…</div>}{catalogState === "error" && <div className="mqtt-catalog-empty">Source catalog unavailable</div>}{catalogState === "ready" && <div className="mqtt-catalog-list">{catalog.map((entry) => { const selected = draft.sources.some((source) => source.reference && referenceKey(source.reference) === referenceKey(entry.descriptor.reference)); return <article key={referenceKey(entry.descriptor.reference)}><div><strong>{entry.descriptor.name}</strong><span>{entry.descriptor.owner_name || "Core"} · {entry.descriptor.reference.kind}</span></div><div><strong>{entry.descriptor.data_type}</strong><span>{entry.descriptor.unit || "No unit"} · {entry.descriptor.period_kind}</span></div><PublisherSourceCurrent entry={entry} /><button type="button" disabled={selected} onClick={() => addSource(entry)}>{selected ? <Check size={14} /> : <Plus size={14} />}{selected ? "Added" : "Add"}</button></article>; })}</div>}</div><div className="mqtt-source-list">{draft.sources.map((source) => <div className="http-source-row" key={source.id}><div><strong>{source.name}</strong><small>{source.owner_name} · {source.data_type} · {source.unit || "No unit"}</small></div><label><span>Alias</span><input aria-label={`Alias for ${source.name}`} value={source.alias} onChange={(event) => updateSource(source.id, event.target.value)} /></label><button aria-label={`Remove ${source.name}`} type="button" onClick={() => removeSource(source.id)}><Trash2 size={16} /></button></div>)}</div><div className="mqtt-subsection"><div className="mqtt-subsection-heading"><Clock3 /><div><h3>Snapshot trigger</h3><p>Controls when Core refreshes the retained snapshot, not Datasource polling.</p></div></div><div className="mqtt-mode-grid"><button className={draft.trigger.mode === "interval" ? "is-selected" : ""} type="button" onClick={() => setDraft((current) => ({ ...current, trigger: { ...current.trigger, mode: "interval" } }))}><Clock3 /><strong>Interval</strong><small>Refresh on a fixed cadence</small></button><button className={draft.trigger.mode === "on_change" ? "is-selected" : ""} type="button" onClick={() => setDraft((current) => ({ ...current, trigger: { ...current.trigger, mode: "on_change" } }))}><RefreshCw /><strong>On change</strong><small>Coalesce changes from one alias</small></button></div><div className="mqtt-form-grid">{draft.trigger.mode === "interval" ? <label><span>Interval (ms)</span><input aria-label="Snapshot interval" type="number" value={draft.trigger.interval_ms} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, interval_ms: Number(event.target.value) } }))} /></label> : <><label><span>Trigger alias</span><select aria-label="Trigger alias" value={draft.trigger.source_alias} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, source_alias: event.target.value } }))}>{draft.sources.map((source) => <option key={source.id}>{source.alias}</option>)}</select></label><label><span>Coalesce (ms)</span><input aria-label="Coalesce window" type="number" value={draft.trigger.coalesce_ms} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, coalesce_ms: Number(event.target.value) } }))} /></label></>}</div></div></section>}

          {step === 2 && <section className="mqtt-panel"><div className="mqtt-panel-heading"><Code2 /><div><h2>Advanced JSON response template</h2><p>Generate a complete response from selected sources, or type your own JSON and use the syntax guide at the cursor.</p></div></div><div className="http-template-generator"><div><strong>Generate from selected sources</strong><small>Tags include live value provenance. Windowed Plugin outputs also include period bounds and coverage.</small></div><button type="button" onClick={generatePayloadTemplate}><Code2 size={16} />Generate JSON</button></div><div className="mqtt-template-palette"><div className="mqtt-palette-controls"><label><span>Helper</span><select aria-label="Template helper" value={payloadHelper} onChange={(event) => setPayloadHelper(event.target.value as MQTTPayloadSourceHelper)}>{mqttPayloadSourceHelpers.map((helper) => <option key={helper.id} value={helper.id}>{helper.label}</option>)}</select></label><div><span>Insert syntax</span><div className="mqtt-source-syntax-list">{draft.sources.map((source) => <button key={source.id} type="button" onClick={() => insertSyntax(mqttPayloadSourceSyntax(payloadHelper, source.alias))}><strong>{source.alias}</strong><code>{mqttPayloadSourceSyntax(payloadHelper, source.alias)}</code></button>)}</div><div className="mqtt-context-syntax-list">{["{{published_at}}", "{{published_unix_ms}}", "{{publisher_id}}"].map((syntax) => <button key={syntax} type="button" onClick={() => insertSyntax(syntax)}><code>{syntax}</code></button>)}</div></div></div></div><div className="mqtt-editor-grid"><div className="mqtt-editor"><label><span>Response payload template</span><small>{new TextEncoder().encode(draft.response.payload_template).length} / 16,384 bytes</small></label><textarea aria-label="Response payload template" ref={templateRef} spellCheck={false} value={draft.response.payload_template} onChange={(event) => { setDraft((current) => ({ ...current, response: { payload_template: event.target.value } })); setServerValidation(null); }} /></div><div className="mqtt-preview"><header><div><strong>Rendered fixture</strong><small>Local preview; Core validation is authoritative</small></div><div role="tablist">{fixtures.map((item) => <button role="tab" aria-selected={fixture === item.id} className={fixture === item.id ? "is-active" : ""} type="button" key={item.id} onClick={() => setFixture(item.id)}>{item.label}</button>)}</div></header>{preview.result ? <pre>{preview.result.rendered}</pre> : <div className="mqtt-preview-error"><CircleAlert />{preview.error}</div>}</div></div><div className="mqtt-core-validation"><button type="button" onClick={() => void validateOnServer()}><ShieldCheck size={16} />Validate with Core</button><span>{serverValidation ? `${serverValidation.helper_calls} helpers · ${serverValidation.referenced_aliases.length} referenced aliases` : "Checks exact aliases, types, bounds, and all three fixtures."}</span></div></section>}

          {step === 3 && <section className="mqtt-panel"><div className="mqtt-panel-heading"><Server /><div><h2>Review and runtime</h2><p>Save only as disabled, then enable the listener from this page.</p></div></div><div className="http-review-grid"><article><span>Endpoint</span><strong>{endpoint}</strong><small>{draft.http.access_mode} · {draft.http.quality_policy} quality policy</small></article><article><span>Sources</span><strong>{draft.sources.length}</strong><small>{draft.trigger.mode === "interval" ? `Every ${draft.trigger.interval_ms} ms` : `On ${draft.trigger.source_alias}`}</small></article><article><span>Limits</span><strong>{draft.http.max_connections} connections</strong><small>{draft.http.max_header_bytes.toLocaleString()} header bytes</small></article></div><details className="mqtt-advanced"><summary>Timeout and connection limits</summary><div className="mqtt-form-grid"><label><span>Read timeout (ms)</span><input type="number" value={draft.http.read_timeout_ms} onChange={(event) => updateHTTP("read_timeout_ms", Number(event.target.value))} /></label><label><span>Write timeout (ms)</span><input type="number" value={draft.http.write_timeout_ms} onChange={(event) => updateHTTP("write_timeout_ms", Number(event.target.value))} /></label><label><span>Idle timeout (ms)</span><input type="number" value={draft.http.idle_timeout_ms} onChange={(event) => updateHTTP("idle_timeout_ms", Number(event.target.value))} /></label><label><span>Maximum header bytes</span><input type="number" value={draft.http.max_header_bytes} onChange={(event) => updateHTTP("max_header_bytes", Number(event.target.value))} /></label><label><span>Maximum connections</span><input type="number" value={draft.http.max_connections} onChange={(event) => updateHTTP("max_connections", Number(event.target.value))} /></label><label><span>Quality policy</span><select aria-label="Quality policy" value={draft.http.quality_policy} onChange={(event) => updateHTTP("quality_policy", event.target.value as HTTPServerPublisherDraft["http"]["quality_policy"])}><option value="payload">Payload: always render explicit quality</option><option value="strict">Strict: degraded response uses 503</option></select></label></div></details><div className="mqtt-review"><header><div><h3>Canonical configuration</h3><p>References profile identity only; no secret values.</p></div><button type="button" onClick={() => void copyConfig()}><Copy size={15} />Copy config</button></header><pre>{configJSON}</pre></div></section>}
        </fieldset>
        {step === 3 && publisher && <HTTPServerPublisherOperations publisher={publisher} endpoint={endpoint} onPublisherChange={(next) => { setPublisher(next); setDraft((current) => ({ ...current, enabled: next.enabled })); }} />}
        {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
        {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
        <footer className="mqtt-actions"><button type="button" disabled={step === 0} onClick={() => { setMessage(""); setStep((current) => Math.max(0, current - 1)); }}><ArrowLeft size={17} />Back</button><div>{step < steps.length - 1 ? <button className="is-primary" type="button" onClick={nextStep}>Continue<ArrowRight size={17} /></button> : <><button type="button" onClick={() => { localStorage.setItem("iot-edge.http-server-publisher-draft.v1", JSON.stringify(draft)); setNotice("Draft saved locally without secret values."); }}><Info size={16} />Save local draft</button><button className="is-primary" type="submit" disabled={saving || configurationLocked || detailState !== "ready"}><Check size={17} />{saving ? "Saving…" : publisher ? "Update configuration" : "Save disabled Publisher"}</button></>}</div></footer>
      </form>
    </div>
  </VGatewayShell>;
}

export default HTTPServerPublisherWizardPage;
