import { create } from "zustand";

import type { SSEConnectionState } from "../../../hooks/useSSE";
import type { TagRuntimeValue } from "../../../types/tag";

export const MAX_RETAINED_LIVE_TAGS = 1_000;
export const MAX_RETAINED_VALUES_PER_TAG = 10;

export interface TagLiveEntry {
  value: TagRuntimeValue;
  history: TagRuntimeValue[];
  receivedAt: number;
}

interface TagLiveState {
  connectionState: SSEConnectionState;
  connectionError: string | null;
  connectedAt: number | null;
  lastEventAt: number | null;
  lastSequence: number;
  entries: Record<string, TagLiveEntry>;
  retainedTagIds: string[];
  reconnectKey: number;
  ingest: (value: TagRuntimeValue) => void;
  requestReconnect: () => void;
  reset: () => void;
  setConnectionError: (message: string | null) => void;
  setConnectionState: (state: SSEConnectionState) => void;
}

const initialState = {
  connectionState: "idle" as const,
  connectionError: null,
  connectedAt: null,
  lastEventAt: null,
  lastSequence: 0,
  entries: {} as Record<string, TagLiveEntry>,
  retainedTagIds: [] as string[],
  reconnectKey: 0,
};

const emptyTagHistory: TagRuntimeValue[] = [];

export const useTagLiveStore = create<TagLiveState>((set) => ({
  ...initialState,

  ingest: (value) => set((state) => {
    if (!Number.isSafeInteger(value.sequence) || value.sequence <= state.lastSequence) {
      return state;
    }

    const receivedAt = Date.now();
    const previousHistory = state.entries[value.tag_id]?.history ?? [];
    const history = [...previousHistory, value].slice(-MAX_RETAINED_VALUES_PER_TAG);
    const entries = { ...state.entries, [value.tag_id]: { value, history, receivedAt } };
    const retainedTagIds = state.retainedTagIds.filter((tagId) => tagId !== value.tag_id);
    retainedTagIds.push(value.tag_id);

    while (retainedTagIds.length > MAX_RETAINED_LIVE_TAGS) {
      const removedTagId = retainedTagIds.shift();
      if (removedTagId) delete entries[removedTagId];
    }

    return {
      entries,
      retainedTagIds,
      lastSequence: value.sequence,
      lastEventAt: receivedAt,
      connectionError: null,
    };
  }),

  requestReconnect: () => set((state) => ({ reconnectKey: state.reconnectKey + 1 })),

  reset: () => set(initialState),

  setConnectionError: (connectionError) => set({ connectionError }),

  setConnectionState: (connectionState) => set(connectionState === "live"
    ? { connectionState, connectedAt: Date.now(), connectionError: null }
    : { connectionState }),
}));

export function useTagLiveValue(tagId: string): TagRuntimeValue | null {
  return useTagLiveStore((state) => state.entries[tagId]?.value ?? null);
}

export function useTagLiveHistory(tagId: string): TagRuntimeValue[] {
  return useTagLiveStore((state) => state.entries[tagId]?.history ?? emptyTagHistory);
}
