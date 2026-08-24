import { describe, expect, it, vi } from "vitest";

import type { PublisherSourceCatalogEntry, PublisherSourceDraft } from "../../../types/publisher";
import { nextPublisherSourceAlias, publisherSourceDraft, publisherSourceReferenceKey, selectedPublisherSources } from "./shared";

const source: PublisherSourceCatalogEntry = {
  descriptor: {
    reference: { kind: "plugin_output", plugin_instance_id: "plugin-1", output_key: "today.cost" },
    name: "Today Cost (THB)", owner_name: "Energy", schema_version: 1, data_type: "float64",
    unit: "THB", period_kind: "windowed", enabled: true,
  },
  current: { quality: "partial" },
};

describe("shared Publisher source utilities", () => {
  it("creates immutable catalog drafts with deterministic unique aliases", () => {
    vi.stubGlobal("crypto", { randomUUID: () => "source-id" });
    const existing = [{ alias: "today_cost_thb_", reference: { kind: "tag", tag_id: "tag-1" } }] as PublisherSourceDraft[];
    const draft = publisherSourceDraft(source, existing);

    expect(draft).toEqual({
      id: "source-id", alias: "today_cost_thb__2", reference: source.descriptor.reference,
      name: "Today Cost (THB)", owner_name: "Energy", kind: "plugin_output", data_type: "float64",
      unit: "THB", period_kind: "windowed",
    });
    expect(publisherSourceReferenceKey(draft.reference!)).toBe("plugin_output:plugin-1:today.cost");
    vi.unstubAllGlobals();
  });

  it("converts only catalog-backed drafts into save selections", () => {
    const selected = [{ alias: "power", reference: { kind: "tag", tag_id: "tag-1" } }] as PublisherSourceDraft[];
    expect(selectedPublisherSources(selected)).toEqual([{ alias: "power", reference: { kind: "tag", tag_id: "tag-1" } }]);
    expect(selectedPublisherSources([...selected, { alias: "legacy" } as PublisherSourceDraft])).toBeNull();
    expect(nextPublisherSourceAlias("123", [])).toBe("source");
  });
});
