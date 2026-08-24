import type {
  PublisherSourceCatalogEntry,
  PublisherSourceDraft,
  PublisherSourceReference,
  PublisherSourceSelection,
} from "../../../types/publisher";

let fallbackDraftIDSequence = 0;

export function createPublisherDraftID(): string {
  const webCrypto = globalThis.crypto as Crypto | undefined;
  if (typeof webCrypto?.randomUUID === "function") return webCrypto.randomUUID();

  if (typeof webCrypto?.getRandomValues === "function") {
    const bytes = webCrypto.getRandomValues(new Uint8Array(16));
    bytes[6] = (bytes[6] & 0x0f) | 0x40;
    bytes[8] = (bytes[8] & 0x3f) | 0x80;
    const hex = Array.from(bytes, (byte) => byte.toString(16).padStart(2, "0"));
    return `${hex.slice(0, 4).join("")}-${hex.slice(4, 6).join("")}-${hex.slice(6, 8).join("")}-${hex.slice(8, 10).join("")}-${hex.slice(10).join("")}`;
  }

  fallbackDraftIDSequence += 1;
  return `publisher-draft-${Date.now().toString(36)}-${fallbackDraftIDSequence.toString(36)}`;
}

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
    id: createPublisherDraftID(),
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
