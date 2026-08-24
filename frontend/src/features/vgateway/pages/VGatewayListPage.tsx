import { useCallback, useEffect, useMemo, useState } from "react";
import axios from "axios";
import {
  Activity,
  AlertTriangle,
  Boxes,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Cpu,
  FileChartColumn,
  Gauge,
  LayoutDashboard,
  LoaderCircle,
  Menu,
  Pencil,
  Play,
  PlugZap,
  Plus,
  Puzzle,
  RefreshCw,
  Search,
  Settings,
  Tags,
  Trash2,
  Unplug,
  X,
} from "lucide-react";
import { Link, NavLink, useNavigate } from "react-router-dom";

import {
  connectVGateway,
  deleteVGateway,
  disconnectVGateway,
  listVGateways,
} from "../../../services/vgateway.service";
import { useAuthStore } from "../../../stores/auth.store";
import InternetStatus from "../../system/components/InternetStatus";
import type {
  VGatewayConnectionStatus,
  VGatewayListItem,
  VGatewayPagination,
} from "../../../types/vgateway";
import "./VGatewayListPage.css";

type LoadState = "loading" | "ready" | "error";
type EnabledFilter = "all" | "enabled" | "disabled";

const emptyPagination: VGatewayPagination = {
  page: 1,
  per_page: 20,
  total: 0,
  total_pages: 0,
};

const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

function statusLabel(status: VGatewayConnectionStatus): string {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function formatActivity(value?: string | null): string {
  if (!value) {
    return "No activity yet";
  }

  const date = new Date(value);
  if (Number.isNaN(date.getTime())) {
    return "Unknown";
  }

  return dateFormatter.format(date);
}

function getErrorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as
      | { error?: { message?: string } }
      | undefined;

    if (data?.error?.message) {
      return data.error.message;
    }
  }

  return "Unable to load vGateways. Check the API connection and try again.";
}

