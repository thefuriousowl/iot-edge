import axios from "axios";
import { ArrowLeft, CircleAlert, Clock3, DatabaseZap, LoaderCircle, RadioTower, RefreshCw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Link, useParams } from "react-router-dom";

import { getTag, getTagValues, monitorTagValues } from "../../../services/tag.service";
import type { Tag, TagRuntimeValue } from "../../../types/tag";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "./TagDetailPage.css";

type LoadState = "loading" | "ready" | "error";
type StreamState = "connecting" | "live" | "disconnected";

const dateFormatter = new Intl.DateTimeFormat(undefined, { dateStyle: "medium", timeStyle: "medium" });

function formatValue(value: boolean | number | null): string {
  if (value === null) return "—";
  if (typeof value === "boolean") return String(value);
  return new Intl.NumberFormat(undefined, { maximumFractionDigits: 8, useGrouping: false }).format(value);
}

function formatTime(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : dateFormatter.format(date);
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { error?: { message?: string } } | undefined;
    if (data?.error?.message) return data.error.message;
  }
  return "Unable to load this Tag monitor.";
}

function mergeHistory(current: TagRuntimeValue[], incoming: TagRuntimeValue[]): TagRuntimeValue[] {
  const bySequence = new Map(current.map((value) => [value.sequence, value]));
  incoming.forEach((value) => bySequence.set(value.sequence, value));
  return [...bySequence.values()].sort((left, right) => left.sequence - right.sequence).slice(-10);
}

function TagConfiguration({ entity }: { entity: Tag }) {
  if (entity.type === "reading") {
    const rawType = entity.config.decoder.data_type ?? entity.data_type;
    return <dl>
      <div><dt>Datasource</dt><dd><code>{entity.datasource_id}</code></dd></div>
      <div><dt>Raw → final type</dt><dd>{rawType} → {entity.data_type}</dd></div>
      <div><dt>Decoder</dt><dd>{entity.config.decoder.type}</dd></div>
      <div><dt>Byte offset / order</dt><dd>{entity.config.decoder.config.byte_offset} / {entity.config.decoder.config.byte_order}</dd></div>
      <div><dt>Engineering scaling</dt><dd>{entity.config.transform ? `× ${entity.config.transform.config.gain} + ${entity.config.transform.config.offset}` : "None"}</dd></div>
    </dl>;
  }
  if (entity.type === "constant") {
    return <dl><div><dt>Configured value</dt><dd>{String(entity.config.value)}</dd></div><div><dt>Data type</dt><dd>{entity.data_type}</dd></div></dl>;
  }
  return <dl>
    <div><dt>Expression</dt><dd><code>{entity.config.expression}</code></dd></div>
    <div><dt>Trigger Tag</dt><dd>{entity.config.trigger ? <code>{entity.config.trigger.tag_id}</code> : "Not configured (legacy Tag)"}</dd></div>
    <div><dt>Trigger mode</dt><dd>{entity.config.trigger?.mode ?? (entity.config.trigger ? "on_sample" : "—")}</dd></div>
    <div><dt>Data type</dt><dd>{entity.data_type}</dd></div>
  </dl>;
}

