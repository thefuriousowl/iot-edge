import { useEffect, useRef } from "react";

import type { SSEStreamContext } from "../types/sse";

export type SSEConnectionState =
  | "idle"
  | "connecting"
  | "live"
  | "reconnecting"
  | "disconnected";

interface UseSSEOptions<Value> {
  enabled: boolean;
  getLastEventId: () => number | null;
  open: (context: SSEStreamContext<Value>) => Promise<void>;
  onMessage: (value: Value) => void;
  onStateChange?: (state: SSEConnectionState) => void;
  onError?: (error: unknown) => void;
  retryDelayMs?: number;
  maxRetryDelayMs?: number;
  restartKey?: number;
}

function positiveDelay(value: number, fallback: number): number {
  return Number.isFinite(value) && value >= 0 ? value : fallback;
}

export function useSSE<Value>({
  enabled,
  getLastEventId,
  open,
  onMessage,
  onStateChange,
  onError,
  retryDelayMs = 3_000,
  maxRetryDelayMs = 30_000,
  restartKey = 0,
}: UseSSEOptions<Value>): void {
  const callbacks = useRef({ getLastEventId, open, onMessage, onStateChange, onError });

  useEffect(() => {
    callbacks.current = { getLastEventId, open, onMessage, onStateChange, onError };
  }, [getLastEventId, onError, onMessage, onStateChange, open]);

  useEffect(() => {
    if (!enabled) {
      callbacks.current.onStateChange?.("idle");
      return;
    }

    let stopped = false;
    let activeController: AbortController | null = null;
    let retryTimer: ReturnType<typeof setTimeout> | null = null;
    const baseDelay = positiveDelay(retryDelayMs, 3_000);
    const maximumDelay = Math.max(baseDelay, positiveDelay(maxRetryDelayMs, 30_000));

    const waitForRetry = (milliseconds: number) => new Promise<void>((resolve) => {
      retryTimer = setTimeout(resolve, milliseconds);
    });

    void (async () => {
      let firstAttempt = true;
      let nextDelay = baseDelay;

      while (!stopped) {
        callbacks.current.onStateChange?.(firstAttempt ? "connecting" : "reconnecting");
        activeController = new AbortController();
        let opened = false;

        try {
          await callbacks.current.open({
            signal: activeController.signal,
            lastEventId: callbacks.current.getLastEventId(),
            onOpen: () => {
              opened = true;
              callbacks.current.onStateChange?.("live");
            },
            onMessage: (value) => callbacks.current.onMessage(value),
            onRetry: (milliseconds) => {
              nextDelay = Math.min(positiveDelay(milliseconds, baseDelay), maximumDelay);
            },
          });
        } catch (error: unknown) {
          if (activeController.signal.aborted || stopped) return;
          callbacks.current.onError?.(error);
        }

        if (stopped) return;

        callbacks.current.onStateChange?.("disconnected");
        await waitForRetry(nextDelay);
        retryTimer = null;
        if (stopped) return;

        firstAttempt = false;
        nextDelay = opened
          ? baseDelay
          : Math.min(Math.max(nextDelay, baseDelay) * 2, maximumDelay);
      }
    })();

    return () => {
      stopped = true;
      activeController?.abort();
      if (retryTimer) clearTimeout(retryTimer);
    };
  }, [enabled, maxRetryDelayMs, restartKey, retryDelayMs]);
}
