// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";

import {
  cleanup,
  fireEvent,
  render,
  screen,
  waitFor,
} from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { MemoryRouter, Route, Routes } from "react-router-dom";

import { getMe, login } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import LoginPage from "./LoginPage";

vi.mock("../../../services/auth.service", () => ({
  getMe: vi.fn(),
  login: vi.fn(),
}));

const mockedLogin = vi.mocked(login);
const mockedGetMe = vi.mocked(getMe);

function renderLoginPage() {
  return render(
    <MemoryRouter initialEntries={["/login"]}>
      <Routes>
        <Route path="/login" element={<LoginPage />} />
        <Route path="/dashboard" element={<p>Dashboard reached</p>} />
      </Routes>
    </MemoryRouter>,
  );
}

describe("LoginPage", () => {
  beforeEach(() => {
    mockedLogin.mockReset();
    mockedGetMe.mockReset();
    useAuthStore.getState().clearSession();
  });

  afterEach(() => {
    cleanup();
  });

  it("renders the required login controls", () => {
    renderLoginPage();

    expect(
      screen.getByRole("heading", { name: "Welcome back" }),
    ).toBeInTheDocument();
    expect(screen.getByLabelText("Username")).toHaveAttribute(
      "autocomplete",
      "username",
    );
    expect(screen.getByLabelText("Password")).toHaveAttribute(
      "autocomplete",
      "current-password",
    );
    expect(screen.getByRole("button", { name: "Sign in" })).toBeEnabled();
  });

  it("validates credentials before calling the API", async () => {
    renderLoginPage();

    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Username is required")).toBeInTheDocument();
    expect(screen.getByText("Password is required")).toBeInTheDocument();
    expect(mockedLogin).not.toHaveBeenCalled();
  });

  it("toggles password visibility", () => {
    renderLoginPage();
    const password = screen.getByLabelText("Password");

    expect(password).toHaveAttribute("type", "password");

    fireEvent.click(screen.getByRole("button", { name: "Show password" }));

    expect(password).toHaveAttribute("type", "text");
    expect(
      screen.getByRole("button", { name: "Hide password" }),
    ).toHaveAttribute("aria-pressed", "true");
  });

  it("creates a session and redirects after successful login", async () => {
    mockedLogin.mockResolvedValue({
      access_token: "test-access-token",
      token_type: "Bearer",
      expires_in: 900,
    });
    mockedGetMe.mockResolvedValue({
      id: "4dd34e70-cae3-4ae9-871f-e4a4a796b750",
      username: "admin",
      created_at: "2026-08-21T08:00:00Z",
      last_login: "2026-08-21T09:00:00Z",
    });
    renderLoginPage();

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "SecureP@ss123" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByText("Dashboard reached")).toBeInTheDocument();
    expect(mockedLogin).toHaveBeenCalledWith({
      username: "admin",
      password: "SecureP@ss123",
    });
    expect(mockedGetMe).toHaveBeenCalledOnce();
    expect(useAuthStore.getState()).toMatchObject({
      accessToken: "test-access-token",
      isAuthenticated: true,
      isLoading: false,
      user: {
        username: "admin",
      },
    });
  });

  it("clears partial session state and shows an API error", async () => {
    mockedLogin.mockRejectedValue(new Error("network unavailable"));
    renderLoginPage();

    fireEvent.change(screen.getByLabelText("Username"), {
      target: { value: "admin" },
    });
    fireEvent.change(screen.getByLabelText("Password"), {
      target: { value: "wrong-password" },
    });
    fireEvent.click(screen.getByRole("button", { name: "Sign in" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(
      "Unable to sign in. Check your connection and try again.",
    );
    await waitFor(() => {
      expect(useAuthStore.getState()).toMatchObject({
        accessToken: null,
        isAuthenticated: false,
        isLoading: false,
        user: null,
      });
    });
  });
});
