import { useCallback, useEffect, useMemo, useState } from "react";
import axios from "axios";
import {
  AlertTriangle,
  CircleHelp,
  Info,
  LoaderCircle,
  Pencil,
  Play,
  RefreshCw,
  Unplug,
} from "lucide-react";
import { useNavigate, useParams } from "react-router-dom";

import {
  connectVGateway,
  disconnectVGateway,
  getVGateway,
  getVGatewayStatus,
} from "../../../services/vgateway.service";
import type {
  VGatewayConnectionStatus,
  VGatewayDetail,
  VGatewayHealthStatus,
  VGatewayStatusResponse,
} from "../../../types/vgateway";
import VGatewayShell from "../components/VGatewayShell";
import DeviceWorkspace from "../../device/components/DeviceWorkspace";
import "./VGatewayListPage.css";
import "./VGatewayDetailPage.css";

type DetailLoadState = "loading" | "ready" | "error";
type StatusLoadState = "loading" | "ready" | "error";

const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

function formatDate(value?: string | null, fallback = "Never"): string {
  if (!value) {
    return fallback;
  }

  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : dateFormatter.format(date);
}

function statusLabel(status: VGatewayConnectionStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function healthLabel(status: VGatewayHealthStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function getErrorMessage(error: unknown, fallback: string): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: { message?: string } }
      | undefined;

    if (data?.error?.message) {
      return data.error.message;
    }
  }

  return fallback;
}

function VGatewayDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [gateway, setGateway] = useState<VGatewayDetail | null>(null);
  const [runtime, setRuntime] = useState<VGatewayStatusResponse | null>(null);
  const [detailState, setDetailState] = useState<DetailLoadState>("loading");
  const [statusState, setStatusState] = useState<StatusLoadState>("loading");
  const [detailError, setDetailError] = useState<string | null>(null);
  const [statusError, setStatusError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [actionPending, setActionPending] = useState(false);
  const [detailVersion, setDetailVersion] = useState(0);

  const refreshStatus = useCallback(async () => {
    if (!id) {
      return;
    }

    setStatusState("loading");
    setStatusError(null);
    try {
      const response = await getVGatewayStatus(id);
      setRuntime(response);
      setStatusState("ready");
    } catch (error) {
      setStatusError(
        getErrorMessage(
          error,
          "Unable to refresh runtime status. Gateway details are still available.",
        ),
      );
      setStatusState("error");
    }
  }, [id]);

  useEffect(() => {
    const controller = new AbortController();

    async function loadDetail() {
      if (!id) {
        setDetailError("Invalid vGateway ID.");
        setDetailState("error");
        return;
      }

      setDetailState("loading");
      setStatusState("loading");
      setDetailError(null);
      setStatusError(null);
      setActionError(null);

      const [detailResult, statusResult] = await Promise.allSettled([
        getVGateway(id, controller.signal),
        getVGatewayStatus(id, controller.signal),
      ]);

      if (controller.signal.aborted) {
        return;
      }

      if (detailResult.status === "rejected") {
        setDetailError(
          getErrorMessage(
            detailResult.reason,
            "Unable to load this vGateway. Check the API connection and try again.",
          ),
        );
        setDetailState("error");
        return;
      }

      setGateway(detailResult.value);
      setDetailState("ready");

      if (statusResult.status === "fulfilled") {
        setRuntime(statusResult.value);
        setStatusState("ready");
      } else {
        setStatusError(
          getErrorMessage(
            statusResult.reason,
            "Unable to load runtime status. Gateway details are still available.",
          ),
        );
        setStatusState("error");
      }
    }

    void loadDetail();

    return () => controller.abort();
  }, [detailVersion, id]);

  const effectiveStatus = runtime?.status ?? gateway?.status ?? "disconnected";
  const statistics = useMemo(
    () =>
      runtime?.statistics ?? {
        request_count: gateway?.statistics.request_count ?? 0,
        error_count: gateway?.statistics.error_count ?? 0,
        bytes_received: 0,
        avg_latency_ms: gateway?.statistics.avg_latency_ms ?? null,
      },
    [gateway, runtime],
  );

  async function handleConnection() {
    if (!id || !gateway) {
      return;
    }

    setActionPending(true);
    setActionError(null);
    try {
      if (effectiveStatus === "connected") {
        await disconnectVGateway(id);
      } else {
        await connectVGateway(id);
      }
      await refreshStatus();
    } catch (error) {
      setActionError(
        getErrorMessage(error, "Unable to change the gateway connection state."),
      );
    } finally {
      setActionPending(false);
    }
  }

  const connectedAt = runtime?.connected_at ?? gateway?.statistics.connected_at;
  const health = runtime?.health ?? {
    status: "unknown" as const,
    last_check: null,
    latency_ms: null,
  };

  return (
    <VGatewayShell
      breadcrumb={
        <>
          vGateways <span>/</span> <strong>{gateway?.name ?? "Gateway details"}</strong>
        </>
      }
    >
      <div className="vgateway-detail-content">
        {detailState === "loading" && (
          <div className="vgateway-detail-state" role="status">
            <LoaderCircle aria-hidden="true" className="is-spinning" />
            <strong>Loading vGateway details…</strong>
          </div>
        )}

        {detailState === "error" && (
          <div className="vgateway-detail-state is-error" role="alert">
            <AlertTriangle aria-hidden="true" />
            <strong>Unable to load vGateway</strong>
            <p>{detailError}</p>
            <button type="button" onClick={() => setDetailVersion((value) => value + 1)}>
              Try again
            </button>
          </div>
        )}

        {detailState === "ready" && gateway && (
          <>
            <section className="vgateway-detail-hero">
              <div>
                <h1>{gateway.name}</h1>
                <p>{gateway.description || "No description provided."}</p>
                <div className={`vgateway-detail-status is-${effectiveStatus}`}>
                  <span aria-hidden="true" />
                  <strong>{statusLabel(effectiveStatus)}</strong>
                  <small>
                    {effectiveStatus === "connected"
                      ? `Connected since ${formatDate(connectedAt, "Unknown")}`
                      : gateway.enabled
                        ? "The gateway is not currently connected."
                        : "This gateway is disabled."}
                  </small>
                </div>
              </div>
              <div className="vgateway-detail-actions">
                <button type="button" onClick={() => navigate(`/vgateways/${gateway.id}/edit`)}>
                  <Pencil aria-hidden="true" size={18} />
                  Edit Gateway
                </button>
                <button
                  type="button"
                  className="is-primary"
                  disabled={!gateway.enabled || effectiveStatus === "connecting" || actionPending}
                  title={!gateway.enabled ? "Enable the gateway before connecting" : undefined}
                  onClick={() => void handleConnection()}
                >
                  {actionPending ? (
                    <LoaderCircle aria-hidden="true" className="is-spinning" size={18} />
                  ) : effectiveStatus === "connected" ? (
                    <Unplug aria-hidden="true" size={18} />
                  ) : (
                    <Play aria-hidden="true" size={18} />
                  )}
                  {actionPending
                    ? "Updating…"
                    : effectiveStatus === "connected"
                      ? "Disconnect"
                      : "Connect"}
                </button>
              </div>
            </section>

            {(actionError || statusError) && (
              <div className="vgateway-detail-alert" role="alert">
                <AlertTriangle aria-hidden="true" size={19} />
                <span>{actionError ?? statusError}</span>
                {statusError && (
                  <button type="button" onClick={() => void refreshStatus()}>
                    Retry
                  </button>
                )}
              </div>
            )}

            <section className="vgateway-runtime" aria-label="Runtime metrics">
              <div className="vgateway-runtime-heading">
                <h2>Runtime metrics</h2>
                <button
                  type="button"
                  disabled={statusState === "loading"}
                  onClick={() => void refreshStatus()}
                >
                  <RefreshCw
                    aria-hidden="true"
                    className={statusState === "loading" ? "is-spinning" : undefined}
                    size={18}
                  />
                  {statusState === "loading" ? "Refreshing…" : "Refresh"}
                </button>
              </div>
              <div className="vgateway-runtime-grid">
                <article><span>Requests</span><strong>{statistics.request_count.toLocaleString()}</strong></article>
                <article><span>Errors</span><strong>{statistics.error_count.toLocaleString()}</strong></article>
                <article><span>Average latency</span><strong>{statistics.avg_latency_ms === null ? "Not available" : `${statistics.avg_latency_ms.toFixed(2)} ms`}</strong></article>
                <article><span>Last activity</span><strong>{formatDate(runtime?.last_activity, "No activity yet")}</strong></article>
              </div>
            </section>

            <div className="vgateway-detail-columns">
              <section className="vgateway-detail-panel">
                <h2>Connection details</h2>
                <dl>
                  <div><dt>Type</dt><dd>Modbus TCP</dd></div>
                  <div><dt>Endpoint</dt><dd>{gateway.config.host}:{gateway.config.port}</dd></div>
                  <div><dt>Enabled</dt><dd>{gateway.enabled ? "Yes" : "No"}</dd></div>
                  <div><dt>Keep alive</dt><dd>{gateway.config.keep_alive ? "On" : "Off"}</dd></div>
                  <div><dt>Timeout</dt><dd>{gateway.config.timeout} ms</dd></div>
                  <div><dt>Retry policy</dt><dd>{gateway.config.retry_count} attempts / {gateway.config.retry_delay} ms delay</dd></div>
                  <div><dt>Reconnect interval</dt><dd>{gateway.config.reconnect_interval} s</dd></div>
                </dl>
              </section>

              <section className="vgateway-detail-panel vgateway-health-panel">
                <h2>Health</h2>
                <dl>
                  <div><dt>Status</dt><dd className={`is-${health.status}`}>{healthLabel(health.status)}</dd></div>
                  <div><dt>Last check</dt><dd>{formatDate(health.last_check)}</dd></div>
                  <div><dt>Probe latency</dt><dd>{health.latency_ms === null ? "Not available" : `${health.latency_ms.toFixed(2)} ms`}</dd></div>
                </dl>
                {health.status === "unknown" && (
                  <p><CircleHelp aria-hidden="true" size={19} /> Health probes are not available yet. This gateway will report health once probes are configured.</p>
                )}
              </section>
            </div>

            <DeviceWorkspace vgatewayId={gateway.id} />

            <footer className="vgateway-detail-metadata">
              <Info aria-hidden="true" size={18} />
              <dl>
                <div><dt>Gateway ID</dt><dd>{gateway.id}</dd></div>
                <div><dt>Created</dt><dd>{formatDate(gateway.created_at, "Unknown")}</dd></div>
                <div><dt>Updated</dt><dd>{formatDate(gateway.updated_at, "Unknown")}</dd></div>
              </dl>
            </footer>
          </>
        )}
      </div>
    </VGatewayShell>
  );
}

export default VGatewayDetailPage;
