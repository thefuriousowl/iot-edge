import {
  Activity,
  Check,
  CircleAlert,
  CircleDashed,
  LoaderCircle,
  Play,
  Radio,
  RefreshCw,
  RotateCcw,
  Send,
  Waypoints,
} from "lucide-react";
import axios from "axios";
import { useCallback, useEffect, useState } from "react";

import {
  disableDataPublisher,
  enableDataPublisher,
  getDataPublisherStatus,
  listPublisherDiagnostics,
  restartDataPublisher,
  testMQTTConnection,
} from "../../../services/publisher.service";
import type {
  DataPublisher,
  MQTTDiagnosticEvent,
  PublisherRuntimeStatus,
} from "../../../types/publisher";
import PublisherRuntimeSources from "./PublisherRuntimeSources";

interface MQTTPublisherOperationsProps {
  publisher: DataPublisher;
  diagnosticHistoryDepth: number;
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

function MQTTPublisherOperations({ publisher, diagnosticHistoryDepth, onPublisherChange }: MQTTPublisherOperationsProps) {
  const [runtime, setRuntime] = useState<PublisherRuntimeStatus>(publisher.runtime);
  const [diagnostics, setDiagnostics] = useState<MQTTDiagnosticEvent[]>([]);
  const [busy, setBusy] = useState("");
  const [message, setMessage] = useState("");
  const [notice, setNotice] = useState("");
  const connected = runtime.state === "running" && runtime.connected;

  const refreshRuntime = useCallback(async (signal?: AbortSignal) => {
    const [nextRuntime, nextDiagnostics] = await Promise.all([
      getDataPublisherStatus(publisher.id, signal),
      listPublisherDiagnostics(publisher.id, signal),
    ]);
    setRuntime(nextRuntime);
    setDiagnostics(Array.isArray(nextDiagnostics) ? nextDiagnostics : []);
  }, [publisher.id]);

  useEffect(() => {
    const controller = new AbortController();
    const initialTimer = window.setTimeout(() => {
      void refreshRuntime(controller.signal).catch((error: unknown) => {
        if (!controller.signal.aborted) setMessage(errorMessage(error, "Unable to load MQTT runtime state"));
      });
    }, 0);
    const timer = window.setInterval(() => {
      void refreshRuntime(controller.signal).catch(() => undefined);
    }, 2_000);
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
        setNotice("MQTT Publisher restart requested.");
      } else {
        const next = action === "enable"
          ? await enableDataPublisher(publisher.id)
          : await disableDataPublisher(publisher.id);
        onPublisherChange(next);
        setRuntime(next.runtime);
        setNotice(action === "enable" ? "MQTT Publisher enabled." : "MQTT Publisher disabled. Configuration is unlocked.");
      }
      await refreshRuntime();
    } catch (error) {
      setMessage(errorMessage(error, `Unable to ${action} MQTT Publisher`));
    } finally {
      setBusy("");
    }
  }

  async function testConnection() {
    setBusy("test");
    setMessage("");
    setNotice("");
    try {
      const result = await testMQTTConnection(publisher.id);
      setNotice(`Connection succeeded in ${result.latency_ms} ms at ${displayTime(result.connected_at)}. No telemetry was published.`);
    } catch (error) {
      setMessage(errorMessage(error, "MQTT connection test failed"));
    } finally {
      setBusy("");
    }
  }

  return <section className="mqtt-operations" aria-label="MQTT Publisher operations">
    <div className="mqtt-lifecycle"><div><strong>Desired state: {publisher.enabled ? "Enabled" : "Disabled"}</strong><span>Runtime: {runtime.state} · Config v{runtime.config_version}</span></div><div><button type="button" disabled={publisher.enabled || Boolean(busy)} onClick={() => void testConnection()}>{busy === "test" ? <LoaderCircle className="is-spinning" size={16} /> : <Radio size={16} />}Test connection</button>{publisher.enabled ? <button type="button" disabled={Boolean(busy)} onClick={() => void runLifecycle("disable")}><CircleDashed size={16} />Disable</button> : <button className="is-primary" type="button" disabled={Boolean(busy)} onClick={() => void runLifecycle("enable")}><Play size={16} />Enable</button>}<button type="button" disabled={!publisher.enabled || Boolean(busy)} onClick={() => void runLifecycle("restart")}><RotateCcw size={16} />Restart</button></div></div>

    <div className="mqtt-runtime-grid" aria-label="MQTT runtime status"><article><CircleDashed /><span>Connection</span><strong>{connected ? "Connected" : "Disconnected"}</strong><small>{runtime.connection_count} connections · last {displayTime(runtime.last_connected_at)}</small></article><article><Send /><span>Delivery</span><strong>{runtime.delivery_count.toLocaleString()}</strong><small>{runtime.delivery_failure_count} failed · last {displayTime(runtime.last_delivered_at)}</small></article><article><RefreshCw /><span>Reconnects / drops</span><strong>{runtime.reconnect_count} / {runtime.transport_drop_count}</strong><small>Queue {runtime.transport_queue_depth} · requests {runtime.request_count}</small></article></div>
    {(runtime.last_error || runtime.transport_error) && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{runtime.last_error || runtime.transport_error}</div>}
    <PublisherRuntimeSources sources={runtime.sources} />

    <div className="mqtt-monitor"><header><div><h3>Generic diagnostic monitor</h3><p>Messages are shown without assuming an ACK/Error JSON schema.</p></div><span>{diagnostics.length} / {diagnosticHistoryDepth}</span></header>{diagnostics.length === 0 ? <div className="mqtt-monitor-empty"><Waypoints /><strong>No diagnostic messages received</strong><span>Enable the Publisher to subscribe to the configured diagnostic topics.</span></div> : <div className="mqtt-monitor-events">{diagnostics.map((event) => <article key={event.sequence}><header><div><strong>{event.label}</strong><span>{event.topic}</span></div><small>{displayTime(event.received_at)}</small></header><div><span>QoS {event.qos}</span><span>{event.format}</span>{event.retained && <span>retained</span>}{event.duplicate && <span>duplicate</span>}{event.truncated && <span>truncated</span>}</div><pre>{event.payload}</pre></article>)}</div>}</div>

    {message && <div className="mqtt-message is-error" role="alert"><CircleAlert size={18} />{message}</div>}
    {notice && <div className="mqtt-message" role="status"><Check size={18} />{notice}</div>}
    <button className="mqtt-refresh-runtime" type="button" disabled={Boolean(busy)} onClick={() => void refreshRuntime()}><Activity size={16} />Refresh runtime</button>
  </section>;
}

export default MQTTPublisherOperations;
