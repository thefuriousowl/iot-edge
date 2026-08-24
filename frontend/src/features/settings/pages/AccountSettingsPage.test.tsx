// @vitest-environment jsdom

import "@testing-library/jest-dom/vitest";
import { act, cleanup, fireEvent, render, screen, waitFor } from "@testing-library/react";
import { MemoryRouter, Route, Routes, useLocation } from "react-router-dom";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

import { changePassword } from "../../../services/auth.service";
import { useAuthStore } from "../../../stores/auth.store";
import type { ChangePasswordResponse } from "../../../types/auth";
import AccountSettingsPage from "./AccountSettingsPage";

vi.mock("../../../services/auth.service", () => ({ changePassword: vi.fn() }));
vi.mock("../../system/components/InternetStatus", () => ({ default: () => <span>Internet status</span> }));

const mockedChangePassword = vi.mocked(changePassword);

function LoginResult() {
  const location = useLocation();
  return <p>Login reached {JSON.stringify(location.state)}</p>;
}

function renderPage() {
  return render(
    <MemoryRouter initialEntries={["/settings/account"]}>
      <Routes>
        <Route path="/settings/account" element={<AccountSettingsPage />} />
        <Route path="/login" element={<LoginResult />} />
      </Routes>
    </MemoryRouter>,
  );
}

function fillValidForm() {
  fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "CurrentP@ss1" } });
  fireEvent.change(screen.getByLabelText("New password"), { target: { value: "FreshP@ss3" } });
  fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "FreshP@ss3" } });
}

function apiError(code: string, message: string) {
  return { isAxiosError: true, response: { data: { error: { code, message } } } };
}

