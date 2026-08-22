// @vitest-environment jsdom

import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { monitorAllTagValues } from "../../../services/tag.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { AuthUser } from "../../../types/auth";
import type { SSEStreamContext } from "../../../types/sse";
import type { TagRuntimeValue } from "../../../types/tag";
import { useTagLiveStore } from "../stores/tagLive.store";
import TagLiveStream from "./TagLiveStream";

vi.mock("../../../services/tag.service", () => ({ monitorAllTagValues: vi.fn() }));

const mockedMonitor = vi.mocked(monitorAllTagValues);
const user: AuthUser = { id: "11111111-1111-4111-8111-111111111111", username: "admin", created_at: "2026-08-22T00:00:00Z", last_login: null };
const value: TagRuntimeValue = { tag_id: "tag-a", sequence: 8, observed_at: "2026-08-22T00:00:00Z", stored_at: "2026-08-22T00:00:00Z", quality: "good", data_type: "float64", value: 42 };

describe("TagLiveStream", () => {
  const contexts: SSEStreamContext<TagRuntimeValue>[] = [];

  beforeEach(() => {
    contexts.length = 0;
    mockedMonitor.mockReset();
    mockedMonitor.mockImplementation(async (context) => {
      contexts.push(context);
      await new Promise<void>((resolve) => context.signal.addEventListener("abort", () => resolve(), { once: true }));
    });
    useTagLiveStore.getState().reset();
    useAuthStore.setState({ user, accessToken: "token", isAuthenticated: true, isInitialized: true, isLoading: false });
  });

  afterEach(() => {
    cleanup();
    useTagLiveStore.getState().reset();
    useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false });
  });

  it("owns one authenticated global stream, publishes values, and resumes after manual reconnect", async () => {
    render(<TagLiveStream />);
    await waitFor(() => expect(mockedMonitor).toHaveBeenCalledOnce());
    expect(contexts[0].lastEventId).toBeNull();

    contexts[0].onOpen();
    contexts[0].onMessage(value);
    expect(useTagLiveStore.getState()).toMatchObject({ connectionState: "live", lastSequence: 8 });
    expect(useTagLiveStore.getState().entries["tag-a"].value).toEqual(value);

    useTagLiveStore.getState().requestReconnect();
    await waitFor(() => expect(mockedMonitor).toHaveBeenCalledTimes(2));
    expect(contexts[0].signal.aborted).toBe(true);
    expect(contexts[1].lastEventId).toBe(8);
  });

  it("does not connect anonymously and clears retained values on logout", async () => {
    useAuthStore.setState({ user: null, accessToken: null, isAuthenticated: false });
    useTagLiveStore.getState().ingest(value);
    render(<TagLiveStream />);

    await waitFor(() => expect(useTagLiveStore.getState().lastSequence).toBe(0));
    expect(mockedMonitor).not.toHaveBeenCalled();
  });
});
