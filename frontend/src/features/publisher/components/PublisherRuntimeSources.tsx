import { Activity, Clock3 } from "lucide-react";

import type { PublisherRuntimeSource } from "../../../types/publisher";

interface PublisherRuntimeSourcesProps {
  sources?: PublisherRuntimeSource[];
}

function displayTime(value?: string): string {
  if (!value) return "Unavailable";
  const date = new Date(value);
  return Number.isNaN(date.valueOf()) ? value : date.toLocaleString();
}

function sourceIdentity(source: PublisherRuntimeSource): string {
  if (source.reference.kind === "tag") return `Tag · ${source.reference.tag_id}`;
  return `Plugin output · ${source.reference.output_key}`;
}

function PublisherRuntimeSources({ sources = [] }: PublisherRuntimeSourcesProps) {
  return <section className="publisher-runtime-sources" aria-label="Runtime source provenance">
    <header><div><h3>Runtime source provenance</h3><p>The latest trigger snapshot keeps each source’s own observation or period timestamps.</p></div><span>{sources.length} sources</span></header>
    {sources.length === 0
      ? <div className="publisher-runtime-sources-empty"><Activity size={20} /><strong>No runtime source snapshot</strong><span>Enable the Publisher and wait for its first trigger snapshot.</span></div>
      : <div className="publisher-runtime-source-grid">{sources.map((source) => {
          const period = source.period_start && source.period_end && source.period_start !== source.period_end;
          const observedAt = source.observed_at ?? (!period ? source.period_end : undefined);
          return <article key={source.alias}>
            <header><div><strong>{source.alias}</strong><small title={sourceIdentity(source)}>{sourceIdentity(source)}</small></div><span className={`mqtt-source-quality is-${source.available ? source.quality : "unavailable"}`}>{source.available ? source.quality : "unavailable"}</span></header>
            <dl><div><dt>Sequence</dt><dd>{source.sequence ?? "Unavailable"}</dd></div>{source.coverage_percent !== undefined && <div><dt>Coverage</dt><dd>{source.coverage_percent.toFixed(source.coverage_percent % 1 === 0 ? 0 : 1)}%</dd></div>}</dl>
            <p><Clock3 size={14} />{period
              ? <span aria-label={`Period from ${source.period_start} to ${source.period_end}`}>{displayTime(source.period_start)} → {displayTime(source.period_end)}</span>
              : <span aria-label={observedAt ? `Observed at ${observedAt}` : "Observation unavailable"}>{observedAt ? `Observed ${displayTime(observedAt)}` : "Observation unavailable"}</span>}</p>
          </article>;
        })}</div>}
  </section>;
}

export default PublisherRuntimeSources;
