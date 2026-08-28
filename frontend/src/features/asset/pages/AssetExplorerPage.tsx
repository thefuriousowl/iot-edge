import { ChevronDown, ChevronRight, CircleAlert, LoaderCircle, Network, Search, X } from "lucide-react";
import { type FormEvent, type KeyboardEvent, useState } from "react";
import { useSearchParams } from "react-router-dom";

import { useAssetChildren, useAssetRoots, useAssets } from "../../../hooks/useAssets";
import type { Asset } from "../../../types/asset";
import VGatewayShell from "../../vgateway/components/VGatewayShell";
import "../../vgateway/pages/VGatewayListPage.css";
import "./AssetExplorerPage.css";
import AssetWorkspace from "../components/AssetWorkspace";
import AssetDetailPanel from "../components/AssetDetailPanel";

interface AssetBranchProps {
  asset: Asset;
  depth: number;
  expanded: Set<string>;
  selectedID: string | null;
  onToggle: (id: string) => void;
  onSelect: (asset: Asset) => void;
}

function moveTreeFocus(event: KeyboardEvent<HTMLButtonElement>, direction: number | "first" | "last") {
  const tree = event.currentTarget.closest('[role="tree"]');
  const items = Array.from(tree?.querySelectorAll<HTMLButtonElement>('[role="treeitem"]:not([disabled])') ?? []);
  const index = items.indexOf(event.currentTarget);
  const next = direction === "first" ? items[0] : direction === "last" ? items.at(-1) : items[index + direction];
  next?.focus();
}

function AssetBranch({ asset, depth, expanded, selectedID, onToggle, onSelect }: AssetBranchProps) {
  const isExpanded = expanded.has(asset.id);
  const children = useAssetChildren(isExpanded ? asset.id : "");
  const hasLoadedChildren = (children.data?.data.length ?? 0) > 0;

  function onKeyDown(event: KeyboardEvent<HTMLButtonElement>) {
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault(); moveTreeFocus(event, event.key === "ArrowDown" ? 1 : -1); return;
    }
    if (event.key === "Home" || event.key === "End") {
      event.preventDefault(); moveTreeFocus(event, event.key === "Home" ? "first" : "last"); return;
    }
    if (event.key === "ArrowRight") {
      event.preventDefault();
      if (!isExpanded) onToggle(asset.id);
      else event.currentTarget.closest("li")?.querySelector<HTMLButtonElement>('[role="group"] [role="treeitem"]')?.focus();
    }
    if (event.key === "ArrowLeft") {
      event.preventDefault();
      if (isExpanded) onToggle(asset.id);
      else event.currentTarget.closest("li")?.parentElement?.closest("li")?.querySelector<HTMLButtonElement>(":scope > [role=treeitem]")?.focus();
    }
  }

  return <li className="asset-tree-branch" role="none">
    <button
      className={selectedID === asset.id ? "asset-tree-item is-selected" : "asset-tree-item"}
      role="treeitem"
      aria-level={depth + 1}
      aria-selected={selectedID === asset.id}
      aria-expanded={isExpanded}
      style={{ "--asset-depth": depth } as React.CSSProperties}
      type="button"
      onClick={() => onSelect(asset)}
      onDoubleClick={() => onToggle(asset.id)}
      onKeyDown={onKeyDown}
    >
      <span className="asset-tree-toggle" aria-hidden="true" onClick={(event) => { event.stopPropagation(); onToggle(asset.id); }}>
        {children.isFetching ? <LoaderCircle className="is-spinning" size={15} /> : isExpanded ? <ChevronDown size={15} /> : <ChevronRight size={15} />}
      </span>
      <span className={`asset-kind-icon is-${asset.kind}`}><Network aria-hidden="true" size={16} /></span>
      <span className="asset-tree-copy"><strong>{asset.name}</strong><small>{asset.kind}</small></span>
      <span className={asset.enabled ? "asset-state is-enabled" : "asset-state"}>{asset.enabled ? "Enabled" : "Disabled"}</span>
    </button>
    {isExpanded && <ul role="group">
      {children.isError && <li className="asset-tree-inline-error" role="alert">Couldn’t load children</li>}
      {children.isSuccess && !hasLoadedChildren && <li className="asset-tree-empty">No child assets</li>}
      {children.data?.data.map((child) => <AssetBranch key={child.id} asset={child} depth={depth + 1} expanded={expanded} selectedID={selectedID} onToggle={onToggle} onSelect={onSelect} />)}
    </ul>}
  </li>;
}

