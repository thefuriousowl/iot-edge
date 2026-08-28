// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAuthStore } from "../../../stores/auth.store";
import { expectNoAxeViolations } from "../../../test/axe";
import type { AuthUser } from "../../../types/auth";
import VGatewayShell from "./VGatewayShell";

vi.mock("../../system/components/InternetStatus", () => ({
  default: () => <span>Internet status</span>,
}));

const user: AuthUser = {
  id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
  username: "iot-admin",
  created_at: "2026-08-21T08:00:00Z",
  last_login: null,
};

function renderShell(path = "/vgateways") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <VGatewayShell breadcrumb={<span>Gateway inventory</span>}>
        <h1>Workspace content</h1>
      </VGatewayShell>
    </MemoryRouter>,
  );
}

describe("VGatewayShell", () => {
  beforeEach(() => {
    useAuthStore.getState().clearSession();
  });

  afterEach(() => {
    cleanup();
    vi.unstubAllGlobals();
  });

  it("renders accessible canonical navigation, active location, and authenticated identity", async () => {
    useAuthStore.getState().setSession(user, "test-access-token");
    const { container } = renderShell();

    const navigation = screen.getByRole("navigation", {
      name: "Primary navigation",
    });
    expect(within(navigation).getByRole("link", { name: "vGateways" })).toHaveClass(
      "active",
    );
    expect(within(navigation).getAllByRole("link")).toHaveLength(11);
    expect(within(navigation).getByRole("link", { name: "Assets" })).toHaveAttribute("href", "/assets");
    expect(screen.getByText("Gateway inventory")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Workspace content" })).toBeInTheDocument();
    expect(screen.getByText("iot-admin")).toBeInTheDocument();
    expect(screen.getByText("I")).toBeInTheDocument();
    expect(screen.getByText("Internet status")).toBeInTheDocument();
    await expectNoAxeViolations(container);
  });

  it("moves focus into mobile navigation, closes with Escape, and restores focus", async () => {
    const { container } = renderShell();
    const sidebar = container.querySelector(".vgateway-sidebar");
    const backdrop = container.querySelector(".vgateway-nav-backdrop");
    const openButton = screen.getByRole("button", { name: "Open navigation" });

    expect(sidebar).not.toHaveClass("is-open");
    expect(backdrop).not.toHaveClass("is-open");
    expect(screen.getByText("Admin")).toBeInTheDocument();
    expect(screen.getByText("A")).toBeInTheDocument();

    openButton.focus();
    fireEvent.click(openButton);
    expect(sidebar).toHaveClass("is-open");
    expect(backdrop).toHaveClass("is-open");
    const closeButton = within(sidebar as HTMLElement).getByRole("button", {
      name: "Close navigation",
    });
    await waitFor(() => expect(closeButton).toHaveFocus());

    fireEvent.keyDown(document, { key: "Escape" });
    expect(sidebar).not.toHaveClass("is-open");
    await waitFor(() => expect(openButton).toHaveFocus());

    fireEvent.click(openButton);
    await waitFor(() => expect(closeButton).toHaveFocus());
    fireEvent.click(backdrop as HTMLElement);
    expect(sidebar).not.toHaveClass("is-open");
    expect(backdrop).not.toHaveClass("is-open");
    await waitFor(() => expect(openButton).toHaveFocus());
  });

  it("removes a closed compact drawer from the accessibility tree", async () => {
    vi.stubGlobal("matchMedia", vi.fn().mockReturnValue({
      matches: true,
      media: "(max-width: 980px)",
      onchange: null,
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
      addListener: vi.fn(),
      removeListener: vi.fn(),
      dispatchEvent: vi.fn(),
    }));
    const { container } = renderShell();
    const sidebar = container.querySelector(".vgateway-sidebar");

    await waitFor(() => expect(sidebar).toHaveAttribute("aria-hidden", "true"));
    expect(sidebar).toHaveAttribute("inert");

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    expect(sidebar).not.toHaveAttribute("aria-hidden");
    expect(sidebar).not.toHaveAttribute("inert");
  });
});
