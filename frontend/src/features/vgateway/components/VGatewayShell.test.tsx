// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { useAuthStore } from "../../../stores/auth.store";
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
  });

  it("renders canonical navigation, active location, and authenticated identity", () => {
    useAuthStore.getState().setSession(user, "test-access-token");
    renderShell();

    const navigation = screen.getByRole("navigation", {
      name: "Primary navigation",
    });
    expect(within(navigation).getByRole("link", { name: "vGateways" })).toHaveClass(
      "active",
    );
    expect(within(navigation).getAllByRole("link")).toHaveLength(10);
    expect(screen.getByText("Gateway inventory")).toBeInTheDocument();
    expect(screen.getByRole("heading", { name: "Workspace content" })).toBeInTheDocument();
    expect(screen.getByText("iot-admin")).toBeInTheDocument();
    expect(screen.getByText("I")).toBeInTheDocument();
    expect(screen.getByText("Internet status")).toBeInTheDocument();
  });

  it("opens and closes mobile navigation through both controls", () => {
    const { container } = renderShell();
    const sidebar = container.querySelector(".vgateway-sidebar");
    const backdrop = container.querySelector(".vgateway-nav-backdrop");

    expect(sidebar).not.toHaveClass("is-open");
    expect(backdrop).not.toHaveClass("is-open");
    expect(screen.getByText("Admin")).toBeInTheDocument();
    expect(screen.getByText("A")).toBeInTheDocument();

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    expect(sidebar).toHaveClass("is-open");
    expect(backdrop).toHaveClass("is-open");

    fireEvent.click(within(sidebar as HTMLElement).getByRole("button", {
      name: "Close navigation",
    }));
    expect(sidebar).not.toHaveClass("is-open");

    fireEvent.click(screen.getByRole("button", { name: "Open navigation" }));
    fireEvent.click(backdrop as HTMLElement);
    expect(sidebar).not.toHaveClass("is-open");
    expect(backdrop).not.toHaveClass("is-open");
  });
});
