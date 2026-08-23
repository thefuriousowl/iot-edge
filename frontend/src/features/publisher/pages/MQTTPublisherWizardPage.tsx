import {
  Activity,
  ArrowLeft,
  ArrowRight,
  Braces,
  Check,
  CircleAlert,
  CircleDashed,
  Clock3,
  Copy,
  DatabaseZap,
  FileKey2,
  Info,
  LockKeyhole,
  Plus,
  Radio,
  RefreshCw,
  Send,
  ShieldCheck,
  Trash2,
  Unplug,
  Waypoints,
} from "lucide-react";
import { type FormEvent, useMemo, useState } from "react";
import { Link } from "react-router-dom";

import type {
  MQTTPayloadFixture,
  MQTTPayloadMapping,
  MQTTPublisherDraft,
  PublisherSourceDataType,
  PublisherSourceDraft,
} from "../../../types/publisher";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import {
  exportMQTTPublisherConfig,
  generatePayloadTemplate,
  initialMQTTPublisherDraft,
  mqttPayloadHelpers,
  previewPayloadTemplate,
  validateBrokerURL,
  validateMQTTPublisherDraft,
  validateSecretReference,
  validateSourceAliases,
} from "../utils/mqtt";
import "../../vgateway/pages/VGatewayListPage.css";
import "./MQTTPublisherWizardPage.css";

const steps = ["Connection", "Sources", "Payload", "Diagnostics"];
const dataTypes: PublisherSourceDataType[] = ["bool", "int16", "uint16", "int32", "uint32", "float32", "float64", "string"];
const fixtures: Array<{ id: MQTTPayloadFixture; label: string }> = [
  { id: "good", label: "Good" },
  { id: "unavailable", label: "Unavailable" },
  { id: "windowed", label: "Windowed" },
];

function loadMQTTPublisherDraft(): MQTTPublisherDraft {
  const initial = initialMQTTPublisherDraft();
  try {
    const raw = localStorage.getItem("iot-edge.mqtt-publisher-draft.v1");
    if (!raw) return initial;
    const stored = JSON.parse(raw) as Partial<MQTTPublisherDraft>;
    if (!stored.trigger || !stored.mqtt || !Array.isArray(stored.sources) || !Array.isArray(stored.mappings) || !Array.isArray(stored.mqtt.diagnostics)) return initial;
    return {
      ...initial,
      ...stored,
      trigger: { ...initial.trigger, ...stored.trigger },
      sources: stored.sources,
      mappings: stored.mappings,
      mqtt: {
        ...initial.mqtt,
        ...stored.mqtt,
        auth: { ...initial.mqtt.auth, ...stored.mqtt.auth },
        tls: { ...initial.mqtt.tls, ...stored.mqtt.tls },
        publish: { ...initial.mqtt.publish, ...stored.mqtt.publish },
        diagnostics: stored.mqtt.diagnostics,
      },
    };
  } catch {
    return initial;
  }
}

function updateAt<T extends { id: string }>(items: T[], id: string, update: Partial<T>): T[] {
  return items.map((item) => item.id === id ? { ...item, ...update } : item);
}

