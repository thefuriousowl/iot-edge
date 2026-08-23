import axios from "axios";
import { Activity, ChevronLeft, ChevronRight, CircleAlert, CircleStop, LoaderCircle, Play, RadioTower, RefreshCw, RotateCcw, Search, Settings2, Trash2 } from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { deleteDataPublisher, disableDataPublisher, enableDataPublisher, listDataPublishers, restartDataPublisher } from "../../../services/publisher.service";
import type { DataPublisher } from "../../../types/publisher";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../plugin/pages/PluginListPage.css";
import "../../vgateway/pages/VGatewayListPage.css";

const perPage = 20;

function errorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const body = error.response?.data as { error?: { message?: string } } | undefined;
    if (body?.error?.message) return body.error.message;
  }
  return "Unable to load Data Publishers. Check the API connection and try again.";
}

function positivePage(value: string | null): number {
  const page = Number(value);
  return Number.isInteger(page) && page > 0 ? page : 1;
}

function DataPublisherListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const search = searchParams.get("search")?.trim() ?? "";
  const rawEnabled = searchParams.get("enabled");
  const enabled = rawEnabled === "true" ? true : rawEnabled === "false" ? false : undefined;
  const page = positivePage(searchParams.get("page"));
  const [publishers, setPublishers] = useState<DataPublisher[]>([]);
  const [pagination, setPagination] = useState({ page: 1, per_page: perPage, total: 0, total_pages: 0 });
  const [state, setState] = useState<"loading" | "ready" | "error">("loading");
  const [message, setMessage] = useState("");
  const [refresh, setRefresh] = useState(0);
  const [actionID, setActionID] = useState<string | null>(null);

  useEffect(() => {
    const controller = new AbortController();
    void listDataPublishers({ type: "mqtt", enabled, search: search || undefined, page, per_page: perPage }, controller.signal).then((response) => {
      if (controller.signal.aborted) return;
      setPublishers(response.data);
      setPagination(response.pagination);
      setMessage("");
      setState("ready");
    }).catch((error: unknown) => {
      if (controller.signal.aborted) return;
      setMessage(errorMessage(error));
      setState("error");
    });
    return () => controller.abort();
  }, [enabled, page, refresh, search]);

  const updateFilter = useCallback((name: string, value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(name, value); else next.delete(name);
    if (name !== "page") next.delete("page");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const running = useMemo(() => publishers.filter((item) => item.runtime.state === "running").length, [publishers]);
  const issues = useMemo(() => publishers.filter((item) => item.runtime.state === "error").length, [publishers]);

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    updateFilter("search", String(new FormData(event.currentTarget).get("search") ?? "").trim() || undefined);
  }

  async function runAction(item: DataPublisher, action: "enable" | "disable" | "restart" | "delete") {
    if (action === "delete" && !window.confirm(`Delete “${item.name}”? Its reusable Credential Profile will remain available.`)) return;
    setActionID(item.id);
    setMessage("");
    try {
      if (action === "enable") await enableDataPublisher(item.id);
      if (action === "disable") await disableDataPublisher(item.id);
      if (action === "restart") await restartDataPublisher(item.id);
      if (action === "delete") await deleteDataPublisher(item.id);
      setRefresh((value) => value + 1);
    } catch (error) {
      setMessage(errorMessage(error));
    } finally {
      setActionID(null);
    }
  }

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Data Publishers</strong></>}>
    <div className="plugin-content">
      <section className="plugin-heading"><div><p className="plugin-eyebrow">Core egress</p><h1>MQTT Publishers</h1><p>Publish existing Core Tag and Plugin-output snapshots without creating additional acquisition requests.</p></div><Link className="plugin-primary-link" to="/data-publishers/new/mqtt"><RadioTower size={18} />New MQTT Publisher</Link></section>
      <section className="plugin-metrics" aria-label="Publisher summary"><article><RadioTower /><span>Total Publishers</span><strong>{pagination.total}</strong></article><article><Play /><span>Running on page</span><strong>{running}</strong></article><article><Activity className={issues ? "is-alert" : ""} /><span>Runtime issues</span><strong>{issues}</strong><small>{issues ? "Review sanitized errors" : "No runtime errors"}</small></article></section>
      <section className="plugin-toolbar" aria-label="Publisher filters"><form role="search" onSubmit={submitSearch}><Search size={18} /><label className="sr-only" htmlFor="publisher-search">Search Publishers</label><input id="publisher-search" name="search" type="search" key={search} defaultValue={search} placeholder="Search MQTT Publishers…" /><button type="submit">Search</button></form><select aria-label="Desired state" value={rawEnabled ?? "all"} onChange={(event) => updateFilter("enabled", event.target.value === "all" ? undefined : event.target.value)}><option value="all">All desired states</option><option value="true">Enabled</option><option value="false">Disabled</option></select><button type="button" onClick={() => setRefresh((value) => value + 1)}><RefreshCw className={state === "loading" ? "is-spinning" : ""} size={17} /> Refresh</button></section>
      {message && state !== "error" && <div className="plugin-alert" role="alert"><CircleAlert size={18} />{message}</div>}
      <section className="plugin-list" aria-label="MQTT Publishers">
        {state === "loading" && <div className="plugin-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading MQTT Publishers…</strong></div>}
        {state === "error" && <div className="plugin-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load Data Publishers</strong><span>{message}</span><button type="button" onClick={() => setRefresh((value) => value + 1)}>Try again</button></div>}
        {state === "ready" && publishers.length === 0 && <div className="plugin-state"><RadioTower /><strong>No MQTT Publishers found</strong><span>Create a disabled definition, select an optional Credential Profile, test it, then enable the runtime.</span><Link to="/data-publishers/new/mqtt">New MQTT Publisher</Link></div>}
        {state === "ready" && publishers.length > 0 && <><div className="plugin-table-scroll"><table><thead><tr><th>Publisher</th><th>Desired</th><th>Runtime</th><th>Delivery</th><th>Connection</th><th><span className="sr-only">Actions</span></th></tr></thead><tbody>{publishers.map((item) => <tr key={item.id}><td data-label="Publisher"><strong>{item.name}</strong><small>{item.source_count} sources · Config v{item.config_version}</small></td><td data-label="Desired"><span className={`plugin-desired is-${item.enabled ? "enabled" : "disabled"}`}>{item.enabled ? "Enabled" : "Disabled"}</span></td><td data-label="Runtime"><span className={`plugin-runtime is-${item.runtime.state}`}><i />{item.runtime.state}</span>{(item.runtime.last_error || item.runtime.transport_error) && <small className="plugin-runtime-error">{item.runtime.last_error || item.runtime.transport_error}</small>}</td><td data-label="Delivery">{item.runtime.delivery_count} delivered<small>{item.runtime.transport_drop_count} dropped · {item.runtime.delivery_failure_count} failed</small></td><td data-label="Connection">{item.runtime.connected ? "Connected" : "Disconnected"}<small>{item.runtime.reconnect_count} reconnects</small></td><td data-label="Actions"><div className="plugin-row-actions"><Link aria-label={`Configure ${item.name}`} to={`/data-publishers/${encodeURIComponent(item.id)}/mqtt`}><Settings2 size={16} /><span>Configure</span></Link>{item.enabled ? <button aria-label={`Disable ${item.name}`} disabled={actionID === item.id} onClick={() => void runAction(item, "disable")}><CircleStop size={16} /><span>Disable</span></button> : <button aria-label={`Enable ${item.name}`} disabled={actionID === item.id} onClick={() => void runAction(item, "enable")}><Play size={16} /><span>Enable</span></button>}<button aria-label={`Restart ${item.name}`} disabled={!item.enabled || actionID === item.id} onClick={() => void runAction(item, "restart")}><RotateCcw size={16} /><span>Restart</span></button><button className="is-danger" aria-label={`Delete ${item.name}`} disabled={actionID === item.id} onClick={() => void runAction(item, "delete")}><Trash2 size={16} /><span>Delete</span></button></div></td></tr>)}</tbody></table></div><footer className="plugin-pagination"><p>Showing {publishers.length} of {pagination.total}</p><div><button aria-label="Previous page" disabled={page <= 1} onClick={() => updateFilter("page", String(page - 1))}><ChevronLeft /></button><span>{pagination.page}</span><button aria-label="Next page" disabled={page >= pagination.total_pages} onClick={() => updateFilter("page", String(page + 1))}><ChevronRight /></button></div></footer></>}
      </section>
    </div>
  </VGatewayShell>;
}

export default DataPublisherListPage;
