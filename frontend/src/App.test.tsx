// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";

import App from "./App";
import { getHealth } from "./services/health.service";

vi.mock("./services/health.service", () => ({
  getHealth: vi.fn(),
}));

const mockedGetHealth = vi.mocked(getHealth);

describe("App", () => {
  afterEach(() => {
    cleanup();
    vi.resetAllMocks();
  });

  it("shows a loading state while checking the backend", () => {
    mockedGetHealth.mockReturnValue(new Promise(() => {}));

    render(<App />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Checking backend connection",
    );
  });

  it("shows backend status and version when the health check succeeds", async () => {
    mockedGetHealth.mockResolvedValue({
      status: "ok",
      version: "0.1.0",
    });

    render(<App />);

    expect(await screen.findByText("Backend connected")).toBeInTheDocument();
    expect(screen.getByText("ok")).toBeInTheDocument();
    expect(screen.getByText("0.1.0")).toBeInTheDocument();
  });

  it("shows an error state when the health check fails", async () => {
    mockedGetHealth.mockRejectedValue(new Error("connection failed"));

    render(<App />);

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Backend unavailable",
    );
  });
});
