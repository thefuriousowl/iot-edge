import axios from "axios";
import {
  Boxes,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  Cpu,
  ExternalLink,
  Gauge,
  LoaderCircle,
  RadioTower,
  RefreshCw,
  Search,
} from "lucide-react";
import { type FormEvent, useCallback, useEffect, useMemo, useState } from "react";
import { Link, useSearchParams } from "react-router-dom";

import { listDeviceInventory } from "../../../services/device.service";
import { listVGateways } from "../../../services/vgateway.service";
import type {
  DeviceInventoryItem,
  DeviceInventoryPagination,
  DeviceType,
} from "../../../types/device";
import type { VGatewayListItem } from "../../../types/vgateway";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./DeviceListPage.css";

type LoadState = "loading" | "ready" | "error";
type EnabledFilter = "all" | "enabled" | "disabled";

const devicesPerPage = 20;
const deviceTypes: DeviceType[] = ["modbus_device"];
const emptyPagination: DeviceInventoryPagination = {
  page: 1,
  per_page: devicesPerPage,
  total: 0,
  total_pages: 0,
};
const dateFormatter = new Intl.DateTimeFormat(undefined, {
  dateStyle: "medium",
  timeStyle: "short",
});

function positivePage(value: string | null): number {
  const parsed = Number(value);
  return Number.isInteger(parsed) && parsed > 0 ? parsed : 1;
}

function isDeviceType(value: string | null): value is DeviceType {
  return value !== null && deviceTypes.includes(value as DeviceType);
}

function typeLabel(value: string): string {
  if (value === "modbus_tcp") return "Modbus TCP";
  return value
    .replace(/_device$/, "")
    .split("_")
    .map((part) => part.charAt(0).toUpperCase() + part.slice(1))
    .join(" ");
}

function countLabel(count: number, singular: string): string {
  return `${count} ${singular}${count === 1 ? "" : "s"}`;
}

function formatUpdatedAt(value: string): string {
  const date = new Date(value);
  return Number.isNaN(date.getTime()) ? "Unknown" : dateFormatter.format(date);
}

function getErrorMessage(error: unknown): string {
  if (axios.isAxiosError(error)) {
    const data = error.response?.data as { error?: { message?: string } } | undefined;
    if (data?.error?.message) return data.error.message;
  }
  return "Unable to load devices. Check the API connection and try again.";
}

function effectiveState(entity: DeviceInventoryItem): { label: string; className: string } {
  if (!entity.vgateway_enabled) return { label: "Gateway paused", className: "is-paused" };
  if (!entity.enabled) return { label: "Device paused", className: "is-paused" };
  return { label: "Enabled", className: "is-enabled" };
}

function DeviceListPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const querySearch = searchParams.get("search")?.trim() ?? "";
  const selectedGatewayID = searchParams.get("vgateway_id") ?? undefined;
  const selectedType = isDeviceType(searchParams.get("type"))
    ? (searchParams.get("type") as DeviceType)
    : undefined;
  const rawEnabled = searchParams.get("enabled");
  const enabledFilter: EnabledFilter = rawEnabled === "true"
    ? "enabled"
    : rawEnabled === "false"
      ? "disabled"
      : "all";
  const page = positivePage(searchParams.get("page"));

  const [devices, setDevices] = useState<DeviceInventoryItem[]>([]);
  const [gateways, setGateways] = useState<VGatewayListItem[]>([]);
  const [pagination, setPagination] = useState(emptyPagination);
  const [loadState, setLoadState] = useState<LoadState>("loading");
  const [loadError, setLoadError] = useState<string | null>(null);
  const [refreshVersion, setRefreshVersion] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    void listVGateways({ page: 1, per_page: 100 }, controller.signal)
      .then((response) => {
        if (!controller.signal.aborted) setGateways(response.data);
      })
      .catch(() => undefined);
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      setLoadState("loading");
      setLoadError(null);
      try {
        const response = await listDeviceInventory(
          {
            vgateway_id: selectedGatewayID,
            type: selectedType,
            enabled: enabledFilter === "all" ? undefined : enabledFilter === "enabled",
            search: querySearch || undefined,
            page,
            per_page: devicesPerPage,
          },
          controller.signal,
        );
        if (!controller.signal.aborted) {
          setDevices(response.data);
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
    void load();
    return () => controller.abort();
  }, [enabledFilter, page, querySearch, refreshVersion, selectedGatewayID, selectedType]);

  const updateFilter = useCallback((name: string, value?: string) => {
    const next = new URLSearchParams(searchParams);
    if (value) next.set(name, value);
    else next.delete(name);
    if (name !== "page") next.delete("page");
    setSearchParams(next, { replace: true });
  }, [searchParams, setSearchParams]);

  const activeFilters = useMemo(() => [
    selectedGatewayID,
    selectedType,
    enabledFilter !== "all",
    querySearch,
  ].filter(Boolean).length, [enabledFilter, querySearch, selectedGatewayID, selectedType]);
  const enabledOnPage = devices.filter((entity) => entity.enabled && entity.vgateway_enabled).length;
  const datasourcesOnPage = devices.reduce((total, entity) => total + entity.datasource_count, 0);

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = new FormData(event.currentTarget).get("search");
    const search = typeof value === "string" ? value.trim() : "";
    updateFilter("search", search || undefined);
  }

  function clearFilters() {
    setSearchParams({}, { replace: true });
  }

  return (
    <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Devices</strong></>}>
      <div className="device-inventory-content">
        <section className="device-inventory-heading">
          <div>
            <h1>Devices</h1>
            <p>Inspect protocol devices and their acquisition footprint across every vGateway.</p>
          </div>
          <Link className="device-inventory-gateways" to="/vgateways">
            <Cpu aria-hidden="true" size={18} /> Manage vGateways
          </Link>
        </section>

        <section className="device-inventory-metrics" aria-label="Device summary">
          <article><Boxes aria-hidden="true" /><span>Total devices</span><strong>{pagination.total}</strong></article>
          <article><RadioTower aria-hidden="true" /><span>Enabled on page</span><strong>{enabledOnPage}</strong></article>
          <article><Gauge aria-hidden="true" /><span>Datasources on page</span><strong>{datasourcesOnPage}</strong></article>
        </section>

        <section className="device-inventory-toolbar" aria-label="Device filters">
          <form className="device-inventory-search" role="search" onSubmit={submitSearch}>
            <Search aria-hidden="true" size={19} />
            <label className="sr-only" htmlFor="device-search">Search devices</label>
            <input id="device-search" key={querySearch} name="search" type="search" placeholder="Search devices or gateways…" defaultValue={querySearch} />
            <button type="submit">Search</button>
          </form>
          <label>
            <span className="sr-only">vGateway</span>
            <select aria-label="vGateway" value={selectedGatewayID ?? "all"} onChange={(event) => updateFilter("vgateway_id", event.target.value === "all" ? undefined : event.target.value)}>
              <option value="all">All vGateways</option>
              {gateways.map((gateway) => <option key={gateway.id} value={gateway.id}>{gateway.name}</option>)}
            </select>
          </label>
          <label>
            <span className="sr-only">Device type</span>
            <select aria-label="Device type" value={selectedType ?? "all"} onChange={(event) => updateFilter("type", event.target.value === "all" ? undefined : event.target.value)}>
              <option value="all">All protocols</option>
              {deviceTypes.map((type) => <option key={type} value={type}>{typeLabel(type)}</option>)}
            </select>
          </label>
          <label>
            <span className="sr-only">Enabled state</span>
            <select aria-label="Enabled state" value={enabledFilter} onChange={(event) => {
              const value = event.target.value as EnabledFilter;
              updateFilter("enabled", value === "all" ? undefined : String(value === "enabled"));
            }}>
              <option value="all">All states</option>
              <option value="enabled">Enabled</option>
              <option value="disabled">Disabled</option>
            </select>
          </label>
          <button className="device-inventory-refresh" type="button" disabled={loadState === "loading"} onClick={() => setRefreshVersion((current) => current + 1)}>
            <RefreshCw aria-hidden="true" className={loadState === "loading" ? "is-spinning" : ""} size={18} /> Refresh
          </button>
        </section>

        {activeFilters > 0 && <div className="device-inventory-active-filters"><span>{activeFilters} active {activeFilters === 1 ? "filter" : "filters"}</span><button type="button" onClick={clearFilters}>Clear all</button></div>}

        <section className="device-inventory-list" aria-label="Devices">
          {loadState === "loading" && <div className="device-inventory-state" role="status"><LoaderCircle aria-hidden="true" className="is-spinning" size={27} /><strong>Loading devices…</strong><span>Collecting the acquisition topology.</span></div>}
          {loadState === "error" && <div className="device-inventory-state is-error" role="alert"><CircleAlert aria-hidden="true" size={29} /><strong>Couldn’t load devices</strong><span>{loadError}</span><button type="button" onClick={() => setRefreshVersion((current) => current + 1)}>Try again</button></div>}
          {loadState === "ready" && devices.length === 0 && <div className="device-inventory-state"><Boxes aria-hidden="true" size={30} /><strong>{activeFilters > 0 ? "No matching devices" : "No devices yet"}</strong><span>{activeFilters > 0 ? "Adjust or clear the filters to broaden the result." : "Create a device inside a vGateway to begin defining datasources."}</span>{activeFilters > 0 ? <button type="button" onClick={clearFilters}>Clear filters</button> : <Link to="/vgateways">Open vGateways</Link>}</div>}
          {loadState === "ready" && devices.length > 0 && <>
            <div className="device-inventory-table-scroll"><table>
              <thead><tr><th>Device</th><th>vGateway</th><th>Protocol</th><th>Acquisition</th><th>State</th><th>Updated</th><th aria-label="Actions" /></tr></thead>
              <tbody>{devices.map((entity) => {
                const state = effectiveState(entity);
                return <tr key={entity.id}>
                  <td data-label="Device"><span className="device-inventory-icon"><Cpu aria-hidden="true" size={19} /></span><span><strong>{entity.name}</strong><small>{entity.description ?? "No description"}</small></span></td>
                  <td data-label="vGateway"><strong>{entity.vgateway_name}</strong><small>{typeLabel(entity.vgateway_type)}</small></td>
                  <td data-label="Protocol"><code>{typeLabel(entity.type)}</code></td>
                  <td data-label="Acquisition"><strong>{countLabel(entity.datasource_count, "datasource")}</strong><small>{countLabel(entity.tag_count, "reading tag")}</small></td>
                  <td data-label="State"><span className={`device-inventory-state-pill ${state.className}`}><span />{state.label}</span></td>
                  <td data-label="Updated">{formatUpdatedAt(entity.updated_at)}</td>
                  <td data-label="Actions"><Link aria-label={`Open ${entity.name} topology`} to={`/vgateways/${encodeURIComponent(entity.vgateway_id)}#devices`}><ExternalLink aria-hidden="true" size={17} /> Open</Link></td>
                </tr>;
              })}</tbody>
            </table></div>
            <footer className="device-inventory-pagination"><p>Showing {devices.length} of {pagination.total} devices</p><div>
              <button type="button" aria-label="Previous page" disabled={page <= 1} onClick={() => updateFilter("page", String(Math.max(1, page - 1)))}><ChevronLeft aria-hidden="true" size={19} /></button>
              <span aria-label={`Page ${pagination.page}`}>{pagination.page}</span>
              <button type="button" aria-label="Next page" disabled={pagination.total_pages === 0 || page >= pagination.total_pages} onClick={() => updateFilter("page", String(page + 1))}><ChevronRight aria-hidden="true" size={19} /></button>
            </div></footer>
          </>}
        </section>
      </div>
    </VGatewayShell>
  );
}

export default DeviceListPage;
