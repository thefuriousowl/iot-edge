// @vitest-environment jsdom

import { cleanup, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import { useAuthStore } from "../../stores/auth.store";
import AuthInitializer from "./AuthInitializer";

afterEach(() => {
  cleanup();
});

describe("AuthInitializer", () => {
  it("initializes the shared authentication session after mounting", async () => {
    const originalInitializeSession = useAuthStore.getState().initializeSession;
    const initializeSession = vi.fn().mockResolvedValue(undefined);
    useAuthStore.setState({ initializeSession });

    try {
      render(<AuthInitializer />);

      await waitFor(() => {
        expect(initializeSession).toHaveBeenCalledOnce();
      });
    } finally {
      useAuthStore.setState({ initializeSession: originalInitializeSession });
    }
  });
});
