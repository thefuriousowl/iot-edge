// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";

import SessionLoading from "./SessionLoading";

afterEach(() => {
  cleanup();
});

describe("SessionLoading", () => {
  it("announces the pending session check without exposing its icon", () => {
    const { container } = render(<SessionLoading />);

    expect(screen.getByRole("status")).toHaveTextContent(
      "Checking your session…",
    );
    expect(container.querySelector("svg")).toHaveAttribute("aria-hidden", "true");
  });
});
