import { useEffect } from "react";

import { useSSE } from "../../../hooks/useSSE";
import { monitorAllTagValues } from "../../../services/tag.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { TagRuntimeValue } from "../../../types/tag";
import { useTagLiveStore } from "../stores/tagLive.store";

function streamErrorMessage(error: unknown): string {
  return error instanceof Error ? error.message : "Tag live stream disconnected";
}

function TagLiveStream() {
  const enabled = useAuthStore((state) => state.isAuthenticated && Boolean(state.accessToken));
  const reconnectKey = useTagLiveStore((state) => state.reconnectKey);

  useSSE<TagRuntimeValue>({
    enabled,
    restartKey: reconnectKey,
    getLastEventId: () => useTagLiveStore.getState().lastSequence || null,
    open: monitorAllTagValues,
    onMessage: (value) => useTagLiveStore.getState().ingest(value),
    onStateChange: (state) => useTagLiveStore.getState().setConnectionState(state),
    onError: (error) => useTagLiveStore.getState().setConnectionError(streamErrorMessage(error)),
  });

  useEffect(() => {
    if (!enabled) useTagLiveStore.getState().reset();
  }, [enabled]);

  return null;
}

export default TagLiveStream;
