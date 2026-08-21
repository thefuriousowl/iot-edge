// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import { cleanup, render, screen } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it } from "vitest";
import {
  MemoryRouter,
  Route,
  Routes,
  useLocation,
} from "react-router-dom";

import { useAuthStore } from "../../stores/auth.store";
import type { AuthUser } from "../../types/auth";
import ProtectedRoute from "./ProtectedRoute";

const user: AuthUser = {
  id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
  username: "admin",
  created_at: "2026-08-21T08:00:00Z",
  last_login: null,
};

function LoginDestination() {
  const location = useLocation();
  const from = (location.state as { from?: Location } | null)?.from;

  return (
    <p>
      Login reached
      {from ? ` from ${from.pathname}` : ""}
    </p>
  );
}

function renderProtectedRoute() {
  return render(
    <MemoryRouter initialEntries={["/dashboard"]}>
      <Routes>
        <Route
          path="/dashboard"
          element={
            <ProtectedRoute>
              <p>Protected content</p>
            </ProtectedRoute>
          }
        />
        <Route path="/login" element={<LoginDestination />} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("ProtectedRoute", () => {
  beforeEach(() => {
    useAuthStore.getState().clearSession();
  });

  afterEach(() => {
    cleanup();
  });

  it("shows a session loading state before deciding where to route", () => {
    useAuthStore.setState({ isInitialized: false, isLoading: true });

    renderProtectedRoute();

    expect(screen.getByRole("status")).toHaveTextContent(
      "Checking your session…",
    );
    expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
    expect(screen.queryByText(/Login reached/)).not.toBeInTheDocument();
  });

  it("redirects an anonymous visitor to login and preserves the destination", () => {
    renderProtectedRoute();

    expect(screen.getByText("Login reached from /dashboard")).toBeInTheDocument();
    expect(screen.queryByText("Protected content")).not.toBeInTheDocument();
  });

  it("renders protected content for an authenticated user", () => {
    useAuthStore.getState().setSession(user, "test-access-token");

    renderProtectedRoute();

    expect(screen.getByText("Protected content")).toBeInTheDocument();
    expect(screen.queryByText(/Login reached/)).not.toBeInTheDocument();
  });
});
