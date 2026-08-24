import {
  ArrowLeft,
  ArrowRight,
  Braces,
  Check,
  CircleAlert,
  Copy,
  FileKey2,
  Info,
  LockKeyhole,
  Plus,
  Radio,
  RefreshCw,
  ShieldCheck,
  Trash2,
  Unplug,
  Waypoints,
} from "lucide-react";
import axios from "axios";
import { type FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { Link, useNavigate, useParams } from "react-router-dom";

import {
  createDataPublisher,
  getDataPublisher,
  listPublisherSources,
  updateDataPublisher,
  validatePublisherPayload,
} from "../../../services/publisher.service";
import { listCredentials } from "../../../services/credential.service";
import type { CredentialProfile } from "../../../types/credential";
import type {
  DataPublisher,
  MQTTPayloadFixture,
  MQTTPublisherDraft,
  PublisherPayloadValidation,
  PublisherSourceCatalogEntry,
  PublisherSourceDraft,
  PublisherSourceKind,
} from "../../../types/publisher";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import MQTTPublisherOperations from "../components/MQTTPublisherOperations";
import PublisherSourceSelector from "../components/PublisherSourceSelector";
import PublisherTemplateEditor from "../components/PublisherTemplateEditor";
import PublisherTriggerSetup from "../components/PublisherTriggerSetup";
import {
  exportMQTTPublisherConfig,
  buildBrokerURL,
  importMQTTPublisherDraft,
  initialMQTTPublisherDraft,
  previewPayloadTemplate,
  validateBrokerURL,
  validateMQTTPublisherDraft,
  validateSourceAliases,
} from "../utils/mqtt";
import { publisherSourceDraft, publisherSourceReferenceKey, selectedPublisherSources } from "../utils/shared";
import "../../vgateway/pages/VGatewayListPage.css";
import "./MQTTPublisherWizardPage.css";

const steps = ["Connection", "Sources", "Payload", "Diagnostics"];

function loadMQTTPublisherDraft(): MQTTPublisherDraft {
  const initial = initialMQTTPublisherDraft();
  try {
    const raw = localStorage.getItem("iot-edge.mqtt-publisher-draft.v1");
    if (!raw) return initial;
    const stored = JSON.parse(raw) as Partial<MQTTPublisherDraft>;
    if (!stored.trigger || !stored.mqtt || !Array.isArray(stored.sources) || !Array.isArray(stored.mqtt.diagnostics)) return initial;
    return {
      ...initial,
      name: stored.name ?? initial.name,
      enabled: stored.enabled ?? initial.enabled,
      credential_id: stored.credential_id ?? initial.credential_id,
      credential_slots: Array.isArray(stored.credential_slots) ? stored.credential_slots : initial.credential_slots,
      trigger: { ...initial.trigger, ...stored.trigger },
      sources: stored.sources,
      mqtt: {
        ...initial.mqtt,
        ...stored.mqtt,
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

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    return body?.error?.message ?? fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

function MQTTPublisherWizardPage() {
  const { id: routePublisherID } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [publisher, setPublisher] = useState<DataPublisher | null>(null);
  const [draft, setDraft] = useState<MQTTPublisherDraft>(() => routePublisherID ? initialMQTTPublisherDraft() : loadMQTTPublisherDraft());
  const [step, setStep] = useState(0);
  const [fixture, setFixture] = useState<MQTTPayloadFixture>("good");
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const [catalog, setCatalog] = useState<PublisherSourceCatalogEntry[]>([]);
  const [catalogKind, setCatalogKind] = useState<PublisherSourceKind | "">("");
  const [catalogSearch, setCatalogSearch] = useState("");
  const [catalogState, setCatalogState] = useState<"loading" | "ready" | "error">("loading");
  const [credentials, setCredentials] = useState<CredentialProfile[]>([]);
  const [serverValidation, setServerValidation] = useState<PublisherPayloadValidation | null>(null);
  const [saving, setSaving] = useState(false);
  const [detailState, setDetailState] = useState<"ready" | "loading" | "error">(routePublisherID ? "loading" : "ready");
  const secure = draft.mqtt.use_tls;
  const configurationLocked = Boolean(publisher?.enabled);
  const payloadTemplateRef = useRef<HTMLTextAreaElement>(null);
  const stepHeadingRef = useRef<HTMLHeadingElement>(null);
  const previousStepRef = useRef(step);
  const preview = useMemo(() => {
    try {
      return { result: previewPayloadTemplate(draft.mqtt.publish.payload_template, draft.sources, fixture), error: "" };
    } catch (error) {
      return { result: null, error: error instanceof Error ? error.message : "Payload preview failed" };
    }
  }, [draft.mqtt.publish.payload_template, draft.sources, fixture]);
  const configJSON = useMemo(() => JSON.stringify(exportMQTTPublisherConfig(draft), null, 2), [draft]);

  useEffect(() => {
    if (previousStepRef.current !== step) {
      stepHeadingRef.current?.focus();
      previousStepRef.current = step;
    }
  }, [step]);

  useEffect(() => {
    const controller = new AbortController();
    const timer = window.setTimeout(() => {
      void listPublisherSources({
        ...(catalogKind ? { kind: catalogKind } : {}),
        enabled: true,
        ...(catalogSearch.trim() ? { search: catalogSearch.trim() } : {}),
      }, controller.signal).then((entries) => {
        setCatalog(entries);
        setCatalogState("ready");
      }).catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setCatalogState("error");
          setMessage(errorMessage(error, "Unable to load the Publisher source catalog"));
        }
      });
    }, 150);
    return () => { controller.abort(); window.clearTimeout(timer); };
  }, [catalogKind, catalogSearch]);

  useEffect(() => {
    const controller = new AbortController();
    void listCredentials({ type: "mqtt" }, controller.signal).then((profiles) => {
      if (!controller.signal.aborted) setCredentials(profiles);
    }).catch((error: unknown) => {
      if (!controller.signal.aborted) setMessage(errorMessage(error, "Unable to load Credential Profiles"));
    });
    return () => controller.abort();
  }, []);

  useEffect(() => {
    if (!routePublisherID) return;
    const controller = new AbortController();
    void Promise.all([
      getDataPublisher(routePublisherID, controller.signal),
      listPublisherSources(undefined, controller.signal),
    ]).then(([entity, entries]) => {
      if (controller.signal.aborted) return;
      if (entity.type !== "mqtt") throw new Error("This Data Publisher is not an MQTT Publisher");
      setPublisher(entity);
      setDraft(importMQTTPublisherDraft(entity, entries));
      setDetailState("ready");
      setMessage("");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setDetailState("error");
      setMessage(errorMessage(error, "Unable to load the MQTT Publisher"));
    });
    return () => controller.abort();
  }, [routePublisherID]);

  function updateMQTT<Key extends keyof MQTTPublisherDraft["mqtt"]>(key: Key, value: MQTTPublisherDraft["mqtt"][Key]) {
    setDraft((current) => ({ ...current, mqtt: { ...current.mqtt, [key]: value } }));
  }

  function validateStep(): string | null {
    if (step === 0) {
      if (!draft.name.trim()) return "Publisher name is required";
      const brokerError = validateBrokerURL(buildBrokerURL(draft.mqtt.broker_host, draft.mqtt.broker_port, secure));
      if (brokerError) return brokerError;
      if (!secure && !draft.mqtt.plaintext_acknowledged) return "Acknowledge the plain MQTT exposure before continuing";
      if (draft.credential_slots.includes("mqtt.password") && !draft.credential_slots.includes("mqtt.username")) return "The selected Credential Profile has a password but no username";
    }
    if (step === 1) {
      const aliasErrors = validateSourceAliases(draft.sources);
      if (draft.sources.length === 0) return "Add at least one source alias";
      if (aliasErrors.length > 0) return aliasErrors[0];
      if (!selectedPublisherSources(draft.sources)) return "Replace draft schemas with sources selected from the Core catalog";
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
    setServerValidation(null);
    setDraft((current) => {
      const previous = current.sources.find((source) => source.id === id);
      const nextSources = updateAt(current.sources, id, update);
      const nextAlias = update.alias;
      return {
        ...current,
        sources: nextSources,
        trigger: nextAlias && previous?.alias === current.trigger.source_alias ? { ...current.trigger, source_alias: nextAlias } : current.trigger,
      };
    });
  }

  function addCatalogSource(entry: PublisherSourceCatalogEntry) {
    setDraft((current) => {
      if (current.sources.some((source) => source.reference && publisherSourceReferenceKey(source.reference) === publisherSourceReferenceKey(entry.descriptor.reference))) return current;
      const source = publisherSourceDraft(entry, current.sources);
      const sources = [...current.sources, source];
      return {
        ...current,
        sources,
        trigger: current.trigger.source_alias ? current.trigger : { ...current.trigger, source_alias: source.alias },
      };
    });
    setServerValidation(null);
  }

  function removeSource(id: string) {
    setServerValidation(null);
    setDraft((current) => {
      const removed = current.sources.find((source) => source.id === id);
      const sources = current.sources.filter((source) => source.id !== id);
      return {
        ...current,
        sources,
        trigger: removed?.alias === current.trigger.source_alias ? { ...current.trigger, source_alias: sources[0]?.alias ?? "" } : current.trigger,
      };
    });
  }

  function insertPayloadSyntax(syntax: string) {
    const editor = payloadTemplateRef.current;
    const template = draft.mqtt.publish.payload_template;
    const start = editor?.selectionStart ?? template.length;
    const end = editor?.selectionEnd ?? start;
    const next = `${template.slice(0, start)}${syntax}${template.slice(end)}`;
    updateMQTT("publish", { ...draft.mqtt.publish, payload_template: next });
    setServerValidation(null);
    setMessage("");
    setNotice(`Inserted ${syntax}`);
    window.requestAnimationFrame(() => {
      const cursor = start + syntax.length;
      payloadTemplateRef.current?.focus();
      payloadTemplateRef.current?.setSelectionRange(cursor, cursor);
    });
  }

  function saveLocalDraft() {
    localStorage.setItem("iot-edge.mqtt-publisher-draft.v1", JSON.stringify(draft));
    setMessage("");
    setNotice("Draft saved in this browser. Secret values are not stored here.");
  }

  async function validateOnServer(): Promise<PublisherPayloadValidation | null> {
    const sources = selectedPublisherSources(draft.sources);
    if (!sources) {
      setMessage("Replace draft schemas with sources selected from the Core catalog");
      return null;
    }
    try {
      const result = await validatePublisherPayload(draft.mqtt.publish.payload_template, sources);
      setServerValidation(result);
      setMessage("");
      setNotice(`Core validation passed: ${result.helper_calls} helpers, ${result.referenced_aliases.length} aliases.`);
      return result;
    } catch (error) {
      setServerValidation(null);
      setMessage(errorMessage(error, "Core rejected the MQTT payload template"));
      return null;
    }
  }

  async function submit(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (configurationLocked) {
      setMessage("Disable the MQTT Publisher before changing its configuration");
      return;
    }
    const errors = validateMQTTPublisherDraft(draft);
    if (errors.length > 0) {
      setMessage(errors[0]);
      return;
    }
    const sources = selectedPublisherSources(draft.sources);
    if (!sources) {
      setMessage("Replace draft schemas with sources selected from the Core catalog");
      return;
    }
    setSaving(true);
    try {
      if (!await validateOnServer()) return;
      const request = {
        name: draft.name.trim(),
        description: null,
        credential_id: draft.credential_id || null,
        config: exportMQTTPublisherConfig(draft),
        sources,
      };
      const saved = publisher
        ? await updateDataPublisher(publisher.id, request)
        : await createDataPublisher({ type: "mqtt", enabled: false, ...request });
      setPublisher(saved);
      localStorage.setItem("iot-edge.mqtt-publisher-draft.v1", JSON.stringify(draft));
      setMessage("");
      setNotice(publisher ? "Disabled MQTT Publisher configuration updated." : "Disabled MQTT Publisher saved. Test the connection, then enable it.");
      if (!publisher) navigate(`/data-publishers/${encodeURIComponent(saved.id)}/mqtt`, { replace: true });
    } catch (error) {
      setMessage(errorMessage(error, "Unable to save the MQTT Publisher"));
    } finally {
      setSaving(false);
    }
  }

  async function copyConfig() {
    try {
      await navigator.clipboard.writeText(configJSON);
      setNotice("MQTT configuration copied. Secret values are never included.");
      setMessage("");
    } catch {
      setMessage("Clipboard access is unavailable in this browser");
    }
  }

  function selectCredential(id: string) {
    const selected = credentials.find((profile) => profile.id === id);
    setDraft((current) => ({
      ...current,
      credential_id: id,
      credential_slots: selected?.secrets.map((secret) => secret.slot) ?? [],
    }));
  }

  function setTLS(useTLS: boolean) {
    setDraft((current) => {
      const previousDefault = current.mqtt.use_tls ? 8883 : 1883;
      return {
        ...current,
        mqtt: {
          ...current.mqtt,
          use_tls: useTLS,
          broker_port: current.mqtt.broker_port === previousDefault ? (useTLS ? 8883 : 1883) : current.mqtt.broker_port,
          plaintext_acknowledged: useTLS ? false : current.mqtt.plaintext_acknowledged,
        },
      };
    });
  }

  return (
    <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Data Publishers <span>/</span> <strong>MQTT</strong></>}>
      <div className="mqtt-wizard-content">
        <header className="mqtt-heading">
          <Link to="/data-publishers"><ArrowLeft size={17} /> Data Publishers</Link>
          <div className="mqtt-heading-row">
            <div>
              <p>Core Data Publisher</p>
              <h1>{publisher ? publisher.name : "MQTT Publisher"}</h1>
              <span>Build verified MQTT/TLS telemetry payloads from Core Tags and typed Plugin outputs—without creating extra acquisition reads.</span>
            </div>
            <div className="mqtt-contract-badge"><ShieldCheck size={18} /><span><strong>MQTT runtime ready</strong><small>{publisher ? `${publisher.enabled ? "Enabled" : "Disabled"} · Config v${publisher.config_version}` : "Save disabled before connecting"}</small></span></div>
          </div>
        </header>

        <div className="mqtt-api-note" role="note">
          <Info size={18} />
          <span><strong>Secure MQTT workflow.</strong> Save while disabled, select an optional Credential Profile, run a non-publishing connection test, then enable delivery. Runtime diagnostics never assume a fixed broker payload schema.</span>
        </div>

        {configurationLocked && <div className="mqtt-lock-note" role="note"><LockKeyhole size={17} /><span><strong>Configuration locked while enabled.</strong> Disable this Publisher from the runtime panel before editing broker, sources, payload, or diagnostic subscriptions.</span></div>}
        {detailState === "loading" && <div className="mqtt-loading" role="status"><RefreshCw className="is-spinning" size={18} />Loading MQTT Publisher…</div>}

        <ol className="mqtt-steps" aria-label="MQTT setup progress">
          {steps.map((label, index) => <li key={label} aria-current={index === step ? "step" : undefined} className={index === step ? "is-current" : index < step ? "is-complete" : ""}><span>{index < step ? <Check aria-hidden="true" size={15} /> : index + 1}</span><strong>{label}</strong></li>)}
        </ol>

        <form className="mqtt-wizard-form" onSubmit={submit}>
          <fieldset className="mqtt-config-fieldset" disabled={configurationLocked || detailState !== "ready"}>
          {step === 0 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Radio /><div><h2 ref={stepHeadingRef} tabIndex={-1}>Broker connection</h2><p>Host, port, and TLS are configured independently. Authentication is optional and comes from a reusable Core Credential Profile.</p></div></div>
            <div className="mqtt-form-grid">
              <label><span>Publisher name</span><input autoFocus value={draft.name} onChange={(event) => setDraft((current) => ({ ...current, name: event.target.value }))} /></label>
              <label><span>Broker host</span><input aria-describedby="mqtt-broker-help" spellCheck={false} value={draft.mqtt.broker_host} onChange={(event) => updateMQTT("broker_host", event.target.value)} /><small id="mqtt-broker-help">Example: mqtt.example.com</small></label>
              <label><span>Broker port</span><input type="number" min="1" max="65535" value={draft.mqtt.broker_port} onChange={(event) => updateMQTT("broker_port", Number(event.target.value))} /><small>Common defaults: 8883 with TLS, 1883 without TLS.</small></label>
              <label><span>Client ID <i>optional</i></span><input spellCheck={false} value={draft.mqtt.client_id} onChange={(event) => updateMQTT("client_id", event.target.value)} /><small>Leave blank to let Core derive a stable Publisher client ID.</small></label>
              <label><span>Transport security</span><select aria-label="Transport security" value={secure ? "tls" : "plain"} onChange={(event) => setTLS(event.target.value === "tls")}><option value="tls">TLS with server verification</option><option value="plain">Plain MQTT</option></select><small>{secure ? <><LockKeyhole size={13} /> Server certificate verification is always enabled.</> : <><Unplug size={13} /> Traffic is not encrypted.</>}</small></label>
            </div>
            {!secure && <label className="mqtt-warning-check"><input type="checkbox" checked={draft.mqtt.plaintext_acknowledged} onChange={(event) => updateMQTT("plaintext_acknowledged", event.target.checked)} /><span><strong>I understand this connection is not encrypted</strong><small>Use only on a trusted isolated network. Authentication values can be exposed in transit.</small></span></label>}

            <div className="mqtt-subsection"><div className="mqtt-subsection-heading"><FileKey2 size={18} /><div><h3>Credential Profile <i>optional</i></h3><p>Select a reusable encrypted profile, or choose No credentials for brokers that do not require authentication.</p></div></div><div className="mqtt-form-grid"><label><span>Credential Profile</span><select value={draft.credential_id} onChange={(event) => selectCredential(event.target.value)}><option value="">No credentials</option>{credentials.map((profile) => <option key={profile.id} value={profile.id}>{profile.name}</option>)}</select><small>{draft.credential_id ? `${draft.credential_slots.length} encrypted slot(s) configured` : "No username, password, custom CA, or client certificate will be used."}</small></label><label><span>Manage credentials</span><Link className="mqtt-inline-link" to="/credentials/new">Create Credential Profile</Link><small>Secret values are entered only on the protected Credentials page.</small></label></div></div>

            {secure && <div className="mqtt-subsection"><div className="mqtt-subsection-heading"><ShieldCheck size={18} /><div><h3>TLS verification and optional mTLS</h3><p>System roots and broker hostname verification are always active. Custom CA and client identity are loaded from the selected Credential Profile.</p></div></div><div className="mqtt-form-grid"><label><span>Server name override <i>optional</i></span><input spellCheck={false} value={draft.mqtt.tls.server_name} onChange={(event) => updateMQTT("tls", { ...draft.mqtt.tls, server_name: event.target.value })} /><small>Usually blank; Core derives it from the broker hostname.</small></label></div></div>}
          </section>}

          {step === 1 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Radio /><div><h2 ref={stepHeadingRef} tabIndex={-1}>Payload source aliases</h2><p>Select immutable Core Tags or typed Plugin outputs. Every asynchronous source keeps its own observation or period provenance.</p></div></div>
            <PublisherSourceSelector catalog={catalog} catalogState={catalogState} search={catalogSearch} kind={catalogKind} sources={draft.sources} onSearchChange={(value) => { setCatalogState("loading"); setCatalogSearch(value); }} onKindChange={(value) => { setCatalogState("loading"); setCatalogKind(value); }} onAdd={addCatalogSource} onUpdate={updateSource} onRemove={removeSource} />
            <PublisherTriggerSetup trigger={draft.trigger} sources={draft.sources} onChange={(trigger) => setDraft((current) => ({ ...current, trigger }))} />
          </section>}

          {step === 2 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Braces /><div><h2 ref={stepHeadingRef} tabIndex={-1}>Custom JSON payload</h2><p>Write the complete payload template yourself. The syntax palette only inserts helpers at the editor cursor and never replaces your JSON.</p></div></div>
            <div className="mqtt-publish-grid"><label><span>Publish topic</span><input spellCheck={false} value={draft.mqtt.publish.topic} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, topic: event.target.value })} /></label><label><span>QoS</span><select value={draft.mqtt.publish.qos} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, qos: Number(event.target.value) as 0 | 1 })}><option value={0}>0 · At most once</option><option value={1}>1 · At least once</option></select></label><label className="mqtt-inline-check"><input type="checkbox" checked={draft.mqtt.publish.retain} onChange={(event) => updateMQTT("publish", { ...draft.mqtt.publish, retain: event.target.checked })} /><span><strong>Retain latest payload</strong><small>Broker stores the last message for new subscribers.</small></span></label></div>

            <PublisherTemplateEditor editorRef={payloadTemplateRef} editorID="mqtt-payload-template" editorLabel="Advanced payload template" template={draft.mqtt.publish.payload_template} sources={draft.sources} fixture={fixture} preview={preview} validation={serverValidation} onTemplateChange={(payloadTemplate) => { updateMQTT("publish", { ...draft.mqtt.publish, payload_template: payloadTemplate }); setServerValidation(null); }} onFixtureChange={setFixture} onInsert={insertPayloadSyntax} onValidate={() => void validateOnServer()} />
          </section>}

          {step === 3 && <section className="mqtt-panel">
            <div className="mqtt-panel-heading"><Waypoints /><div><h2 ref={stepHeadingRef} tabIndex={-1}>Diagnostics and review</h2><p>Subscribe to arbitrary broker response topics. Messages remain generic JSON, text, or base64 binary—no ACK/Error field schema is assumed.</p></div></div>
            <div className="mqtt-diagnostic-list">{draft.mqtt.diagnostics.map((diagnostic) => <div className="mqtt-diagnostic-row" key={diagnostic.id}><label><span>Label</span><input aria-label={`Diagnostic label ${diagnostic.id}`} spellCheck={false} value={diagnostic.label} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { label: event.target.value }))} /></label><label><span>Topic filter</span><input aria-label={`Topic filter for ${diagnostic.label}`} spellCheck={false} value={diagnostic.topic_filter} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { topic_filter: event.target.value }))} /></label><label><span>QoS</span><select aria-label={`QoS for ${diagnostic.label}`} value={diagnostic.qos} onChange={(event) => updateMQTT("diagnostics", updateAt(draft.mqtt.diagnostics, diagnostic.id, { qos: Number(event.target.value) as 0 | 1 }))}><option value={0}>0</option><option value={1}>1</option></select></label><button type="button" aria-label={`Remove diagnostic ${diagnostic.label}`} onClick={() => updateMQTT("diagnostics", draft.mqtt.diagnostics.filter((candidate) => candidate.id !== diagnostic.id))}><Trash2 size={17} /></button></div>)}</div><button className="mqtt-add-button" type="button" disabled={draft.mqtt.diagnostics.length >= 16} onClick={() => updateMQTT("diagnostics", [...draft.mqtt.diagnostics, { id: crypto.randomUUID(), label: `diagnostic_${draft.mqtt.diagnostics.length + 1}`, topic_filter: "site/edge/#", qos: 1 }])}><Plus size={17} /> Add diagnostic subscription</button>

            <details className="mqtt-advanced"><summary>Transport limits and reconnect policy</summary><div className="mqtt-form-grid"><label><span>Keep alive (ms)</span><input type="number" min="10000" max="3600000" value={draft.mqtt.keep_alive_ms} onChange={(event) => updateMQTT("keep_alive_ms", Number(event.target.value))} /></label><label><span>Connect timeout (ms)</span><input type="number" min="1000" max="120000" value={draft.mqtt.connect_timeout_ms} onChange={(event) => updateMQTT("connect_timeout_ms", Number(event.target.value))} /></label><label><span>Publish timeout (ms)</span><input type="number" min="1000" max="120000" value={draft.mqtt.publish_timeout_ms} onChange={(event) => updateMQTT("publish_timeout_ms", Number(event.target.value))} /></label><label><span>Reconnect minimum (ms)</span><input type="number" min="100" max="60000" value={draft.mqtt.reconnect_min_ms} onChange={(event) => updateMQTT("reconnect_min_ms", Number(event.target.value))} /></label><label><span>Reconnect maximum (ms)</span><input type="number" min={draft.mqtt.reconnect_min_ms} max="300000" value={draft.mqtt.reconnect_max_ms} onChange={(event) => updateMQTT("reconnect_max_ms", Number(event.target.value))} /></label><label><span>Offline queue capacity</span><input type="number" min="1" max="10000" value={draft.mqtt.queue_capacity} onChange={(event) => updateMQTT("queue_capacity", Number(event.target.value))} /></label><label><span>Diagnostic history</span><input type="number" min="1" max="1000" value={draft.mqtt.diagnostic_history_depth} onChange={(event) => updateMQTT("diagnostic_history_depth", Number(event.target.value))} /></label></div></details>

            <div className="mqtt-review"><header><div><h3>Server configuration preview</h3><p>Contains reference names only. No username, password, certificate, or private key material is serialized.</p></div><button type="button" onClick={() => void copyConfig()}><Copy size={16} /> Copy config</button></header><pre>{configJSON}</pre></div>
          </section>}
          </fieldset>

          {step === 3 && publisher && <MQTTPublisherOperations publisher={publisher} diagnosticHistoryDepth={draft.mqtt.diagnostic_history_depth} onPublisherChange={(next) => { setPublisher(next); setDraft((current) => ({ ...current, enabled: next.enabled })); }} />}

          {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
          {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
          <footer className="mqtt-actions"><button type="button" disabled={step === 0} onClick={() => { setMessage(""); setNotice(""); setStep((current) => Math.max(0, current - 1)); }}><ArrowLeft size={17} /> Back</button><div>{step < steps.length - 1 ? <button className="is-primary" type="button" onClick={continueWizard}>Continue <ArrowRight size={17} /></button> : <><button type="button" onClick={saveLocalDraft}>Save local draft</button><button className="is-primary" type="submit" disabled={saving || configurationLocked || detailState !== "ready"}><Check size={17} /> {saving ? "Saving…" : publisher ? "Update configuration" : "Save disabled Publisher"}</button></>}</div></footer>
        </form>
      </div>
    </VGatewayShell>
  );
}

export default MQTTPublisherWizardPage;
