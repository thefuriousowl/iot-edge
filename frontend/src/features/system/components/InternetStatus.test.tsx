// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { act, cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { getInternetStatus } from "../../../services/health.service";
import InternetStatus from "./InternetStatus";

vi.mock("../../../services/health.service", () => ({
  getInternetStatus: vi.fn(),
}));

const mockedGetInternetStatus = vi.mocked(getInternetStatus);

describe("InternetStatus", () => {
  beforeEach(() => {
    mockedGetInternetStatus.mockReset();
  });

  afterEach(() => {
    cleanup();
    vi.useRealTimers();
  });

  it("shows checking before reporting verified online status and latency", async () => {
    let resolveStatus:
      | ((value: Awaited<ReturnType<typeof getInternetStatus>>) => void)
      | undefined;
    mockedGetInternetStatus.mockReturnValue(
      new Promise((resolve) => {
        resolveStatus = resolve;
      }),
    );

    render(<InternetStatus />);

    expect(screen.getByRole("status", { name: "Internet connection: Checking" })).toBeInTheDocument();
    await act(async () => {
      resolveStatus?.({
        status: "online",
        checked_at: "2026-08-21T06:00:00Z",
        latency_ms: 12.5,
      });
    });

    const indicator = screen.getByRole("status", { name: "Internet connection: Online" });
    expect(indicator).toHaveAttribute("title", "Internet online · 12.50 ms");
  });

  it("reports backend-verified offline and unknown states", async () => {
    mockedGetInternetStatus.mockResolvedValueOnce({
      status: "offline",
      checked_at: "2026-08-21T06:00:00Z",
      latency_ms: null,
    });
    const firstRender = render(<InternetStatus />);

    expect(await screen.findByRole("status", { name: "Internet connection: Offline" })).toHaveAttribute(
      "title",
      "Internet connection unavailable",
    );
    firstRender.unmount();

    mockedGetInternetStatus.mockRejectedValueOnce(new Error("backend unavailable"));
    render(<InternetStatus />);
    expect(await screen.findByRole("status", { name: "Internet connection: Unknown" })).toHaveAttribute(
      "title",
      "Unable to verify internet connection",
    );
  });

  it("responds immediately to browser offline and online events", async () => {
    mockedGetInternetStatus
      .mockResolvedValueOnce({
        status: "online",
        checked_at: "2026-08-21T06:00:00Z",
        latency_ms: 10,
      })
      .mockResolvedValueOnce({
        status: "online",
        checked_at: "2026-08-21T06:01:00Z",
        latency_ms: 8,
      });
    render(<InternetStatus />);
    await screen.findByRole("status", { name: "Internet connection: Online" });

    act(() => window.dispatchEvent(new Event("offline")));
    expect(screen.getByRole("status", { name: "Internet connection: Offline" })).toBeInTheDocument();

    act(() => window.dispatchEvent(new Event("online")));
    expect(screen.getByRole("status", { name: "Internet connection: Checking" })).toBeInTheDocument();
    expect(await screen.findByRole("status", { name: "Internet connection: Online" })).toBeInTheDocument();
    expect(mockedGetInternetStatus).toHaveBeenCalledTimes(2);
  });

  it("polls without replacing the current status with a checking state", async () => {
    vi.useFakeTimers();
    mockedGetInternetStatus
      .mockResolvedValueOnce({
        status: "online",
        checked_at: "2026-08-21T06:00:00Z",
        latency_ms: 10,
      })
      .mockResolvedValueOnce({
        status: "offline",
        checked_at: "2026-08-21T06:00:30Z",
        latency_ms: null,
      });
    render(<InternetStatus />);

    await act(async () => Promise.resolve());
    expect(screen.getByRole("status", { name: "Internet connection: Online" })).toBeInTheDocument();

    await act(async () => {
      await vi.advanceTimersByTimeAsync(30_000);
    });
    expect(screen.getByRole("status", { name: "Internet connection: Offline" })).toBeInTheDocument();
    expect(mockedGetInternetStatus).toHaveBeenCalledTimes(2);
  });
});
