// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { useAuthStore } from "../../stores/auth.store";
import type { AuthUser } from "../../types/auth";
import AuthRoute from "./AuthRoute";

const user: AuthUser = {
  id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
  username: "admin",
  created_at: "2026-08-21T08:00:00Z",
  last_login: null,
};

function renderAuthRoute(mode: "entry" | "login" | "setup", path = "/") {
  return render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route
          path={path}
          element={
            <AuthRoute mode={mode}>
              <p>Requested auth page</p>
            </AuthRoute>
          }
        />
        <Route path="/dashboard" element={<p>Dashboard reached</p>} />
        <Route path="/login" element={<p>Login reached</p>} />
        <Route path="/setup" element={<p>Setup reached</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("AuthRoute", () => {
  beforeEach(() => {
    useAuthStore.getState().clearSession();
    useAuthStore.setState({
      isLoading: false,
      setupRequired: null,
    });
  });

  afterEach(() => {
    cleanup();
  });

  it("shows session loading before choosing an auth route", () => {
    useAuthStore.setState({ isInitialized: false, isLoading: true });

    renderAuthRoute("entry");

    expect(screen.getByRole("status")).toHaveTextContent(
      "Checking your session…",
    );
  });

  it("redirects the entry route to setup when setup is required", () => {
    useAuthStore.setState({ setupRequired: true });

    renderAuthRoute("entry");

    expect(screen.getByText("Setup reached")).toBeInTheDocument();
  });

  it("renders the setup page only while setup is required", () => {
    useAuthStore.setState({ setupRequired: true });

    renderAuthRoute("setup", "/setup");

    expect(screen.getByText("Requested auth page")).toBeInTheDocument();
  });

  it("redirects setup to login after setup is complete", () => {
    useAuthStore.setState({ setupRequired: false });

    renderAuthRoute("setup", "/setup");

    expect(screen.getByText("Login reached")).toBeInTheDocument();
  });

  it("redirects an authenticated visitor away from login", () => {
    useAuthStore.getState().setSession(user, "test-access-token");
    useAuthStore.setState({ setupRequired: false });

    renderAuthRoute("login", "/login");

    expect(screen.getByText("Dashboard reached")).toBeInTheDocument();
  });
});