function MQTTPublisherWizardPage() {
  const [draft, setDraft] = useState<MQTTPublisherDraft>(loadMQTTPublisherDraft);
  const [step, setStep] = useState(0);
  const [fixture, setFixture] = useState<MQTTPayloadFixture>("good");
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const secure = draft.mqtt.broker_url.trim().startsWith("mqtts://");
  const preview = useMemo(() => {
    try {
      return { result: previewPayloadTemplate(draft.mqtt.publish.payload_template, draft.sources, fixture), error: "" };
    } catch (error) {
      return { result: null, error: error instanceof Error ? error.message : "Payload preview failed" };
    }
  }, [draft.mqtt.publish.payload_template, draft.sources, fixture]);
  const configJSON = useMemo(() => JSON.stringify(exportMQTTPublisherConfig(draft), null, 2), [draft]);

  function updateMQTT<Key extends keyof MQTTPublisherDraft["mqtt"]>(key: Key, value: MQTTPublisherDraft["mqtt"][Key]) {
    setDraft((current) => ({ ...current, mqtt: { ...current.mqtt, [key]: value } }));
  }

  function validateStep(): string | null {
    if (step === 0) {
      if (!draft.name.trim()) return "Publisher name is required";
      const brokerError = validateBrokerURL(draft.mqtt.broker_url.trim());
      if (brokerError) return brokerError;
      if (!secure && !draft.mqtt.plaintext_acknowledged) return "Acknowledge the plain MQTT exposure before continuing";
      if (!validateSecretReference(draft.mqtt.auth.username_ref) || !validateSecretReference(draft.mqtt.auth.password_ref)) return "Authentication secret references are invalid";
      if (draft.mqtt.auth.password_ref && !draft.mqtt.auth.username_ref) return "Password reference requires a username reference";
      if (secure && (!validateSecretReference(draft.mqtt.tls.custom_ca_ref) || !validateSecretReference(draft.mqtt.tls.client_identity_ref))) return "TLS secret references are invalid";
    }
    if (step === 1) {
      const aliasErrors = validateSourceAliases(draft.sources);
      if (draft.sources.length === 0) return "Add at least one source alias";
      if (aliasErrors.length > 0) return aliasErrors[0];
      if (draft.trigger.mode === "interval" && (draft.trigger.interval_ms < 100 || draft.trigger.interval_ms > 86_400_000)) return "Interval must be between 100 ms and 24 hours";
      if (draft.trigger.mode === "on_change" && !draft.sources.some((source) => source.alias === draft.trigger.source_alias)) return "Select a valid on-change source alias";
    }
    if (step === 2 && preview.error) return preview.error;
    if (step === 3) {
      const errors = validateMQTTPublisherDraft(draft);
      if (errors.length > 0) return errors[0];
    }
    return null;
  }

  function continueWizard() {
    const error = validateStep();
    if (error) {
      setMessage(error);
      return;
    }
    setMessage("");
    setNotice("");
    setStep((current) => Math.min(steps.length - 1, current + 1));
  }

  function updateSource(id: string, update: Partial<PublisherSourceDraft>) {
    setDraft((current) => {
      const previous = current.sources.find((source) => source.id === id);
      const nextSources = updateAt(current.sources, id, update);
      const nextAlias = update.alias;
      return {
        ...current,
        sources: nextSources,
        trigger: nextAlias && previous?.alias === current.trigger.source_alias ? { ...current.trigger, source_alias: nextAlias } : current.trigger,
        mappings: nextAlias && previous ? current.mappings.map((mapping) => mapping.alias === previous.alias ? { ...mapping, alias: nextAlias } : mapping) : current.mappings,
      };
    });
  }

  function addSource() {
    const index = draft.sources.length + 1;
    const source: PublisherSourceDraft = {
      id: crypto.randomUUID(),
      alias: `source_${index}`,
      name: `Source ${index}`,
      owner_name: "Payload schema draft",
      kind: "tag",
      data_type: "float64",
      unit: "",
      period_kind: "instantaneous",
    };
    setDraft((current) => ({ ...current, sources: [...current.sources, source] }));
  }

  function removeSource(id: string) {
    setDraft((current) => {
      const removed = current.sources.find((source) => source.id === id);
      const sources = current.sources.filter((source) => source.id !== id);
      return {
        ...current,
        sources,
        trigger: removed?.alias === current.trigger.source_alias ? { ...current.trigger, source_alias: sources[0]?.alias ?? "" } : current.trigger,
        mappings: removed ? current.mappings.filter((mapping) => mapping.alias !== removed.alias) : current.mappings,
      };
    });
  }

  function addMapping() {
    const mapping: MQTTPayloadMapping = {
      id: crypto.randomUUID(),
      field: `field_${draft.mappings.length + 1}`,
      alias: draft.sources[0]?.alias ?? "",
      helper: "value",
    };
    setDraft((current) => ({ ...current, mappings: [...current.mappings, mapping] }));
  }

  function regenerateTemplate() {
    setDraft((current) => ({
      ...current,
      mqtt: {
        ...current.mqtt,
        publish: { ...current.mqtt.publish, payload_template: generatePayloadTemplate(current.mappings) },
      },
    }));
    setNotice("Advanced JSON regenerated from the field mapper.");
    setMessage("");
  }

  function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const errors = validateMQTTPublisherDraft(draft);
    if (errors.length > 0) {
      setMessage(errors[0]);
      return;
    }
    localStorage.setItem("iot-edge.mqtt-publisher-draft.v1", JSON.stringify(draft));
    setMessage("");
    setNotice("Draft saved in this browser. Server save remains disabled until the Publisher Admin API is available.");
  }

  async function copyConfig() {
    try {
      await navigator.clipboard.writeText(configJSON);
      setNotice("Secret-reference-only MQTT configuration copied.");
      setMessage("");
    } catch {
      setMessage("Clipboard access is unavailable in this browser");
    }
  }

  return (
    <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Publishers <span>/</span> <strong>MQTT</strong></>}>
      <div className="mqtt-wizard-content">
        <header className="mqtt-heading">
          <Link to="/dashboard"><ArrowLeft size={17} /> Dashboard</Link>
          <div className="mqtt-heading-row">
            <div>
              <p>Core Data Publisher</p>
              <h1>MQTT Publisher</h1>
              <span>Build verified MQTT/TLS telemetry payloads from Core Tags and typed Plugin outputs—without creating extra acquisition reads.</span>
            </div>
            <div className="mqtt-contract-badge"><ShieldCheck size={18} /><span><strong>Transport ready</strong><small>Admin API integration pending</small></span></div>
          </div>
        </header>

        <div className="mqtt-api-note" role="note">
          <Info size={18} />
          <span><strong>Frontend-first workspace.</strong> Validation, fixtures, mapping, and local drafts work now. Server save, source catalog, secret upload, connection test, and live status stay intentionally unavailable until BE-9.12 exposes authenticated endpoints.</span>
        </div>

        <ol className="mqtt-steps" aria-label="MQTT setup progress">
          {steps.map((label, index) => <li key={label} className={index === step ? "is-current" : index < step ? "is-complete" : ""}><span>{index < step ? <Check size={15} /> : index + 1}</span><strong>{label}</strong></li>)}
        </ol>

        <form className="mqtt-wizard-form" onSubmit={submit}>
          {step === 0 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Radio /><div><h2>Broker connection</h2><p>Use an explicit port. Credentials are represented only by Publisher-owned secret names and never embedded in this draft.</p></div></div>
            <div className="mqtt-form-grid">
              <label><span>Publisher name</span><input autoFocus value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} /></label>
              <label><span>Broker URL</span><input aria-describedby="mqtt-broker-help" spellCheck={false} value={draft.mqtt.broker_url} onChange={(event) => updateMQTT("broker_url", event.target.value)} /><small id="mqtt-broker-help">Example: mqtts://broker.example.com:8883</small></label>
              <label><span>Client ID <i>optional</i></span><input spellCheck={false} value={draft.mqtt.client_id} onChange={(event) => updateMQTT("client_id", event.target.value)} /><small>Leave blank to let Core derive a stable Publisher client ID.</small></label>
              <label><span>Transport security</span><div className={`mqtt-security-state ${secure ? "is-secure" : "is-plain"}`}>{secure ? <LockKeyhole size={17} /> : <Unplug size={17} />}<strong>{secure ? "TLS with server verification" : "Plain MQTT"}</strong></div></label>
            </div>
            {!secure && <label className="mqtt-warning-check"><input type="checkbox" checked={draft.mqtt.plaintext_acknowledged} onChange={(event) => updateMQTT("plaintext_acknowledged", event.target.checked)} /><span><strong>I understand this connection is not encrypted</strong><small>Use only on a trusted isolated network. Authentication values can be exposed in transit.</small></span></label>}

            <div className="mqtt-subsection"><div className="mqtt-subsection-heading"><FileKey2 size={18} /><div><h3>Authentication references</h3><p>Enter secret names, not usernames or passwords. Secret material will be uploaded through the protected API later.</p></div></div><div className="mqtt-form-grid"><label><span>Username secret reference</span><input spellCheck={false} value={draft.mqtt.auth.username_ref} onChange={(event) => updateMQTT("auth", { ...draft.mqtt.auth, username_ref: event.target.value })} /></label><label><span>Password secret reference</span><input spellCheck={false} value={draft.mqtt.auth.password_ref} onChange={(event) => updateMQTT("auth", { ...draft.mqtt.auth, password_ref: event.target.value })} /></label></div></div>

            {secure && <div className="mqtt-subsection"><div className="mqtt-subsection-heading"><ShieldCheck size={18} /><div><h3>TLS verification and optional mTLS</h3><p>System roots and broker hostname verification are always active. There is no insecure skip-verify option.</p></div></div><div className="mqtt-form-grid"><label><span>Server name override <i>optional</i></span><input spellCheck={false} value={draft.mqtt.tls.server_name} onChange={(event) => updateMQTT("tls", { ...draft.mqtt.tls, server_name: event.target.value })} /><small>Usually blank; Core derives it from the broker hostname.</small></label><label><span>Custom CA secret <i>optional</i></span><input spellCheck={false} value={draft.mqtt.tls.custom_ca_ref} onChange={(event) => updateMQTT("tls", { ...draft.mqtt.tls, custom_ca_ref: event.target.value })} /></label><label><span>Client identity secret <i>optional mTLS</i></span><input spellCheck={false} value={draft.mqtt.tls.client_identity_ref} onChange={(event) => updateMQTT("tls", { ...draft.mqtt.tls, client_identity_ref: event.target.value })} /><small>One atomic certificate + private-key identity reference.</small></label></div></div>}
          </section>}

          {step === 1 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><DatabaseZap /><div><h2>Payload source aliases</h2><p>Define the schema used by the editor now. BE-9.12 will replace these draft descriptors with searchable Tag and Plugin-output catalog selections.</p></div></div>
            <div className="mqtt-source-note"><CircleAlert size={17} /><span>These rows are payload schema drafts, not saved source references. Plugin outputs retain period start/end and Tags remain instantaneous.</span></div>
            <div className="mqtt-source-list">
              {draft.sources.map((source) => <article key={source.id} className="mqtt-source-row">
                <label><span>Alias</span><input aria-label={`Alias for ${source.name}`} spellCheck={false} value={source.alias} onChange={(event) => updateSource(source.id, { alias: event.target.value })} /></label>
                <label><span>Display name</span><input aria-label={`Display name for ${source.alias}`} value={source.name} onChange={(event) => updateSource(source.id, { name: event.target.value })} /></label>
                <label><span>Kind</span><select aria-label={`Kind for ${source.alias}`} value={source.kind} onChange={(event) => updateSource(source.id, { kind: event.target.value as PublisherSourceDraft["kind"], period_kind: event.target.value === "tag" ? "instantaneous" : source.period_kind })}><option value="tag">Core Tag</option><option value="plugin_output">Plugin output</option></select></label>
                <label><span>Data type</span><select aria-label={`Data type for ${source.alias}`} value={source.data_type} onChange={(event) => updateSource(source.id, { data_type: event.target.value as PublisherSourceDataType })}>{dataTypes.map((type) => <option key={type}>{type}</option>)}</select></label>
                <label><span>Unit</span><input aria-label={`Unit for ${source.alias}`} value={source.unit} onChange={(event) => updateSource(source.id, { unit: event.target.value })} /></label>
                <label><span>Period</span><select aria-label={`Period for ${source.alias}`} disabled={source.kind === "tag"} value={source.kind === "tag" ? "instantaneous" : source.period_kind} onChange={(event) => updateSource(source.id, { period_kind: event.target.value as PublisherSourceDraft["period_kind"] })}><option value="instantaneous">Instantaneous</option><option value="windowed">Windowed</option></select></label>
                <button type="button" aria-label={`Remove source ${source.alias}`} onClick={() => removeSource(source.id)}><Trash2 size={17} /></button>
              </article>)}
            </div>
            <button className="mqtt-add-button" type="button" onClick={addSource}><Plus size={17} /> Add source schema</button>

            <div className="mqtt-subsection"><div className="mqtt-subsection-heading"><Clock3 size={18} /><div><h3>Publish trigger</h3><p>The trigger snapshots already-available source values. It never changes Datasource polling or requests a Device.</p></div></div><div className="mqtt-mode-grid"><button className={draft.trigger.mode === "interval" ? "is-selected" : ""} type="button" onClick={() => setDraft((current) => ({ ...current, trigger: { ...current.trigger, mode: "interval" } }))}><Clock3 /><strong>Fixed interval</strong><small>Publish the latest mixed snapshot on a timer.</small></button><button className={draft.trigger.mode === "on_change" ? "is-selected" : ""} type="button" onClick={() => setDraft((current) => ({ ...current, trigger: { ...current.trigger, mode: "on_change" } }))}><Activity /><strong>On source change</strong><small>Coalesce updates from one selected alias.</small></button></div><div className="mqtt-form-grid">{draft.trigger.mode === "interval" ? <label><span>Publish interval (ms)</span><input type="number" min="100" max="86400000" value={draft.trigger.interval_ms} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, interval_ms: Number(event.target.value) } }))} /></label> : <><label><span>Trigger source</span><select value={draft.trigger.source_alias} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, source_alias: event.target.value } }))}>{draft.sources.map((source) => <option key={source.id} value={source.alias}>{source.alias}</option>)}</select></label><label><span>Coalesce window (ms)</span><input type="number" min="1" max="60000" value={draft.trigger.coalesce_ms} onChange={(event) => setDraft((current) => ({ ...current, trigger: { ...current.trigger, coalesce_ms: Number(event.target.value) } }))} /></label></>}</div></div>
          </section>}

          {step === 2 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Braces /><div><h2>Custom JSON payload</h2><p>Use the guided mapper for common fields, then refine the generated template. Only the documented helper allowlist is accepted.</p></div></div>
            <div className="mqtt-publish-grid"><label><span>Publish topic</span><input spellCheck={false} value={draft.mqtt.publish.topic} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, topic: event.target.value })} /></label><label><span>QoS</span><select value={draft.mqtt.publish.qos} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, qos: Number(event.target.value) as 0 | 1 })}><option value={0}>0 · At most once</option><option value={1}>1 · At least once</option></select></label><label className="mqtt-inline-check"><input type="checkbox" checked={draft.mqtt.publish.retain} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, retain: event.target.checked })} /><span><strong>Retain latest payload</strong><small>Broker stores the last message for new subscribers.</small></span></label></div>

            <div className="mqtt-mapper"><header><div><h3>Guided field mapper</h3><p>Custom column names map to one source alias and one safe helper.</p></div><div><button type="button" onClick={addMapping}><Plus size={16} /> Add field</button><button className="is-emphasis" type="button" onClick={regenerateTemplate}><RefreshCw size={16} /> Generate JSON</button></div></header>{draft.mappings.map((mapping) => <div className="mqtt-mapping-row" key={mapping.id}><label><span>JSON field</span><input aria-label={`JSON field ${mapping.id}`} value={mapping.field} onChange={(event) => setDraft((current) => ({ ...current, mappings: updateAt(current.mappings, mapping.id, { field: event.target.value }) }))} /></label><label><span>Source alias</span><select aria-label={`Source alias for ${mapping.field}`} value={mapping.alias} onChange={(event) => setDraft((current) => ({ ...current, mappings: updateAt(current.mappings, mapping.id, { alias: event.target.value }) }))}>{draft.sources.map((source) => <option key={source.id} value={source.alias}>{source.alias}</option>)}</select></label><label><span>Helper</span><select aria-label={`Helper for ${mapping.field}`} value={mapping.helper} onChange={(event) => setDraft((current) => ({ ...current, mappings: updateAt(current.mappings, mapping.id, { helper: event.target.value as MQTTPayloadMapping["helper"] }) }))}><option value="value">Value</option><option value="quality">Quality</option><option value="unit">Unit</option><option value="observed_at">Observed at</option><option value="period_start">Period start</option><option value="period_end">Period end</option><option value="coverage">Coverage</option></select></label><button type="button" aria-label={`Remove mapping ${mapping.field}`} onClick={() => setDraft((current) => ({ ...current, mappings: current.mappings.filter((candidate) => candidate.id !== mapping.id) }))}><Trash2 size={16} /></button></div>)}</div>

            <div className="mqtt-editor-grid"><div className="mqtt-editor"><label htmlFor="mqtt-payload-template"><span>Advanced JSON template</span><small>{new TextEncoder().encode(draft.mqtt.publish.payload_template).length.toLocaleString()} / 16,384 bytes</small></label><textarea id="mqtt-payload-template" spellCheck={false} value={draft.mqtt.publish.payload_template} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, payload_template: event.target.value })} /><details><summary>Allowed helpers ({mqttPayloadHelpers.length})</summary><code>{mqttPayloadHelpers.join(" · ")}</code><p>Examples: {`{{value "active_power_kw"}}`} · {`{{round "active_power_kw" 2}}`} · {`{{default "active_power_kw" 0}}`} · {`{{published_unix_ms}}`}</p></details></div><div className="mqtt-preview"><header><div><strong>Fixture preview</strong>{preview.result && <small>{preview.result.helperCalls} helpers · {preview.result.referencedAliases.length} aliases</small>}</div><div role="tablist" aria-label="Payload fixture">{fixtures.map((option) => <button key={option.id} className={fixture === option.id ? "is-active" : ""} type="button" role="tab" aria-selected={fixture === option.id} onClick={() => setFixture(option.id)}>{option.label}</button>)}</div></header>{preview.error ? <div className="mqtt-preview-error" role="alert"><CircleAlert />{preview.error}</div> : <pre>{preview.result?.rendered}</pre>}</div></div>
          </section>}

          {step === 3 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Waypoints /><div><h2>Diagnostics and review</h2><p>Subscribe to arbitrary broker response topics. Messages remain generic JSON, text, or base64 binary—no ACK/Error field schema is assumed.</p></div></div>
            <div className="mqtt-diagnostic-list">{draft.mqtt.diagnostics.map((diagnostic) => <div className="mqtt-diagnostic-row" key={diagnostic.id}><label><span>Label</span><input aria-label={`Diagnostic label ${diagnostic.id}`} spellCheck={false} value={diagnostic.label} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { label: event.target.value }))} /></label><label><span>Topic filter</span><input aria-label={`Topic filter for ${diagnostic.label}`} spellCheck={false} value={diagnostic.topic_filter} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { topic_filter: event.target.value }))} /></label><label><span>QoS</span><select aria-label={`QoS for ${diagnostic.label}`} value={diagnostic.qos} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { qos: Number(event.target.value) as 0 | 1 }))}><option value={0}>0</option><option value={1}>1</option></select></label><button type="button" aria-label={`Remove diagnostic ${diagnostic.label}`} onClick={() => updateMQTT("diagnostics", draft.mqtt.diagnostics.filter((candidate) => candidate.id !== diagnostic.id))}><Trash2 size={17} /></button></div>)}</div><button className="mqtt-add-button" type="button" disabled={draft.mqtt.diagnostics.length >= 16} onClick={() => updateMQTT("diagnostics", [...draft.mqtt.diagnostics, { id: crypto.randomUUID(), label: `diagnostic_${draft.mqtt.diagnostics.length + 1}`, topic_filter: "site/edge/#", qos: 1 }])}><Plus size={17} /> Add diagnostic subscription</button>

            <div className="mqtt-runtime-grid" aria-label="MQTT runtime preview"><article><CircleDashed /><span>Connection</span><strong>Not connected</strong><small>Connection test requires BE-9.12</small></article><article><Send /><span>Delivery</span><strong>—</strong><small>QoS {draft.mqtt.publish.qos} · queue {draft.mqtt.queue_capacity}</small></article><article><RefreshCw /><span>Reconnects / drops</span><strong>— / —</strong><small>Live counters appear after save</small></article></div>

            <div className="mqtt-monitor"><header><div><h3>Generic diagnostic monitor</h3><p>Runtime events will show label, exact topic, QoS, retain/duplicate flags, receive time, format, truncation, and the untouched payload.</p></div><span>History {draft.mqtt.diagnostic_history_depth}</span></header><div className="mqtt-monitor-empty"><Waypoints /><strong>Awaiting Publisher runtime</strong><span>No broker message is fabricated in this frontend-first view.</span></div></div>

            <details className="mqtt-advanced"><summary>Transport limits and reconnect policy</summary><div className="mqtt-form-grid"><label><span>Keep alive (ms)</span><input type="number" min="10000" max="3600000" value={draft.mqtt.keep_alive_ms} onChange={(event) => updateMQTT("keep_alive_ms", Number(event.target.value))} /></label><label><span>Connect timeout (ms)</span><input type="number" min="1000" max="120000" value={draft.mqtt.connect_timeout_ms} onChange={(event) => updateMQTT("connect_timeout_ms", Number(event.target.value))} /></label><label><span>Publish timeout (ms)</span><input type="number" min="1000" max="120000" value={draft.mqtt.publish_timeout_ms} onChange={(event) => updateMQTT("publish_timeout_ms", Number(event.target.value))} /></label><label><span>Reconnect minimum (ms)</span><input type="number" min="100" max="60000" value={draft.mqtt.reconnect_min_ms} onChange={(event) => updateMQTT("reconnect_min_ms", Number(event.target.value))} /></label><label><span>Reconnect maximum (ms)</span><input type="number" min={draft.mqtt.reconnect_min_ms} max="300000" value={draft.mqtt.reconnect_max_ms} onChange={(event) => updateMQTT("reconnect_max_ms", Number(event.target.value))} /></label><label><span>Offline queue capacity</span><input type="number" min="1" max="10000" value={draft.mqtt.queue_capacity} onChange={(event) => updateMQTT("queue_capacity", Number(event.target.value))} /></label><label><span>Diagnostic history</span><input type="number" min="1" max="1000" value={draft.mqtt.diagnostic_history_depth} onChange={(event) => updateMQTT("diagnostic_history_depth", Number(event.target.value))} /></label></div></details>

            <div className="mqtt-review"><header><div><h3>Server configuration preview</h3><p>Contains reference names only. No username, password, certificate, or private key material is serialized.</p></div><button type="button" onClick={() => void copyConfig()}><Copy size={16} /> Copy config</button></header><pre>{configJSON}</pre></div>
          </section>}

          {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
          {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
          <footer className="mqtt-actions"><button type="button" disabled={step === 0} onClick={() => { setMessage(""); setNotice(""); setStep((current) => Math.max(0, current - 1)); }}><ArrowLeft size={17} /> Back</button><div><button type="button" disabled title="Available after BE-9.12 connection-test API"><Radio size={17} /> Test connection</button>{step < steps.length - 1 ? <button className="is-primary" type="button" onClick={continueWizard}>Continue <ArrowRight size={17} /></button> : <button className="is-primary" type="submit"><Check size={17} /> Save local draft</button>}</div></footer>
        </form>
      </div>
    </VGatewayShell>
  );
}

export default MQTTPublisherWizardPage;