function AssetExplorerPage() {
  const [params, setParams] = useSearchParams();
  const query = params.get("q") ?? "";
  const selectedID = params.get("asset");
  const roots = useAssetRoots();
  const search = useAssets(query ? { search: query, per_page: 100 } : undefined);
  const [expanded, setExpanded] = useState<Set<string>>(new Set());
  const [selectedAsset, setSelectedAsset] = useState<Asset | null>(null);
  const visible = query ? search.data?.data ?? [] : roots.data?.data ?? [];
  const loading = query ? search.isLoading : roots.isLoading;
  const failed = query ? search.isError : roots.isError;

  function updateRoute(values: { q?: string | null; asset?: string | null }) {
    const next = new URLSearchParams(params);
    for (const [key, value] of Object.entries(values)) {
      if (value) next.set(key, value);
      else next.delete(key);
    }
    setParams(next, { replace: true });
  }

  function submitSearch(event: FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const value = new FormData(event.currentTarget).get("search");
    updateRoute({ q: typeof value === "string" ? value.trim() : null, asset: null });
  }

  function select(asset: Asset) { setSelectedAsset(asset); updateRoute({ asset: asset.id }); }
  function toggle(id: string) { setExpanded((current) => { const next = new Set(current); if (next.has(id)) next.delete(id); else next.add(id); return next; }); }
  const selected = selectedAsset?.id === selectedID ? selectedAsset : visible.find((asset) => asset.id === selectedID) ?? null;

  return <VGatewayShell breadcrumb={<>Dashboard <span>/</span> <strong>Assets</strong></>}>
    <div className="asset-explorer-content">
      <header className="asset-explorer-heading"><div><p className="asset-eyebrow">Operational hierarchy</p><h1>Asset Explorer</h1><p>Navigate sites, systems, equipment and meters without changing the acquisition topology.</p></div><span className="asset-root-count">{roots.data?.data.length ?? 0}<small>root assets</small></span></header>
      <section className="asset-explorer-layout">
        <aside className="asset-tree-panel" aria-label="Asset hierarchy">
          <form className="asset-search" role="search" onSubmit={submitSearch}><Search aria-hidden="true" size={18} /><label className="sr-only" htmlFor="asset-search">Search assets</label><input id="asset-search" name="search" type="search" defaultValue={query} placeholder="Search the hierarchy…" /><button type="submit">Search</button>{query && <button className="asset-search-clear" type="button" aria-label="Clear asset search" onClick={() => updateRoute({ q: null, asset: null })}><X size={17} /></button>}</form>
          <div className="asset-tree-scroll">
            {loading && <div className="asset-tree-state" role="status"><LoaderCircle className="is-spinning" /><strong>Loading assets…</strong></div>}
            {failed && <div className="asset-tree-state is-error" role="alert"><CircleAlert /><strong>Couldn’t load assets</strong><button type="button" onClick={() => void (query ? search.refetch() : roots.refetch())}>Try again</button></div>}
            {!loading && !failed && visible.length === 0 && <div className="asset-tree-state"><Network /><strong>{query ? "No matching assets" : "No assets yet"}</strong><span>{query ? "Try a broader search." : "Create a site to start the operational hierarchy."}</span></div>}
            {!loading && !failed && visible.length > 0 && <ul className="asset-tree" role="tree" aria-label={query ? "Asset search results" : "Asset hierarchy"}>{visible.map((asset) => <AssetBranch key={asset.id} asset={asset} depth={0} expanded={expanded} selectedID={selectedID} onToggle={toggle} onSelect={select} />)}</ul>}
          </div>
        </aside>
        <section className="asset-selection" aria-live="polite">
          <AssetWorkspace selected={selected} onCreated={select} onDeleted={() => { setSelectedAsset(null); updateRoute({ asset: null }); }} />
          {selected ? <AssetDetailPanel asset={selected} initialTab={params.get("view") === "connectivity" ? "connectivity" : "overview"} /> : <div className="asset-selection-empty"><Network size={36} /><h2>Select an asset</h2><p>Choose a node to inspect its operational context. Use arrow keys to move through the tree.</p></div>}
        </section>
      </section>
    </div>
  </VGatewayShell>;
}

export default AssetExplorerPage;