function VGatewayListPage() {
  const navigate = useNavigate();
  const username = useAuthStore((state) => state.user?.username ?? "Admin");
  const [gateways, setGateways] = useState<VGatewayListItem[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [actionError, setActionError] = useState<string | null>(null);
  const [deletingID, setDeletingID] = useState<string | null>(null);
  const [connectingID, setConnectingID] = useState<string | null>(null);
  const [search, setSearch] = useState("");
  const [enabledFilter, setEnabledFilter] = useState<EnabledFilter>("all");
  const [page, setPage] = useState(1);
  const [refreshVersion, setRefreshVersion] = useState(0);
  const [navigationOpen, setNavigationOpen] = useState(false);

  const refresh = useCallback(() => {
    setRefreshVersion((current) => current + 1);
  }, []);

  useEffect(() => {
    const controller = new AbortController();

    async function loadGateways() {
      setLoadState("loading");
      setLoadError(null);

      try {
        const response = await listVGateways(
          {
            page,
            per_page: 20,
            enabled:
              enabledFilter === "all"
                ? undefined
                : enabledFilter === "enabled",
          },
          controller.signal,
        );

        if (!controller.signal.aborted) {
          setGateways(response.data);
          setPagination(response.pagination);
          setLoadState("ready");
        }
      } catch (error) {
        if (!controller.signal.aborted) {
          setLoadError(getErrorMessage(error));
          setLoadState("error");
        }
      }
    }

    void loadGateways();

    return () => {
      controller.abort();
    };
  }, [enabledFilter, page, refreshVersion]);

  const visibleGateways = useMemo(() => {
    const normalizedSearch = search.trim().toLocaleLowerCase();
    if (!normalizedSearch) {
      return gateways;
    }

    return gateways.filter((gateway) =>
      [gateway.name, gateway.description ?? "", gateway.type]
        .join(" ")
        .toLocaleLowerCase()
        .includes(normalizedSearch),
    );
  }, [gateways, search]);

  const connectedCount = gateways.filter(
    (gateway) => gateway.status === "connected",
  ).length;
  const attentionCount = gateways.filter(
    (gateway) => gateway.status === "error",
  ).length;

  async function handleDelete(gateway: VGatewayListItem) {
    if (!window.confirm(`Delete “${gateway.name}”? This cannot be undone.`)) {
      return;
    }

    setActionError(null);
    setDeletingID(gateway.id);
    try {
      await deleteVGateway(gateway.id);
      if (gateways.length === 1 && page > 1) {
        setPage((current) => current - 1);
      } else {
        refresh();
      }
    } catch (error) {
      setActionError(getErrorMessage(error));
    } finally {
      setDeletingID(null);
    }
  }

  async function handleConnection(gateway: VGatewayListItem) {
    setActionError(null);
    setConnectingID(gateway.id);
    try {
      if (gateway.status === "connected") {
        await disconnectVGateway(gateway.id);
      } else {
        await connectVGateway(gateway.id);
      }
      refresh();
    } catch (error) {
      setActionError(getErrorMessage(error));
    } finally {
      setConnectingID(null);
    }
  }

  function handleEnabledFilter(value: EnabledFilter) {
    setEnabledFilter(value);
    setPage(1);
  }

  return (
    <div className="vgateway-page">
      <button
        className={navigationOpen ? "vgateway-nav-backdrop is-open" : "vgateway-nav-backdrop"}
        type="button"
        aria-label="Close navigation"
        onClick={() => setNavigationOpen(false)}
      />

      <aside className={navigationOpen ? "vgateway-sidebar is-open" : "vgateway-sidebar"}>
        <div className="vgateway-brand">
          <Activity aria-hidden="true" strokeWidth={2.25} />
          <span>IoT Edge</span>
          <button
            type="button"
            className="vgateway-nav-close"
            aria-label="Close navigation"
            onClick={() => setNavigationOpen(false)}
          >
            <X aria-hidden="true" size={22} />
          </button>
        </div>

        <nav className="vgateway-navigation" aria-label="Primary navigation">
          <NavLink to="/dashboard">
            <LayoutDashboard aria-hidden="true" size={21} />
            Dashboard
          </NavLink>
          <NavLink to="/vgateways">
            <Cpu aria-hidden="true" size={21} />
            vGateways
          </NavLink>
          <span aria-disabled="true">
            <Boxes aria-hidden="true" size={21} />
            Devices
          </span>
          <NavLink to="/tags">
            <Tags aria-hidden="true" size={21} />
            Tags
          </NavLink>
          <NavLink to="/reports">
            <FileChartColumn aria-hidden="true" size={21} />
            Reports
          </NavLink>
          <span aria-disabled="true">
            <Puzzle aria-hidden="true" size={21} />
            Plugins
          </span>
          <NavLink to="/settings">
            <Settings aria-hidden="true" size={21} />
            Settings
          </NavLink>
        </nav>

        <Link className="vgateway-health-link" to="/dashboard">
          <span className="vgateway-health-dot" />
          <span>
            <strong>System health</strong>
            <small>Healthy</small>
          </span>
          <ChevronRight aria-hidden="true" size={18} />
        </Link>
      </aside>

      <main className="vgateway-workspace">
        <header className="vgateway-topbar">
          <button
            type="button"
            className="vgateway-menu-button"
            aria-label="Open navigation"
            onClick={() => setNavigationOpen(true)}
          >
            <Menu aria-hidden="true" size={23} />
          </button>
          <div className="vgateway-mobile-brand">
            <Activity aria-hidden="true" size={24} />
            <span>IoT Edge</span>
          </div>
          <p>
            Dashboard <span>/</span> <strong>vGateways</strong>
          </p>
          <div className="vgateway-topbar-actions">
            <InternetStatus />
            <div className="vgateway-user">
              <span>{username.slice(0, 1).toUpperCase()}</span>
              <strong>{username}</strong>
            </div>
          </div>
        </header>

        <div className="vgateway-content">
          <section className="vgateway-heading">
            <div>
              <h1>vGateways</h1>
              <p>Manage and monitor your virtual gateways.</p>
            </div>
            <button
              className="vgateway-primary-action"
              type="button"
              onClick={() => navigate("/vgateways/new")}
            >
              <Plus aria-hidden="true" size={19} />
              Add Gateway
            </button>
          </section>

          <section className="vgateway-metrics" aria-label="Gateway summary">
            <article>
              <Gauge aria-hidden="true" />
              <span>Total gateways</span>
              <strong>{pagination.total}</strong>
            </article>
            <article>
              <PlugZap aria-hidden="true" className="is-healthy" />
              <span>Connected</span>
              <strong>{connectedCount}</strong>
            </article>
            <article>
              <AlertTriangle aria-hidden="true" className="is-alert" />
              <span>Needs attention</span>
              <strong>{attentionCount}</strong>
            </article>
          </section>

          <section className="vgateway-toolbar" aria-label="Gateway filters">
            <label className="vgateway-search">
              <Search aria-hidden="true" size={20} />
              <span className="sr-only">Search gateways</span>
              <input
                type="search"
                placeholder="Search gateways…"
                value={search}
                onChange={(event) => setSearch(event.target.value)}
              />
            </label>
            <label>
              <span className="sr-only">Gateway type</span>
              <select aria-label="Gateway type" defaultValue="all">
                <option value="all">All types</option>
                <option value="modbus_tcp">Modbus TCP</option>
              </select>
            </label>
            <label>
              <span className="sr-only">Enabled state</span>
              <select
                aria-label="Enabled state"
                value={enabledFilter}
                onChange={(event) => handleEnabledFilter(event.target.value as EnabledFilter)}
              >
                <option value="all">All states</option>
                <option value="enabled">Enabled</option>
                <option value="disabled">Disabled</option>
              </select>
            </label>
            <button
              type="button"
              className="vgateway-refresh"
              onClick={refresh}
              disabled={loadState === "loading"}
            >
              <RefreshCw
                aria-hidden="true"
                className={loadState === "loading" ? "is-spinning" : ""}
                size={19}
              />
              Refresh
            </button>
          </section>

          {actionError && (
            <div className="vgateway-inline-error" role="alert">
              <CircleAlert aria-hidden="true" size={18} />
              <span>{actionError}</span>
              <button type="button" onClick={() => setActionError(null)} aria-label="Dismiss error">
                <X aria-hidden="true" size={17} />
              </button>
            </div>
          )}

          <section className="vgateway-list-shell" aria-label="vGateways">
            {loadState === "loading" && (
              <div className="vgateway-state" role="status">
                <LoaderCircle aria-hidden="true" className="is-spinning" size={26} />
                <strong>Loading vGateways…</strong>
                <span>Fetching the latest gateway state.</span>
              </div>
            )}

            {loadState === "error" && (
              <div className="vgateway-state is-error" role="alert">
                <CircleAlert aria-hidden="true" size={28} />
                <strong>Couldn’t load vGateways</strong>
                <span>{loadError}</span>
                <button type="button" onClick={refresh}>Try again</button>
              </div>
            )}

            {loadState === "ready" && gateways.length === 0 && (
              <div className="vgateway-state">
                <Cpu aria-hidden="true" size={30} />
                <strong>No vGateways yet</strong>
                <span>Add your first gateway to connect edge devices.</span>
              </div>
            )}

            {loadState === "ready" && gateways.length > 0 && visibleGateways.length === 0 && (
              <div className="vgateway-state">
                <Search aria-hidden="true" size={28} />
                <strong>No matching gateways</strong>
                <span>Try a different name or clear the search.</span>
                <button type="button" onClick={() => setSearch("")}>Clear search</button>
              </div>
            )}

            {loadState === "ready" && visibleGateways.length > 0 && (
              <>
                <div className="vgateway-table-scroll">
                  <table>
                    <thead>
                      <tr>
                        <th>Name</th>
                        <th>Type</th>
                        <th>Status</th>
                        <th>Devices</th>
                        <th>Last activity</th>
                        <th><span className="sr-only">Actions</span></th>
                      </tr>
                    </thead>
                    <tbody>
                      {visibleGateways.map((gateway) => (
                        <tr key={gateway.id}>
                          <td data-label="Name">
                            <span className="vgateway-name-icon"><Cpu aria-hidden="true" size={19} /></span>
                            <span>
                              <Link className="vgateway-name-link" to={`/vgateways/${gateway.id}`}>
                                {gateway.name}
                              </Link>
                              {gateway.description && <small>{gateway.description}</small>}
                            </span>
                          </td>
                          <td data-label="Type">Modbus TCP</td>
                          <td data-label="Status">
                            <span className={`vgateway-status is-${gateway.status}`}>
                              <span />
                              {statusLabel(gateway.status)}
                            </span>
                          </td>
                          <td data-label="Devices">{gateway.device_count}</td>
                          <td data-label="Last activity">{formatActivity(gateway.last_activity)}</td>
                          <td className="vgateway-row-actions">
                            <button
                              type="button"
                              disabled={!gateway.enabled || gateway.status === "connecting" || connectingID === gateway.id}
                              title={!gateway.enabled ? "Enable the gateway before connecting" : undefined}
                              aria-label={`${gateway.status === "connected" ? "Disconnect" : "Connect"} ${gateway.name}`}
                              onClick={() => void handleConnection(gateway)}
                            >
                              {connectingID === gateway.id ? (
                                <LoaderCircle aria-hidden="true" className="is-spinning" size={18} />
                              ) : gateway.status === "connected" ? (
                                <Unplug aria-hidden="true" size={18} />
                              ) : (
                                <Play aria-hidden="true" size={18} />
                              )}
                            </button>
                            <button
                              type="button"
                              aria-label={`Edit ${gateway.name}`}
                              onClick={() => navigate(`/vgateways/${gateway.id}/edit`)}
                            >
                              <Pencil aria-hidden="true" size={18} />
                            </button>
                            <button
                              type="button"
                              className="is-danger"
                              aria-label={`Delete ${gateway.name}`}
                              disabled={deletingID === gateway.id}
                              onClick={() => void handleDelete(gateway)}
                            >
                              {deletingID === gateway.id ? (
                                <LoaderCircle aria-hidden="true" className="is-spinning" size={18} />
                              ) : (
                                <Trash2 aria-hidden="true" size={18} />
                              )}
                            </button>
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </div>

                <footer className="vgateway-pagination">
                  <p>
                    Showing {visibleGateways.length} of {pagination.total} gateways
                  </p>
                  <div>
                    <button
                      type="button"
                      aria-label="Previous page"
                      disabled={page <= 1}
                      onClick={() => setPage((current) => Math.max(1, current - 1))}
                    >
                      <ChevronLeft aria-hidden="true" size={19} />
                    </button>
                    <span aria-label={`Page ${pagination.page}`}>{pagination.page}</span>
                    <button
                      type="button"
                      aria-label="Next page"
                      disabled={pagination.total_pages === 0 || page >= pagination.total_pages}
                      onClick={() => setPage((current) => current + 1)}
                    >
                      <ChevronRight aria-hidden="true" size={19} />
                    </button>
                  </div>
                </footer>
              </>
            )}
          </section>
        </div>
      </main>
    </div>
  );
}

export default VGatewayListPage;