describe("AccountSettingsPage", () => {
  beforeEach(() => {
    mockedChangePassword.mockReset();
    useAuthStore.setState({
      user: { id: "user-1", username: "admin", created_at: "2026-08-21T08:00:00Z", last_login: "2026-08-24T07:00:00Z" },
      accessToken: "token",
      isAuthenticated: true,
      isInitialized: true,
      isLoading: false,
      setupRequired: false,
    });
  });
  afterEach(() => cleanup());

  it("shows the protected profile, re-authentication warning, and password controls", () => {
    renderPage();

    expect(screen.getByRole("heading", { name: "admin" })).toBeInTheDocument();
    expect(screen.getByText("Fresh login required")).toBeInTheDocument();
    expect(screen.getByLabelText("Current password")).toHaveAttribute("autocomplete", "current-password");
    expect(screen.getByLabelText("New password")).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByLabelText("Confirm new password")).toHaveAttribute("autocomplete", "new-password");
    expect(screen.getByText(/cannot match your current or two previous passwords/i)).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /Account/ })).toHaveClass("active");
  });

  it("validates required fields before calling the API", async () => {
    renderPage();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText("Current password is required")).toBeInTheDocument();
    expect(screen.getByText("New password is required")).toBeInTheDocument();
    expect(screen.getByText("Confirm password is required")).toBeInTheDocument();
    expect(mockedChangePassword).not.toHaveBeenCalled();
  });

  it("rejects weak, matching-current, mismatched, and username-containing passwords", async () => {
    renderPage();
    fireEvent.change(screen.getByLabelText("Current password"), { target: { value: "CurrentP@ss1" } });
    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "password" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "different" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("New password must contain at least 3 character types")).toBeInTheDocument();
    expect(screen.getByText("Passwords do not match")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "CurrentP@ss1" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "CurrentP@ss1" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("New password must be different from current password")).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText("New password"), { target: { value: "Admin@123Secure" } });
    fireEvent.change(screen.getByLabelText("Confirm new password"), { target: { value: "Admin@123Secure" } });
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));
    expect(await screen.findByText("New password must not contain your username")).toBeInTheDocument();
    expect(mockedChangePassword).not.toHaveBeenCalled();
  });

  it("updates shared requirements and independently toggles all password fields", () => {
    renderPage();
    fillValidForm();

    expect(screen.getByText("8–128 characters").closest("li")).toHaveClass("is-valid");
    expect(screen.getByText("At least 3 of: uppercase, lowercase, number, special character").closest("li")).toHaveClass("is-valid");
    expect(screen.getByText("Does not contain your username").closest("li")).toHaveClass("is-valid");

    for (const label of ["Current password", "New password", "Confirm new password"]) {
      const input = screen.getByLabelText(label);
      fireEvent.click(screen.getByRole("button", { name: `Show ${label.toLocaleLowerCase()}` }));
      expect(input).toHaveAttribute("type", "text");
      expect(screen.getByRole("button", { name: `Hide ${label.toLocaleLowerCase()}` })).toHaveAttribute("aria-pressed", "true");
    }
  });

  it("submits the strict request once, clears the session, and forces Login", async () => {
    mockedChangePassword.mockResolvedValue({ message: "Password changed successfully" });
    renderPage();
    fillValidForm();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText(/Login reached.*passwordChanged/i)).toBeInTheDocument();
    expect(mockedChangePassword).toHaveBeenCalledWith({ current_password: "CurrentP@ss1", new_password: "FreshP@ss3", confirm_password: "FreshP@ss3" });
    expect(mockedChangePassword).toHaveBeenCalledOnce();
    expect(useAuthStore.getState()).toMatchObject({ user: null, accessToken: null, isAuthenticated: false });
  });

  it.each([
    ["AUTH001", "Invalid credentials", "Current password is incorrect."],
    ["AUTH002", "Account locked", "Account is locked. Sign in again after the lock expires."],
    ["AUTH006", "Password requirements not met", "The new password does not meet the password requirements."],
    ["AUTH007", "Password recently used", "This password was used recently. Choose a different password."],
  ])("maps %s safely and clears all entered password material", async (code, backendMessage, expectedMessage) => {
    mockedChangePassword.mockRejectedValue(apiError(code, backendMessage));
    renderPage();
    fillValidForm();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByRole("alert")).toHaveTextContent(expectedMessage);
    expect(screen.getByLabelText("Current password")).toHaveValue("");
    expect(screen.getByLabelText("New password")).toHaveValue("");
    expect(screen.getByLabelText("Confirm new password")).toHaveValue("");
    expect(screen.queryByText("Login reached", { exact: false })).not.toBeInTheDocument();
  });

  it("sanitizes unknown failures without leaking exception text", async () => {
    mockedChangePassword.mockRejectedValue(new Error("password=must-not-leak"));
    renderPage();
    fillValidForm();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByRole("alert")).toHaveTextContent("Unable to change password");
    expect(screen.getByRole("alert")).not.toHaveTextContent("must-not-leak");
  });

  it("clears an expired session and returns to Login", async () => {
    mockedChangePassword.mockRejectedValue(apiError("AUTH005", "Session expired"));
    renderPage();
    fillValidForm();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    expect(await screen.findByText(/Login reached.*sessionExpired/i)).toBeInTheDocument();
    expect(useAuthStore.getState()).toMatchObject({ user: null, accessToken: null, isAuthenticated: false });
  });

  it("prevents duplicate submission while the request is pending", async () => {
    let resolveRequest!: (response: ChangePasswordResponse) => void;
    mockedChangePassword.mockImplementation(() => new Promise((resolve) => { resolveRequest = resolve; }));
    renderPage();
    fillValidForm();
    fireEvent.click(screen.getByRole("button", { name: "Change password" }));

    const pending = await screen.findByRole("button", { name: "Changing password…" });
    expect(pending).toBeDisabled();
    fireEvent.click(pending);
    expect(mockedChangePassword).toHaveBeenCalledOnce();
    await act(async () => resolveRequest({ message: "Password changed successfully" }));
    await waitFor(() => expect(screen.getByText(/Login reached.*passwordChanged/i)).toBeInTheDocument());
  });

  it("shows an empty protected-account state without user material", () => {
    useAuthStore.setState({ user: null });
    renderPage();

    expect(screen.getByText("Account details unavailable")).toBeInTheDocument();
    expect(screen.queryByText("admin")).not.toBeInTheDocument();
    expect(screen.queryByLabelText("Current password")).not.toBeInTheDocument();
  });
});
