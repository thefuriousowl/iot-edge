import { Check, CircleAlert, Plus, Trash2 } from "lucide-react";

import type {
  PublisherSourceCatalogEntry,
  PublisherSourceDataType,
  PublisherSourceDraft,
  PublisherSourceKind,
} from "../../../types/publisher";
import { publisherSourceReferenceKey } from "../utils/shared";
import PublisherSourceCurrent from "./PublisherSourceCurrent";

const dataTypes: PublisherSourceDataType[] = ["bool", "int16", "uint16", "int32", "uint32", "float32", "float64", "string"];

interface PublisherSourceSelectorProps {
  catalog: PublisherSourceCatalogEntry[];
  catalogState: "loading" | "ready" | "error";
  search: string;
  kind: PublisherSourceKind | "";
  sources: PublisherSourceDraft[];
  onSearchChange: (value: string) => void;
  onKindChange: (value: PublisherSourceKind | "") => void;
  onAdd: (entry: PublisherSourceCatalogEntry) => void;
  onUpdate: (sourceID: string, update: Partial<PublisherSourceDraft>) => void;
  onRemove: (sourceID: string) => void;
}

function PublisherSourceSelector({ catalog, catalogState, search, kind, sources, onSearchChange, onKindChange, onAdd, onUpdate, onRemove }: PublisherSourceSelectorProps) {
  return <>
    <div className="mqtt-source-catalog">
      <header><div><h3>Core source catalog</h3><p>Descriptors are owned by Core. Only the Publisher alias is editable after selection.</p></div><span>{catalogState === "loading" ? "Loading…" : `${catalog.length} found`}</span></header>
      <div className="mqtt-catalog-filters">
        <label><span>Search</span><input aria-label="Search Publisher sources" value={search} onChange={(event) => onSearchChange(event.target.value)} placeholder="Name, owner, unit…" /></label>
        <label><span>Kind</span><select aria-label="Publisher source kind" value={kind} onChange={(event) => onKindChange(event.target.value as PublisherSourceKind | "")}><option value="">All sources</option><option value="tag">Core Tags</option><option value="plugin_output">Plugin outputs</option></select></label>
      </div>
      {catalogState === "error"
        ? <div className="mqtt-catalog-empty">Source catalog unavailable. Existing local draft remains untouched.</div>
        : catalogState === "ready" && catalog.length === 0
          ? <div className="mqtt-catalog-empty">No enabled sources match this filter.</div>
          : <div className="mqtt-catalog-list">{catalog.map((entry) => {
              const descriptor = entry.descriptor;
              const selected = sources.some((source) => source.reference && publisherSourceReferenceKey(source.reference) === publisherSourceReferenceKey(descriptor.reference));
              return <article key={publisherSourceReferenceKey(descriptor.reference)}><div><strong>{descriptor.name}</strong><span>{descriptor.owner_name || "Core"} · {descriptor.reference.kind === "tag" ? "Tag" : "Plugin output"}</span></div><div><strong>{descriptor.data_type}</strong><span>{descriptor.unit || "No unit"} · {descriptor.period_kind}</span></div><PublisherSourceCurrent entry={entry} /><button type="button" disabled={selected} onClick={() => onAdd(entry)}>{selected ? <Check size={16} /> : <Plus size={16} />}{selected ? "Selected" : "Add"}</button></article>;
            })}</div>}
    </div>
    <div className="mqtt-source-note"><CircleAlert size={17} /><span><strong>Sources are asynchronous.</strong> Every Tag or Plugin output keeps its own observed time or period. A Publisher trigger snapshots the latest available values; it does not align timestamps or initiate acquisition.</span></div>
    {sources.some((source) => !source.reference) && <div className="mqtt-source-note"><CircleAlert size={17} /><span>This browser contains legacy schema-only rows. Remove them and select real Core sources before server validation or save.</span></div>}
    <div className="mqtt-source-list">
      {sources.map((source) => <article key={source.id} className="mqtt-source-row">
        <label><span>Alias</span><input aria-label={`Alias for ${source.name}`} spellCheck={false} value={source.alias} onChange={(event) => onUpdate(source.id, { alias: event.target.value })} /></label>
        <label><span>Display name</span><input aria-label={`Display name for ${source.alias}`} disabled={Boolean(source.reference)} value={source.name} onChange={(event) => onUpdate(source.id, { name: event.target.value })} /></label>
        <label><span>Kind</span><select aria-label={`Kind for ${source.alias}`} disabled={Boolean(source.reference)} value={source.kind} onChange={(event) => onUpdate(source.id, { kind: event.target.value as PublisherSourceDraft["kind"], period_kind: event.target.value === "tag" ? "instantaneous" : source.period_kind })}><option value="tag">Core Tag</option><option value="plugin_output">Plugin output</option></select></label>
        <label><span>Data type</span><select aria-label={`Data type for ${source.alias}`} disabled={Boolean(source.reference)} value={source.data_type} onChange={(event) => onUpdate(source.id, { data_type: event.target.value as PublisherSourceDataType })}>{dataTypes.map((type) => <option key={type}>{type}</option>)}</select></label>
        <label><span>Unit</span><input aria-label={`Unit for ${source.alias}`} disabled={Boolean(source.reference)} value={source.unit} onChange={(event) => onUpdate(source.id, { unit: event.target.value })} /></label>
        <label><span>Period</span><select aria-label={`Period for ${source.alias}`} disabled={source.kind === "tag" || Boolean(source.reference)} value={source.kind === "tag" ? "instantaneous" : source.period_kind} onChange={(event) => onUpdate(source.id, { period_kind: event.target.value as PublisherSourceDraft["period_kind"] })}><option value="instantaneous">Instantaneous</option><option value="windowed">Windowed</option></select></label>
        <button type="button" aria-label={`Remove source ${source.alias}`} onClick={() => onRemove(source.id)}><Trash2 size={17} /></button>
      </article>)}
    </div>
  </>;
}

export default PublisherSourceSelector;
