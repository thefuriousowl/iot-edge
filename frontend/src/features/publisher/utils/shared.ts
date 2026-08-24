import type {
  PublisherSourceCatalogEntry,
  PublisherSourceDraft,
  PublisherSourceReference,
  PublisherSourceSelection,
} from "../../../types/publisher";

export function publisherSourceReferenceKey(reference: PublisherSourceReference): string {
  return reference.kind === "tag"
    ? `tag:${reference.tag_id}`
    : `plugin_output:${reference.plugin_instance_id}:${reference.output_key}`;
}

export function selectedPublisherSources(sources: PublisherSourceDraft[]): PublisherSourceSelection[] | null {
  if (sources.some((source) => !source.reference)) return null;
  return sources.map((source) => ({ alias: source.alias, reference: source.reference! }));
}

export function nextPublisherSourceAlias(name: string, existing: PublisherSourceDraft[]): string {
  const base = name.trim().toLowerCase().replace(/[^a-z0-9_.-]+/g, "_").replace(/^[^a-z_]+/, "") || "source";
  let candidate = base.slice(0, 64);
  let suffix = 2;
  while (existing.some((source) => source.alias === candidate)) {
    const ending = `_${suffix}`;
    candidate = `${base.slice(0, 64 - ending.length)}${ending}`;
    suffix += 1;
  }
  return candidate;
}

export function publisherSourceDraft(entry: PublisherSourceCatalogEntry, existing: PublisherSourceDraft[]): PublisherSourceDraft {
  const { descriptor } = entry;
  return {
    id: crypto.randomUUID(),
    alias: nextPublisherSourceAlias(descriptor.name, existing),
    reference: descriptor.reference,
    name: descriptor.name,
    owner_name: descriptor.owner_name ?? "Core",
    kind: descriptor.reference.kind,
    data_type: descriptor.data_type,
    unit: descriptor.unit ?? "",
    period_kind: descriptor.period_kind,
  };
}
