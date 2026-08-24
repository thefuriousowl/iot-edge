import axios from "axios";
import { Activity, Check, CircleAlert, CircleDashed, Copy, LoaderCircle, Play, RefreshCw, RotateCcw, Server } from "lucide-react";
import { useCallback, useEffect, useState } from "react";

import { disableDataPublisher, enableDataPublisher, getDataPublisherStatus, probeHTTPServerListener, restartDataPublisher } from "../../../services/publisher.service";
import type { DataPublisher, PublisherRuntimeStatus } from "../../../types/publisher";
import PublisherRuntimeSources from "./PublisherRuntimeSources";

interface HTTPServerPublisherOperationsProps {
  publisher: DataPublisher;
  endpoint: string;
  onPublisherChange: (publisher: DataPublisher) => void;
}

function displayTime(value?: string): string {
  if (!value) return "Never";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString();
}

function errorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    return body?.error?.message ?? fallback;
  }
  return error instanceof Error ? error.message : fallback;
}

function HTTPServerPublisherOperations({ publisher, endpoint, onPublisherChange }: HTTPServerPublisherOperationsProps) {
  const [runtime, setRuntime] = useState<PublisherRuntimeStatus>(publisher.runtime);
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const activeConnections = runtime.state === "running" ? runtime.active_connections ?? 0 : 0;

  const refreshRuntime = useCallback(async (signal?: AbortSignal) => {
    setRuntime(await getDataPublisherStatus(publisher.id, signal));
  }, [publisher.id]);

  useEffect(() => {
    const controller = new AbortController();
    const initialTimer = window.setTimeout(() => void refreshRuntime(controller.signal).catch((error: unknown) => {
      if (!controller.signal.aborted) setMessage(errorMessage(error, "Unable to load HTTP Server runtime state"));
    }), 0);
    const timer = window.setInterval(() => void refreshRuntime(controller.signal).catch(() => undefined), 2_000);
    return () => {
      controller.abort();
      window.clearTimeout(initialTimer);
      window.clearInterval(timer);
    };
  }, [refreshRuntime]);

  async function runLifecycle(action: "enable" | "disable" | "restart") {
    setBusy(action);
    setMessage("");
    setNotice("");
    try {
      if (action === "restart") {
        const response = await restartDataPublisher(publisher.id);
        setRuntime(response.runtime);
        setNotice("HTTP Server restart requested.");
      } else {
        const next = action === "enable" ? await enableDataPublisher(publisher.id) : await disableDataPublisher(publisher.id);
        onPublisherChange(next);
        setRuntime(next.runtime);
        setNotice(action === "enable" ? "HTTP Server enabled." : "HTTP Server disabled. Configuration is unlocked.");
      }
      await refreshRuntime();
    } catch (error) {
      setMessage(errorMessage(error, `Unable to ${action} HTTP Server`));
    } finally {
      setBusy("");
    }
  }

  async function copyEndpoint() {
    try {
      await navigator.clipboard.writeText(endpoint);
      setNotice("Endpoint copied.");
    } catch {
      setMessage("Unable to copy the endpoint. Select and copy it manually.");
    }
  }

  async function probeListener() {
    setBusy("probe");
    setMessage("");
    setNotice("");
    try {
      const result = await probeHTTPServerListener(publisher.id);
      setNotice(`Listener reachable in ${result.latency_ms.toFixed(2)} ms. No HTTP request or acquisition read was sent.`);
      await refreshRuntime();
    } catch (error) {
      setMessage(errorMessage(error, "Unable to probe HTTP Server listener"));
    } finally {
      setBusy("");
    }
  }

  return <section className="mqtt-operations" aria-label="HTTP Server Publisher operations">
    <div className="http-endpoint-card"><div><Server size={18} /><span><small>External GET endpoint</small><code>{endpoint}</code></span></div><button type="button" onClick={() => void copyEndpoint()}><Copy size={15} />Copy</button></div>
    <div className="mqtt-lifecycle"><div><strong>Desired state: {publisher.enabled ? "Enabled" : "Disabled"}</strong><span>Runtime: {runtime.state} · Config v{runtime.config_version}</span></div><div>{publisher.enabled ? <button type="button" disabled={Boolean(busy)} onClick={() => void runLifecycle("disable")}><CircleDashed size={16} />Disable</button> : <button className="is-primary" type="button" disabled={Boolean(busy)} onClick={() => void runLifecycle("enable")}><Play size={16} />Enable</button>}<button type="button" disabled={!publisher.enabled || Boolean(busy)} onClick={() => void runLifecycle("restart")}><RotateCcw size={16} />Restart</button></div></div>
    <div className="mqtt-runtime-grid" aria-label="HTTP Server runtime status"><article><Activity /><span>Requests</span><strong>{(runtime.external_request_count ?? 0).toLocaleString()}</strong><small>Last {displayTime(runtime.last_external_request_at)}</small></article><article><CircleAlert /><span>Rejected</span><strong>{(runtime.rejected_request_count ?? 0).toLocaleString()}</strong><small>Strict quality and invalid requests</small></article><article><Server /><span>Connections</span><strong>{activeConnections.toLocaleString()}</strong><small>{runtime.publish_count} retained snapshot updates · last {displayTime(runtime.last_publish_at)}</small></article></div>
    {(runtime.last_error || runtime.transport_error) && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{runtime.last_error || runtime.transport_error}</div>}
    <PublisherRuntimeSources sources={runtime.sources} />
    <div className="mqtt-api-note"><CircleDashed size={18} /><span><strong>Local TCP listener probe.</strong> Confirms the configured socket accepts a connection without authentication, HTTP GET, payload rendering, or acquisition reads.</span></div>
    {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
    {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
    <div className="mqtt-operation-buttons"><button type="button" disabled={!publisher.enabled || runtime.state !== "running" || Boolean(busy)} onClick={() => void probeListener()}>{busy === "probe" ? <LoaderCircle className="is-spinning" size={16} /> : <Activity size={16} />}Probe listener</button><button className="mqtt-refresh-runtime" type="button" disabled={Boolean(busy)} onClick={() => void refreshRuntime()}>{busy && busy !== "probe" ? <LoaderCircle className="is-spinning" size={16} /> : <RefreshCw size={16} />}Refresh runtime</button></div>
  </section>;
}

export default HTTPServerPublisherOperations;
