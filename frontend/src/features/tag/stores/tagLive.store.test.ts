import { beforeEach, describe, expect, it, vi } from "vitest";

import type { TagRuntimeValue } from "../../../types/tag";
import { MAX_RETAINED_LIVE_TAGS, MAX_RETAINED_VALUES_PER_TAG, useTagLiveStore } from "./tagLive.store";

function runtimeValue(tagId: string, sequence: number, quality: "good" | "bad" = "good"): TagRuntimeValue {
  return {
    tag_id: tagId,
    sequence,
    observed_at: "2026-08-22T00:00:00Z",
    stored_at: "2026-08-22T00:00:00Z",
    quality,
    data_type: "float64",
    value: quality === "good" ? sequence : null,
    ...(quality === "bad" ? { error: "Illegal Data Address" } : {}),
  };
}

describe("Tag live store", () => {
  beforeEach(() => {
    useTagLiveStore.getState().reset();
    vi.useRealTimers();
  });

  it("deduplicates global sequences and exposes per-Tag quality and error metadata", () => {
    vi.setSystemTime(new Date("2026-08-22T00:00:00Z"));
    useTagLiveStore.getState().ingest(runtimeValue("tag-a", 1));
    useTagLiveStore.getState().ingest(runtimeValue("tag-b", 1, "bad"));
    useTagLiveStore.getState().ingest(runtimeValue("tag-b", 2, "bad"));

    const state = useTagLiveStore.getState();
    expect(state.lastSequence).toBe(2);
    expect(state.entries["tag-a"].value.value).toBe(1);
    expect(state.entries["tag-b"].value).toMatchObject({ quality: "bad", error: "Illegal Data Address" });
    expect(state.entries["tag-b"].receivedAt).toBe(Date.parse("2026-08-22T00:00:00Z"));
  });

  it("retains only the most recently updated bounded Tag set", () => {
    for (let index = 1; index <= MAX_RETAINED_LIVE_TAGS + 1; index += 1) {
      useTagLiveStore.getState().ingest(runtimeValue(`tag-${index}`, index));
    }

    const state = useTagLiveStore.getState();
    expect(state.retainedTagIds).toHaveLength(MAX_RETAINED_LIVE_TAGS);
    expect(state.entries["tag-1"]).toBeUndefined();
    expect(state.entries[`tag-${MAX_RETAINED_LIVE_TAGS + 1}`]).toBeDefined();
  });

  it("retains a bounded sequence-ordered live window for each Tag", () => {
    for (let sequence = 1; sequence <= MAX_RETAINED_VALUES_PER_TAG + 2; sequence += 1) {
      useTagLiveStore.getState().ingest(runtimeValue("tag-a", sequence));
    }

    const history = useTagLiveStore.getState().entries["tag-a"].history;
    expect(history).toHaveLength(MAX_RETAINED_VALUES_PER_TAG);
    expect(history.map((value) => value.sequence)).toEqual([3, 4, 5, 6, 7, 8, 9, 10, 11, 12]);
  });

  it("tracks connection lifecycle, manual reconnects, and resets session state", () => {
    useTagLiveStore.getState().setConnectionError("network down");
    useTagLiveStore.getState().setConnectionState("disconnected");
    useTagLiveStore.getState().requestReconnect();
    expect(useTagLiveStore.getState()).toMatchObject({ connectionState: "disconnected", connectionError: "network down", reconnectKey: 1 });

    useTagLiveStore.getState().setConnectionState("live");
    expect(useTagLiveStore.getState().connectionError).toBeNull();
    expect(useTagLiveStore.getState().connectedAt).not.toBeNull();

    useTagLiveStore.getState().reset();
    expect(useTagLiveStore.getState()).toMatchObject({ connectionState: "idle", lastSequence: 0, entries: {}, reconnectKey: 0 });
  });
});
