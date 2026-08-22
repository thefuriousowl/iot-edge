// @vitest-environment jsdom

import { act, cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import type { SSEStreamContext } from "../types/sse";
import { type SSEConnectionState, useSSE } from "./useSSE";

interface HarnessProps {
  getLastEventId: () => number | null;
  onMessage: (value: number) => void;
  onStateChange: (state: SSEConnectionState) => void;
  open: (context: SSEStreamContext<number>) => Promise<void>;
  restartKey?: number;
}

function Harness(props: HarnessProps) {
  useSSE({ ...props, enabled: true, retryDelayMs: 20, maxRetryDelayMs: 80 });
  return null;
}

describe("useSSE", () => {
  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("opens with the current cursor, forwards messages, and aborts on cleanup", async () => {
    let context: SSEStreamContext<number> | undefined;
    const open = vi.fn(async (next: SSEStreamContext<number>) => {
      context = next;
      next.onOpen();
      await new Promise<void>((resolve) => next.signal.addEventListener("abort", () => resolve(), { once: true }));
    });
    const onMessage = vi.fn();
    const onStateChange = vi.fn();
    const view = render(<Harness getLastEventId={() => 41} open={open} onMessage={onMessage} onStateChange={onStateChange} />);

    await waitFor(() => expect(open).toHaveBeenCalledOnce());
    expect(context?.lastEventId).toBe(41);
    expect(onStateChange).toHaveBeenCalledWith("connecting");
    expect(onStateChange).toHaveBeenCalledWith("live");
    act(() => context?.onMessage(42));
    expect(onMessage).toHaveBeenCalledWith(42);

    view.unmount();
    expect(context?.signal.aborted).toBe(true);
  });

  it("uses the server retry delay and resumes from the latest cursor", async () => {
    vi.useFakeTimers();
    let cursor = 4;
    const contexts: SSEStreamContext<number>[] = [];
    const open = vi.fn(async (context: SSEStreamContext<number>) => {
      contexts.push(context);
      if (contexts.length === 1) {
        context.onOpen();
        context.onRetry(25);
        context.onMessage(5);
        cursor = 5;
      } else {
        await new Promise<void>((resolve) => context.signal.addEventListener("abort", () => resolve(), { once: true }));
      }
    });
    const onStateChange = vi.fn();
    render(<Harness getLastEventId={() => cursor} open={open} onMessage={vi.fn()} onStateChange={onStateChange} />);

    await act(async () => {});
    expect(open).toHaveBeenCalledOnce();
    expect(contexts[0].lastEventId).toBe(4);
    expect(onStateChange).toHaveBeenCalledWith("disconnected");

    await act(async () => vi.advanceTimersByTimeAsync(25));
    expect(open).toHaveBeenCalledTimes(2);
    expect(contexts[1].lastEventId).toBe(5);
    expect(onStateChange).toHaveBeenCalledWith("reconnecting");
  });

  it("restarts immediately when the restart key changes", async () => {
    const contexts: SSEStreamContext<number>[] = [];
    const open = vi.fn(async (context: SSEStreamContext<number>) => {
      contexts.push(context);
      await new Promise<void>((resolve) => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    });
    const props = { getLastEventId: () => null, open, onMessage: vi.fn(), onStateChange: vi.fn() };
    const view = render(<Harness {...props} restartKey={0} />);
    await waitFor(() => expect(open).toHaveBeenCalledOnce());

    view.rerender(<Harness {...props} restartKey={1} />);
    await waitFor(() => expect(open).toHaveBeenCalledTimes(2));

    expect(contexts[0].signal.aborted).toBe(true);
    expect(contexts[1].signal.aborted).toBe(false);
  });
});
