import axios from "axios";
import { Activity, ChevronLeft, ChevronRight, CircleAlert, CircleStop, Gauge, LoaderCircle, Play, PlugZap, RefreshCw, RotateCcw, Search, Settings2, Trash2 } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { deletePlugin, disablePlugin, enablePlugin, listPlugins, listPluginTypes, restartPlugin } from "../../../services/plugin.service";
import type { DataLoggerPagination } from "../../../types/datalogger";
import type { PluginInstanceSummary, PluginManifest, PluginRuntimeState } from "../../../types/plugin";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./PluginListPage.css";

const perPage = 20;
const emptyPagination: DataLoggerPagination = { page: 1, per_page: perPage, total: 0, total_pages: 0 };

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Plugins. Check the API connection and try again.";
}

function runtimeLabel(state: PluginRuntimeState): string {
  return state.slice(0, 1).toUpperCase() + state.slice(1);
}

function PluginListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const search = searchParams.get("search")?.trim() ?? "";
  const type = searchParams.get("type") || undefined;
  const rawEnabled = searchParams.get("enabled");
  const enabled = rawEnabled === "true" ? true : rawEnabled === "false" ? false : undefined;
  const page = positivePage(searchParams.get("page"));
  const [plugins, setPlugins] = useState<PluginInstanceSummary[]>([]);
  const [manifests, setManifests] = useState<PluginManifest[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [actionID, setActionID] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void Promise.all([
      listPluginTypes(controller.signal),
      listPlugins({ type, enabled, search: search || undefined, page, per_page: perPage }, controller.signal),
    ]).then(([typesResponse, listResponse]) => {
      if (controller.signal.aborted) return;
      setManifests(typesResponse.data);
      setPlugins(listResponse.data);
      setPagination(listResponse.pagination);
      setMessage("");
      setState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(errorMessage(error));
      setState("error");
    });
    return () => controller.abort();
  }, [enabled, page, refresh, search, type]);

  const updateFilter = useCallback((name: string, value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(name, value); else next.delete(name);
    if (name !== "page") next.delete("page");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const enabledCount = useMemo(() => plugins.filter((plugin) => plugin.enabled).length, [plugins]);
  const runningCount = useMemo(() => plugins.filter((plugin) => plugin.runtime.state === "running").length, [plugins]);
  const issueCount = useMemo(() => plugins.filter((plugin) => plugin.runtime.state === "error").length, [plugins]);

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    updateFilter("search", String(new FormData(event.currentTarget).get("search") ?? "").trim() || undefined);
  }

  async function runAction(plugin: PluginInstanceSummary, action: "enable" | "disable" | "restart" | "delete") {
    if (action === "delete" && !window.confirm(`Delete “${plugin.name}”? Data Logger history will not be removed.`)) return;
    setActionID(plugin.id);
    setMessage("");
    try {
      if (action === "enable") await enablePlugin(plugin.id);
      if (action === "disable") await disablePlugin(plugin.id);
      if (action === "restart") await restartPlugin(plugin.id);
      if (action === "delete") await deletePlugin(plugin.id);
      setRefresh((value) => value + 1);
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setActionID(null);
    }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Plugins</strong></>}>
    <div className="plugin-content">
      <section className="plugin-heading"><div><p className="plugin-eyebrow">Built-in extensions</p><h1>Plugins</h1><p>Manage installed capabilities without giving Plugins direct access to Devices or protocols.</p></div><Link className="plugin-primary-link" to="/plugins/new/energy"><PlugZap size={18} /><span>New Energy instance</span></Link></section>
      <section className="plugin-metrics" aria-label="Plugin summary"><article><PlugZap /><span>Total instances</span><strong>{pagination.total}</strong></article><article><Play /><span>Desired enabled</span><strong>{enabledCount}</strong></article><article><Activity className={issueCount > 0 ? "is-alert" : ""} /><span>Running on page</span><strong>{runningCount}</strong><small>{issueCount > 0 ? `${issueCount} need attention` : "No runtime errors"}</small></article></section>
      <section className="plugin-toolbar" aria-label="Plugin filters"><form role="search" onSubmit={submitSearch}><Search size={18} /><label className="sr-only" htmlFor="plugin-search">Search Plugins</label><input id="plugin-search" name="search" type="search" key={search} defaultValue={search} placeholder="Search Plugin instances…" /><button type="submit">Search</button></form><select aria-label="Plugin type" value={type ?? "all"} onChange={(event) => updateFilter("type", event.target.value === "all" ? undefined : event.target.value)}><option value="all">All types</option>{manifests.map((manifest) => <option key={manifest.type} value={manifest.type}>{manifest.name}</option>)}</select><select aria-label="Desired state" value={rawEnabled ?? "all"} onChange={(event) => updateFilter("enabled", event.target.value === "all" ? undefined : event.target.value)}><option value="all">All desired states</option><option value="true">Enabled</option><option value="false">Disabled</option></select><button type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw className={state === "loading" ? "is-spinning" : ""} size={17} /> Refresh</button></section>
      {message && state !== "error" && <div className="plugin-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      <section className="plugin-list" aria-label="Plugin instances">
        {state === "loading" && <div className="plugin-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading Plugins…</strong></div>}
        {state === "error" && <div className="plugin-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Plugins</strong><span>{message}</span><button type="button" onClick={() => setRefresh((value) => value + 1)}>Try again</button></div>}
        {state === "ready" && plugins.length === 0 && <div className="plugin-state"><PlugZap /><strong>No Plugin instances found</strong><span>Create an Energy instance from synchronized Data Logger power Tags.</span><Link to="/plugins/new/energy">Configure Energy</Link></div>}
        {state === "ready" && plugins.length > 0 && <>
          <div className="plugin-table-scroll"><table><thead><tr><th>Instance</th><th>Type</th><th>Desired</th><th>Runtime</th><th>Last transition</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{plugins.map((plugin) => <tr key={plugin.id}><td data-label="Instance"><strong>{plugin.name}</strong><small>Config v{plugin.config_version}</small></td><td data-label="Type">{manifests.find((manifest) => manifest.type === plugin.type)?.name ?? plugin.type}</td><td data-label="Desired"><span className={`plugin-desired is-${plugin.enabled ? "enabled" : "disabled"}`}>{plugin.enabled ? "Enabled" : "Disabled"}</span></td><td data-label="Runtime"><span className={`plugin-runtime is-${plugin.runtime.state}`}><i />{runtimeLabel(plugin.runtime.state)}</span>{plugin.runtime.error && <small className="plugin-runtime-error" title={plugin.runtime.error.code}>{plugin.runtime.error.message}</small>}</td><td data-label="Last transition">{plugin.runtime.last_transition_at ? new Date(plugin.runtime.last_transition_at).toLocaleString() : "Not started"}</td><td data-label="Actions"><div className="plugin-row-actions"><Link aria-label={`Open ${plugin.name} dashboard`} to={`/plugins/${encodeURIComponent(plugin.id)}/energy`}><Gauge size={16} /><span>Dashboard</span></Link><Link aria-label={`Configure ${plugin.name}`} to={`/plugins/${encodeURIComponent(plugin.id)}/configure`}><Settings2 size={16} /><span>Configure</span></Link>{plugin.enabled ? <button type="button" aria-label={`Disable ${plugin.name}`} disabled={actionID === plugin.id} onClick={() => void runAction(plugin, "disable")}><CircleStop size={16} /><span>Disable</span></button> : <button type="button" aria-label={`Enable ${plugin.name}`} disabled={actionID === plugin.id} onClick={() => void runAction(plugin, "enable")}><Play size={16} /><span>Enable</span></button>}<button type="button" aria-label={`Restart ${plugin.name}`} disabled={!plugin.enabled || actionID === plugin.id} onClick={() => void runAction(plugin, "restart")}><RotateCcw size={16} /><span>Restart</span></button><button className="is-danger" type="button" aria-label={`Delete ${plugin.name}`} disabled={actionID === plugin.id} onClick={() => void runAction(plugin, "delete")}><Trash2 size={16} /><span>Delete</span></button></div></td></tr>)}</tbody></table></div>
          <footer className="plugin-pagination"><p>Showing {plugins.length} of {pagination.total} Plugin instances</p><div><button aria-label="Previous page" disabled={page <= 1} onClick={() => updateFilter("page", String(page - 1))}><ChevronLeft /></button><span>{pagination.page}</span><button aria-label="Next page" disabled={page >= pagination.total_pages} onClick={() => updateFilter("page", String(page + 1))}><ChevronRight /></button></div></footer>
        </>}
      </section>
    </div>
  </VGatewayShell>;
}

export default PluginListPage;