function TagDetailPage() {
  const { id = "" } = useParams();
  const [entity, setEntity] = useState<Tag | null>(null);
  const [history, setHistory] = useState<TagRuntimeValue[]>([]);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState("");
  const [loadVersion, setLoadVersion] = useState(0);
  const [streamVersion, setStreamVersion] = useState(0);
  const [streamState, setStreamState] = useState<StreamState>("connecting");

  useEffect(() => {
    const controller = new AbortController();
    Promise.all([getTag(id, controller.signal), getTagValues(id, 10, controller.signal)])
      .then(([tag, values]) => {
        if (controller.signal.aborted) return;
        setEntity(tag);
        const loadedHistory = Array.isArray(values.history) ? values.history : [];
        setHistory((current) => mergeHistory(current, [...loadedHistory, ...(values.latest ? [values.latest] : [])]));
        setLoadState("ready");
      })
      .catch((error: unknown) => {
        if (!controller.signal.aborted) {
          setLoadError(errorMessage(error));
          setLoadState("error");
        }
      });
    return () => controller.abort();
  }, [id, loadVersion]);

  useEffect(() => {
    const controller = new AbortController();
    monitorTagValues(
      id,
      (value) => {
        setHistory((current) => mergeHistory(current, [value]));
        setStreamState("live");
      },
      controller.signal,
      () => setStreamState("live"),
    ).then(() => {
      if (!controller.signal.aborted) setStreamState("disconnected");
    }).catch(() => {
      if (!controller.signal.aborted) setStreamState("disconnected");
    });
    return () => controller.abort();
  }, [id, streamVersion]);

  const latest = history.at(-1) ?? null;
  const newestFirst = useMemo(() => [...history].reverse(), [history]);

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> Tags <span>/</span> <strong>{entity?.name ?? "Monitor"}</strong></>}>
    <div className="tag-detail-content">
      <header className="tag-detail-heading">
        <div><Link to="/tags"><ArrowLeft size={17} /> Back to Tags</Link><h1>{entity?.name ?? "Tag monitor"}</h1><p>{entity?.description ?? "Configuration and real-time runtime values."}</p></div>
        <span className={`tag-stream-state is-${streamState}`}><i />{streamState}</span>
      </header>

      {loadState === "loading" && <section className="tag-detail-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Tag monitor…</strong></section>}
      {loadState === "error" && <section className="tag-detail-state is-error" role="alert"><CircleAlert /><strong>{loadError}</strong><button type="button" onClick={() => { setLoadState("loading"); setLoadError(""); setLoadVersion((value) => value + 1); }}>Try again</button></section>}

      {loadState === "ready" && entity && <>
        <section className="tag-detail-live" aria-label="Current Tag value">
          <div><RadioTower /><span>Current runtime value</span><strong>{latest ? formatValue(latest.value) : "Waiting for sample"}</strong><small>{latest ? `${latest.data_type} · sequence ${latest.sequence}` : entity.enabled ? "The runtime has not published a value yet." : "This Tag is disabled."}</small></div>
          <div><span>Quality</span><strong className={`is-${latest?.quality ?? "waiting"}`}>{latest?.quality ?? (entity.enabled ? "waiting" : "paused")}</strong><small>{latest?.error ?? "No active error"}</small></div>
          <div><Clock3 /><span>Observed at</span><strong>{latest ? formatTime(latest.observed_at) : "—"}</strong><small>{latest ? `Stored ${formatTime(latest.stored_at)}` : "Source observation time"}</small></div>
        </section>

        <div className="tag-detail-grid">
          <section className="tag-detail-card"><header><div><DatabaseZap /><h2>Configured definition</h2></div><span>{entity.type}</span></header><TagConfiguration entity={entity} /></section>
          <section className="tag-detail-card is-history"><header><div><Clock3 /><h2>Latest 10 runtime values</h2></div><span>Memory only</span></header>
            {newestFirst.length === 0 ? <p className="tag-history-empty">No runtime samples yet.</p> : <div className="tag-history-scroll"><table><thead><tr><th>Value</th><th>Quality</th><th>Observed</th><th>Seq.</th></tr></thead><tbody>{newestFirst.map((value) => <tr key={value.sequence}><td>{formatValue(value.value)}</td><td><span className={`tag-history-quality is-${value.quality}`}>{value.quality}</span>{value.error && <small>{value.error}</small>}</td><td>{formatTime(value.observed_at)}</td><td>{value.sequence}</td></tr>)}</tbody></table></div>}
          </section>
        </div>

        <footer className="tag-detail-note"><DatabaseZap /><span>The latest value survives server restarts. This 10-sample memory window restarts from that persisted value; persistent history remains owned by Data Logger.</span>{streamState === "disconnected" && <button type="button" onClick={() => { setStreamState("connecting"); setStreamVersion((value) => value + 1); }}><RefreshCw size={15} /> Reconnect</button>}</footer>
      </>}
    </div>
  </VGatewayShell>;
}

export default TagDetailPage;
